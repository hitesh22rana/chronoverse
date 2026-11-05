package workflows_test

import (
	"context"
	"encoding/base64"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/go-playground/validator/v10"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	model "github.com/hitesh22rana/chronoverse/internal/model/workflows"
	service "github.com/hitesh22rana/chronoverse/internal/service/workflows"
	mocks "github.com/hitesh22rana/chronoverse/internal/service/workflows/mock"
	workflowspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/workflows"
)

const (
	cursorBoundaryOwner    = "00000000-0000-4000-8000-000000000011"
	cursorBoundaryWorkflow = "00000000-0000-4000-8000-000000000012"
)

type cursorBoundaryKey struct{}

func TestListWorkflowsDecodesCursorAndPreservesCaller(t *testing.T) {
	for _, kind := range []string{"HEARTBEAT", "CONTAINER"} {
		t.Run(kind, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			repo := mocks.NewMockRepository(ctrl)
			raw := cursorBoundaryWorkflow + "$2026-11-05T01:02:03.123456Z"
			req := &workflowspb.ListWorkflowsRequest{
				UserId: cursorBoundaryOwner, Cursor: base64.StdEncoding.EncodeToString([]byte(raw)),
				Filters: &workflowspb.ListWorkflowsFilters{Kind: kind, BuildStatus: "COMPLETED", Query: strings.Repeat("界", 100), IntervalMin: 1, IntervalMax: 5},
			}
			original := proto.Clone(req)
			filters := &model.ListWorkflowsFilters{Kind: kind, BuildStatus: "COMPLETED", Query: strings.Repeat("界", 100), IntervalMin: 1, IntervalMax: 5}
			cache := mocks.NewMockCache(ctrl)
			cache.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, status.Error(codes.NotFound, "cache miss"))
			background := make(chan (<-chan struct{}), 1)
			cache.EXPECT().Set(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(got context.Context, _ string, _ any, _ time.Duration) error {
				if got.Value(cursorBoundaryKey{}) != "caller" {
					t.Error("cache population lost caller values")
				}
				background <- got.Done()
				return nil
			})
			svc := service.New(validator.New(), repo, cache)
			ctx, cancel := context.WithTimeout(context.WithValue(t.Context(), cursorBoundaryKey{}, "caller"), 5*time.Second)
			defer cancel()
			repo.EXPECT().ListWorkflows(gomock.Any(), cursorBoundaryOwner, raw, filters).
				DoAndReturn(func(got context.Context, _, _ string, _ *model.ListWorkflowsFilters) (*model.ListWorkflowsResponse, error) {
					if got.Value(cursorBoundaryKey{}) != "caller" || got.Done() != ctx.Done() {
						t.Error("repository lost caller context")
					}
					return &model.ListWorkflowsResponse{}, nil
				})
			got, err := svc.ListWorkflows(ctx, req)
			if err != nil || got == nil || !reflect.DeepEqual(*got, model.ListWorkflowsResponse{}) {
				t.Fatalf("response=%+v err=%v", got, err)
			}
			if !proto.Equal(req, original) {
				t.Fatal("request was mutated")
			}
			select {
			case done := <-background:
				select {
				case <-done:
				case <-ctx.Done():
					t.Fatal("cache population did not release its context")
				}
			case <-ctx.Done():
				t.Fatal("cache population did not run")
			}
		})
	}
}

func TestListWorkflowsRejectsBadInputsBeforeRepository(t *testing.T) {
	for _, name := range []string{"cursor", "owner", "kind", "build status", "query", "interval", "negative minimum", "negative maximum"} {
		t.Run(name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			repo := mocks.NewMockRepository(ctrl)
			svc := service.New(validator.New(), repo, mocks.NewMockCache(ctrl))
			req := &workflowspb.ListWorkflowsRequest{UserId: cursorBoundaryOwner, Filters: &workflowspb.ListWorkflowsFilters{Kind: "HEARTBEAT"}}
			switch name {
			case "cursor":
				req.Cursor = "%%%"
			case "owner":
				req.UserId = ""
			case "build status":
				req.Filters.BuildStatus = "NOT_A_STATUS"
			case "negative minimum":
				req.Filters.IntervalMin = -1
			case "negative maximum":
				req.Filters.IntervalMax = -1
			case "kind":
				req.Filters.Kind = "NOT_A_KIND"
			case "query":
				req.Filters.Query = strings.Repeat("界", 101)
			case "interval":
				req.Filters.IntervalMin = 5
				req.Filters.IntervalMax = 1
			}
			before := proto.Clone(req)
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			got, err := svc.ListWorkflows(ctx, req)
			if got != nil || status.Code(err) != codes.InvalidArgument {
				t.Fatalf("response=%+v err=%v, want InvalidArgument", got, err)
			}
			if !proto.Equal(req, before) {
				t.Fatal("invalid request was mutated")
			}
		})
	}
}
