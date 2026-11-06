package workflows_test

import (
	"context"
	"encoding/base64"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/go-playground/validator/v10"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	model "github.com/hitesh22rana/chronoverse/internal/model/workflows"
	loggerpkg "github.com/hitesh22rana/chronoverse/internal/pkg/logger"
	service "github.com/hitesh22rana/chronoverse/internal/service/workflows"
	mocks "github.com/hitesh22rana/chronoverse/internal/service/workflows/mock"
	workflowspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/workflows"
)

type boundaryLogExporter struct{ messages chan string }

func (e *boundaryLogExporter) Export(ctx context.Context, records []sdklog.Record) error {
	for i := range records {
		select {
		case e.messages <- records[i].Body().AsString():
		case <-ctx.Done():
			return ctx.Err()
		default:
			return errors.New("unexpected extra log records")
		}
	}
	return nil
}
func (*boundaryLogExporter) Shutdown(context.Context) error   { return nil }
func (*boundaryLogExporter) ForceFlush(context.Context) error { return nil }

func TestListWorkflowsCacheWriteFailureDoesNotLoseResults(t *testing.T) {
	for _, fail := range []bool{false, true} {
		name, wantMessage := "success", "cached workflows list"
		if fail {
			name, wantMessage = "failure", "failed to cache workflows list"
		}
		t.Run(name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			repo, cache := mocks.NewMockRepository(ctrl), mocks.NewMockCache(ctrl)
			exporter := &boundaryLogExporter{messages: make(chan string, 4)}
			provider := sdklog.NewLoggerProvider(sdklog.WithProcessor(sdklog.NewSimpleProcessor(exporter)))
			t.Cleanup(func() {
				if err := provider.Shutdown(context.Background()); err != nil {
					t.Error(err)
				}
			})
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			ctx, _ = loggerpkg.Init(ctx, "workflow-boundary-test", provider)
			req := &workflowspb.ListWorkflowsRequest{UserId: cursorBoundaryOwner}
			original := proto.Clone(req)
			cache.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, status.Error(codes.NotFound, "miss"))
			record := model.WorkflowByUserIDResponse{
				ID: cursorBoundaryWorkflow, Name: "cache contract", Kind: "HEARTBEAT",
				Payload: `{"endpoint":"https://example.com"}`, WorkflowBuildStatus: "COMPLETED",
				Interval: 60, MaxConsecutiveJobFailuresAllowed: 3, Generation: 1,
				CreatedAt: time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC), UpdatedAt: time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC),
			}
			wantRecord := record
			cursor := base64.StdEncoding.EncodeToString([]byte("00000000-0000-4000-8000-000000000013$2026-11-05T00:00:00Z"))
			want := model.ListWorkflowsResponse{Workflows: []*model.WorkflowByUserIDResponse{&wantRecord}, Cursor: cursor}
			response := &model.ListWorkflowsResponse{Workflows: []*model.WorkflowByUserIDResponse{&record}, Cursor: cursor}
			repo.EXPECT().ListWorkflows(gomock.Any(), cursorBoundaryOwner, "", &model.ListWorkflowsFilters{}).Return(response, nil)
			released := make(chan (<-chan struct{}), 1)
			cache.EXPECT().Set(gomock.Any(), gomock.Any(), &want, gomock.Any()).DoAndReturn(func(bg context.Context, _ string, _ any, _ time.Duration) error {
				released <- bg.Done()
				if fail {
					return errors.New("cache unavailable")
				}
				return nil
			})
			got, err := service.New(validator.New(), repo, cache).ListWorkflows(ctx, req)
			if err != nil || got == nil || !reflect.DeepEqual(*got, want) {
				t.Fatalf("response=%+v err=%v", got, err)
			}
			if !proto.Equal(req, original) {
				t.Fatal("request was mutated")
			}
			assertBoundaryCacheOutcome(ctx, t, exporter.messages, wantMessage, released)
		})
	}
}

func TestListWorkflowsStopsOnCacheContextFailure(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		name, wantCode := "deadline", codes.DeadlineExceeded
		if canceled {
			name, wantCode = "canceled", codes.Canceled
		}
		t.Run(name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			repo, cache := mocks.NewMockRepository(ctrl), mocks.NewMockCache(ctrl)
			var ctx context.Context
			var cancel context.CancelFunc
			if canceled {
				ctx, cancel = context.WithCancel(t.Context())
			} else {
				ctx, cancel = context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
			}
			cancel()
			req := &workflowspb.ListWorkflowsRequest{UserId: cursorBoundaryOwner}
			original := proto.Clone(req)
			cache.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, ctx.Err())
			got, err := service.New(validator.New(), repo, cache).ListWorkflows(ctx, req)
			if got != nil || status.Code(err) != wantCode {
				t.Fatalf("response=%+v err=%v, want %s", got, err, wantCode)
			}
			if !proto.Equal(req, original) {
				t.Fatal("request was mutated")
			}
		})
	}
}

func assertBoundaryCacheOutcome(ctx context.Context, t *testing.T, messages <-chan string, wantMessage string, released <-chan (<-chan struct{})) {
	t.Helper()
	select {
	case message := <-messages:
		if message != wantMessage {
			t.Fatalf("log=%q, want %q", message, wantMessage)
		}
	case <-ctx.Done():
		t.Fatal("cache outcome was not logged")
	}
	select {
	case done := <-released:
		select {
		case <-done:
		case <-ctx.Done():
			t.Fatal("cache worker did not release its context")
		}
	case <-ctx.Done():
		t.Fatal("cache worker did not run")
	}
}
