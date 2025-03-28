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

	// Create a new service
	s := workflows.New(validator.New(), repo)

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
				UserId:   "user1",
				Name:     "workflow1",
				Payload:  `{"action": "run", "params": {"foo": "bar"}}`,
				Kind:     "HEARTBEAT",
				Interval: 1,
			},
			mock: func(req *workflowspb.CreateWorkflowRequest) {
				repo.EXPECT().CreateWorkflow(
					gomock.Any(),
					req.GetUserId(),
					req.GetName(),
					req.GetPayload(),
					req.GetKind(),
					req.GetInterval(),
				).Return("workflow_id", nil)
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
				Payload:  `{"action": "run", "params": {"foo": "bar"}}`,
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
				UserId:   "user1",
				Name:     "workflow1",
				Payload:  `invalid json`,
				Kind:     "HEARTBEAT",
				Interval: 1,
			},
			mock:  func(_ *workflowspb.CreateWorkflowRequest) {},
			want:  want{},
			isErr: true,
		},
		{
			name: "error: internal server error",
			req: &workflowspb.CreateWorkflowRequest{
				UserId:   "user1",
				Name:     "workflow1",
				Payload:  `{"action": "run", "params": {"foo": "bar"}}`,
				Kind:     "HEARTBEAT",
				Interval: 1,
			},
			mock: func(req *workflowspb.CreateWorkflowRequest) {
				repo.EXPECT().CreateWorkflow(
					gomock.Any(),
					req.GetUserId(),
					req.GetName(),
					req.GetPayload(),
					req.GetKind(),
					req.GetInterval(),
				).Return("", status.Error(codes.Internal, "internal server error"))
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

	// Create a new service
	s := workflows.New(validator.New(), repo)

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
				Id:       "workflow_id",
				UserId:   "user1",
				Name:     "workflow1",
				Payload:  `{"action": "run", "params": {"foo": "bar"}}`,
				Interval: 1,
			},
			mock: func(req *workflowspb.UpdateWorkflowRequest) {
				repo.EXPECT().UpdateWorkflow(
					gomock.Any(),
					req.GetId(),
					req.GetUserId(),
					req.GetName(),
					req.GetPayload(),
					req.GetInterval(),
				).Return(nil)
			},
			isErr: false,
		},
		{
			name: "error: missing required fields in request",
			req: &workflowspb.UpdateWorkflowRequest{
				Id:       "",
				UserId:   "user1",
				Name:     "workflow1",
				Payload:  `{"action": "run", "params": {"foo": "bar"}}`,
				Interval: 1,
			},
			mock:  func(_ *workflowspb.UpdateWorkflowRequest) {},
			isErr: true,
		},
		{
			name: "error: invalid payload",
			req: &workflowspb.UpdateWorkflowRequest{
				Id:       "workflow_id",
				UserId:   "user1",
				Name:     "workflow1",
				Payload:  `invalid json`,
				Interval: 1,
			},
			mock:  func(_ *workflowspb.UpdateWorkflowRequest) {},
			isErr: true,
		},
		{
			name: "error: workflow not found",
			req: &workflowspb.UpdateWorkflowRequest{
				Id:       "invalid_workflow_id",
				UserId:   "user1",
				Name:     "workflow1",
				Payload:  `{"action": "run", "params": {"foo": "bar"}}`,
				Interval: 1,
			},
			mock: func(req *workflowspb.UpdateWorkflowRequest) {
				repo.EXPECT().UpdateWorkflow(
					gomock.Any(),
					req.GetId(),
					req.GetUserId(),
					req.GetName(),
					req.GetPayload(),
					req.GetInterval(),
				).Return(status.Error(codes.NotFound, "workflow not found"))
			},
			isErr: true,
		},
		{
			name: "error: internal server error",
			req: &workflowspb.UpdateWorkflowRequest{
				Id:       "workflow_id",
				UserId:   "user1",
				Name:     "workflow1",
				Payload:  `{"action": "run", "params": {"foo": "bar"}}`,
				Interval: 1,
			},
			mock: func(req *workflowspb.UpdateWorkflowRequest) {
				repo.EXPECT().UpdateWorkflow(
					gomock.Any(),
					req.GetId(),
					req.GetUserId(),
					req.GetName(),
					req.GetPayload(),
					req.GetInterval(),
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

	// Create a new service
	s := workflows.New(validator.New(), repo)

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
				BuildStatus: "COMPLETED",
			},
			mock: func(req *workflowspb.UpdateWorkflowBuildStatusRequest) {
				repo.EXPECT().UpdateWorkflowBuildStatus(
					gomock.Any(),
					req.GetId(),
					req.GetBuildStatus(),
				).Return(nil)
			},
			isErr: false,
		},
		{
			name: "error: missing workflow ID",
			req: &workflowspb.UpdateWorkflowBuildStatusRequest{
				Id:          "",
				BuildStatus: "COMPLETED",
			},
			mock:  func(_ *workflowspb.UpdateWorkflowBuildStatusRequest) {},
			isErr: true,
		},
		{
			name: "error: missing build status",
			req: &workflowspb.UpdateWorkflowBuildStatusRequest{
				Id:          "workflow_id",
				BuildStatus: "",
			},
			mock:  func(_ *workflowspb.UpdateWorkflowBuildStatusRequest) {},
			isErr: true,
		},
		{
			name: "error: invalid build status",
			req: &workflowspb.UpdateWorkflowBuildStatusRequest{
				Id:          "workflow_id",
				BuildStatus: "INVALID",
			},
			mock:  func(_ *workflowspb.UpdateWorkflowBuildStatusRequest) {},
			isErr: true,
		},
		{
			name: "error: workflow not found",
			req: &workflowspb.UpdateWorkflowBuildStatusRequest{
				Id:          "invalid_workflow_id",
				BuildStatus: "COMPLETED",
			},
			mock: func(req *workflowspb.UpdateWorkflowBuildStatusRequest) {
				repo.EXPECT().UpdateWorkflowBuildStatus(
					gomock.Any(),
					req.GetId(),
					req.GetBuildStatus(),
				).Return(status.Error(codes.NotFound, "workflow not found"))
			},
			isErr: true,
		},
		{
			name: "error: internal server error",
			req: &workflowspb.UpdateWorkflowBuildStatusRequest{
				Id:          "workflow_id",
				BuildStatus: "COMPLETED",
			},
			mock: func(req *workflowspb.UpdateWorkflowBuildStatusRequest) {
				repo.EXPECT().UpdateWorkflowBuildStatus(
					gomock.Any(),
					req.GetId(),
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

	// Create a new service
	s := workflows.New(validator.New(), repo)

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
			name: "success",
			req: &workflowspb.GetWorkflowRequest{
				Id:     "workflow_id",
				UserId: "user_id",
			},
			mock: func(req *workflowspb.GetWorkflowRequest) {
				repo.EXPECT().GetWorkflow(
					gomock.Any(),
					req.GetId(),
					req.GetUserId(),
				).Return(&workflowsmodel.GetWorkflowResponse{
					ID:                  "workflow_id",
					Name:                "workflow1",
					Payload:             `{"action": "run", "params": {"foo": "bar"}}`,
					Kind:                "HEARTBEAT",
					WorkflowBuildStatus: "COMPLETED",
					Interval:            1,
					CreatedAt:           createdAt,
					UpdatedAt:           updatedAt,
					TerminatedAt:        terminatedAt,
				}, nil)
			},
			want: want{
				&workflowsmodel.GetWorkflowResponse{
					ID:                  "workflow_id",
					Name:                "workflow1",
					Payload:             `{"action": "run", "params": {"foo": "bar"}}`,
					Kind:                "HEARTBEAT",
					WorkflowBuildStatus: "COMPLETED",
					Interval:            1,
					CreatedAt:           createdAt,
					UpdatedAt:           updatedAt,
					TerminatedAt:        terminatedAt,
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

	// Create a new service
	s := workflows.New(validator.New(), repo)

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
					UserID:              "user1",
					Name:                "workflow1",
					Payload:             `{"action": "run", "params": {"foo": "bar"}}`,
					Kind:                "HEARTBEAT",
					WorkflowBuildStatus: "COMPLETED",
					Interval:            1,
					CreatedAt:           createdAt,
					UpdatedAt:           updatedAt,
					TerminatedAt:        terminatedAt,
				}, nil)
			},
			want: want{
				&workflowsmodel.GetWorkflowByIDResponse{
					UserID:              "user1",
					Name:                "workflow1",
					Payload:             `{"action": "run", "params": {"foo": "bar"}}`,
					Kind:                "HEARTBEAT",
					WorkflowBuildStatus: "COMPLETED",
					Interval:            1,
					CreatedAt:           createdAt,
					UpdatedAt:           updatedAt,
					TerminatedAt:        terminatedAt,
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

func TestTerminateWorkflow(t *testing.T) {
	ctrl := gomock.NewController(t)

	// Create a mock repository
	repo := workflowsmock.NewMockRepository(ctrl)

	// Create a new service
	s := workflows.New(validator.New(), repo)

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

	// Create a new service
	s := workflows.New(validator.New(), repo)

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
			name: "success",
			req: &workflowspb.ListWorkflowsRequest{
				UserId: "user1",
				Cursor: "",
			},
			mock: func(req *workflowspb.ListWorkflowsRequest) {
				repo.EXPECT().ListWorkflows(
					gomock.Any(),
					req.GetUserId(),
					req.GetCursor(),
				).Return(&workflowsmodel.ListWorkflowsResponse{
					Workflows: []*workflowsmodel.WorkflowByUserIDResponse{
						{
							ID:                  "workflow_id",
							Name:                "workflow1",
							Payload:             `{"action": "run", "params": {"foo": "bar"}}`,
							Kind:                "HEARTBEAT",
							WorkflowBuildStatus: "COMPLETED",
							Interval:            1,
							CreatedAt:           createdAt,
							UpdatedAt:           updatedAt,
							TerminatedAt:        terminatedAt,
						},
					},
					Cursor: "",
				}, nil)
			},
			want: want{
				&workflowsmodel.ListWorkflowsResponse{
					Workflows: []*workflowsmodel.WorkflowByUserIDResponse{
						{
							ID:                  "workflow_id",
							Name:                "workflow1",
							Payload:             `{"action": "run", "params": {"foo": "bar"}}`,
							Kind:                "HEARTBEAT",
							WorkflowBuildStatus: "COMPLETED",
							Interval:            1,
							CreatedAt:           createdAt,
							UpdatedAt:           updatedAt,
							TerminatedAt:        terminatedAt,
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
				repo.EXPECT().ListWorkflows(
					gomock.Any(),
					req.GetUserId(),
					req.GetCursor(),
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
				repo.EXPECT().ListWorkflows(
					gomock.Any(),
					req.GetUserId(),
					req.GetCursor(),
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
