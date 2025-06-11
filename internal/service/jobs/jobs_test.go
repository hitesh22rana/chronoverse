package jobs_test

import (
	"database/sql"
	"testing"
	"time"

	"github.com/go-playground/validator/v10"
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

	// Create a new service
	s := jobs.New(validator.New(), repo)

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

	// Create a new service
	s := jobs.New(validator.New(), repo)

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

	// Create a new service
	s := jobs.New(validator.New(), repo)

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

	// Create a new service
	s := jobs.New(validator.New(), repo)

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

	// Create a new service
	s := jobs.New(validator.New(), repo)

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
				repo.EXPECT().GetJobLogs(
					gomock.Any(),
					req.GetId(),
					req.GetWorkflowId(),
					req.GetUserId(),
					req.GetCursor(),
				).Return(&jobsmodel.GetJobLogsResponse{
					ID:         "job_id",
					WorkflowID: "workflow_id",
					JobLogs: []*jobsmodel.JobLog{
						{
							Timestamp:   timestamp,
							Message:     "log 1",
							SequenceNum: 1,
						},
						{
							Timestamp:   timestamp,
							Message:     "log 2",
							SequenceNum: 2,
						},
					},
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
						},
						{
							Timestamp:   timestamp,
							Message:     "log 2",
							SequenceNum: 2,
						},
					},
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
				repo.EXPECT().GetJobLogs(
					gomock.Any(),
					req.GetId(),
					req.GetWorkflowId(),
					req.GetUserId(),
					req.GetCursor(),
				).Return(nil, status.Error(codes.NotFound, "job not found"))
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
				repo.EXPECT().GetJobLogs(
					gomock.Any(),
					req.GetId(),
					req.GetWorkflowId(),
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

func TestListJobs(t *testing.T) {
	ctrl := gomock.NewController(t)

	// Create a mock repository
	repo := jobsmock.NewMockRepository(ctrl)

	// Create a new service
	s := jobs.New(validator.New(), repo)

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
