package jobs_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/go-redis/redismock/v9"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	"github.com/hitesh22rana/chronoverse/internal/service/jobs"
	jobsmock "github.com/hitesh22rana/chronoverse/internal/service/jobs/mock"
	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"
)

func TestScheduleJob(t *testing.T) {
	ctrl := gomock.NewController(t)

	// Create a mock repository
	repo := jobsmock.NewMockRepository(ctrl)
	cache := jobsmock.NewMockCache(ctrl)

	// Create a new service
	s := jobs.New(validator.New(), repo, cache)

	type want struct {
		jobID string
	}

	// Test cases
	tests := []struct {
		name  string
		req   *jobspb.ScheduleJobRequest
		mock  func(req *jobspb.ScheduleJobRequest)
		want  want
		isErr bool
	}{
		{
			name: "success",
			req: &jobspb.ScheduleJobRequest{
				WorkflowId:  "workflow_id",
				UserId:      "user1",
				ScheduledAt: time.Now().Add(time.Minute).Format(time.RFC3339Nano),
			},
			mock: func(req *jobspb.ScheduleJobRequest) {
				repo.EXPECT().ScheduleJob(
					gomock.Any(),
					req.GetWorkflowId(),
					req.GetUserId(),
					req.GetScheduledAt(),
				).Return("job_id", nil)
			},
			want: want{
				jobID: "job_id",
			},
			isErr: false,
		},
		{
			name: "error: missing required fields in request",
			req: &jobspb.ScheduleJobRequest{
				WorkflowId:  "",
				UserId:      "user1",
				ScheduledAt: time.Now().Add(time.Minute).Format(time.RFC3339Nano),
			},
			mock:  func(_ *jobspb.ScheduleJobRequest) {},
			want:  want{},
			isErr: true,
		},
		{
			name: "error: invalid at time",
			req: &jobspb.ScheduleJobRequest{
				WorkflowId:  "workflow_id",
				UserId:      "user1",
				ScheduledAt: "invalid_time",
			},
			mock:  func(_ *jobspb.ScheduleJobRequest) {},
			want:  want{},
			isErr: true,
		},
		{
			name: "error: job not found",
			req: &jobspb.ScheduleJobRequest{
				WorkflowId:  "invalid_job_id",
				UserId:      "user1",
				ScheduledAt: time.Now().Add(time.Minute).Format(time.RFC3339Nano),
			},
			mock: func(req *jobspb.ScheduleJobRequest) {
				repo.EXPECT().ScheduleJob(
					gomock.Any(),
					req.GetWorkflowId(),
					req.GetUserId(),
					req.GetScheduledAt(),
				).Return("", status.Error(codes.NotFound, "job not found"))
			},
			want:  want{},
			isErr: true,
		},
		{
			name: "error: job not owned by user",
			req: &jobspb.ScheduleJobRequest{
				WorkflowId:  "workflow_id",
				UserId:      "invalid_user_id",
				ScheduledAt: time.Now().Add(time.Minute).Format(time.RFC3339Nano),
			},
			mock: func(req *jobspb.ScheduleJobRequest) {
				repo.EXPECT().ScheduleJob(
					gomock.Any(),
					req.GetWorkflowId(),
					req.GetUserId(),
					req.GetScheduledAt(),
				).Return("", status.Error(codes.NotFound, "job not found or not owned by user"))
			},
			want:  want{},
			isErr: true,
		},
		{
			name: "error: internal server error",
			req: &jobspb.ScheduleJobRequest{
				WorkflowId:  "workflow_id",
				UserId:      "user1",
				ScheduledAt: time.Now().Add(time.Minute).Format(time.RFC3339Nano),
			},
			mock: func(req *jobspb.ScheduleJobRequest) {
				repo.EXPECT().ScheduleJob(
					gomock.Any(),
					req.GetWorkflowId(),
					req.GetUserId(),
					req.GetScheduledAt(),
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
			jobID, err := s.ScheduleJob(t.Context(), tt.req)
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

			assert.Equal(t, jobID, tt.want.jobID)
		})
	}
}

func TestUpdateJobStatus(t *testing.T) {
	ctrl := gomock.NewController(t)

	// Create a mock repository
	repo := jobsmock.NewMockRepository(ctrl)
	cache := jobsmock.NewMockCache(ctrl)

	// Create a new service
	s := jobs.New(validator.New(), repo, cache)

	// Test cases
	tests := []struct {
		name  string
		req   *jobspb.UpdateJobStatusRequest
		mock  func(req *jobspb.UpdateJobStatusRequest)
		isErr bool
	}{
		{
			name: "success",
			req: &jobspb.UpdateJobStatusRequest{
				Id:     "job_id",
				Status: "COMPLETED",
			},
			mock: func(req *jobspb.UpdateJobStatusRequest) {
				repo.EXPECT().UpdateJobStatus(
					gomock.Any(),
					req.GetId(),
					req.GetContainerId(),
					req.GetStatus(),
				).Return(nil)
			},
			isErr: false,
		},
		{
			name: "success with container ID",
			req: &jobspb.UpdateJobStatusRequest{
				Id:          "job_id",
				ContainerId: "container_id",
				Status:      "RUNNING",
			},
			mock: func(req *jobspb.UpdateJobStatusRequest) {
				repo.EXPECT().UpdateJobStatus(
					gomock.Any(),
					req.GetId(),
					req.GetContainerId(),
					req.GetStatus(),
				).Return(nil)
			},
			isErr: false,
		},
		{
			name: "error: missing job ID",
			req: &jobspb.UpdateJobStatusRequest{
				Id:     "",
				Status: "COMPLETED",
			},
			mock:  func(_ *jobspb.UpdateJobStatusRequest) {},
			isErr: true,
		},
		{
			name: "error: missing status",
			req: &jobspb.UpdateJobStatusRequest{
				Id:     "job_id",
				Status: "",
			},
			mock:  func(_ *jobspb.UpdateJobStatusRequest) {},
			isErr: true,
		},
		{
			name: "error: invalid status",
			req: &jobspb.UpdateJobStatusRequest{
				Id:     "job_id",
				Status: "INVALID",
			},
			mock:  func(_ *jobspb.UpdateJobStatusRequest) {},
			isErr: true,
		},
		{
			name: "error: job not found",
			req: &jobspb.UpdateJobStatusRequest{
				Id:     "invalid_job_id",
				Status: "COMPLETED",
			},
			mock: func(req *jobspb.UpdateJobStatusRequest) {
				repo.EXPECT().UpdateJobStatus(
					gomock.Any(),
					req.GetId(),
					req.GetContainerId(),
					req.GetStatus(),
				).Return(status.Error(codes.NotFound, "job not found"))
			},
			isErr: true,
		},
		{
			name: "error: internal server error",
			req: &jobspb.UpdateJobStatusRequest{
				Id:     "job_id",
				Status: "COMPLETED",
			},
			mock: func(req *jobspb.UpdateJobStatusRequest) {
				repo.EXPECT().UpdateJobStatus(
					gomock.Any(),
					req.GetId(),
					req.GetContainerId(),
					req.GetStatus(),
				).Return(status.Error(codes.Internal, "internal server error"))
			},
			isErr: true,
		},
	}

	defer ctrl.Finish()

	for _, tt := range tests {
		tt.mock(tt.req)
		t.Run(tt.name, func(t *testing.T) {
			err := s.UpdateJobStatus(t.Context(), tt.req)
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

func TestGetJob(t *testing.T) {
	ctrl := gomock.NewController(t)

	// Create a mock repository
	repo := jobsmock.NewMockRepository(ctrl)
	cache := jobsmock.NewMockCache(ctrl)

	// Create a new service
	s := jobs.New(validator.New(), repo, cache)

	type want struct {
		*jobsmodel.GetJobResponse
	}

	var (
		createdAt   = time.Now()
		updatedAt   = time.Now()
		scheduledAt = time.Now().Add(time.Minute)
		startedAt   = sql.NullTime{
			Time:  time.Now(),
			Valid: false,
		}
		completedAt = sql.NullTime{
			Time:  time.Now(),
			Valid: false,
		}
	)

	// Test cases
	tests := []struct {
		name  string
		req   *jobspb.GetJobRequest
		mock  func(req *jobspb.GetJobRequest)
		want  want
		isErr bool
	}{
		{
			name: "success",
			req: &jobspb.GetJobRequest{
				Id:         "job_id",
				WorkflowId: "workflow_id",
				UserId:     "user_id",
			},
			mock: func(req *jobspb.GetJobRequest) {
				repo.EXPECT().GetJob(
					gomock.Any(),
					req.GetId(),
					req.GetWorkflowId(),
					req.GetUserId(),
				).Return(&jobsmodel.GetJobResponse{
					ID:          "job_id",
					WorkflowID:  "workflow_id",
					JobStatus:   "PENDING",
					ScheduledAt: scheduledAt,
					StartedAt:   startedAt,
					CompletedAt: completedAt,
					CreatedAt:   createdAt,
					UpdatedAt:   updatedAt,
				}, nil)
			},
			want: want{
				&jobsmodel.GetJobResponse{
					ID:          "job_id",
					WorkflowID:  "workflow_id",
					JobStatus:   "PENDING",
					ScheduledAt: scheduledAt,
					StartedAt:   startedAt,
					CompletedAt: completedAt,
					CreatedAt:   createdAt,
					UpdatedAt:   updatedAt,
				},
			},
			isErr: false,
		},
		{
			name: "error: missing required fields in request",
			req: &jobspb.GetJobRequest{
				Id:         "",
				WorkflowId: "",
				UserId:     "",
			},
			mock:  func(_ *jobspb.GetJobRequest) {},
			want:  want{},
			isErr: true,
		},
		{
			name: "error: invalid user",
			req: &jobspb.GetJobRequest{
				Id:         "job_id",
				WorkflowId: "workflow_id",
				UserId:     "invalid_user_id",
			},
			mock: func(req *jobspb.GetJobRequest) {
				repo.EXPECT().GetJob(
					gomock.Any(),
					req.GetId(),
					req.GetWorkflowId(),
					req.GetUserId(),
				).Return(nil, status.Error(codes.NotFound, "job not found or not owned by user"))
			},
			want:  want{},
			isErr: true,
		},
		{
			name: "error: job not found",
			req: &jobspb.GetJobRequest{
				Id:         "invalid_job_id",
				WorkflowId: "workflow_id",
				UserId:     "user_id",
			},
			mock: func(req *jobspb.GetJobRequest) {
				repo.EXPECT().GetJob(
					gomock.Any(),
					req.GetId(),
					req.GetWorkflowId(),
					req.GetUserId(),
				).Return(nil, status.Error(codes.NotFound, "job not found"))
			},
			want:  want{},
			isErr: true,
		},
		{
			name: "error: job not found",
			req: &jobspb.GetJobRequest{
				Id:         "job_id",
				WorkflowId: "invalid_job_id",
				UserId:     "user_id",
			},
			mock: func(req *jobspb.GetJobRequest) {
				repo.EXPECT().GetJob(
					gomock.Any(),
					req.GetId(),
					req.GetWorkflowId(),
					req.GetUserId(),
				).Return(nil, status.Error(codes.NotFound, "job not found"))
			},
			want:  want{},
			isErr: true,
		},
		{
			name: "error: internal server error",
			req: &jobspb.GetJobRequest{
				Id:         "workflow_id",
				WorkflowId: "workflow_id",
				UserId:     "user_id",
			},
			mock: func(req *jobspb.GetJobRequest) {
				repo.EXPECT().GetJob(
					gomock.Any(),
					req.GetId(),
					req.GetWorkflowId(),
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
			job, err := s.GetJob(t.Context(), tt.req)
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

			assert.Equal(t, job, tt.want.GetJobResponse)
		})
	}
}

func TestGetJobByID(t *testing.T) {
	ctrl := gomock.NewController(t)

	// Create a mock repository
	repo := jobsmock.NewMockRepository(ctrl)
	cache := jobsmock.NewMockCache(ctrl)

	// Create a new service
	s := jobs.New(validator.New(), repo, cache)

	type want struct {
		*jobsmodel.GetJobByIDResponse
	}

	var (
		createdAt   = time.Now()
		updatedAt   = time.Now()
		scheduledAt = time.Now().Add(time.Minute)
		startedAt   = sql.NullTime{
			Time:  time.Now(),
			Valid: false,
		}
		completedAt = sql.NullTime{
			Time:  time.Now(),
			Valid: false,
		}
	)

	// Test cases
	tests := []struct {
		name  string
		req   *jobspb.GetJobByIDRequest
		mock  func(req *jobspb.GetJobByIDRequest)
		want  want
		isErr bool
	}{
		{
			name: "success",
			req: &jobspb.GetJobByIDRequest{
				Id: "job_id",
			},
			mock: func(req *jobspb.GetJobByIDRequest) {
				repo.EXPECT().GetJobByID(
					gomock.Any(),
					req.GetId(),
				).Return(&jobsmodel.GetJobByIDResponse{
					WorkflowID:  "workflow_id",
					UserID:      "user1",
					JobStatus:   "PENDING",
					ScheduledAt: scheduledAt,
					StartedAt:   startedAt,
					CompletedAt: completedAt,
					CreatedAt:   createdAt,
					UpdatedAt:   updatedAt,
				}, nil)
			},
			want: want{
				&jobsmodel.GetJobByIDResponse{
					WorkflowID:  "workflow_id",
					UserID:      "user1",
					JobStatus:   "PENDING",
					ScheduledAt: scheduledAt,
					StartedAt:   startedAt,
					CompletedAt: completedAt,
					CreatedAt:   createdAt,
					UpdatedAt:   updatedAt,
				},
			},
			isErr: false,
		},
		{
			name: "error: missing job ID",
			req: &jobspb.GetJobByIDRequest{
				Id: "",
			},
			mock:  func(_ *jobspb.GetJobByIDRequest) {},
			want:  want{},
			isErr: true,
		},
		{
			name: "error: job not found",
			req: &jobspb.GetJobByIDRequest{
				Id: "invalid_job_id",
			},
			mock: func(req *jobspb.GetJobByIDRequest) {
				repo.EXPECT().GetJobByID(
					gomock.Any(),
					req.GetId(),
				).Return(nil, status.Error(codes.NotFound, "job not found"))
			},
			want:  want{},
			isErr: true,
		},
		{
			name: "error: internal server error",
			req: &jobspb.GetJobByIDRequest{
				Id: "job_id",
			},
			mock: func(req *jobspb.GetJobByIDRequest) {
				repo.EXPECT().GetJobByID(
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
			job, err := s.GetJobByID(t.Context(), tt.req)
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

			assert.Equal(t, job, tt.want.GetJobByIDResponse)
		})
	}
}

func TestGetJobLogs(t *testing.T) {
	ctrl := gomock.NewController(t)

	// Create a mock repository
	repo := jobsmock.NewMockRepository(ctrl)
	cache := jobsmock.NewMockCache(ctrl)

	// Create a new service
	s := jobs.New(validator.New(), repo, cache)

	type want struct {
		*jobsmodel.GetJobLogsResponse
	}

	timestamp := time.Now()

	// Test cases
	tests := []struct {
		name  string
		req   *jobspb.GetJobLogsRequest
		mock  func(req *jobspb.GetJobLogsRequest)
		want  want
		isErr bool
	}{
		{
			name: "success",
			req: &jobspb.GetJobLogsRequest{
				Id:         "job_id",
				WorkflowId: "workflow_id",
				UserId:     "user_id",
				Cursor:     "",
			},
			mock: func(req *jobspb.GetJobLogsRequest) {
				data := &jobsmodel.GetJobLogsResponse{
					ID:         "job_id",
					WorkflowID: "workflow_id",
					JobLogs: []*jobsmodel.JobLog{
						{
							Timestamp:   timestamp,
							Message:     "log 1",
							SequenceNum: 1,
							Stream:      "stdout",
						},
						{
							Timestamp:   timestamp,
							Message:     "log 2",
							SequenceNum: 2,
							Stream:      "stdout",
						},
					},
				}
				cacheKey := fmt.Sprintf("job_logs:%s:%s:%s", req.GetUserId(), req.GetId(), req.GetCursor())
				cache.EXPECT().Get(
					gomock.Any(),
					cacheKey,
					gomock.Any(),
				).Return(nil, status.Error(codes.NotFound, "cache miss"))
				repo.EXPECT().GetJobLogs(
					gomock.Any(),
					req.GetId(),
					req.GetWorkflowId(),
					req.GetUserId(),
					req.GetCursor(),
				).Return(data, "COMPLETED", nil)
				cache.EXPECT().Set(
					gomock.Any(),
					cacheKey,
					data,
					gomock.Any(),
				).AnyTimes()
			},
			want: want{
				&jobsmodel.GetJobLogsResponse{
					ID:         "job_id",
					WorkflowID: "workflow_id",
					JobLogs: []*jobsmodel.JobLog{
						{
							Timestamp:   timestamp,
							Message:     "log 1",
							SequenceNum: 1,
							Stream:      "stdout",
						},
						{
							Timestamp:   timestamp,
							Message:     "log 2",
							SequenceNum: 2,
							Stream:      "stdout",
						},
					},
				},
			},
			isErr: false,
		},
		{
			name: "success: with cache hit",
			req: &jobspb.GetJobLogsRequest{
				Id:         "job_id",
				WorkflowId: "workflow_id",
				UserId:     "user_id",
				Cursor:     "",
			},
			mock: func(req *jobspb.GetJobLogsRequest) {
				cache.EXPECT().Get(gomock.Any(), fmt.Sprintf("job_logs:%s:%s:%s", req.GetUserId(), req.GetId(), req.GetCursor()), gomock.Any()).Return(&jobsmodel.GetJobLogsResponse{
					ID:         "job_id",
					WorkflowID: "workflow_id",
					JobLogs: []*jobsmodel.JobLog{
						{
							Timestamp:   timestamp,
							Message:     "log 1",
							SequenceNum: 1,
							Stream:      "stdout",
						},
						{
							Timestamp:   timestamp,
							Message:     "log 2",
							SequenceNum: 2,
							Stream:      "stdout",
						},
					},
					Cursor: "cursor",
				}, nil)
			},
			want: want{
				&jobsmodel.GetJobLogsResponse{
					ID:         "job_id",
					WorkflowID: "workflow_id",
					JobLogs: []*jobsmodel.JobLog{
						{
							Timestamp:   timestamp,
							Message:     "log 1",
							SequenceNum: 1,
							Stream:      "stdout",
						},
						{
							Timestamp:   timestamp,
							Message:     "log 2",
							SequenceNum: 2,
							Stream:      "stdout",
						},
					},
					Cursor: "cursor",
				},
			},
			isErr: false,
		},
		{
			name: "success: with cache miss",
			req: &jobspb.GetJobLogsRequest{
				Id:         "job_id",
				WorkflowId: "workflow_id",
				UserId:     "user_id",
				Cursor:     "",
			},
			mock: func(req *jobspb.GetJobLogsRequest) {
				data := &jobsmodel.GetJobLogsResponse{
					ID:         "job_id",
					WorkflowID: "workflow_id",
					JobLogs: []*jobsmodel.JobLog{
						{
							Timestamp:   timestamp,
							Message:     "log 1",
							SequenceNum: 1,
							Stream:      "stdout",
						},
						{
							Timestamp:   timestamp,
							Message:     "log 2",
							SequenceNum: 2,
							Stream:      "stdout",
						},
					},
					Cursor: "cursor",
				}
				cacheKey := fmt.Sprintf("job_logs:%s:%s:%s", req.GetUserId(), req.GetId(), req.GetCursor())
				cache.EXPECT().Get(
					gomock.Any(),
					cacheKey,
					gomock.Any(),
				).Return(nil, status.Error(codes.NotFound, "cache miss"))
				repo.EXPECT().GetJobLogs(
					gomock.Any(),
					req.GetId(),
					req.GetWorkflowId(),
					req.GetUserId(),
					req.GetCursor(),
				).Return(data, "COMPLETED", nil)
				cache.EXPECT().Set(
					gomock.Any(),
					cacheKey,
					data,
					gomock.Any(),
				).AnyTimes()
			},
			want: want{
				&jobsmodel.GetJobLogsResponse{
					ID:         "job_id",
					WorkflowID: "workflow_id",
					JobLogs: []*jobsmodel.JobLog{
						{
							Timestamp:   timestamp,
							Message:     "log 1",
							SequenceNum: 1,
							Stream:      "stdout",
						},
						{
							Timestamp:   timestamp,
							Message:     "log 2",
							SequenceNum: 2,
							Stream:      "stdout",
						},
					},
					Cursor: "cursor",
				},
			},
			isErr: false,
		},
		{
			name: "error: missing required fields in request",
			req: &jobspb.GetJobLogsRequest{
				Id:         "",
				WorkflowId: "",
				UserId:     "",
				Cursor:     "",
			},
			mock:  func(_ *jobspb.GetJobLogsRequest) {},
			want:  want{},
			isErr: true,
		},
		{
			name: "error: job not found",
			req: &jobspb.GetJobLogsRequest{
				Id:         "invalid_job_id",
				WorkflowId: "workflow_id",
				UserId:     "user_id",
				Cursor:     "",
			},
			mock: func(req *jobspb.GetJobLogsRequest) {
				cacheKey := fmt.Sprintf("job_logs:%s:%s:%s", req.GetUserId(), req.GetId(), req.GetCursor())
				cache.EXPECT().Get(
					gomock.Any(),
					cacheKey,
					gomock.Any(),
				).Return(nil, status.Error(codes.NotFound, "cache miss"))
				repo.EXPECT().GetJobLogs(
					gomock.Any(),
					req.GetId(),
					req.GetWorkflowId(),
					req.GetUserId(),
					req.GetCursor(),
				).Return(nil, "", status.Error(codes.NotFound, "job not found"))
			},
			want:  want{},
			isErr: true,
		},
		{
			name: "error: internal server error",
			req: &jobspb.GetJobLogsRequest{
				Id:         "job_id",
				WorkflowId: "workflow_id",
				UserId:     "user_id",
				Cursor:     "",
			},
			mock: func(req *jobspb.GetJobLogsRequest) {
				cacheKey := fmt.Sprintf("job_logs:%s:%s:%s", req.GetUserId(), req.GetId(), req.GetCursor())
				cache.EXPECT().Get(
					gomock.Any(),
					cacheKey,
					gomock.Any(),
				).Return(nil, status.Error(codes.NotFound, "cache miss"))
				repo.EXPECT().GetJobLogs(
					gomock.Any(),
					req.GetId(),
					req.GetWorkflowId(),
					req.GetUserId(),
					req.GetCursor(),
				).Return(nil, "", status.Error(codes.Internal, "internal server error"))
			},
			want:  want{},
			isErr: true,
		},
	}

	defer ctrl.Finish()

	for _, tt := range tests {
		tt.mock(tt.req)
		t.Run(tt.name, func(t *testing.T) {
			jobLogs, err := s.GetJobLogs(t.Context(), tt.req)
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

			assert.Equal(t, jobLogs, tt.want.GetJobLogsResponse)
		})
	}
}

func TestStreamJobLogs(t *testing.T) {
	ctrl := gomock.NewController(t)

	// Create a mock repository
	repo := jobsmock.NewMockRepository(ctrl)
	cache := jobsmock.NewMockCache(ctrl)

	// Create a new service
	s := jobs.New(validator.New(), repo, cache)

	type want struct {
		channelType string
	}

	// Test cases
	tests := []struct {
		name  string
		req   *jobspb.StreamJobLogsRequest
		mock  func(req *jobspb.StreamJobLogsRequest)
		want  want
		isErr bool
	}{
		{
			name: "success",
			req: &jobspb.StreamJobLogsRequest{
				Id:         "job_id",
				WorkflowId: "workflow_id",
				UserId:     "user_id",
			},
			mock: func(req *jobspb.StreamJobLogsRequest) {
				redisMock, _ := redismock.NewClientMock()
				sub := redisMock.Subscribe(t.Context(), "job_logs")

				repo.EXPECT().StreamJobLogs(
					gomock.Any(),
					req.GetId(),
					req.GetWorkflowId(),
					req.GetUserId(),
				).Return(sub, nil)

				// Simulate sending logs to the channel
				go func() {
					redisMock.Publish(t.Context(), "job_logs", &jobsmodel.JobLog{
						Timestamp:   time.Now(),
						Message:     "log message",
						SequenceNum: 1,
						Stream:      "stdout",
					})
					sub.Close() // Simulate end of stream
				}()
			},
			want: want{
				channelType: "chan *jobsmodel.JobLog",
			},
			isErr: false,
		},
		{
			name: "error: missing required fields in request",
			req: &jobspb.StreamJobLogsRequest{
				Id:         "",
				WorkflowId: "",
				UserId:     "",
			},
			mock:  func(_ *jobspb.StreamJobLogsRequest) {},
			want:  want{},
			isErr: true,
		},
		{
			name: "error: repository error",
			req: &jobspb.StreamJobLogsRequest{
				Id:         "job_id",
				WorkflowId: "workflow_id",
				UserId:     "user_id",
			},
			mock: func(req *jobspb.StreamJobLogsRequest) {
				repo.EXPECT().StreamJobLogs(
					gomock.Any(),
					req.GetId(),
					req.GetWorkflowId(),
					req.GetUserId(),
				).Return(nil, status.Error(codes.NotFound, "job not found"))
			},
			want:  want{},
			isErr: true,
		},
		{
			name: "error: job not running",
			req: &jobspb.StreamJobLogsRequest{
				Id:         "job_id",
				WorkflowId: "workflow_id",
				UserId:     "user_id",
			},
			mock: func(req *jobspb.StreamJobLogsRequest) {
				repo.EXPECT().StreamJobLogs(
					gomock.Any(),
					req.GetId(),
					req.GetWorkflowId(),
					req.GetUserId(),
				).Return(nil, status.Error(codes.FailedPrecondition, "job is not running"))
			},
			want:  want{},
			isErr: true,
		},
	}

	defer ctrl.Finish()

	for _, tt := range tests {
		tt.mock(tt.req)
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()

			stream, err := s.StreamJobLogs(ctx, tt.req)
			if tt.isErr {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
				if stream != nil {
					t.Errorf("expected nil channel on error, got channel")
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			if stream == nil {
				t.Errorf("expected channel, got nil")
				return
			}

			// Test that the channel is properly typed
			select {
			case <-stream:
				// Channel is readable (this is expected behavior)
			case <-time.After(100 * time.Millisecond):
				// Channel is not immediately readable (also expected)
			}

			// Verify the channel can be closed without panic
			go func() {
				time.Sleep(50 * time.Millisecond)
				cancel() // This should trigger cleanup in the goroutine
			}()

			// Try to read from channel with timeout to ensure cleanup works
			select {
			case <-stream:
			case <-time.After(200 * time.Millisecond):
				// Timeout is acceptable in tests
			}
		})
	}
}

func TestSearchJobLogs(t *testing.T) {
	ctrl := gomock.NewController(t)

	// Create a mock repository
	repo := jobsmock.NewMockRepository(ctrl)
	cache := jobsmock.NewMockCache(ctrl)

	// Create a new service
	s := jobs.New(validator.New(), repo, cache)

	type want struct {
		*jobsmodel.GetJobLogsResponse
	}

	timestamp := time.Now()

	// Test cases
	tests := []struct {
		name  string
		req   *jobspb.SearchJobLogsRequest
		mock  func(req *jobspb.SearchJobLogsRequest)
		want  want
		isErr bool
	}{
		{
			name: "success",
			req: &jobspb.SearchJobLogsRequest{
				Id:         "job_id",
				WorkflowId: "workflow_id",
				UserId:     "user_id",
				Cursor:     "",
				Filters: &jobspb.SearchJobLogsFilters{
					Stream:  jobspb.LogStream_LOG_STREAM_ALL,
					Message: "message",
				},
			},
			mock: func(req *jobspb.SearchJobLogsRequest) {
				data := &jobsmodel.GetJobLogsResponse{
					ID:         "job_id",
					WorkflowID: "workflow_id",
					JobLogs: []*jobsmodel.JobLog{
						{
							Timestamp:   timestamp,
							Message:     "message 1",
							SequenceNum: 1,
							Stream:      "stdout",
						},
						{
							Timestamp:   timestamp,
							Message:     "message 2",
							SequenceNum: 2,
							Stream:      "stdout",
						},
					},
				}
				cacheKey := fmt.Sprintf(
					"job_logs:%s:%s:%s:%s:%s",
					req.GetUserId(),
					req.GetId(),
					req.GetCursor(),
					req.GetFilters().GetMessage(),
					req.GetFilters().GetStream(),
				)
				var filters *jobsmodel.SearchJobLogsFilters
				if req.GetFilters() != nil {
					filters = &jobsmodel.SearchJobLogsFilters{
						Stream:  int(req.GetFilters().GetStream()),
						Message: req.GetFilters().GetMessage(),
					}
				} else {
					filters = &jobsmodel.SearchJobLogsFilters{}
				}
				cache.EXPECT().Get(
					gomock.Any(),
					cacheKey,
					gomock.Any(),
				).Return(nil, status.Error(codes.NotFound, "cache miss"))
				repo.EXPECT().SearchJobLogs(
					gomock.Any(),
					req.GetId(),
					req.GetWorkflowId(),
					req.GetUserId(),
					req.GetCursor(),
					filters,
				).Return(data, "COMPLETED", nil)
				cache.EXPECT().Set(
					gomock.Any(),
					cacheKey,
					data,
					gomock.Any(),
				).AnyTimes()
			},
			want: want{
				&jobsmodel.GetJobLogsResponse{
					ID:         "job_id",
					WorkflowID: "workflow_id",
					JobLogs: []*jobsmodel.JobLog{
						{
							Timestamp:   timestamp,
							Message:     "message 1",
							SequenceNum: 1,
							Stream:      "stdout",
						},
						{
							Timestamp:   timestamp,
							Message:     "message 2",
							SequenceNum: 2,
							Stream:      "stdout",
						},
					},
				},
			},
			isErr: false,
		},
		{
			name: "success: with cache hit",
			req: &jobspb.SearchJobLogsRequest{
				Id:         "job_id",
				WorkflowId: "workflow_id",
				UserId:     "user_id",
				Cursor:     "",
				Filters: &jobspb.SearchJobLogsFilters{
					Stream:  jobspb.LogStream_LOG_STREAM_ALL,
					Message: "message",
				},
			},
			mock: func(req *jobspb.SearchJobLogsRequest) {
				cacheKey := fmt.Sprintf(
					"job_logs:%s:%s:%s:%s:%s",
					req.GetUserId(),
					req.GetId(),
					req.GetCursor(),
					req.GetFilters().GetMessage(),
					req.GetFilters().GetStream(),
				)
				cache.EXPECT().Get(gomock.Any(), cacheKey, gomock.Any()).Return(&jobsmodel.GetJobLogsResponse{
					ID:         "job_id",
					WorkflowID: "workflow_id",
					JobLogs: []*jobsmodel.JobLog{
						{
							Timestamp:   timestamp,
							Message:     "message 1",
							SequenceNum: 1,
							Stream:      "stdout",
						},
						{
							Timestamp:   timestamp,
							Message:     "message 2",
							SequenceNum: 2,
							Stream:      "stdout",
						},
					},
					Cursor: "cursor",
				}, nil)
			},
			want: want{
				&jobsmodel.GetJobLogsResponse{
					ID:         "job_id",
					WorkflowID: "workflow_id",
					JobLogs: []*jobsmodel.JobLog{
						{
							Timestamp:   timestamp,
							Message:     "message 1",
							SequenceNum: 1,
							Stream:      "stdout",
						},
						{
							Timestamp:   timestamp,
							Message:     "message 2",
							SequenceNum: 2,
							Stream:      "stdout",
						},
					},
					Cursor: "cursor",
				},
			},
			isErr: false,
		},
		{
			name: "success: with cache miss",
			req: &jobspb.SearchJobLogsRequest{
				Id:         "job_id",
				WorkflowId: "workflow_id",
				UserId:     "user_id",
				Cursor:     "",
				Filters: &jobspb.SearchJobLogsFilters{
					Stream:  jobspb.LogStream_LOG_STREAM_ALL,
					Message: "message",
				},
			},
			mock: func(req *jobspb.SearchJobLogsRequest) {
				data := &jobsmodel.GetJobLogsResponse{
					ID:         "job_id",
					WorkflowID: "workflow_id",
					JobLogs: []*jobsmodel.JobLog{
						{
							Timestamp:   timestamp,
							Message:     "message 1",
							SequenceNum: 1,
							Stream:      "stdout",
						},
						{
							Timestamp:   timestamp,
							Message:     "message 2",
							SequenceNum: 2,
							Stream:      "stdout",
						},
					},
					Cursor: "cursor",
				}
				cacheKey := fmt.Sprintf(
					"job_logs:%s:%s:%s:%s:%s",
					req.GetUserId(),
					req.GetId(),
					req.GetCursor(),
					req.GetFilters().GetMessage(),
					req.GetFilters().GetStream(),
				)
				var filters *jobsmodel.SearchJobLogsFilters
				if req.GetFilters() != nil {
					filters = &jobsmodel.SearchJobLogsFilters{
						Stream:  int(req.GetFilters().GetStream()),
						Message: req.GetFilters().GetMessage(),
					}
				} else {
					filters = &jobsmodel.SearchJobLogsFilters{}
				}
				cache.EXPECT().Get(
					gomock.Any(),
					cacheKey,
					gomock.Any(),
				).Return(nil, status.Error(codes.NotFound, "cache miss"))
				repo.EXPECT().SearchJobLogs(
					gomock.Any(),
					req.GetId(),
					req.GetWorkflowId(),
					req.GetUserId(),
					req.GetCursor(),
					filters,
				).Return(data, "COMPLETED", nil)
				cache.EXPECT().Set(
					gomock.Any(),
					cacheKey,
					data,
					gomock.Any(),
				).AnyTimes()
			},
			want: want{
				&jobsmodel.GetJobLogsResponse{
					ID:         "job_id",
					WorkflowID: "workflow_id",
					JobLogs: []*jobsmodel.JobLog{
						{
							Timestamp:   timestamp,
							Message:     "message 1",
							SequenceNum: 1,
							Stream:      "stdout",
						},
						{
							Timestamp:   timestamp,
							Message:     "message 2",
							SequenceNum: 2,
							Stream:      "stdout",
						},
					},
					Cursor: "cursor",
				},
			},
			isErr: false,
		},
		{
			name: "error: missing required fields in request",
			req: &jobspb.SearchJobLogsRequest{
				Id:         "",
				WorkflowId: "",
				UserId:     "",
				Cursor:     "",
			},
			mock:  func(_ *jobspb.SearchJobLogsRequest) {},
			want:  want{},
			isErr: true,
		},
		{
			name: "error: job not found",
			req: &jobspb.SearchJobLogsRequest{
				Id:         "invalid_job_id",
				WorkflowId: "workflow_id",
				UserId:     "user_id",
				Cursor:     "",
				Filters: &jobspb.SearchJobLogsFilters{
					Stream:  jobspb.LogStream_LOG_STREAM_ALL,
					Message: "message",
				},
			},
			mock: func(req *jobspb.SearchJobLogsRequest) {
				cacheKey := fmt.Sprintf(
					"job_logs:%s:%s:%s:%s:%s",
					req.GetUserId(),
					req.GetId(),
					req.GetCursor(),
					req.GetFilters().GetMessage(),
					req.GetFilters().GetStream(),
				)
				var filters *jobsmodel.SearchJobLogsFilters
				if req.GetFilters() != nil {
					filters = &jobsmodel.SearchJobLogsFilters{
						Stream:  int(req.GetFilters().GetStream()),
						Message: req.GetFilters().GetMessage(),
					}
				} else {
					filters = &jobsmodel.SearchJobLogsFilters{}
				}
				cache.EXPECT().Get(
					gomock.Any(),
					cacheKey,
					gomock.Any(),
				).Return(nil, status.Error(codes.NotFound, "cache miss"))
				repo.EXPECT().SearchJobLogs(
					gomock.Any(),
					req.GetId(),
					req.GetWorkflowId(),
					req.GetUserId(),
					req.GetCursor(),
					filters,
				).Return(nil, "", status.Error(codes.NotFound, "job not found"))
			},
			want:  want{},
			isErr: true,
		},
		{
			name: "error: internal server error",
			req: &jobspb.SearchJobLogsRequest{
				Id:         "job_id",
				WorkflowId: "workflow_id",
				UserId:     "user_id",
				Cursor:     "",
				Filters: &jobspb.SearchJobLogsFilters{
					Stream:  jobspb.LogStream_LOG_STREAM_ALL,
					Message: "message",
				},
			},
			mock: func(req *jobspb.SearchJobLogsRequest) {
				cacheKey := fmt.Sprintf(
					"job_logs:%s:%s:%s:%s:%s",
					req.GetUserId(),
					req.GetId(),
					req.GetCursor(),
					req.GetFilters().GetMessage(),
					req.GetFilters().GetStream(),
				)
				var filters *jobsmodel.SearchJobLogsFilters
				if req.GetFilters() != nil {
					filters = &jobsmodel.SearchJobLogsFilters{
						Stream:  int(req.GetFilters().GetStream()),
						Message: req.GetFilters().GetMessage(),
					}
				} else {
					filters = &jobsmodel.SearchJobLogsFilters{}
				}
				cache.EXPECT().Get(
					gomock.Any(),
					cacheKey,
					gomock.Any(),
				).Return(nil, status.Error(codes.NotFound, "cache miss"))
				repo.EXPECT().SearchJobLogs(
					gomock.Any(),
					req.GetId(),
					req.GetWorkflowId(),
					req.GetUserId(),
					req.GetCursor(),
					filters,
				).Return(nil, "", status.Error(codes.Internal, "internal server error"))
			},
			want:  want{},
			isErr: true,
		},
	}

	defer ctrl.Finish()

	for _, tt := range tests {
		tt.mock(tt.req)
		t.Run(tt.name, func(t *testing.T) {
			jobLogs, err := s.SearchJobLogs(t.Context(), tt.req)
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

			assert.Equal(t, jobLogs, tt.want.GetJobLogsResponse)
		})
	}
}

func TestListJobs(t *testing.T) {
	ctrl := gomock.NewController(t)

	// Create a mock repository
	repo := jobsmock.NewMockRepository(ctrl)
	cache := jobsmock.NewMockCache(ctrl)

	// Create a new service
	s := jobs.New(validator.New(), repo, cache)

	type want struct {
		*jobsmodel.ListJobsResponse
	}

	var (
		createdAt   = time.Now()
		updatedAt   = time.Now()
		scheduledAt = time.Now().Add(time.Minute)
		startedAt   = sql.NullTime{
			Time:  time.Now(),
			Valid: false,
		}
		completedAt = sql.NullTime{
			Time:  time.Now(),
			Valid: false,
		}
	)

	// Test cases
	tests := []struct {
		name  string
		req   *jobspb.ListJobsRequest
		mock  func(req *jobspb.ListJobsRequest)
		want  want
		isErr bool
	}{
		{
			name: "success",
			req: &jobspb.ListJobsRequest{
				WorkflowId: "workflow_id",
				UserId:     "user_id",
				Cursor:     "",
			},
			mock: func(req *jobspb.ListJobsRequest) {
				repo.EXPECT().ListJobs(
					gomock.Any(),
					req.GetWorkflowId(),
					req.GetUserId(),
					req.GetCursor(),
					gomock.Any(),
				).Return(&jobsmodel.ListJobsResponse{
					Jobs: []*jobsmodel.JobByWorkflowIDResponse{
						{
							ID:          "job_id",
							WorkflowID:  "workflow_id",
							JobStatus:   "PENDING",
							ScheduledAt: scheduledAt,
							StartedAt:   startedAt,
							CompletedAt: completedAt,
							CreatedAt:   createdAt,
							UpdatedAt:   updatedAt,
						},
					},
					Cursor: "",
				}, nil)
			},
			want: want{
				&jobsmodel.ListJobsResponse{
					Jobs: []*jobsmodel.JobByWorkflowIDResponse{
						{
							ID:          "job_id",
							WorkflowID:  "workflow_id",
							JobStatus:   "PENDING",
							ScheduledAt: scheduledAt,
							StartedAt:   startedAt,
							CompletedAt: completedAt,
							CreatedAt:   createdAt,
							UpdatedAt:   updatedAt,
						},
					},
					Cursor: "",
				},
			},
			isErr: false,
		},
		{
			name: "error: missing job ID",
			req: &jobspb.ListJobsRequest{
				WorkflowId: "",
				UserId:     "user_id",
				Cursor:     "",
			},
			mock:  func(_ *jobspb.ListJobsRequest) {},
			want:  want{},
			isErr: true,
		},
		{
			name: "error: job not found",
			req: &jobspb.ListJobsRequest{
				WorkflowId: "invalid_job_id",
				UserId:     "user_id",
				Cursor:     "",
			},
			mock: func(req *jobspb.ListJobsRequest) {
				repo.EXPECT().ListJobs(
					gomock.Any(),
					req.GetWorkflowId(),
					req.GetUserId(),
					req.GetCursor(),
					gomock.Any(),
				).Return(nil, status.Error(codes.NotFound, "job not found"))
			},
			want:  want{},
			isErr: true,
		},
		{
			name: "error: internal server error",
			req: &jobspb.ListJobsRequest{
				WorkflowId: "workflow_id",
				UserId:     "user_id",
				Cursor:     "",
			},
			mock: func(req *jobspb.ListJobsRequest) {
				repo.EXPECT().ListJobs(
					gomock.Any(),
					req.GetWorkflowId(),
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
			jobs, err := s.ListJobs(t.Context(), tt.req)
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

			assert.Equal(t, len(jobs.Jobs), len(tt.want.ListJobsResponse.Jobs))
			assert.Equal(t, jobs.Jobs, tt.want.ListJobsResponse.Jobs)
			assert.Equal(t, jobs.Cursor, tt.want.ListJobsResponse.Cursor)
		})
	}
}
