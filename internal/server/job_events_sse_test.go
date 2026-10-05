//nolint:testpackage // Tests the unexported SSE handler and stream forwarder directly.
package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"
)

// scriptedLogStream is a deterministic stand-in for the jobs-service
// server-streaming client. Each Recv consumes one entry from script; the final
// entry's error ends the stream.
type scriptedLogStream struct {
	grpc.ClientStream

	script []streamStep
	calls  int
}

type streamStep struct {
	log *jobspb.Log
	err error
}

func newScriptedLogStream(script ...streamStep) *scriptedLogStream {
	return &scriptedLogStream{script: script}
}

func (s *scriptedLogStream) Recv() (*jobspb.Log, error) {
	if s.calls >= len(s.script) {
		return nil, io.EOF
	}
	step := s.script[s.calls]
	s.calls++
	return step.log, step.err
}

// Context returns a live context so the double satisfies grpc.ClientStream.
// The forwarder under test reads the request context, not this one.
func (s *scriptedLogStream) Context() context.Context {
	return context.Background()
}

func (s *scriptedLogStream) Header() (metadata.MD, error) {
	return metadata.MD{}, nil
}
func (s *scriptedLogStream) Trailer() metadata.MD { return nil }
func (s *scriptedLogStream) CloseSend() error     { return nil }
func (s *scriptedLogStream) SendMsg(any) error    { return nil }
func (s *scriptedLogStream) RecvMsg(any) error    { return nil }

// blockingLogStream parks in Recv until the stream context is canceled, which
// is how a real client behaves when the SSE client disconnects.
type blockingLogStream struct {
	grpc.ClientStream

	// Mirrors grpc.ClientStream.Context so cancellation of the request can be
	// observed from inside Recv.
	//nolint:containedctx // The field reproduces the gRPC client-stream contract.
	ctx         context.Context
	recvEntered chan struct{}
	canceledAt  chan struct{}
	// recvCalls is atomic because the handler goroutine writes it while the
	// test goroutine may read it during a failing run.
	recvCalls atomic.Int64
}

func newBlockingLogStream(ctx context.Context) *blockingLogStream {
	return &blockingLogStream{
		ctx:         ctx,
		recvEntered: make(chan struct{}, 1),
		canceledAt:  make(chan struct{}, 1),
	}
}

// Recv blocks until the stream context is canceled. The signals are buffered
// sends rather than channel closes so a forwarder that retries Recv is reported
// through recvCalls instead of panicking on an already-closed channel.
func (s *blockingLogStream) Recv() (*jobspb.Log, error) {
	s.recvCalls.Add(1)
	select {
	case s.recvEntered <- struct{}{}:
	default:
	}
	<-s.ctx.Done()
	select {
	case s.canceledAt <- struct{}{}:
	default:
	}
	return nil, s.ctx.Err()
}

func (s *blockingLogStream) Context() context.Context { return s.ctx }
func (s *blockingLogStream) Header() (metadata.MD, error) {
	return metadata.MD{}, nil
}
func (s *blockingLogStream) Trailer() metadata.MD { return nil }
func (s *blockingLogStream) CloseSend() error     { return nil }
func (s *blockingLogStream) SendMsg(any) error    { return nil }
func (s *blockingLogStream) RecvMsg(any) error    { return nil }

// sseJobsClient serves the stream to the SSE handler. A nil stream is returned
// together with an error when streamErr is set.
type sseJobsClient struct {
	jobspb.JobsServiceClient

	// stream is handed back as-is unless newStream is set. newStream builds the
	// stream from the context the handler passed in, so a test can observe the
	// handler's own wiring instead of assuming it; streamCtx keeps the context
	// the handler supplied for inspection.
	stream    jobspb.JobsService_StreamJobLogsClient
	newStream func(context.Context) jobspb.JobsService_StreamJobLogsClient
	streamErr error
	//nolint:containedctx // The double must hold the handler's context to assert on it.
	streamCtx context.Context
	calls     int
	request   *jobspb.StreamJobLogsRequest
}

func (c *sseJobsClient) StreamJobLogs(
	ctx context.Context,
	req *jobspb.StreamJobLogsRequest,
	_ ...grpc.CallOption,
) (jobspb.JobsService_StreamJobLogsClient, error) {
	c.calls++
	c.request = req
	c.streamCtx = ctx
	if c.streamErr != nil {
		return nil, c.streamErr
	}
	if c.newStream != nil {
		return c.newStream(ctx), nil
	}
	return c.stream, nil
}

func newSSERequest(t *testing.T, userID string) *http.Request {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, "/workflows/wf-1/jobs/job-1/events", http.NoBody)
	req.SetPathValue("workflow_id", "wf-1")
	req.SetPathValue("job_id", "job-1")
	if userID == "" {
		return req
	}
	return req.WithContext(context.WithValue(req.Context(), userIDKey{}, userID))
}

// assertSSEHeaders pins the streaming response headers every SSE client needs
// and asserts the response is never gzip encoded.
func assertSSEHeaders(t *testing.T, res *httptest.ResponseRecorder) {
	t.Helper()

	if got := res.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("Content-Type = %q, want text/event-stream", got)
	}
	if got := res.Header().Get("Cache-Control"); got != "no-cache" {
		t.Fatalf("Cache-Control = %q, want no-cache", got)
	}
	if got := res.Header().Get("Connection"); got != "keep-alive" {
		t.Fatalf("Connection = %q, want keep-alive", got)
	}
	if got := res.Header().Get("Content-Encoding"); got != "" {
		t.Fatalf("Content-Encoding = %q, want empty for SSE", got)
	}
}

// TestHandleJobEventsRejectsMissingIdentifiers covers the guards that keep an
// unauthenticated or unrouted SSE request from opening a backend stream.
func TestHandleJobEventsRejectsMissingIdentifiers(t *testing.T) {
	tests := []struct {
		name      string
		userID    string
		clearPath string
		wantMsg   string
	}{
		{name: "missing workflow id", userID: "user-1", clearPath: "workflow_id", wantMsg: "workflow ID not found\n"},
		{name: "missing job id", userID: "user-1", clearPath: "job_id", wantMsg: "job ID not found\n"},
		{name: "missing authenticated user", wantMsg: "user ID not found\n"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &sseJobsClient{}
			s := &Server{jobsClient: client, logger: newSilentLogger()}

			req := newSSERequest(t, test.userID)
			if test.clearPath != "" {
				req.SetPathValue(test.clearPath, "")
			}

			res := httptest.NewRecorder()
			s.handleJobEvents(res, req)

			if res.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d (body %q)", res.Code, http.StatusBadRequest, res.Body.String())
			}
			if got := res.Body.String(); got != test.wantMsg {
				t.Fatalf("body = %q, want %q", got, test.wantMsg)
			}
			if client.calls != 0 {
				t.Fatalf("StreamJobLogs called %d times for a rejected request", client.calls)
			}
		})
	}
}

// TestHandleJobEventsStreamStartFailure verifies a backend refusal is reported
// as an SSE error frame rather than an HTTP error status, since the streaming
// headers are already committed.
func TestHandleJobEventsStreamStartFailure(t *testing.T) {
	client := &sseJobsClient{
		streamErr: status.Error(codes.PermissionDenied, "job belongs to user-2"),
	}
	s := &Server{jobsClient: client, logger: newSilentLogger()}

	res := httptest.NewRecorder()
	s.handleJobEvents(res, newSSERequest(t, "user-1"))

	if client.calls != 1 {
		t.Fatalf("StreamJobLogs calls = %d, want 1", client.calls)
	}
	assertSSEHeaders(t, res)

	want := "event: error\ndata: {\"message\":\"stream failed\"}\n\n"
	if got := res.Body.String(); got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
	if strings.Contains(res.Body.String(), "user-2") || strings.Contains(res.Body.String(), "PermissionDenied") {
		t.Fatal("SSE error frame leaked backend detail")
	}
	if !res.Flushed {
		t.Fatal("SSE error frame was never flushed")
	}
	// The request must reach the backend fully identified and unfiltered.
	if got := client.request.GetId(); got != "job-1" {
		t.Fatalf("job id = %q, want job-1", got)
	}
	if got := client.request.GetWorkflowId(); got != "wf-1" {
		t.Fatalf("workflow id = %q, want wf-1", got)
	}
	if got := client.request.GetUserId(); got != "user-1" {
		t.Fatalf("user id = %q, want user-1", got)
	}
}

// TestHandleJobEventsForwardsLogs verifies the connected handshake and the
// per-log SSE framing, and that the trailing end frame is emitted once the
// backend closes the stream cleanly.
func TestHandleJobEventsForwardsLogs(t *testing.T) {
	stream := newScriptedLogStream(
		streamStep{log: &jobspb.Log{
			Timestamp:   "2026-06-05T12:00:00Z",
			Message:     "first line",
			SequenceNum: 1,
			Stream:      logStreamStdout,
			EventId:     "event-1",
		}},
		// A nil message is skipped rather than forwarded as an empty frame.
		streamStep{log: nil},
		streamStep{log: &jobspb.Log{
			Timestamp:   "2026-06-05T12:00:01Z",
			Message:     `{"level":"error"}`,
			SequenceNum: 2,
			Stream:      "combined",
			EventId:     "event-2",
		}},
	)
	client := &sseJobsClient{stream: stream}
	s := &Server{jobsClient: client, logger: newSilentLogger()}

	res := httptest.NewRecorder()
	s.handleJobEvents(res, newSSERequest(t, "user-1"))

	assertSSEHeaders(t, res)

	body := res.Body.String()
	for _, want := range []string{
		"event: connected\ndata: {\"status\":\"connected\"}\n\n",
		"event: log\ndata: {\"timestamp\":\"2026-06-05T12:00:00Z\",\"message\":\"first line\",",
		"\"sequence_num\":1,\"stream\":\"stdout\",\"event_id\":\"event-1\"}\n\n",
		"event: log\ndata: {\"timestamp\":\"2026-06-05T12:00:01Z\",\"message\":\"{\\\"level\\\":\\\"error\\\"}\",",
		"\"sequence_num\":2,\"stream\":\"combined\",\"event_id\":\"event-2\"}\n\n",
		"event: end\ndata: {\"status\":\"stream_ended\"}\n\n",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %q, got:\n%s", want, body)
		}
	}
	if got := strings.Count(body, "event: log\n"); got != 2 {
		t.Fatalf("log frames = %d, want 2 (body:\n%s)", got, body)
	}
	if strings.Contains(body, "event: error") {
		t.Fatalf("unexpected error frame (body:\n%s)", body)
	}
	if !res.Flushed {
		t.Fatal("SSE frames were never flushed")
	}
	// The nil message is consumed but not forwarded.
	if got := stream.calls; got != 3 {
		t.Fatalf("Recv calls = %d, want 3", got)
	}
}

// TestHandleJobEventsMidStreamFailure verifies a stream that dies after the
// handshake reports one error frame and then stops forwarding.
func TestHandleJobEventsMidStreamFailure(t *testing.T) {
	stream := newScriptedLogStream(
		streamStep{log: &jobspb.Log{Message: "before failure", SequenceNum: 1}},
		streamStep{err: status.Error(codes.Unavailable, "clickhouse 10.0.0.4:9000 refused")},
	)
	client := &sseJobsClient{stream: stream}
	s := &Server{jobsClient: client, logger: newSilentLogger()}

	res := httptest.NewRecorder()
	s.handleJobEvents(res, newSSERequest(t, "user-1"))

	assertSSEHeaders(t, res)

	body := res.Body.String()
	if !strings.Contains(body, "event: log\ndata: {\"message\":\"before failure\"") {
		t.Fatalf("missing forwarded log (body:\n%s)", body)
	}
	want := "event: error\ndata: {\"message\":\"stream failed\"}\n\n"
	if !strings.Contains(body, want) {
		t.Fatalf("body missing %q, got:\n%s", want, body)
	}
	if strings.Contains(body, "10.0.0.4") || strings.Contains(body, "clickhouse") {
		t.Fatalf("SSE error frame leaked backend detail (body:\n%s)", body)
	}
	if strings.Contains(body, "event: end") {
		t.Fatalf("a failed stream must not also report a clean end (body:\n%s)", body)
	}
	if got := strings.Count(body, "event: error"); got != 1 {
		t.Fatalf("error frames = %d, want 1 (body:\n%s)", got, body)
	}
	// The forwarder must stop at the first failure instead of spinning.
	if got := stream.calls; got != 2 {
		t.Fatalf("Recv calls = %d, want 2", got)
	}
}

// blockingStreamStarted hands the test the stream the handler was served plus
// the cancellation that can always unpark it again.
type blockingStreamStarted struct {
	stream *blockingLogStream
	cancel context.CancelFunc
}

// sseStreamWaitTimeout bounds every wait in the cancellation test so a handler
// that never tears the stream down fails the test instead of hanging the run.
const sseStreamWaitTimeout = 5 * time.Second

// TestHandleJobEventsCancellationTearsDownStream verifies that canceling the
// request context both stops the handler and cancels the context the handler
// handed the backend stream, so an abandoned SSE connection cannot leak a
// jobs-service subscription. The double builds its stream from the context
// StreamJobLogs actually received, so a handler that forwarded a detached
// context instead of the request context fails here instead of passing
// vacuously.
func TestHandleJobEventsCancellationTearsDownStream(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	// Releases the request-scoped work even when an assertion below fails.
	defer cancel()

	streamReady := make(chan blockingStreamStarted, 1)
	client := &sseJobsClient{}
	client.newStream = func(streamCtx context.Context) jobspb.JobsService_StreamJobLogsClient {
		// Wrap the context the handler actually supplied in a cancelable child
		// so the test can always unpark Recv, even for the regression under
		// test where that context is never canceled.
		childCtx, cancelChild := context.WithCancel(streamCtx)
		stream := newBlockingLogStream(childCtx)
		streamReady <- blockingStreamStarted{stream: stream, cancel: cancelChild}
		return stream
	}
	s := &Server{jobsClient: client, logger: newSilentLogger()}

	req := httptest.NewRequest(http.MethodGet, "/workflows/wf-1/jobs/job-1/events", http.NoBody).
		WithContext(context.WithValue(ctx, userIDKey{}, "user-1"))
	req.SetPathValue("workflow_id", "wf-1")
	req.SetPathValue("job_id", "job-1")

	res := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.handleJobEvents(res, req)
	}()

	var started blockingStreamStarted
	select {
	case started = <-streamReady:
	case <-time.After(sseStreamWaitTimeout):
		t.Fatal("handler never opened the backend stream")
	}
	// Registered only once the stream exists: it must be able to stop the
	// parked Recv, which is the goroutine the handler goroutine is waiting on.
	defer started.cancel()
	stream := started.stream

	// Synchronize on the backend stream actually being parked in Recv.
	select {
	case <-stream.recvEntered:
	case <-time.After(sseStreamWaitTimeout):
		t.Fatal("backend stream never entered Recv")
	}

	cancel()

	select {
	case <-stream.canceledAt:
	case <-time.After(sseStreamWaitTimeout):
		t.Fatal("canceling the request did not cancel the context passed to StreamJobLogs")
	}

	select {
	case <-done:
	case <-time.After(sseStreamWaitTimeout):
		t.Fatal("SSE handler did not return after cancellation")
	}

	// The context the handler passed must be the request context, not a
	// detached one, or the jobs service keeps streaming to nobody. The child
	// context the double added is canceled by now either way, so this inspects
	// the context the handler supplied.
	if client.streamCtx == nil {
		t.Fatal("StreamJobLogs was never called with a context")
	}
	if client.streamCtx.Err() == nil {
		t.Fatal("the context handed to StreamJobLogs is still live after the request was canceled")
	}
	// One read that returned the cancellation error is all that may happen: a
	// forwarder that loops would keep reading the stream.
	if got := stream.recvCalls.Load(); got != 1 {
		t.Fatalf("Recv calls = %d, want 1: the forwarder must stop once the stream is canceled", got)
	}

	body := res.Body.String()
	if !strings.Contains(body, "event: connected") {
		t.Fatalf("missing handshake (body:\n%s)", body)
	}
	// A canceled stream must never be reported as a clean end of the job's
	// log stream: clients rely on "end" meaning the backend finished.
	if strings.Contains(body, "event: end") {
		t.Fatalf("cancellation must not emit an end frame (body:\n%s)", body)
	}
}

// TestForwardJobLogEventsStopsAfterStreamError verifies the forwarder issues
// exactly one error frame and returns instead of retrying a dead stream.
func TestForwardJobLogEventsStopsAfterStreamError(t *testing.T) {
	stream := newScriptedLogStream(
		streamStep{err: status.Error(codes.Internal, "boom")},
		streamStep{log: &jobspb.Log{Message: "must not be forwarded"}},
	)
	res := httptest.NewRecorder()
	s := &Server{logger: newSilentLogger()}

	s.forwardJobLogEvents(t.Context(), res, res, stream)

	body := res.Body.String()
	if got := strings.Count(body, "event: error"); got != 1 {
		t.Fatalf("error frames = %d, want 1 (body:\n%s)", got, body)
	}
	if strings.Contains(body, "must not be forwarded") {
		t.Fatalf("forwarder continued past the stream failure (body:\n%s)", body)
	}
	if got := stream.calls; got != 1 {
		t.Fatalf("Recv calls = %d, want 1", got)
	}
}

// TestForwardJobLogEventsHonoursAlreadyCancelledContext verifies the forwarder
// returns without opening a stream read when the context is already done.
func TestForwardJobLogEventsHonoursAlreadyCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	stream := newBlockingLogStream(ctx)
	res := httptest.NewRecorder()
	s := &Server{logger: newSilentLogger()}

	s.forwardJobLogEvents(ctx, res, res, stream)

	if res.Body.Len() != 0 {
		t.Fatalf("body = %q, want empty for an already canceled context", res.Body.String())
	}
	select {
	case <-stream.recvEntered:
		t.Fatal("forwarder read from a canceled stream")
	default:
	}
}

// TestCompressionMiddlewareSkipsStreamingPaths verifies the SSE and raw
// download routes are never gzipped, which would otherwise buffer the stream.
func TestCompressionMiddlewareSkipsStreamingPaths(t *testing.T) {
	s := &Server{logger: newSilentLogger()}

	tests := []struct {
		name     string
		path     string
		wantGzip bool
	}{
		{name: "job events", path: "/workflows/wf-1/jobs/job-1/events"},
		{name: "raw logs", path: "/workflows/wf-1/jobs/job-1/logs/raw"},
		{name: "json logs are compressed", path: "/workflows/wf-1/jobs/job-1/logs", wantGzip: true},
		{name: "json list jobs is compressed", path: "/workflows/wf-1/jobs", wantGzip: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler := s.withCompressionMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				// Real handlers write the status first, which is what makes the
				// gzip writer set Content-Encoding.
				w.WriteHeader(http.StatusOK)
				if _, err := w.Write([]byte("payload")); err != nil {
					t.Errorf("write: %v", err)
				}
			}))
			req := httptest.NewRequest(http.MethodGet, test.path, http.NoBody)
			req.Header.Set("Accept-Encoding", "gzip")

			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)

			gotGzip := res.Header().Get("Content-Encoding") == "gzip"
			if gotGzip != test.wantGzip {
				t.Fatalf("gzip = %v, want %v (headers %v)", gotGzip, test.wantGzip, res.Header())
			}
			if test.wantGzip && !strings.HasPrefix(res.Header().Get("Vary"), "Accept-Encoding") {
				t.Fatalf("Vary = %q, want it to mention Accept-Encoding", res.Header().Get("Vary"))
			}
		})
	}
}

// newSilentLogger returns a logger that drops output so stream failure paths
// stay quiet without disabling them.
func newSilentLogger() *zap.Logger {
	return zap.NewNop()
}
