package workflows_test

import (
	"database/sql"
	"testing"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	workflowsmodel "github.com/hitesh22rana/chronoverse/internal/model/workflows"
	"github.com/hitesh22rana/chronoverse/internal/service/workflows"
	workflowsmock "github.com/hitesh22rana/chronoverse/internal/service/workflows/mock"
	workflowspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/workflows"
)

func TestCreateWorkflow(t *testing.T) {
	ctrl := gomock.NewController(t)

	// Create a mock repository
	repo := workflowsmock.NewMockRepository(ctrl)
	cache := workflowsmock.NewMockCache(ctrl)

	// Create a new service
	s := workflows.New(validator.New(), repo, cache)

	type want struct {
		workflowID string
	}

	// Test cases
	tests := []struct {
		name  string
		req   *workflowspb.CreateWorkflowRequest
		mock  func(req *workflowspb.CreateWorkflowRequest)
		want  want
		isErr bool
	}{
		{
			name: "success",
			req: &workflowspb.CreateWorkflowRequest{
				UserId:                           "user1",
				Name:                             "workflow1",
				Payload:                          `{"headers": {"Content-Type": "application/json"}, "endpoint": "https://dummyjson.com/test"}`,
				Kind:                             "HEARTBEAT",
				Interval:                         1,
				MaxConsecutiveJobFailuresAllowed: 5,
			},
			mock: func(req *workflowspb.CreateWorkflowRequest) {
				repo.EXPECT().CreateWorkflow(
					gomock.Any(),
					req.GetUserId(),
					req.GetName(),
					req.GetPayload(),
					req.GetKind(),
					req.GetInterval(),
					req.GetMaxConsecutiveJobFailuresAllowed(),
				).Return(&workflowsmodel.GetWorkflowResponse{
					ID:                               "workflow_id",
					Name:                             "workflow1",
					Payload:                          `{"headers": {"Content-Type": "application/json"}, "endpoint": "https://dummyjson.com/test"}`,
					Kind:                             "HEARTBEAT",
					WorkflowBuildStatus:              "COMPLETED",
					Interval:                         1,
					ConsecutiveJobFailuresCount:      0,
					MaxConsecutiveJobFailuresAllowed: 5,
					CreatedAt:                        time.Now(),
					UpdatedAt:                        time.Now(),
					TerminatedAt: sql.NullTime{
						Time:  time.Now(),
						Valid: true,
					},
				}, nil)

				// Simulate a cache delete by pattern
				cache.EXPECT().DeleteByPattern(
					gomock.Any(),
					gomock.Any(),
				).Return(int64(0), nil).AnyTimes()

				// Simulate a cache set
				cache.EXPECT().Set(
					gomock.Any(),
					gomock.Any(),
					gomock.Any(),
					gomock.Any(),
				).Return(nil).AnyTimes()
			},
			want: want{
				workflowID: "workflow_id",
			},
			isErr: false,
		},
		{
			name: "error: missing required fields in request",
			req: &workflowspb.CreateWorkflowRequest{
				UserId:   "",
				Name:     "workflow1",
				Payload:  `{"headers": {"Content-Type": "application/json"}, "endpoint": "https://dummyjson.com/test"}`,
				Kind:     "",
				Interval: 1,
			},
			mock:  func(_ *workflowspb.CreateWorkflowRequest) {},
			want:  want{},
			isErr: true,
		},
		{
			name: "error: invalid payload",
			req: &workflowspb.CreateWorkflowRequest{
				UserId:                           "user1",
				Name:                             "workflow1",
				Payload:                          `invalid json`,
				Kind:                             "HEARTBEAT",
				Interval:                         1,
				MaxConsecutiveJobFailuresAllowed: 0,
			},
			mock:  func(_ *workflowspb.CreateWorkflowRequest) {},
			want:  want{},
			isErr: true,
		},
		{
			name: "error: internal server error",
			req: &workflowspb.CreateWorkflowRequest{
				UserId:                           "user1",
				Name:                             "workflow1",
				Payload:                          `{"headers": {"Content-Type": "application/json"}, "endpoint": "https://dummyjson.com/test"}`,
				Kind:                             "HEARTBEAT",
				Interval:                         1,
				MaxConsecutiveJobFailuresAllowed: 5,
			},
			mock: func(req *workflowspb.CreateWorkflowRequest) {
				repo.EXPECT().CreateWorkflow(
					gomock.Any(),
					req.GetUserId(),
					req.GetName(),
					req.GetPayload(),
					req.GetKind(),
					req.GetInterval(),
					req.GetMaxConsecutiveJobFailuresAllowed(),
				).Return(nil, status.Error(codes.Internal, "internal server error"))
			},
			want:  want{},
			isErr: true,
		},
	}

	defer ctrl.Finish()

	for _, tt := range tests {
		tt.mock(tt.req)
		t.Run(tt.name, func(t *testing.T) {
			workflowID, err := s.CreateWorkflow(t.Context(), tt.req)
			if tt.isErr {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			assert.Equal(t, workflowID, tt.want.workflowID)
		})
	}
}

func TestUpdateWorkflow(t *testing.T) {
	ctrl := gomock.NewController(t)

	// Create a mock repository
	repo := workflowsmock.NewMockRepository(ctrl)
	cache := workflowsmock.NewMockCache(ctrl)

	// Create a new service
	s := workflows.New(validator.New(), repo, cache)

	// Test cases
	tests := []struct {
		name  string
		req   *workflowspb.UpdateWorkflowRequest
		mock  func(req *workflowspb.UpdateWorkflowRequest)
		isErr bool
	}{
		{
			name: "success",
			req: &workflowspb.UpdateWorkflowRequest{
				Id:                               "workflow_id",
				UserId:                           "user1",
				Name:                             "workflow1",
				Payload:                          `{"headers": {"Content-Type": "application/json"}, "endpoint": "https://dummyjson.com/test"}`,
				Interval:                         1,
				MaxConsecutiveJobFailuresAllowed: 5,
			},
			mock: func(req *workflowspb.UpdateWorkflowRequest) {
				repo.EXPECT().UpdateWorkflow(
					gomock.Any(),
					req.GetId(),
					req.GetUserId(),
					req.GetName(),
					req.GetPayload(),
					req.GetInterval(),
					req.GetMaxConsecutiveJobFailuresAllowed(),
				).Return(nil)

				// Simulate a cache delete
				cache.EXPECT().Delete(
					gomock.Any(),
					gomock.Any(),
				).Return(nil).AnyTimes()

				// Simulate a cache delete by pattern
				cache.EXPECT().DeleteByPattern(
					gomock.Any(),
					gomock.Any(),
				).Return(int64(0), nil).AnyTimes()
			},
			isErr: false,
		},
		{
			name: "error: missing required fields in request",
			req: &workflowspb.UpdateWorkflowRequest{
				Id:       "",
				UserId:   "user1",
				Name:     "workflow1",
				Payload:  `{"headers": {"Content-Type": "application/json"}, "endpoint": "https://dummyjson.com/test"}`,
				Interval: 1,
			},
			mock:  func(_ *workflowspb.UpdateWorkflowRequest) {},
			isErr: true,
		},
		{
			name: "error: invalid payload",
			req: &workflowspb.UpdateWorkflowRequest{
				Id:                               "workflow_id",
				UserId:                           "user1",
				Name:                             "workflow1",
				Payload:                          `invalid json`,
				Interval:                         1,
				MaxConsecutiveJobFailuresAllowed: 0,
			},
			mock:  func(_ *workflowspb.UpdateWorkflowRequest) {},
			isErr: true,
		},
		{
			name: "error: workflow not found",
			req: &workflowspb.UpdateWorkflowRequest{
				Id:                               "invalid_workflow_id",
				UserId:                           "user1",
				Name:                             "workflow1",
				Payload:                          `{"headers": {"Content-Type": "application/json"}, "endpoint": "https://dummyjson.com/test"}`,
				Interval:                         1,
				MaxConsecutiveJobFailuresAllowed: 5,
			},
			mock: func(req *workflowspb.UpdateWorkflowRequest) {
				repo.EXPECT().UpdateWorkflow(
					gomock.Any(),
					req.GetId(),
					req.GetUserId(),
					req.GetName(),
					req.GetPayload(),
					req.GetInterval(),
					req.GetMaxConsecutiveJobFailuresAllowed(),
				).Return(status.Error(codes.NotFound, "workflow not found"))
			},
			isErr: true,
		},
		{
			name: "error: internal server error",
			req: &workflowspb.UpdateWorkflowRequest{
				Id:                               "workflow_id",
				UserId:                           "user1",
				Name:                             "workflow1",
				Payload:                          `{"headers": {"Content-Type": "application/json"}, "endpoint": "https://dummyjson.com/test"}`,
				Interval:                         1,
				MaxConsecutiveJobFailuresAllowed: 5,
			},
			mock: func(req *workflowspb.UpdateWorkflowRequest) {
				repo.EXPECT().UpdateWorkflow(
					gomock.Any(),
					req.GetId(),
					req.GetUserId(),
					req.GetName(),
					req.GetPayload(),
					req.GetInterval(),
					req.GetMaxConsecutiveJobFailuresAllowed(),
				).Return(status.Error(codes.Internal, "internal server error"))
			},
			isErr: true,
		},
	}

	defer ctrl.Finish()

	for _, tt := range tests {
		tt.mock(tt.req)
		t.Run(tt.name, func(t *testing.T) {
			err := s.UpdateWorkflow(t.Context(), tt.req)
			if tt.isErr {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
		})
	}
}

func TestUpdateWorkflowBuildStatus(t *testing.T) {
	ctrl := gomock.NewController(t)

	// Create a mock repository
	repo := workflowsmock.NewMockRepository(ctrl)
	cache := workflowsmock.NewMockCache(ctrl)

	// Create a new service
	s := workflows.New(validator.New(), repo, cache)

	// Test cases
	tests := []struct {
		name  string
		req   *workflowspb.UpdateWorkflowBuildStatusRequest
		mock  func(req *workflowspb.UpdateWorkflowBuildStatusRequest)
		isErr bool
	}{
		{
			name: "success",
			req: &workflowspb.UpdateWorkflowBuildStatusRequest{
				Id:          "workflow_id",
				UserId:      "user_id",
				BuildStatus: "COMPLETED",
			},
			mock: func(req *workflowspb.UpdateWorkflowBuildStatusRequest) {
				repo.EXPECT().UpdateWorkflowBuildStatus(
					gomock.Any(),
					req.GetId(),
					req.GetUserId(),
					req.GetBuildStatus(),
				).Return(nil)

				// Simulate a cache delete
				cache.EXPECT().Delete(
					gomock.Any(),
					gomock.Any(),
				).Return(nil).AnyTimes()

				// Simulate a cache delete by pattern
				cache.EXPECT().DeleteByPattern(
					gomock.Any(),
					gomock.Any(),
				).Return(int64(0), nil).AnyTimes()
			},
			isErr: false,
		},
		{
			name: "error: missing workflow ID",
			req: &workflowspb.UpdateWorkflowBuildStatusRequest{
				Id:          "",
				UserId:      "user_id",
				BuildStatus: "COMPLETED",
			},
			mock:  func(_ *workflowspb.UpdateWorkflowBuildStatusRequest) {},
			isErr: true,
		},
		{
			name: "error: missing build status",
			req: &workflowspb.UpdateWorkflowBuildStatusRequest{
				Id:          "workflow_id",
				UserId:      "user_id",
				BuildStatus: "",
			},
			mock:  func(_ *workflowspb.UpdateWorkflowBuildStatusRequest) {},
			isErr: true,
		},
		{
			name: "error: invalid build status",
			req: &workflowspb.UpdateWorkflowBuildStatusRequest{
				Id:          "workflow_id",
				UserId:      "user_id",
				BuildStatus: "INVALID",
			},
			mock:  func(_ *workflowspb.UpdateWorkflowBuildStatusRequest) {},
			isErr: true,
		},
		{
			name: "error: workflow not found",
			req: &workflowspb.UpdateWorkflowBuildStatusRequest{
				Id:          "invalid_workflow_id",
				UserId:      "user_id",
				BuildStatus: "COMPLETED",
			},
			mock: func(req *workflowspb.UpdateWorkflowBuildStatusRequest) {
				repo.EXPECT().UpdateWorkflowBuildStatus(
					gomock.Any(),
					req.GetId(),
					req.GetUserId(),
					req.GetBuildStatus(),
				).Return(status.Error(codes.NotFound, "workflow not found"))
			},
			isErr: true,
		},
		{
			name: "error: internal server error",
			req: &workflowspb.UpdateWorkflowBuildStatusRequest{
				Id:          "workflow_id",
				UserId:      "user_id",
				BuildStatus: "COMPLETED",
			},
			mock: func(req *workflowspb.UpdateWorkflowBuildStatusRequest) {
				repo.EXPECT().UpdateWorkflowBuildStatus(
					gomock.Any(),
					req.GetId(),
					req.GetUserId(),
					req.GetBuildStatus(),
				).Return(status.Error(codes.Internal, "internal server error"))
			},
			isErr: true,
		},
	}

	defer ctrl.Finish()

	for _, tt := range tests {
		tt.mock(tt.req)
		t.Run(tt.name, func(t *testing.T) {
			err := s.UpdateWorkflowBuildStatus(t.Context(), tt.req)
			if tt.isErr {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
		})
	}
}

func TestGetWorkflow(t *testing.T) {
	ctrl := gomock.NewController(t)

	// Create a mock repository
	repo := workflowsmock.NewMockRepository(ctrl)
	cache := workflowsmock.NewMockCache(ctrl)

	// Create a new service
	s := workflows.New(validator.New(), repo, cache)

	type want struct {
		*workflowsmodel.GetWorkflowResponse
	}

	var (
		createdAt    = time.Now()
		updatedAt    = time.Now()
		terminatedAt = sql.NullTime{
			Time:  time.Now(),
			Valid: true,
		}
	)

	// Test cases
	tests := []struct {
		name  string
		req   *workflowspb.GetWorkflowRequest
		mock  func(req *workflowspb.GetWorkflowRequest)
		want  want
		isErr bool
	}{
		{
			name: "success: no cache hit",
			req: &workflowspb.GetWorkflowRequest{
				Id:     "workflow_id",
				UserId: "user_id",
			},
			mock: func(req *workflowspb.GetWorkflowRequest) {
				// Simulate a cache miss
				cache.EXPECT().Get(
					gomock.Any(),
					gomock.Any(),
					gomock.Any(),
				).Return(nil, status.Error(codes.NotFound, "cache miss"))

				repo.EXPECT().GetWorkflow(
					gomock.Any(),
					req.GetId(),
					req.GetUserId(),
				).Return(&workflowsmodel.GetWorkflowResponse{
					ID:                               "workflow_id",
					Name:                             "workflow1",
					Payload:                          `{"headers": {"Content-Type": "application/json"}, "endpoint": "https://dummyjson.com/test"}`,
					Kind:                             "HEARTBEAT",
					WorkflowBuildStatus:              "COMPLETED",
					Interval:                         1,
					ConsecutiveJobFailuresCount:      0,
					MaxConsecutiveJobFailuresAllowed: 5,
					CreatedAt:                        createdAt,
					UpdatedAt:                        updatedAt,
					TerminatedAt:                     terminatedAt,
				}, nil)

				// Simulate a cache delete by pattern
				cache.EXPECT().DeleteByPattern(
					gomock.Any(),
					gomock.Any(),
				).Return(int64(0), nil).AnyTimes()

				// Simulate a cache set
				cache.EXPECT().Set(
					gomock.Any(),
					gomock.Any(),
					gomock.Any(),
					gomock.Any(),
				).Return(nil).AnyTimes()
			},
			want: want{
				&workflowsmodel.GetWorkflowResponse{
					ID:                               "workflow_id",
					Name:                             "workflow1",
					Payload:                          `{"headers": {"Content-Type": "application/json"}, "endpoint": "https://dummyjson.com/test"}`,
					Kind:                             "HEARTBEAT",
					WorkflowBuildStatus:              "COMPLETED",
					Interval:                         1,
					ConsecutiveJobFailuresCount:      0,
					MaxConsecutiveJobFailuresAllowed: 5,
					CreatedAt:                        createdAt,
					UpdatedAt:                        updatedAt,
					TerminatedAt:                     terminatedAt,
				},
			},
			isErr: false,
		},
		{
			name: "success: cache hit",
			req: &workflowspb.GetWorkflowRequest{
				Id:     "workflow_id",
				UserId: "user_id",
			},
			mock: func(_ *workflowspb.GetWorkflowRequest) {
				// Simulate a cache hit
				cache.EXPECT().Get(
					gomock.Any(),
					gomock.Any(),
					gomock.Any(),
				).Return(&workflowsmodel.GetWorkflowResponse{
					ID:                               "workflow_id",
					Name:                             "workflow1",
					Payload:                          `{"headers": {"Content-Type": "application/json"}, "endpoint": "https://dummyjson.com/test"}`,
					Kind:                             "HEARTBEAT",
					WorkflowBuildStatus:              "COMPLETED",
					Interval:                         1,
					ConsecutiveJobFailuresCount:      0,
					MaxConsecutiveJobFailuresAllowed: 5,
					CreatedAt:                        createdAt,
					UpdatedAt:                        updatedAt,
					TerminatedAt:                     terminatedAt,
				}, nil)
			},
			want: want{
				&workflowsmodel.GetWorkflowResponse{
					ID:                               "workflow_id",
					Name:                             "workflow1",
					Payload:                          `{"headers": {"Content-Type": "application/json"}, "endpoint": "https://dummyjson.com/test"}`,
					Kind:                             "HEARTBEAT",
					WorkflowBuildStatus:              "COMPLETED",
					Interval:                         1,
					ConsecutiveJobFailuresCount:      0,
					MaxConsecutiveJobFailuresAllowed: 5,
					CreatedAt:                        createdAt,
					UpdatedAt:                        updatedAt,
					TerminatedAt:                     terminatedAt,
				},
			},
			isErr: false,
		},
		{
			name: "error: missing required fields in request",
			req: &workflowspb.GetWorkflowRequest{
				Id:     "",
				UserId: "",
			},
			mock:  func(_ *workflowspb.GetWorkflowRequest) {},
			want:  want{},
			isErr: true,
		},
		{
			name: "error: invalid user",
			req: &workflowspb.GetWorkflowRequest{
				Id:     "workflow_id",
				UserId: "invalid_user_id",
			},
			mock: func(req *workflowspb.GetWorkflowRequest) {
				// Simulate a cache miss
				cache.EXPECT().Get(
					gomock.Any(),
					gomock.Any(),
					gomock.Any(),
				).Return(nil, status.Error(codes.NotFound, "cache miss"))

				repo.EXPECT().GetWorkflow(
					gomock.Any(),
					req.GetId(),
					req.GetUserId(),
				).Return(nil, status.Error(codes.NotFound, "invalid user"))
			},
			want:  want{},
			isErr: true,
		},
		{
			name: "error: workflow not found",
			req: &workflowspb.GetWorkflowRequest{
				Id:     "invalid_workflow_id",
				UserId: "user_id",
			},
			mock: func(req *workflowspb.GetWorkflowRequest) {
				// Simulate a cache miss
				cache.EXPECT().Get(
					gomock.Any(),
					gomock.Any(),
					gomock.Any(),
				).Return(nil, status.Error(codes.NotFound, "cache miss"))

				repo.EXPECT().GetWorkflow(
					gomock.Any(),
					req.GetId(),
					req.GetUserId(),
				).Return(nil, status.Error(codes.NotFound, "workflow not found"))
			},
			want:  want{},
			isErr: true,
		},
		{
			name: "error: internal server error",
			req: &workflowspb.GetWorkflowRequest{
				Id:     "workflow_id",
				UserId: "user_id",
			},
			mock: func(req *workflowspb.GetWorkflowRequest) {
				// Simulate a cache miss
				cache.EXPECT().Get(
					gomock.Any(),
					gomock.Any(),
					gomock.Any(),
				).Return(nil, status.Error(codes.NotFound, "cache miss"))

				repo.EXPECT().GetWorkflow(
					gomock.Any(),
					req.GetId(),
					req.GetUserId(),
				).Return(nil, status.Error(codes.Internal, "internal server error"))
			},
			want:  want{},
			isErr: true,
		},
	}

	defer ctrl.Finish()

	for _, tt := range tests {
		tt.mock(tt.req)
		t.Run(tt.name, func(t *testing.T) {
			workflow, err := s.GetWorkflow(t.Context(), tt.req)
			if tt.isErr {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			assert.Equal(t, workflow, tt.want.GetWorkflowResponse)
		})
	}
}

func TestGetWorkflowByID(t *testing.T) {
	ctrl := gomock.NewController(t)

	// Create a mock repository
	repo := workflowsmock.NewMockRepository(ctrl)
	cache := workflowsmock.NewMockCache(ctrl)

	// Create a new service
	s := workflows.New(validator.New(), repo, cache)

	type want struct {
		*workflowsmodel.GetWorkflowByIDResponse
	}

	var (
		createdAt    = time.Now()
		updatedAt    = time.Now()
		terminatedAt = sql.NullTime{
			Time:  time.Now(),
			Valid: true,
		}
	)

	// Test cases
	tests := []struct {
		name  string
		req   *workflowspb.GetWorkflowByIDRequest
		mock  func(req *workflowspb.GetWorkflowByIDRequest)
		want  want
		isErr bool
	}{
		{
			name: "success",
			req: &workflowspb.GetWorkflowByIDRequest{
				Id: "workflow_id",
			},
			mock: func(req *workflowspb.GetWorkflowByIDRequest) {
				repo.EXPECT().GetWorkflowByID(
					gomock.Any(),
					req.GetId(),
				).Return(&workflowsmodel.GetWorkflowByIDResponse{
					ID:                               "workflow_id",
					UserID:                           "user1",
					Name:                             "workflow1",
					Payload:                          `{"headers": {"Content-Type": "application/json"}, "endpoint": "https://dummyjson.com/test"}`,
					Kind:                             "HEARTBEAT",
					WorkflowBuildStatus:              "COMPLETED",
					Interval:                         1,
					ConsecutiveJobFailuresCount:      0,
					MaxConsecutiveJobFailuresAllowed: 5,
					CreatedAt:                        createdAt,
					UpdatedAt:                        updatedAt,
					TerminatedAt:                     terminatedAt,
				}, nil)
			},
			want: want{
				&workflowsmodel.GetWorkflowByIDResponse{
					ID:                               "workflow_id",
					UserID:                           "user1",
					Name:                             "workflow1",
					Payload:                          `{"headers": {"Content-Type": "application/json"}, "endpoint": "https://dummyjson.com/test"}`,
					Kind:                             "HEARTBEAT",
					WorkflowBuildStatus:              "COMPLETED",
					Interval:                         1,
					ConsecutiveJobFailuresCount:      0,
					MaxConsecutiveJobFailuresAllowed: 5,
					CreatedAt:                        createdAt,
					UpdatedAt:                        updatedAt,
					TerminatedAt:                     terminatedAt,
				},
			},
			isErr: false,
		},
		{
			name: "error: missing workflow ID",
			req: &workflowspb.GetWorkflowByIDRequest{
				Id: "",
			},
			mock:  func(_ *workflowspb.GetWorkflowByIDRequest) {},
			want:  want{},
			isErr: true,
		},
		{
			name: "error: workflow not found",
			req: &workflowspb.GetWorkflowByIDRequest{
				Id: "invalid_workflow_id",
			},
			mock: func(req *workflowspb.GetWorkflowByIDRequest) {
				repo.EXPECT().GetWorkflowByID(
					gomock.Any(),
					req.GetId(),
				).Return(nil, status.Error(codes.NotFound, "workflow not found"))
			},
			want:  want{},
			isErr: true,
		},
		{
			name: "error: internal server error",
			req: &workflowspb.GetWorkflowByIDRequest{
				Id: "workflow_id",
			},
			mock: func(req *workflowspb.GetWorkflowByIDRequest) {
				repo.EXPECT().GetWorkflowByID(
					gomock.Any(),
					req.GetId(),
				).Return(nil, status.Error(codes.Internal, "internal server error"))
			},
			want:  want{},
			isErr: true,
		},
	}

	defer ctrl.Finish()

	for _, tt := range tests {
		tt.mock(tt.req)
		t.Run(tt.name, func(t *testing.T) {
			workflow, err := s.GetWorkflowByID(t.Context(), tt.req)
			if tt.isErr {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			assert.Equal(t, tt.want.GetWorkflowByIDResponse, workflow)
		})
	}
}

func TestIncrementWorkflowConsecutiveJobFailuresCount(t *testing.T) {
	ctrl := gomock.NewController(t)

	// Create a mock repository
	repo := workflowsmock.NewMockRepository(ctrl)
	cache := workflowsmock.NewMockCache(ctrl)

	// Create a new service
	s := workflows.New(validator.New(), repo, cache)

	type want struct {
		thresholdReached bool
	}

	// Test cases
	tests := []struct {
		name  string
		req   *workflowspb.IncrementWorkflowConsecutiveJobFailuresCountRequest
		mock  func(req *workflowspb.IncrementWorkflowConsecutiveJobFailuresCountRequest)
		want  want
		isErr bool
	}{
		{
			name: "success: threshold not reached",
			req: &workflowspb.IncrementWorkflowConsecutiveJobFailuresCountRequest{
				Id:     "workflow_id",
				UserId: "user_id",
			},
			mock: func(req *workflowspb.IncrementWorkflowConsecutiveJobFailuresCountRequest) {
				repo.EXPECT().IncrementWorkflowConsecutiveJobFailuresCount(
					gomock.Any(),
					req.GetId(),
					req.GetUserId(),
				).Return(false, nil)

				// Simulate a cache delete
				cache.EXPECT().Delete(
					gomock.Any(),
					gomock.Any(),
				).Return(nil).AnyTimes()

				// Simulate a cache delete by pattern
				cache.EXPECT().DeleteByPattern(
					gomock.Any(),
					gomock.Any(),
				).Return(int64(0), nil).AnyTimes()
			},
			want: want{
				thresholdReached: false,
			},
			isErr: false,
		},
		{
			name: "success: threshold reached",
			req: &workflowspb.IncrementWorkflowConsecutiveJobFailuresCountRequest{
				Id:     "workflow_id",
				UserId: "user_id",
			},
			mock: func(req *workflowspb.IncrementWorkflowConsecutiveJobFailuresCountRequest) {
				repo.EXPECT().IncrementWorkflowConsecutiveJobFailuresCount(
					gomock.Any(),
					req.GetId(),
					req.GetUserId(),
				).Return(true, nil)
			},
			want: want{
				thresholdReached: true,
			},
			isErr: false,
		},
		{
			name: "error: missing workflow ID",
			req: &workflowspb.IncrementWorkflowConsecutiveJobFailuresCountRequest{
				Id:     "",
				UserId: "user_id",
			},
			mock:  func(_ *workflowspb.IncrementWorkflowConsecutiveJobFailuresCountRequest) {},
			want:  want{},
			isErr: true,
		},
		{
			name: "error: workflow not found",
			req: &workflowspb.IncrementWorkflowConsecutiveJobFailuresCountRequest{
				Id:     "invalid_workflow_id",
				UserId: "user_id",
			},
			mock: func(req *workflowspb.IncrementWorkflowConsecutiveJobFailuresCountRequest) {
				repo.EXPECT().IncrementWorkflowConsecutiveJobFailuresCount(
					gomock.Any(),
					req.GetId(),
					req.GetUserId(),
				).Return(false, status.Error(codes.NotFound, "workflow not found"))
			},
			want: want{
				thresholdReached: false,
			},
			isErr: true,
		},
		{
			name: "error: internal server error",
			req: &workflowspb.IncrementWorkflowConsecutiveJobFailuresCountRequest{
				Id:     "workflow_id",
				UserId: "user_id",
			},
			mock: func(req *workflowspb.IncrementWorkflowConsecutiveJobFailuresCountRequest) {
				repo.EXPECT().IncrementWorkflowConsecutiveJobFailuresCount(
					gomock.Any(),
					req.GetId(),
					req.GetUserId(),
				).Return(false, status.Error(codes.Internal, "internal server error"))
			},
			want: want{
				thresholdReached: false,
			},
			isErr: true,
		},
	}

	defer ctrl.Finish()

	for _, tt := range tests {
		tt.mock(tt.req)
		t.Run(tt.name, func(t *testing.T) {
			thresholdReached, err := s.IncrementWorkflowConsecutiveJobFailuresCount(t.Context(), tt.req)
			if tt.isErr {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			assert.Equal(t, thresholdReached, tt.want.thresholdReached)
		})
	}
}

func TestResetWorkflowConsecutiveJobFailuresCount(t *testing.T) {
	ctrl := gomock.NewController(t)

	// Create a mock repository
	repo := workflowsmock.NewMockRepository(ctrl)
	cache := workflowsmock.NewMockCache(ctrl)

	// Create a new service
	s := workflows.New(validator.New(), repo, cache)

	// Test cases
	tests := []struct {
		name  string
		req   *workflowspb.ResetWorkflowConsecutiveJobFailuresCountRequest
		mock  func(req *workflowspb.ResetWorkflowConsecutiveJobFailuresCountRequest)
		isErr bool
	}{
		{
			name: "success",
			req: &workflowspb.ResetWorkflowConsecutiveJobFailuresCountRequest{
				Id:     "workflow_id",
				UserId: "user_id",
			},
			mock: func(req *workflowspb.ResetWorkflowConsecutiveJobFailuresCountRequest) {
				repo.EXPECT().ResetWorkflowConsecutiveJobFailuresCount(
					gomock.Any(),
					req.GetId(),
					req.GetUserId(),
				).Return(nil)

				// Simulate a cache delete
				cache.EXPECT().Delete(
					gomock.Any(),
					gomock.Any(),
				).Return(nil).AnyTimes()

				// Simulate a cache delete by pattern
				cache.EXPECT().DeleteByPattern(
					gomock.Any(),
					gomock.Any(),
				).Return(int64(0), nil).AnyTimes()
			},
			isErr: false,
		},
		{
			name: "error: missing workflow ID",
			req: &workflowspb.ResetWorkflowConsecutiveJobFailuresCountRequest{
				Id:     "",
				UserId: "user_id",
			},
			mock:  func(_ *workflowspb.ResetWorkflowConsecutiveJobFailuresCountRequest) {},
			isErr: true,
		},
		{
			name: "error: workflow not found",
			req: &workflowspb.ResetWorkflowConsecutiveJobFailuresCountRequest{
				Id:     "invalid_workflow_id",
				UserId: "user_id",
			},
			mock: func(req *workflowspb.ResetWorkflowConsecutiveJobFailuresCountRequest) {
				repo.EXPECT().ResetWorkflowConsecutiveJobFailuresCount(
					gomock.Any(),
					req.GetId(),
					req.GetUserId(),
				).Return(status.Error(codes.NotFound, "workflow not found"))
			},
			isErr: true,
		},
		{
			name: "error: internal server error",
			req: &workflowspb.ResetWorkflowConsecutiveJobFailuresCountRequest{
				Id:     "workflow_id",
				UserId: "user_id",
			},
			mock: func(req *workflowspb.ResetWorkflowConsecutiveJobFailuresCountRequest) {
				repo.EXPECT().ResetWorkflowConsecutiveJobFailuresCount(
					gomock.Any(),
					req.GetId(),
					req.GetUserId(),
				).Return(status.Error(codes.Internal, "internal server error"))
			},
			isErr: true,
		},
	}

	defer ctrl.Finish()

	for _, tt := range tests {
		tt.mock(tt.req)
		t.Run(tt.name, func(t *testing.T) {
			err := s.ResetWorkflowConsecutiveJobFailuresCount(t.Context(), tt.req)
			if tt.isErr {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
		})
	}
}

func TestTerminateWorkflow(t *testing.T) {
	ctrl := gomock.NewController(t)

	// Create a mock repository
	repo := workflowsmock.NewMockRepository(ctrl)
	cache := workflowsmock.NewMockCache(ctrl)

	// Create a new service
	s := workflows.New(validator.New(), repo, cache)

	// Test cases
	tests := []struct {
		name  string
		req   *workflowspb.TerminateWorkflowRequest
		mock  func(req *workflowspb.TerminateWorkflowRequest)
		isErr bool
	}{
		{
			name: "success",
			req: &workflowspb.TerminateWorkflowRequest{
				Id:     "workflow_id",
				UserId: "user_id",
			},
			mock: func(req *workflowspb.TerminateWorkflowRequest) {
				repo.EXPECT().TerminateWorkflow(
					gomock.Any(),
					req.GetId(),
					req.GetUserId(),
				).Return(nil)

				// Simulate a cache delete
				cache.EXPECT().Delete(
					gomock.Any(),
					gomock.Any(),
				).Return(nil).AnyTimes()

				// Simulate a cache delete by pattern
				cache.EXPECT().DeleteByPattern(
					gomock.Any(),
					gomock.Any(),
				).Return(int64(0), nil).AnyTimes()
			},
			isErr: false,
		},
		{
			name: "error: missing workflow ID",
			req: &workflowspb.TerminateWorkflowRequest{
				Id: "",
			},
			mock:  func(_ *workflowspb.TerminateWorkflowRequest) {},
			isErr: true,
		},
		{
			name: "error: workflow not found",
			req: &workflowspb.TerminateWorkflowRequest{
				Id:     "invalid_workflow_id",
				UserId: "user_id",
			},
			mock: func(req *workflowspb.TerminateWorkflowRequest) {
				repo.EXPECT().TerminateWorkflow(
					gomock.Any(),
					req.GetId(),
					req.GetUserId(),
				).Return(status.Error(codes.NotFound, "workflow not found"))
			},
			isErr: true,
		},
		{
			name: "error: workflow not owned by user",
			req: &workflowspb.TerminateWorkflowRequest{
				Id:     "workflow_id",
				UserId: "invalid_user_id",
			},
			mock: func(req *workflowspb.TerminateWorkflowRequest) {
				repo.EXPECT().TerminateWorkflow(
					gomock.Any(),
					req.GetId(),
					req.GetUserId(),
				).Return(status.Error(codes.NotFound, "workflow not found or not owned by user"))
			},
			isErr: true,
		},
		{
			name: "error: internal server error",
			req: &workflowspb.TerminateWorkflowRequest{
				Id:     "workflow_id",
				UserId: "user_id",
			},
			mock: func(req *workflowspb.TerminateWorkflowRequest) {
				repo.EXPECT().TerminateWorkflow(
					gomock.Any(),
					req.GetId(),
					req.GetUserId(),
				).Return(status.Error(codes.Internal, "internal server error"))
			},
			isErr: true,
		},
	}

	defer ctrl.Finish()

	for _, tt := range tests {
		tt.mock(tt.req)
		t.Run(tt.name, func(t *testing.T) {
			err := s.TerminateWorkflow(t.Context(), tt.req)
			if tt.isErr {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
				return
			}
		})
	}
}

func TestListWorkflows(t *testing.T) {
	ctrl := gomock.NewController(t)

	// Create a mock repository
	repo := workflowsmock.NewMockRepository(ctrl)
	cache := workflowsmock.NewMockCache(ctrl)

	// Create a new service
	s := workflows.New(validator.New(), repo, cache)

	type want struct {
		*workflowsmodel.ListWorkflowsResponse
	}

	var (
		createdAt    = time.Now()
		updatedAt    = time.Now()
		terminatedAt = sql.NullTime{
			Time:  time.Now(),
			Valid: true,
		}
	)

	// Test cases
	tests := []struct {
		name  string
		req   *workflowspb.ListWorkflowsRequest
		mock  func(req *workflowspb.ListWorkflowsRequest)
		want  want
		isErr bool
	}{
		{
			name: "success: no cache hit",
			req: &workflowspb.ListWorkflowsRequest{
				UserId: "user1",
				Cursor: "",
			},
			mock: func(req *workflowspb.ListWorkflowsRequest) {
				// Simulate a cache miss
				cache.EXPECT().Get(
					gomock.Any(),
					gomock.Any(),
					gomock.Any(),
				).Return(nil, status.Error(codes.NotFound, "cache miss"))

				repo.EXPECT().ListWorkflows(
					gomock.Any(),
					req.GetUserId(),
					req.GetCursor(),
					gomock.Any(),
				).Return(&workflowsmodel.ListWorkflowsResponse{
					Workflows: []*workflowsmodel.WorkflowByUserIDResponse{
						{
							ID:                               "workflow_id",
							Name:                             "workflow1",
							Payload:                          `{"headers": {"Content-Type": "application/json"}, "endpoint": "https://dummyjson.com/test"}`,
							Kind:                             "HEARTBEAT",
							WorkflowBuildStatus:              "COMPLETED",
							Interval:                         1,
							ConsecutiveJobFailuresCount:      0,
							MaxConsecutiveJobFailuresAllowed: 5,
							CreatedAt:                        createdAt,
							UpdatedAt:                        updatedAt,
							TerminatedAt:                     terminatedAt,
						},
					},
					Cursor: "",
				}, nil)

				// Simulate a cache set
				cache.EXPECT().Set(
					gomock.Any(),
					gomock.Any(),
					gomock.Any(),
					gomock.Any(),
				).Return(nil).AnyTimes()
			},
			want: want{
				&workflowsmodel.ListWorkflowsResponse{
					Workflows: []*workflowsmodel.WorkflowByUserIDResponse{
						{
							ID:                               "workflow_id",
							Name:                             "workflow1",
							Payload:                          `{"headers": {"Content-Type": "application/json"}, "endpoint": "https://dummyjson.com/test"}`,
							Kind:                             "HEARTBEAT",
							WorkflowBuildStatus:              "COMPLETED",
							Interval:                         1,
							ConsecutiveJobFailuresCount:      0,
							MaxConsecutiveJobFailuresAllowed: 5,
							CreatedAt:                        createdAt,
							UpdatedAt:                        updatedAt,
							TerminatedAt:                     terminatedAt,
						},
					},
					Cursor: "",
				},
			},
			isErr: false,
		},
		{
			name: "success: cache hit",
			req: &workflowspb.ListWorkflowsRequest{
				UserId: "user1",
				Cursor: "",
			},
			mock: func(_ *workflowspb.ListWorkflowsRequest) {
				// Simulate a cache hit using a pre-defined response
				cache.EXPECT().Get(
					gomock.Any(),
					gomock.Any(),
					gomock.Any(),
				).Return(&workflowsmodel.ListWorkflowsResponse{
					Workflows: []*workflowsmodel.WorkflowByUserIDResponse{
						{
							ID:                               "workflow_id",
							Name:                             "workflow1",
							Payload:                          `{"headers": {"Content-Type": "application/json"}, "endpoint": "https://dummyjson.com/test"}`,
							Kind:                             "HEARTBEAT",
							WorkflowBuildStatus:              "COMPLETED",
							Interval:                         1,
							ConsecutiveJobFailuresCount:      0,
							MaxConsecutiveJobFailuresAllowed: 5,
							CreatedAt:                        createdAt,
							UpdatedAt:                        updatedAt,
							TerminatedAt:                     terminatedAt,
						},
					},
					Cursor: "",
				}, nil)
			},
			want: want{
				&workflowsmodel.ListWorkflowsResponse{
					Workflows: []*workflowsmodel.WorkflowByUserIDResponse{
						{
							ID:                               "workflow_id",
							Name:                             "workflow1",
							Payload:                          `{"headers": {"Content-Type": "application/json"}, "endpoint": "https://dummyjson.com/test"}`,
							Kind:                             "HEARTBEAT",
							WorkflowBuildStatus:              "COMPLETED",
							Interval:                         1,
							ConsecutiveJobFailuresCount:      0,
							MaxConsecutiveJobFailuresAllowed: 5,
							CreatedAt:                        createdAt,
							UpdatedAt:                        updatedAt,
							TerminatedAt:                     terminatedAt,
						},
					},
					Cursor: "",
				},
			},
			isErr: false,
		},
		{
			name: "error: missing user ID",
			req: &workflowspb.ListWorkflowsRequest{
				UserId: "",
				Cursor: "",
			},
			mock:  func(_ *workflowspb.ListWorkflowsRequest) {},
			want:  want{},
			isErr: true,
		},
		{
			name: "error: user not found",
			req: &workflowspb.ListWorkflowsRequest{
				UserId: "invalid_user_id",
				Cursor: "",
			},
			mock: func(req *workflowspb.ListWorkflowsRequest) {
				// Simulate a cache miss
				cache.EXPECT().Get(
					gomock.Any(),
					gomock.Any(),
					gomock.Any(),
				).Return(nil, status.Error(codes.NotFound, "cache miss"))

				// Simulate a repository call
				// This should return an error
				repo.EXPECT().ListWorkflows(
					gomock.Any(),
					req.GetUserId(),
					req.GetCursor(),
					gomock.Any(),
				).Return(nil, status.Error(codes.NotFound, "user not found"))
			},
			want:  want{},
			isErr: true,
		},
		{
			name: "error: internal server error",
			req: &workflowspb.ListWorkflowsRequest{
				UserId: "user1",
				Cursor: "",
			},
			mock: func(req *workflowspb.ListWorkflowsRequest) {
				// Simulate a cache miss
				cache.EXPECT().Get(
					gomock.Any(),
					gomock.Any(),
					gomock.Any(),
				).Return(nil, status.Error(codes.NotFound, "cache miss"))

				repo.EXPECT().ListWorkflows(
					gomock.Any(),
					req.GetUserId(),
					req.GetCursor(),
					gomock.Any(),
				).Return(nil, status.Error(codes.Internal, "internal server error"))
			},
			want:  want{},
			isErr: true,
		},
	}

	defer ctrl.Finish()

	for _, tt := range tests {
		tt.mock(tt.req)
		t.Run(tt.name, func(t *testing.T) {
			workflows, err := s.ListWorkflows(t.Context(), tt.req)
			if tt.isErr {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			assert.Equal(t, len(workflows.Workflows), len(tt.want.ListWorkflowsResponse.Workflows))
			assert.Equal(t, workflows.Workflows, tt.want.ListWorkflowsResponse.Workflows)
			assert.Equal(t, workflows.Cursor, tt.want.ListWorkflowsResponse.Cursor)
		})
	}
}
