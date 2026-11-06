//nolint:testpackage // Verifies server stream cancellation without transport races.
package jobs

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace/noop"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	jobsmock "github.com/hitesh22rana/chronoverse/internal/app/jobs/mock"
	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"
)

//nolint:containedctx // Mirrors the gRPC server-stream context contract.
type contractLogServerStream struct {
	grpc.ServerStream
	ctx  context.Context
	send func(*jobspb.Log) error
}

func (s contractLogServerStream) Context() context.Context   { return s.ctx }
func (s contractLogServerStream) Send(log *jobspb.Log) error { return s.send(log) }

func TestStreamHandlerReturnsDeadlineAndSendErrors(t *testing.T) {
	t.Parallel()
	request := &jobspb.StreamJobLogsRequest{Id: "job", WorkflowId: "workflow", UserId: "user"}
	for _, deadline := range []bool{false, true} {
		ctrl := gomock.NewController(t)
		service := jobsmock.NewMockService(ctrl)
		app := &Jobs{tp: noop.NewTracerProvider().Tracer("contract"), svc: service}
		ctx, cancel := context.WithCancel(t.Context())
		if deadline {
			cancel()
			ctx, cancel = context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
		}
		cancel()
		service.EXPECT().StreamJobLogs(gomock.Any(), request).Return(make(chan *jobsmodel.JobLog), nil)
		err := app.StreamJobLogs(request, contractLogServerStream{ctx: ctx, send: func(*jobspb.Log) error { t.Fatal("canceled stream sent data"); return nil }})
		if deadline {
			require.Equal(t, codes.DeadlineExceeded, status.Code(err))
		} else {
			require.NoError(t, err)
		}
	}
	ctrl := gomock.NewController(t)
	service := jobsmock.NewMockService(ctrl)
	app := &Jobs{tp: noop.NewTracerProvider().Tracer("contract"), svc: service}
	logs := make(chan *jobsmodel.JobLog, 2)
	logs <- nil
	logs <- &jobsmodel.JobLog{EventID: "event", Message: "line", SequenceNum: 9, Stream: "stderr", Timestamp: time.Date(2026, time.November, 6, 1, 2, 3, 0, time.UTC)}
	close(logs)
	service.EXPECT().StreamJobLogs(gomock.Any(), request).Return(logs, nil)
	failed := errors.New("transport send failed")
	calls := 0
	err := app.StreamJobLogs(request, contractLogServerStream{ctx: t.Context(), send: func(log *jobspb.Log) error {
		calls++
		require.True(t, proto.Equal(&jobspb.Log{EventId: "event", Message: "line", SequenceNum: 9, Stream: "stderr", Timestamp: "2026-11-06T01:02:03Z"}, log))
		return failed
	}})
	require.ErrorIs(t, err, failed)
	require.Equal(t, 1, calls)
}

func TestInternalJobPolicyRequiresQualifiedMethod(t *testing.T) {
	t.Parallel()
	for _, method := range []string{"", "CancelJob", "/CancelJob", "/jobs.Service/GetJob"} {
		require.False(t, isInternalAPI(method))
	}
	require.True(t, isInternalAPI("/jobs.Service/CancelJob"))
}
