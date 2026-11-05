package jobs_test

import (
	"context"
	"encoding/base64"
	"reflect"
	"testing"
	"time"

	"github.com/go-playground/validator/v10"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	model "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	service "github.com/hitesh22rana/chronoverse/internal/service/jobs"
	mocks "github.com/hitesh22rana/chronoverse/internal/service/jobs/mock"
	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"
)

const (
	cursorBoundaryOwner    = "00000000-0000-4000-8000-000000000011"
	cursorBoundaryWorkflow = "00000000-0000-4000-8000-000000000012"
)

type cursorBoundaryKey struct{}

func TestListJobsDecodesCursorAndPreservesCaller(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := mocks.NewMockRepository(ctrl)
	raw := cursorBoundaryWorkflow + "$2026-11-05T01:02:03.123456Z"
	req := &jobspb.ListJobsRequest{
		UserId: cursorBoundaryOwner, WorkflowId: cursorBoundaryWorkflow,
		Cursor:  base64.StdEncoding.EncodeToString([]byte(raw)),
		Filters: &jobspb.ListJobsFilters{Status: "RUNNING", Trigger: "AUTOMATIC"},
	}
	original := proto.Clone(req)
	filters := &model.ListJobsFilters{Status: "RUNNING", Trigger: "AUTOMATIC"}

	svc := service.New(validator.New(), repo, mocks.NewMockCache(ctrl))
	ctx, cancel := context.WithTimeout(context.WithValue(t.Context(), cursorBoundaryKey{}, "caller"), 5*time.Second)
	defer cancel()
	repo.EXPECT().ListJobs(gomock.Any(), cursorBoundaryWorkflow, cursorBoundaryOwner, raw, filters).
		DoAndReturn(func(got context.Context, _, _, _ string, _ *model.ListJobsFilters) (*model.ListJobsResponse, error) {
			if got.Value(cursorBoundaryKey{}) != "caller" || got.Done() != ctx.Done() {
				t.Error("repository lost caller context")
			}
			return &model.ListJobsResponse{}, nil
		})
	got, err := svc.ListJobs(ctx, req)
	if err != nil || got == nil || !reflect.DeepEqual(*got, model.ListJobsResponse{}) {
		t.Fatalf("response=%+v err=%v", got, err)
	}
	if !proto.Equal(req, original) {
		t.Fatal("request was mutated")
	}
}

func TestListJobsRejectsBadInputsBeforeRepository(t *testing.T) {
	for _, name := range []string{"cursor", "status"} {
		t.Run(name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			repo := mocks.NewMockRepository(ctrl)
			svc := service.New(validator.New(), repo, mocks.NewMockCache(ctrl))
			req := &jobspb.ListJobsRequest{UserId: cursorBoundaryOwner, WorkflowId: cursorBoundaryWorkflow, Filters: &jobspb.ListJobsFilters{}}
			switch name {
			case "cursor":
				req.Cursor = "%%%"
			case "status":
				req.Filters.Status = "UNKNOWN"
			}
			before := proto.Clone(req)
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			got, err := svc.ListJobs(ctx, req)
			if got != nil || status.Code(err) != codes.InvalidArgument {
				t.Fatalf("response=%+v err=%v, want InvalidArgument", got, err)
			}
			if !proto.Equal(req, before) {
				t.Fatal("invalid request was mutated")
			}
		})
	}
}
