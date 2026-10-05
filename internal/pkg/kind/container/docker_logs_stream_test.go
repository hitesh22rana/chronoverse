//nolint:testpackage // Drives the unexported log-stream entry point and its shutdown paths.
package container

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/docker/docker/pkg/stdcopy"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
)

const (
	dockerLogTestContainerID = "container-1"

	// dockerLogStreamTestTimeout bounds every wait on the streaming goroutines so
	// a shutdown regression fails the test instead of hanging the suite.
	dockerLogStreamTestTimeout = 20 * time.Second

	// The stream labels production code publishes under.
	dockerLogTestStdout = dockerLogStreamStdout
	dockerLogTestStderr = dockerLogStreamStderr
)

// fakeDockerAPI describes the Docker endpoints a log-stream test needs.
type fakeDockerAPI struct {
	// logs serves GET /containers/{id}/logs.
	logs http.HandlerFunc
	// wait serves POST /containers/{id}/wait; nil reports a clean exit.
	wait http.HandlerFunc
	// stop serves POST /containers/{id}/stop; nil answers immediately.
	stop http.HandlerFunc
}

// newFakeDockerWorkflow serves a Docker API whose only interesting endpoint is the
// log stream handler; the rest answers the minimum Execute and Logs need.
func newFakeDockerWorkflow(t *testing.T, api fakeDockerAPI) *DockerWorkflow {
	t.Helper()

	networkInspect := fmt.Sprintf(
		`{"Name":%q,"Driver":"bridge","EnableIPv6":false,"Options":{"com.docker.network.bridge.enable_icc":"false","com.docker.network.bridge.name":"chronoverse-br"},"IPAM":{"Config":[{"Subnet":%q}]}}`,
		DefaultWorkloadNetwork,
		DefaultWorkloadSubnet,
	)

	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/_ping":
			rw.Header().Set("API-Version", "1.51")
			writeDockerTestResponse(t, rw, "OK")
		case strings.HasSuffix(r.URL.Path, "/logs"):
			api.logs(rw, r)
		case strings.HasSuffix(r.URL.Path, "/create"):
			rw.Header().Set("Content-Type", "application/json")
			writeDockerTestResponse(t, rw, fmt.Sprintf(`{"Id":%q,"Warnings":[]}`, dockerLogTestContainerID))
		case strings.HasSuffix(r.URL.Path, "/start"):
			rw.WriteHeader(http.StatusNoContent)
		case strings.HasSuffix(r.URL.Path, "/stop"):
			if api.stop != nil {
				api.stop(rw, r)
				return
			}
			rw.WriteHeader(http.StatusNoContent)
		case strings.HasSuffix(r.URL.Path, "/wait"):
			rw.Header().Set("Content-Type", "application/json")
			if api.wait != nil {
				api.wait(rw, r)
				return
			}
			writeDockerTestResponse(t, rw, `{"StatusCode":0,"Error":null}`)
		case strings.Contains(r.URL.Path, "/networks/"):
			rw.Header().Set("Content-Type", "application/json")
			writeDockerTestResponse(t, rw, networkInspect)
		default:
			t.Errorf("unexpected Docker API request: %s %s", r.Method, r.URL.String())
			rw.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)

	workflow, err := NewDockerWorkflow(WithDockerHost(server.URL))
	if err != nil {
		t.Fatalf("NewDockerWorkflow() error = %v", err)
	}
	t.Cleanup(func() {
		_ = workflow.Close()
	})

	return workflow
}

// dockerLogFrame encodes one stdcopy frame, the framing of non-TTY daemon output.
func dockerLogFrame(stream stdcopy.StdType, payload string) []byte {
	var frame bytes.Buffer
	if _, err := stdcopy.NewStdWriter(&frame, stream).Write([]byte(payload)); err != nil {
		panic(fmt.Sprintf("failed to frame %d payload: %v", stream, err))
	}

	return frame.Bytes()
}

// dockerLogStreamResult is everything a caller observed on the log channels.
type dockerLogStreamResult struct {
	logs []*jobsmodel.JobLog
	errs []error
}

// collectDockerLogStream drains both channels until they close; safe to run on
// its own goroutine because it never touches *testing.T.
func collectDockerLogStream(logs <-chan *jobsmodel.JobLog, errs <-chan error) dockerLogStreamResult {
	var result dockerLogStreamResult
	for logs != nil || errs != nil {
		select {
		case log, ok := <-logs:
			if !ok {
				logs = nil
				continue
			}
			result.logs = append(result.logs, log)
		case err, ok := <-errs:
			if !ok {
				errs = nil
				continue
			}
			result.errs = append(result.errs, err)
		}
	}

	return result
}

// awaitDockerLogStream fails the test when the stream never finishes, which is how
// a stranded reader, scanner or error publisher shows up.
func awaitDockerLogStream(t *testing.T, logs <-chan *jobsmodel.JobLog, errs <-chan error) dockerLogStreamResult {
	t.Helper()

	done := make(chan dockerLogStreamResult, 1)
	go func() {
		done <- collectDockerLogStream(logs, errs)
	}()

	select {
	case result := <-done:
		return result
	case <-time.After(dockerLogStreamTestTimeout):
		t.Fatal("container log stream did not terminate")
		return dockerLogStreamResult{}
	}
}

func logMessages(logs []*jobsmodel.JobLog) []string {
	messages := make([]string, 0, len(logs))
	for _, log := range logs {
		messages = append(messages, log.Message)
	}

	return messages
}

// logMessagesByStream groups delivered lines per stream, preserving order within
// each stream; the relative order of stdout and stderr is undefined.
func logMessagesByStream(logs []*jobsmodel.JobLog) map[string][]string {
	byStream := map[string][]string{}
	for _, log := range logs {
		byStream[log.Stream] = append(byStream[log.Stream], log.Message)
	}

	return byStream
}

func totalBytes(messages []string) int {
	total := 0
	for _, message := range messages {
		total += len(message)
	}

	return total
}

// waitForSignal fails the test when a fixture handshake or shutdown signal does
// not arrive in time.
func waitForSignal(t *testing.T, signal <-chan struct{}, failure string) {
	t.Helper()

	select {
	case <-signal:
	case <-time.After(dockerLogStreamTestTimeout):
		t.Fatal(failure)
	}
}

// requireContiguousSequences checks that delivered sequences are zero-based,
// gapless and free of duplicates.
func requireContiguousSequences(t *testing.T, logs []*jobsmodel.JobLog) {
	t.Helper()

	for i, log := range logs {
		if log.SequenceNum != uint32(i) {
			t.Fatalf("log %d sequence = %d, want %d", i, log.SequenceNum, i)
		}
	}
}

// flushDockerLogStream pushes buffered stream bytes out. Best effort: Execute aborts
// the connection when it tears the stream down, and that is under test.
func flushDockerLogStream(rw http.ResponseWriter) {
	_ = http.NewResponseController(rw).Flush()
}

func TestStreamContainerLogsDemuxesStdoutAndStderr(t *testing.T) {
	t.Parallel()

	workflow := newFakeDockerWorkflow(t, fakeDockerAPI{
		logs: func(rw http.ResponseWriter, _ *http.Request) {
			var stream bytes.Buffer
			stream.Write(dockerLogFrame(stdcopy.Stdout, "first stdout line\n"))
			stream.Write(dockerLogFrame(stdcopy.Stderr, "first stderr line\n"))
			stream.Write(dockerLogFrame(stdcopy.Stdout, "\n")) // blank: dropped, no sequence consumed
			stream.Write(dockerLogFrame(stdcopy.Stdout, "second stdout line\n"))
			stream.Write(dockerLogFrame(stdcopy.Stderr, "second stderr line\n"))
			writeDockerTestResponse(t, rw, stream.String())
		},
	})

	logs, errs, err := workflow.Logs(t.Context(), dockerLogTestContainerID)
	if err != nil {
		t.Fatalf("Logs() error = %v", err)
	}

	result := awaitDockerLogStream(t, logs, errs)
	if len(result.errs) != 0 {
		t.Fatalf("Logs() errors = %v, want none", result.errs)
	}
	if len(result.logs) != 4 {
		t.Fatalf("delivered %d log lines, want 4: %v", len(result.logs), logMessages(result.logs))
	}

	for i, log := range result.logs {
		if log.SequenceNum != uint32(i) {
			t.Fatalf("log %d sequence = %d, want %d", i, log.SequenceNum, i)
		}
		if log.Timestamp.IsZero() {
			t.Fatalf("log %d has a zero timestamp", i)
		}
		if log.Message == "" {
			t.Fatalf("log %d is blank; blank lines must not be published", i)
		}
	}

	gotStreams := logMessagesByStream(result.logs)
	if want := []string{"first stdout line", "second stdout line"}; !slices.Equal(gotStreams[dockerLogTestStdout], want) {
		t.Fatalf("stdout lines = %v, want %v", gotStreams[dockerLogTestStdout], want)
	}
	if want := []string{"first stderr line", "second stderr line"}; !slices.Equal(gotStreams[dockerLogTestStderr], want) {
		t.Fatalf("stderr lines = %v, want %v", gotStreams[dockerLogTestStderr], want)
	}
}

// TestStreamContainerLogsAssignsUniqueContiguousSequences pins the sequencing
// contract: numbers are handed out once at the forwarding boundary, so an interleaved
// stream yields 0..N-1 exactly once each.
func TestStreamContainerLogsAssignsUniqueContiguousSequences(t *testing.T) {
	t.Parallel()

	const linesPerStream = 500

	workflow := newFakeDockerWorkflow(t, fakeDockerAPI{
		logs: func(rw http.ResponseWriter, _ *http.Request) {
			var stream bytes.Buffer
			for i := range linesPerStream {
				stream.Write(dockerLogFrame(stdcopy.Stdout, fmt.Sprintf("stdout-%d\n", i)))
				stream.Write(dockerLogFrame(stdcopy.Stderr, fmt.Sprintf("stderr-%d\n", i)))
			}
			writeDockerTestResponse(t, rw, stream.String())
		},
	})

	logs, errs, err := workflow.Logs(t.Context(), dockerLogTestContainerID)
	if err != nil {
		t.Fatalf("Logs() error = %v", err)
	}

	result := awaitDockerLogStream(t, logs, errs)
	if len(result.errs) != 0 {
		t.Fatalf("Logs() errors = %v, want none", result.errs)
	}

	wantTotal := uint32(2 * linesPerStream)
	if len(result.logs) != int(wantTotal) {
		t.Fatalf("delivered %d log lines, want %d", len(result.logs), wantTotal)
	}

	seen := make(map[uint32]bool, wantTotal)
	streams := map[string]int{}
	for _, log := range result.logs {
		if seen[log.SequenceNum] {
			t.Fatalf("sequence %d delivered twice; sequences must be unique", log.SequenceNum)
		}
		seen[log.SequenceNum] = true
		streams[log.Stream]++
	}
	for sequence := range wantTotal {
		if !seen[sequence] {
			t.Fatalf("sequence %d was never delivered; sequences must be contiguous from 0", sequence)
		}
	}
	if streams[dockerLogTestStdout] != linesPerStream || streams[dockerLogTestStderr] != linesPerStream {
		t.Fatalf("per-stream counts = %v, want %d stdout and %d stderr", streams, linesPerStream, linesPerStream)
	}
}

// TestStreamContainerLogsDeliversLinesAboveScannerDefaultLimit guards the 64 KiB
// bufio.Scanner default: workloads emit JSON blobs and stack traces far past it.
func TestStreamContainerLogsDeliversLinesAboveScannerDefaultLimit(t *testing.T) {
	t.Parallel()

	largeStdout := strings.Repeat("s", 128<<10)
	largeStderr := strings.Repeat("e", 96<<10)

	workflow := newFakeDockerWorkflow(t, fakeDockerAPI{
		logs: func(rw http.ResponseWriter, _ *http.Request) {
			var stream bytes.Buffer
			stream.Write(dockerLogFrame(stdcopy.Stdout, "before\n"))
			stream.Write(dockerLogFrame(stdcopy.Stdout, largeStdout+"\n"))
			stream.Write(dockerLogFrame(stdcopy.Stderr, largeStderr+"\n"))
			stream.Write(dockerLogFrame(stdcopy.Stdout, "after\n"))
			writeDockerTestResponse(t, rw, stream.String())
		},
	})

	logs, errs, err := workflow.Logs(t.Context(), dockerLogTestContainerID)
	if err != nil {
		t.Fatalf("Logs() error = %v", err)
	}

	result := awaitDockerLogStream(t, logs, errs)
	if len(result.errs) != 0 {
		t.Fatalf("Logs() errors = %v, want none", result.errs)
	}

	want := map[string][]string{
		dockerLogTestStdout: {"before", largeStdout, "after"},
		dockerLogTestStderr: {largeStderr},
	}
	got := logMessagesByStream(result.logs)
	for stream, wantLines := range want {
		if !slices.Equal(got[stream], wantLines) {
			t.Fatalf("%s delivered %d line(s) (%d bytes), want %d line(s); lines past the scanner default are silently dropped",
				stream, len(got[stream]), totalBytes(got[stream]), len(wantLines))
		}
	}
}

// TestStreamContainerLogsTruncatesOversizedLine requires an explicit report instead
// of a silent truncation, so an unreadable line cannot look like a clean run — and
// requires it as output rather than as a failure, because the cap is a limit on what
// the platform carries: a workload that trips it must not be recorded as failing.
func TestStreamContainerLogsTruncatesOversizedLine(t *testing.T) {
	t.Parallel()

	// One byte past the documented cap: the boundary itself must stay usable.
	oversized := strings.Repeat("x", dockerLogScanMaxLineBytes+1)

	workflow := newFakeDockerWorkflow(t, fakeDockerAPI{
		logs: func(rw http.ResponseWriter, _ *http.Request) {
			var stream bytes.Buffer
			stream.Write(dockerLogFrame(stdcopy.Stdout, "delivered before the overflow\n"))
			stream.Write(dockerLogFrame(stdcopy.Stdout, oversized+"\n"))
			writeDockerTestResponse(t, rw, stream.String())
		},
	})

	logs, errs, err := workflow.Logs(t.Context(), dockerLogTestContainerID)
	if err != nil {
		t.Fatalf("Logs() error = %v", err)
	}

	result := awaitDockerLogStream(t, logs, errs)
	if len(result.errs) != 0 {
		t.Fatalf("Logs() published %v, want no failure for a line past the cap", result.errs)
	}

	// The line before the overflow survives, and the truncation replaces the
	// refused line rather than being appended after it.
	messages := logMessages(result.logs)
	if len(messages) != 2 || messages[0] != "delivered before the overflow" {
		t.Fatalf("delivered %v, want the line before the overflow then the truncation notice", messages)
	}

	notice := messages[1]
	if !strings.Contains(notice, fmt.Sprint(dockerLogScanMaxLineBytes)) {
		t.Fatalf("truncation notice %q does not name the %d byte limit", notice, dockerLogScanMaxLineBytes)
	}
	if !strings.Contains(notice, dockerLogTestStdout) {
		t.Fatalf("truncation notice %q does not name the offending stream", notice)
	}
	if result.logs[1].Stream != dockerLogTestStdout {
		t.Fatalf("truncation notice stream = %q, want %q", result.logs[1].Stream, dockerLogTestStdout)
	}
}

// TestStreamContainerLogsOversizedLineDoesNotFailExecution is the guard for the
// consequence that matters: Execute reports the container's own exit status, so a
// workload that exited 0 stays successful no matter how its output was framed.
func TestStreamContainerLogsOversizedLineDoesNotFailExecution(t *testing.T) {
	t.Parallel()

	oversized := strings.Repeat("x", dockerLogScanMaxLineBytes+1)

	for _, exitCode := range []int{0, 7} {
		t.Run(fmt.Sprint("exit ", exitCode), func(t *testing.T) {
			t.Parallel()

			workflow := newFakeDockerWorkflow(t, fakeDockerAPI{
				logs: func(rw http.ResponseWriter, _ *http.Request) {
					var stream bytes.Buffer
					stream.Write(dockerLogFrame(stdcopy.Stdout, "before\n"))
					stream.Write(dockerLogFrame(stdcopy.Stdout, oversized+"\n"))
					writeDockerTestResponse(t, rw, stream.String())
				},
				wait: func(rw http.ResponseWriter, _ *http.Request) {
					rw.Header().Set("Content-Type", "application/json")
					writeDockerTestResponse(t, rw, fmt.Sprintf(`{"StatusCode":%d,"Error":null}`, exitCode))
				},
			})

			_, logs, errs, err := workflow.Execute(t.Context(), time.Minute, "alpine:latest", []string{"true"}, nil)
			if err != nil {
				t.Fatalf("Execute() error = %v", err)
			}

			result := awaitDockerLogStream(t, logs, errs)
			if exitCode == 0 {
				if len(result.errs) != 0 {
					t.Fatalf("an exited-0 workload published %v, want its success to stand", result.errs)
				}
				return
			}

			// A real failure keeps its own reason: the truncation neither masks
			// it nor adds a second, competing one.
			if len(result.errs) != 1 {
				t.Fatalf("a non-zero exit published %d failures, want exactly the exit one: %v", len(result.errs), result.errs)
			}
			if !strings.Contains(result.errs[0].Error(), "container exited with non-zero code: 7") {
				t.Fatalf("failure = %q, want the container's own exit reason", result.errs[0])
			}
		})
	}
}

// TestStreamContainerLogsLineCapBoundary pins the cap for every terminator the daemon
// can send — LF, CRLF and an unterminated final line — since bufio counts the
// terminator against its maximum and strips the CR of a CRLF. Both ways a line can
// exceed the cap are covered: the explicit length check, and the buffer refusing a
// token outright.
func TestStreamContainerLogsLineCapBoundary(t *testing.T) {
	t.Parallel()

	terminators := []struct {
		name   string
		suffix string
	}{
		{name: "LF", suffix: "\n"},
		{name: "CRLF", suffix: "\r\n"},
		{name: "unterminated EOF", suffix: ""},
	}
	sizes := []struct {
		name           string
		content        int
		wantTruncation bool
	}{
		{name: "one byte below the cap", content: dockerLogScanMaxLineBytes - 1},
		{name: "exactly at the cap", content: dockerLogScanMaxLineBytes},
		{name: "one byte above the cap", content: dockerLogScanMaxLineBytes + 1, wantTruncation: true},
		// Past cap+2 the buffer itself refuses the token, so this exercises the
		// bufio.ErrTooLong path rather than the explicit length check.
		{name: "two bytes above the cap plus terminator", content: dockerLogScanMaxLineBytes + 3, wantTruncation: true},
	}

	for _, terminator := range terminators {
		for _, size := range sizes {
			t.Run(terminator.name+"/"+size.name, func(t *testing.T) {
				t.Parallel()

				line := strings.Repeat("z", size.content)
				workflow := newFakeDockerWorkflow(t, fakeDockerAPI{
					logs: func(rw http.ResponseWriter, _ *http.Request) {
						var stream bytes.Buffer
						stream.Write(dockerLogFrame(stdcopy.Stdout, "before\n"))
						stream.Write(dockerLogFrame(stdcopy.Stdout, line+terminator.suffix))
						// Only meaningful as the last thing the daemon sends: without a
						// delimiter the next line would extend it.
						if terminator.suffix != "" {
							stream.Write(dockerLogFrame(stdcopy.Stdout, "after\n"))
						}
						writeDockerTestResponse(t, rw, stream.String())
					},
				})

				logs, errs, err := workflow.Logs(t.Context(), dockerLogTestContainerID)
				if err != nil {
					t.Fatalf("Logs() error = %v", err)
				}

				result := awaitDockerLogStream(t, logs, errs)
				want := []string{"before", line}
				if terminator.suffix != "" {
					want = append(want, "after")
				}
				if size.wantTruncation {
					if len(result.errs) != 0 {
						t.Fatalf("a %d byte line published %d errors, want none: %v", size.content, len(result.errs), result.errs)
					}
					// The line before the overflow is published, then the notice
					// replaces the refused line.
					got := logMessages(result.logs)
					if len(got) != 2 || got[0] != want[0] {
						t.Fatalf("delivered %v, want the line before the overflow then the truncation notice", got)
					}
					if !strings.Contains(got[1], fmt.Sprint(dockerLogScanMaxLineBytes)) {
						t.Fatalf("truncation notice %q does not name the %d byte limit", got[1], dockerLogScanMaxLineBytes)
					}
					return
				}

				if len(result.errs) != 0 {
					t.Fatalf("a %d byte line failed with %v, want it published", size.content, result.errs)
				}
				// bufio strips the CR of a CRLF: the published content is the
				// payload without the terminator.
				if got := logMessages(result.logs); !slices.Equal(got, want) {
					t.Fatalf("delivered %d line(s) totalling %d bytes, want the %d byte line intact",
						len(got), totalBytes(got), size.content)
				}
			})
		}
	}
}

// TestStreamContainerLogsTruncationStopsOnlyTheOffendingStream records the cost of
// the cap: stdcopy has no way to resume past a line it has already framed, so
// closing the scanner's pipe ends the demultiplexer too. The other stream is
// unaffected, and the notice is what tells a consumer the tail is missing rather
// than never having been produced.
func TestStreamContainerLogsTruncationStopsOnlyTheOffendingStream(t *testing.T) {
	t.Parallel()

	oversized := strings.Repeat("x", dockerLogScanMaxLineBytes+1)

	workflow := newFakeDockerWorkflow(t, fakeDockerAPI{
		logs: func(rw http.ResponseWriter, _ *http.Request) {
			var stream bytes.Buffer
			stream.Write(dockerLogFrame(stdcopy.Stdout, "stdout before\n"))
			stream.Write(dockerLogFrame(stdcopy.Stdout, oversized+"\n"))
			stream.Write(dockerLogFrame(stdcopy.Stderr, "stderr after the overflow\n"))
			writeDockerTestResponse(t, rw, stream.String())
		},
	})

	logs, errs, err := workflow.Logs(t.Context(), dockerLogTestContainerID)
	if err != nil {
		t.Fatalf("Logs() error = %v", err)
	}

	result := awaitDockerLogStream(t, logs, errs)
	if len(result.errs) != 0 {
		t.Fatalf("Logs() published %v, want no failure for a line past the cap", result.errs)
	}

	byStream := logMessagesByStream(result.logs)
	if !slices.Equal(byStream[dockerLogTestStderr], []string{"stderr after the overflow"}) {
		t.Fatalf("stderr delivered %v, want the line after the overflow: stdout truncation must not stop the other stream",
			byStream[dockerLogTestStderr])
	}
	if len(byStream[dockerLogTestStdout]) != 2 || byStream[dockerLogTestStdout][0] != "stdout before" {
		t.Fatalf("stdout delivered %v, want the line before the overflow then the notice", byStream[dockerLogTestStdout])
	}
}

func TestStreamContainerLogsReportsMalformedStream(t *testing.T) {
	t.Parallel()

	workflow := newFakeDockerWorkflow(t, fakeDockerAPI{
		logs: func(rw http.ResponseWriter, _ *http.Request) {
			// A frame header the demultiplexer cannot classify.
			writeDockerTestResponse(t, rw, string([]byte{0x09, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x05})+"oops!")
		},
	})

	logs, errs, err := workflow.Logs(t.Context(), dockerLogTestContainerID)
	if err != nil {
		t.Fatalf("Logs() error = %v", err)
	}

	result := awaitDockerLogStream(t, logs, errs)
	if len(result.logs) != 0 {
		t.Fatalf("delivered %v, want no log lines", logMessages(result.logs))
	}
	if len(result.errs) != 1 {
		t.Fatalf("Logs() errors = %v, want exactly one malformed-stream failure", result.errs)
	}
	if code := status.Code(result.errs[0]); code != codes.Aborted {
		t.Fatalf("malformed-stream code = %s, want %s: %v", code, codes.Aborted, result.errs[0])
	}
	if !strings.Contains(result.errs[0].Error(), "failed to read container logs") {
		t.Fatalf("malformed-stream error = %q, want the log read failure prefix", result.errs[0])
	}
}

func TestStreamContainerLogsClassifiesLogRequestFailures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		status int
		code   codes.Code
	}{
		{name: "missing container", status: http.StatusNotFound, code: codes.NotFound},
		{name: "daemon error", status: http.StatusInternalServerError, code: codes.Aborted},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			workflow := newFakeDockerWorkflow(t, fakeDockerAPI{
				logs: func(rw http.ResponseWriter, _ *http.Request) {
					rw.WriteHeader(tt.status)
					writeDockerTestResponse(t, rw, `{"message":"stream unavailable"}`)
				},
			})

			logs, errs, err := workflow.Logs(t.Context(), dockerLogTestContainerID)
			if err != nil {
				t.Fatalf("Logs() error = %v", err)
			}

			result := awaitDockerLogStream(t, logs, errs)
			if len(result.errs) != 1 {
				t.Fatalf("Logs() errors = %v, want exactly one failure", result.errs)
			}
			if code := status.Code(result.errs[0]); code != tt.code {
				t.Fatalf("code = %s, want %s: %v", code, tt.code, result.errs[0])
			}
		})
	}
}

// newStoppedDaemonWorkflow returns a workflow whose daemon answered the construction
// ping and then went away, so the next request is refused at dial time.
func newStoppedDaemonWorkflow(t *testing.T) *DockerWorkflow {
	t.Helper()

	daemon := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {
		rw.Header().Set("API-Version", "1.51")
		writeDockerTestResponse(t, rw, "OK")
	}))

	workflow, err := NewDockerWorkflow(WithDockerHost(daemon.URL))
	if err != nil {
		daemon.Close()
		t.Fatalf("NewDockerWorkflow() error = %v", err)
	}
	t.Cleanup(func() {
		_ = workflow.Close()
	})
	daemon.Close()

	return workflow
}

// TestStreamContainerLogsReportsDaemonUnavailable separates "the daemon is gone" from
// the daemon-side failures above: a refused dial must surface as Unavailable.
func TestStreamContainerLogsReportsDaemonUnavailable(t *testing.T) {
	t.Parallel()

	workflow := newStoppedDaemonWorkflow(t)

	logCh := make(chan *jobsmodel.JobLog, 1)
	errs := make(chan error, 1)
	workflow.streamContainerLogs(t.Context(), dockerLogTestContainerID, logCh, errs, true)

	if len(logCh) != 0 {
		t.Fatalf("an unreachable daemon published %d log line(s), want none", len(logCh))
	}
	if len(errs) != 1 {
		t.Fatalf("stream failures = %v, want exactly one", len(errs))
	}
	if code := status.Code(<-errs); code != codes.Unavailable {
		t.Fatalf("unreachable-daemon code = %s, want %s", code, codes.Unavailable)
	}
}

// TestStreamContainerLogsCancellationUnblocksBlockedReaderAndReceiver parks the forwarder
// and the demultiplexer mid-publish, then requires cancellation to release both and reach
// the daemon without turning the reader it closed into a stream failure.
func TestStreamContainerLogsCancellationUnblocksBlockedReaderAndReceiver(t *testing.T) {
	t.Parallel()

	attached := make(chan struct{})
	release := make(chan struct{})
	extraFrameSent := make(chan struct{})
	requestAborted := make(chan struct{})

	workflow := newFakeDockerWorkflow(t, fakeDockerAPI{
		logs: func(rw http.ResponseWriter, r *http.Request) {
			// The first line proves the client's request is live end to end.
			if _, err := rw.Write(dockerLogFrame(stdcopy.Stdout, "first line\n")); err != nil {
				return
			}
			flushDockerLogStream(rw)
			close(attached)

			<-release
			if _, err := rw.Write(dockerLogFrame(stdcopy.Stdout, "unread line\n")); err != nil {
				return
			}
			flushDockerLogStream(rw)
			close(extraFrameSent)

			// Then hold the connection open with no further output.
			<-r.Context().Done()
			close(requestAborted)
		},
	})

	ctx, cancel := context.WithCancel(t.Context())
	logCh := make(chan *jobsmodel.JobLog)
	errs := make(chan error, maxContainerLogStreamErrors)

	returned := make(chan struct{})
	go func() {
		defer close(returned)
		workflow.streamContainerLogs(ctx, dockerLogTestContainerID, logCh, errs, true)
	}()

	// The attachment handshake: it proves the request reached the daemon
	// and the stream is live end to end.
	select {
	case line := <-logCh:
		if line.Message != "first line" {
			t.Fatalf("first delivered line = %q, want %q", line.Message, "first line")
		}
	case err := <-errs:
		t.Fatalf("stream failed before it attached: %v", err)
	case <-time.After(dockerLogStreamTestTimeout):
		t.Fatal("the daemon log stream never delivered its first line")
	}
	waitForSignal(t, attached, "the fake daemon never saw the log stream request")

	// The receiver stops reading here on purpose: every further line parks
	// the forwarder, and with it the scanner and the demultiplexer.
	close(release)
	waitForSignal(t, extraFrameSent, "the fake daemon never sent the line the receiver refuses to read")
	// A parked send is not observable from outside, so give the forwarder a bounded
	// window and require the weaker fact it can prove: it still holds the unread line.
	select {
	case <-returned:
		t.Fatal("streamContainerLogs() returned while a published line was never read")
	case <-time.After(50 * time.Millisecond):
	}
	cancel()

	waitForSignal(t, returned, "streamContainerLogs() did not return after cancellation")
	waitForSignal(t, requestAborted, "the daemon log request was never aborted; the reader goroutine is stranded")

	// The channels stay the caller's to close: a publisher still alive would
	// send on them here and take the process down.
	close(logCh)
	close(errs)
	if len(errs) != 0 {
		t.Fatalf("cancellation published %d stream failures, want none", len(errs))
	}
}

// TestStreamContainerLogsRequestCancellationIsNotAStreamFailure pins the difference
// between "the caller went away" and "the stream broke": a cancel before the daemon
// answers must not become a log read failure.
func TestStreamContainerLogsRequestCancellationIsNotAStreamFailure(t *testing.T) {
	t.Parallel()

	attached := make(chan struct{})
	var once atomic.Bool

	workflow := newFakeDockerWorkflow(t, fakeDockerAPI{
		logs: func(_ http.ResponseWriter, r *http.Request) {
			if once.CompareAndSwap(false, true) {
				close(attached)
			}
			// Accept the request but never answer it.
			<-r.Context().Done()
		},
	})

	ctx, cancel := context.WithCancel(t.Context())
	logCh := make(chan *jobsmodel.JobLog)
	errs := make(chan error, maxContainerLogStreamErrors)

	returned := make(chan struct{})
	go func() {
		defer close(returned)
		workflow.streamContainerLogs(ctx, dockerLogTestContainerID, logCh, errs, true)
	}()

	// Without a daemon response this is the only proof the cancellation
	// below reaches the open request rather than arriving before it.
	select {
	case <-attached:
	case <-time.After(dockerLogStreamTestTimeout):
		t.Fatal("the fake daemon never saw the log stream request")
	}
	cancel()

	select {
	case <-returned:
	case <-time.After(dockerLogStreamTestTimeout):
		t.Fatal("streamContainerLogs() did not return after cancellation")
	}

	close(logCh)
	if len(errs) != 0 {
		t.Fatalf("a canceled request published %d failures, want none: %v", len(errs), <-errs)
	}
	close(errs)
}

// runningContainerFixture serves a container that never finishes: the wait releases
// ContainerWait with its headers but never completes, so only a cancellation ends the
// log stream — the state every Execute shutdown path has to handle.
type runningContainerFixture struct {
	// logStream serves the open log stream.
	logStream http.HandlerFunc
	// stop, when set, observes the container stop request.
	stop func()
}

func newRunningContainerWorkflow(t *testing.T, fixture runningContainerFixture) *DockerWorkflow {
	t.Helper()

	return newFakeDockerWorkflow(t, fakeDockerAPI{
		logs: fixture.logStream,
		wait: func(rw http.ResponseWriter, r *http.Request) {
			rw.Header().Set("Content-Type", "application/json")
			rw.WriteHeader(http.StatusOK)
			flushDockerLogStream(rw)
			// The body never completes: the wait stays pending.
			<-r.Context().Done()
		},
		stop: func(rw http.ResponseWriter, _ *http.Request) {
			if fixture.stop != nil {
				fixture.stop()
			}
			rw.WriteHeader(http.StatusNoContent)
		},
	})
}

// TestExecuteDoesNotCloseLogChannelsWhileStreamIsPublishing pins that the log channels
// close only after the streaming goroutine returns: with a slow consumer parking the
// forwarder, a send on a closed channel would take the worker process down.
func TestExecuteDoesNotCloseLogChannelsWhileStreamIsPublishing(t *testing.T) {
	t.Parallel()

	streamAborted := make(chan struct{})

	workflow := newRunningContainerWorkflow(t, runningContainerFixture{
		logStream: func(rw http.ResponseWriter, r *http.Request) {
			if _, err := rw.Write(dockerLogFrame(stdcopy.Stdout, "first streamed line\n")); err != nil {
				return
			}
			flushDockerLogStream(rw)

			// Every further line parks the forwarder on the slow consumer. The
			// loop is bounded so a broken fix cannot outlive the test.
			ticker := time.NewTicker(10 * time.Millisecond)
			defer ticker.Stop()
			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				select {
				case <-r.Context().Done():
					close(streamAborted)
					return
				case <-ticker.C:
					if _, err := rw.Write(dockerLogFrame(stdcopy.Stdout, "streamed line\n")); err != nil {
						return
					}
					flushDockerLogStream(rw)
				}
			}
		},
	})

	_, logs, errs, err := workflow.Execute(t.Context(), time.Second, "alpine:latest", []string{"sleep", "1"}, nil)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	collected := make(chan []*jobsmodel.JobLog, 1)
	streamClosed := make(chan struct{})
	go func() {
		defer close(streamClosed)
		var delivered []*jobsmodel.JobLog
		for log := range logs {
			delivered = append(delivered, log)
			// Deliberately slow: the forwarder parks mid-send for as long as
			// the execution takes to tear the stream down.
			time.Sleep(time.Second)
		}
		collected <- delivered
	}()

	result := awaitDockerLogStream(t, nil, errs)
	if len(result.errs) != 1 {
		t.Fatalf("Execute() published %d errors, want exactly one terminal failure: %v", len(result.errs), result.errs)
	}
	// The deadline and the aborted wait race for the select, so the terminal
	// failure is either the deadline or the execution error; both are valid.
	if message := result.errs[0].Error(); !strings.Contains(message, "container execution timed out") && !strings.Contains(message, "container execution error") {
		t.Fatalf("terminal failure = %q, want the execution deadline or error", message)
	}

	waitForSignal(t, streamAborted, "Execute() never tore the daemon log stream down")
	waitForSignal(t, streamClosed, "the log channel was never closed by Execute()")
	requireContiguousSequences(t, <-collected)
}

// TestExecuteTimeoutClosesChannelsWhenErrorConsumerAbandons covers the case the executor
// can produce: it stops draining both channels while the caller context is live, so the
// deadline must still publish, stop the container and close instead of blocking.
func TestExecuteTimeoutClosesChannelsWhenErrorConsumerAbandons(t *testing.T) {
	t.Parallel()

	containerStopped := make(chan struct{})
	var stopOnce atomic.Bool

	workflow := newRunningContainerWorkflow(t, runningContainerFixture{
		logStream: func(rw http.ResponseWriter, r *http.Request) {
			if _, err := rw.Write(dockerLogFrame(stdcopy.Stdout, "first streamed line\n")); err != nil {
				return
			}
			flushDockerLogStream(rw)
			<-r.Context().Done()
		},
		stop: func() {
			if stopOnce.CompareAndSwap(false, true) {
				close(containerStopped)
			}
		},
	})

	_, logs, errs, err := workflow.Execute(t.Context(), 300*time.Millisecond, "alpine:latest", []string{"sleep", "1"}, nil)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	// Abandon both channels: nothing reads logs or errs from here on.
	select {
	case <-containerStopped:
	case <-time.After(dockerLogStreamTestTimeout):
		t.Fatal("Execute() never stopped the container; it is blocked publishing to an abandoned channel")
	}

	published := make(chan []error, 1)
	go func() {
		for range logs { //nolint:revive // Draining without recording is the point: prove closure.
		}
		var collected []error
		for streamErr := range errs {
			collected = append(collected, streamErr)
		}
		published <- collected
	}()

	select {
	case collected := <-published:
		if len(collected) != 1 {
			t.Fatalf("published %d failures, want exactly the terminal one: %v", len(collected), collected)
		}
	case <-time.After(dockerLogStreamTestTimeout):
		t.Fatal("Execute() never closed the log channels")
	}
}

// TestExecuteReportsCallerCancellation guards the terminal outcome of an execution the
// caller gave up on: cancellation suppresses stream failures, yet the caller must still
// learn the execution ended as canceled. Reading the outcome afterwards also proves the
// shutdown completes with no consumer present.
func TestExecuteReportsCallerCancellation(t *testing.T) {
	t.Parallel()

	streamAborted := make(chan struct{})
	var abortOnce atomic.Bool

	workflow := newRunningContainerWorkflow(t, runningContainerFixture{
		logStream: func(rw http.ResponseWriter, r *http.Request) {
			if _, err := rw.Write(dockerLogFrame(stdcopy.Stdout, "first streamed line\n")); err != nil {
				return
			}
			flushDockerLogStream(rw)
			<-r.Context().Done()
			if abortOnce.CompareAndSwap(false, true) {
				close(streamAborted)
			}
		},
	})

	ctx, cancel := context.WithCancel(t.Context())
	_, logs, errs, err := workflow.Execute(ctx, time.Minute, "alpine:latest", []string{"sleep", "1"}, nil)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	// Drain the logs so the stream cannot stall on backpressure, and cancel
	// only once the first line proves the stream is attached.
	firstLineRead := make(chan struct{})
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		first := true
		for range logs {
			if first {
				first = false
				close(firstLineRead)
			}
		}
	}()

	waitForSignal(t, firstLineRead, "the log stream never delivered its first line")
	cancel()
	waitForSignal(t, drained, "Execute() never closed the log channel after cancellation")

	result := awaitDockerLogStream(t, nil, errs)
	if len(result.errs) != 1 {
		t.Fatalf("cancellation published %d failures, want exactly the terminal one: %v", len(result.errs), result.errs)
	}
	if code := status.Code(result.errs[0]); code != codes.Canceled {
		t.Fatalf("cancellation code = %s, want %s: %v", code, codes.Canceled, result.errs[0])
	}
	if !strings.Contains(result.errs[0].Error(), "container execution canceled") {
		t.Fatalf("cancellation error = %q, want the terminal cancellation message", result.errs[0])
	}

	waitForSignal(t, streamAborted, "Execute() never tore the daemon log stream down")
}

// TestExecuteReportsWaitFailureAsTerminalError covers the branch this hand-off rewrote:
// a wait that fails while the caller context is live becomes the terminal outcome, and
// the live log stream is torn down before both channels close.
func TestExecuteReportsWaitFailureAsTerminalError(t *testing.T) {
	t.Parallel()

	streamAttached, streamAborted := make(chan struct{}), make(chan struct{})
	var attachOnce atomic.Bool

	workflow := newFakeDockerWorkflow(t, fakeDockerAPI{
		logs: func(_ http.ResponseWriter, r *http.Request) {
			if attachOnce.CompareAndSwap(false, true) {
				close(streamAttached)
			}
			<-r.Context().Done()
			close(streamAborted)
		},
		wait: failWaitAfterStreamAttached(t, "wait aborted by the daemon", streamAttached),
	})

	_, logs, errs, err := workflow.Execute(t.Context(), time.Minute, "alpine:latest", []string{"sleep", "1"}, nil)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	drained := make(chan struct{})
	go func() {
		defer close(drained)
		for range logs { //nolint:revive // Draining without recording is the point: prove closure.
		}
	}()

	result := awaitDockerLogStream(t, nil, errs)
	if len(result.errs) != 1 {
		t.Fatalf("a failed wait published %d failures, want exactly the terminal one: %v", len(result.errs), result.errs)
	}
	if code := status.Code(result.errs[0]); code != codes.Aborted {
		t.Fatalf("wait-failure code = %s, want %s: %v", code, codes.Aborted, result.errs[0])
	}
	if !strings.Contains(result.errs[0].Error(), "container execution error") {
		t.Fatalf("wait-failure error = %q, want the terminal execution-error message", result.errs[0])
	}

	waitForSignal(t, streamAborted, "Execute() never tore the daemon log stream down after the wait failed")
	waitForSignal(t, drained, "Execute() never closed the log channel after the wait failed")
}

// TestExecutePublishesNothingWhenContainerIsGone pins the one Execute exit that publishes
// no terminal outcome: an externally removed container leaves nothing to report, and the
// channels just close after the join.
func TestExecutePublishesNothingWhenContainerIsGone(t *testing.T) {
	t.Parallel()

	streamAttached, streamAborted := make(chan struct{}), make(chan struct{})
	var attachOnce atomic.Bool

	workflow := newFakeDockerWorkflow(t, fakeDockerAPI{
		logs: func(_ http.ResponseWriter, r *http.Request) {
			if attachOnce.CompareAndSwap(false, true) {
				close(streamAttached)
			}
			<-r.Context().Done()
			close(streamAborted)
		},
		wait: failWaitAfterStreamAttached(t, "No such container: "+dockerLogTestContainerID, streamAttached),
	})

	_, logs, errs, err := workflow.Execute(t.Context(), time.Minute, "alpine:latest", []string{"sleep", "1"}, nil)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	drained := make(chan struct{})
	go func() {
		defer close(drained)
		for range logs { //nolint:revive // Draining without recording is the point: prove closure.
		}
	}()

	result := awaitDockerLogStream(t, nil, errs)
	if len(result.errs) != 0 {
		t.Fatalf("a removed container published %d failures, want none: %v", len(result.errs), result.errs)
	}

	waitForSignal(t, streamAborted, "Execute() never tore the daemon log stream down after the container vanished")
	waitForSignal(t, drained, "Execute() never closed the log channel after the container vanished")
}

// failWaitAfterStreamAttached releases the wait headers, waits for the log stream to
// attach, then ends the wait with a non-JSON body that the client reports as a wait
// error carrying that text. The headers come first so ContainerWait returns and the
// stream is ever opened; sequencing on the attachment makes the teardown under test
// the one that aborts a live stream.
func failWaitAfterStreamAttached(t *testing.T, body string, attached <-chan struct{}) http.HandlerFunc {
	t.Helper()

	return func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("Content-Type", "application/json")
		rw.WriteHeader(http.StatusOK)
		flushDockerLogStream(rw)

		select {
		case <-attached:
		case <-r.Context().Done():
			return
		}

		writeDockerTestResponse(t, rw, body)
	}
}
