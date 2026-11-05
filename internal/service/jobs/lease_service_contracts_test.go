package jobs_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/go-playground/validator/v10"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	"github.com/hitesh22rana/chronoverse/internal/service/jobs"
	jobsmock "github.com/hitesh22rana/chronoverse/internal/service/jobs/mock"
	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"
)

const (
	leaseContractJobID       = "00000000-0000-4000-8000-000000000001"
	leaseContractWorkflowID  = "00000000-0000-4000-8000-000000000002"
	leaseContractUserID      = "00000000-0000-4000-8000-000000000003"
	leaseContractSecondJobID = "00000000-0000-4000-8000-000000000004"
)

type leaseContextKey struct{}

type leaseServiceContract struct {
	name     string
	request  proto.Message
	required []string
	invalid  map[string]protoreflect.Value
	invoke   func(context.Context, *jobs.Service) (any, error)
	expect   func(*jobsmock.MockRepository, func(context.Context), error) any
}

func leaseServiceCases() []func() *leaseServiceContract {
	expires := time.Date(2026, 5, 4, 12, 13, 14, 123456789, time.FixedZone("offset", 3600))
	recovered := []*jobsmodel.ExpiredJobLease{{ID: leaseContractJobID, LeaseToken: "new-lease", Attempts: 4}, {ID: leaseContractSecondJobID, LeaseToken: "second-lease", Attempts: 1}}
	claimed := &jobsmodel.ClaimedJob{
		ID:               leaseContractJobID,
		WorkflowID:       leaseContractWorkflowID,
		UserID:           leaseContractUserID,
		LeaseToken:       "lease",
		Trigger:          "AUTOMATIC",
		Attempts:         4,
		DispatchAttempts: 3,
		ScheduledAt:      expires,
		LeaseExpiresAt:   expires,
	}
	return []func() *leaseServiceContract{
		func() *leaseServiceContract {
			req := &jobspb.ClaimJobRequest{
				Id:                   leaseContractJobID,
				WorkflowId:           leaseContractWorkflowID,
				WorkerId:             "worker",
				ProcessInstanceId:    "123e4567-e89b-12d3-a456-426614174000",
				CommandId:            "claim",
				LeaseDurationSeconds: 17,
				DispatchAttempt:      3,
			}
			return &leaseServiceContract{
				name: "ClaimJob", request: req, required: []string{"id", "workflow_id", "worker_id", "process_instance_id", "command_id", "lease_duration_seconds", "dispatch_attempt"},
				invalid: map[string]protoreflect.Value{
					"process_instance_id":    protoreflect.ValueOfString("not-a-uuid"),
					"lease_duration_seconds": protoreflect.ValueOfInt32(-1),
					"dispatch_attempt":       protoreflect.ValueOfInt32(-1),
				},
				invoke: func(ctx context.Context, svc *jobs.Service) (any, error) { return svc.ClaimJob(ctx, req) },
				expect: func(repo *jobsmock.MockRepository, check func(context.Context), failure error) any {
					repo.EXPECT().ClaimJob(gomock.Any(),
						req.GetId(),
						req.GetWorkflowId(),
						req.GetWorkerId(),
						req.GetProcessInstanceId(),
						req.GetCommandId(),
						17*time.Second,
						req.GetDispatchAttempt()).DoAndReturn(func(ctx context.Context,
						_ string,
						_ string,
						_ string,
						_ string,
						_ string,
						_ time.Duration,
						_ int32) (*jobsmodel.ClaimedJob,
						bool,
						string,
						error,
					) {
						check(ctx)
						if failure != nil {
							return nil, false, "", failure
						}
						return claimed, true, "", nil
					})
					return &jobspb.ClaimJobResponse{
						Claimed:          true,
						Id:               leaseContractJobID,
						WorkflowId:       leaseContractWorkflowID,
						UserId:           leaseContractUserID,
						LeaseToken:       "lease",
						Trigger:          "AUTOMATIC",
						Attempts:         4,
						DispatchAttempts: 3,
						ScheduledAt:      expires.Format(time.RFC3339Nano),
						LeaseExpiresAt:   expires.UTC().Format(time.RFC3339Nano),
					}
				},
			}
		},
		func() *leaseServiceContract {
			req := &jobspb.FailJobRequest{
				Id:                 leaseContractJobID,
				LeaseToken:         "lease",
				CommandId:          "fail",
				FailureKind:        "USER",
				TerminalReasonCode: "NON_ZERO_EXIT",
				ErrorCode:          "Aborted",
				ErrorMessage:       "workload failed",
			}
			return &leaseServiceContract{
				name: "FailJob", request: req, required: []string{"id", "lease_token", "command_id", "failure_kind", "terminal_reason_code"},
				invalid: map[string]protoreflect.Value{"failure_kind": protoreflect.ValueOfString("unknown"), "terminal_reason_code": protoreflect.ValueOfString("WORKFLOW_TERMINATED")},
				invoke:  func(ctx context.Context, svc *jobs.Service) (any, error) { return nil, svc.FailJob(ctx, req) },
				expect: func(repo *jobsmock.MockRepository, check func(context.Context), failure error) any {
					repo.EXPECT().FailJob(gomock.Any(),
						req.GetId(),
						req.GetLeaseToken(),
						req.GetFailureKind(),
						req.GetErrorCode(),
						req.GetErrorMessage(),
						req.GetTerminalReasonCode(),
						req.GetCommandId()).DoAndReturn(func(ctx context.Context,
						_ string,
						_ string,
						_ string,
						_ string,
						_ string,
						_ string,
						_ string,
					) error {
						check(ctx)
						return failure
					})
					return nil
				},
			}
		},
		func() *leaseServiceContract {
			req := &jobspb.CancelClaimedJobRequest{Id: leaseContractJobID, LeaseToken: "lease", CommandId: "cancel", TerminalReasonCode: "WORKFLOW_TERMINATED"}
			return &leaseServiceContract{
				name: "CancelClaimedJob", request: req, required: []string{"id", "lease_token", "command_id", "terminal_reason_code"},
				invalid: map[string]protoreflect.Value{"terminal_reason_code": protoreflect.ValueOfString("NON_ZERO_EXIT")},
				invoke:  func(ctx context.Context, svc *jobs.Service) (any, error) { return nil, svc.CancelClaimedJob(ctx, req) },
				expect: func(repo *jobsmock.MockRepository, check func(context.Context), failure error) any {
					repo.EXPECT().CancelClaimedJob(gomock.Any(),
						req.GetId(),
						req.GetLeaseToken(),
						req.GetTerminalReasonCode(),
						req.GetCommandId()).DoAndReturn(func(ctx context.Context,
						_ string,
						_ string,
						_ string,
						_ string,
					) error {
						check(ctx)
						return failure
					})
					return nil
				},
			}
		},
		func() *leaseServiceContract {
			req := &jobspb.RenewJobLeaseRequest{Id: leaseContractJobID, LeaseToken: "lease", LeaseDurationSeconds: 17}
			return &leaseServiceContract{
				name: "RenewJobLease", request: req, required: []string{"id", "lease_token", "lease_duration_seconds"},
				invalid: map[string]protoreflect.Value{"lease_duration_seconds": protoreflect.ValueOfInt32(-1)},
				invoke:  func(ctx context.Context, svc *jobs.Service) (any, error) { return svc.RenewJobLease(ctx, req) },
				expect: func(repo *jobsmock.MockRepository, check func(context.Context), failure error) any {
					repo.EXPECT().RenewJobLease(gomock.Any(),
						req.GetId(),
						req.GetLeaseToken(),
						17*time.Second).DoAndReturn(func(ctx context.Context,
						_ string,
						_ string,
						_ time.Duration) (time.Time,
						error,
					) {
						check(ctx)
						if failure != nil {
							return time.Time{}, failure
						}
						return expires, nil
					})
					return expires
				},
			}
		},
		func() *leaseServiceContract {
			req := &jobspb.AttachJobContainerRequest{Id: leaseContractJobID, LeaseToken: "lease", ContainerId: "container", RuntimeNodeId: "runtime", CommandId: "attach"}
			return &leaseServiceContract{
				name: "AttachJobContainer", request: req, required: []string{"id", "lease_token", "container_id", "runtime_node_id", "command_id"},
				invoke: func(ctx context.Context, svc *jobs.Service) (any, error) {
					return nil, svc.AttachJobContainer(ctx, req)
				},
				expect: func(repo *jobsmock.MockRepository, check func(context.Context), failure error) any {
					repo.EXPECT().AttachJobContainer(gomock.Any(),
						req.GetId(),
						req.GetLeaseToken(),
						req.GetContainerId(),
						req.GetRuntimeNodeId(),
						req.GetCommandId()).DoAndReturn(func(ctx context.Context,
						_ string,
						_ string,
						_ string,
						_ string,
						_ string,
					) error {
						check(ctx)
						return failure
					})
					return nil
				},
			}
		},
		func() *leaseServiceContract {
			req := &jobspb.CompleteJobRequest{Id: leaseContractJobID, LeaseToken: "lease", CommandId: "complete"}
			return &leaseServiceContract{
				name: "CompleteJob", request: req, required: []string{"id", "lease_token", "command_id"},
				invoke: func(ctx context.Context, svc *jobs.Service) (any, error) { return nil, svc.CompleteJob(ctx, req) },
				expect: func(repo *jobsmock.MockRepository, check func(context.Context), failure error) any {
					repo.EXPECT().CompleteJob(gomock.Any(),
						req.GetId(),
						req.GetLeaseToken(),
						req.GetCommandId()).DoAndReturn(func(ctx context.Context,
						_ string,
						_ string,
						_ string,
					) error {
						check(ctx)
						return failure
					})
					return nil
				},
			}
		},
		func() *leaseServiceContract {
			req := &jobspb.ReleaseJobForRetryRequest{
				Id:            leaseContractJobID,
				LeaseToken:    "lease",
				CommandId:     "retry",
				NextAttemptAt: "2026-05-04T12:13:14.123456789Z",
				ErrorCode:     "runtime-lost",
				ErrorMessage:  "recovery retry",
			}
			return &leaseServiceContract{
				name: "ReleaseJobForRetry", request: req, required: []string{"id", "lease_token", "command_id", "next_attempt_at"},
				invalid: map[string]protoreflect.Value{"next_attempt_at": protoreflect.ValueOfString("not-a-timestamp")},
				invoke: func(ctx context.Context, svc *jobs.Service) (any, error) {
					return nil, svc.ReleaseJobForRetry(ctx, req)
				},
				expect: func(repo *jobsmock.MockRepository, check func(context.Context), failure error) any {
					repo.EXPECT().ReleaseJobForRetry(gomock.Any(),
						req.GetId(),
						req.GetLeaseToken(),
						req.GetNextAttemptAt(),
						req.GetErrorCode(),
						req.GetErrorMessage(),
						req.GetCommandId()).DoAndReturn(func(ctx context.Context,
						_ string,
						_ string,
						_ string,
						_ string,
						_ string,
						_ string,
					) error {
						check(ctx)
						return failure
					})
					return nil
				},
			}
		},
		func() *leaseServiceContract {
			req := &jobspb.RecoverExpiredJobLeasesRequest{
				BatchSize:            7,
				WorkerId:             "worker",
				ProcessInstanceId:    "123e4567-e89b-12d3-a456-426614174000",
				CommandId:            "recover",
				LeaseDurationSeconds: 17,
			}
			return &leaseServiceContract{
				name: "RecoverExpiredJobLeases", request: req, required: []string{"batch_size", "worker_id", "process_instance_id", "command_id", "lease_duration_seconds"},
				invalid: map[string]protoreflect.Value{
					"process_instance_id":    protoreflect.ValueOfString("not-a-uuid"),
					"batch_size":             protoreflect.ValueOfInt32(-1),
					"lease_duration_seconds": protoreflect.ValueOfInt32(-1),
				},
				invoke: func(ctx context.Context, svc *jobs.Service) (any, error) {
					return svc.RecoverExpiredJobLeases(ctx, req)
				},
				expect: func(repo *jobsmock.MockRepository, check func(context.Context), failure error) any {
					repo.EXPECT().RecoverExpiredJobLeases(gomock.Any(),
						req.GetBatchSize(),
						req.GetWorkerId(),
						req.GetProcessInstanceId(),
						req.GetCommandId(),
						17*time.Second).DoAndReturn(func(ctx context.Context,
						_ int32,
						_ string,
						_ string,
						_ string,
						_ time.Duration) ([]*jobsmodel.ExpiredJobLease,
						error,
					) {
						check(ctx)
						if failure != nil {
							return nil, failure
						}
						return recovered, nil
					})
					want := make([]*jobsmodel.ExpiredJobLease, len(recovered))
					for i, lease := range recovered {
						copyLease := *lease
						want[i] = &copyLease
					}
					return want
				},
			}
		},
	}
}

func TestLeaseServiceValidationAndOwnership(t *testing.T) {
	for _, makeCase := range leaseServiceCases() {
		name := makeCase().name
		t.Run(name, func(t *testing.T) {
			for _, outcome := range []string{"success", "repository failure", "canceled caller"} {
				t.Run(outcome, func(t *testing.T) {
					assertLeaseServiceOutcome(t, makeCase(), outcome)
				})
			}
			for _, field := range makeCase().required {
				t.Run("missing "+field, func(t *testing.T) {
					tc := makeCase()
					descriptor := tc.request.ProtoReflect().Descriptor().Fields().ByName(protoreflect.Name(field))
					if descriptor == nil {
						t.Fatalf("unknown request field %s", field)
					}
					tc.request.ProtoReflect().Clear(descriptor)
					assertLeaseServiceInvalid(t, tc)
				})
			}
			for field, value := range makeCase().invalid {
				t.Run("invalid "+field, func(t *testing.T) {
					tc := makeCase()
					descriptor := tc.request.ProtoReflect().Descriptor().Fields().ByName(protoreflect.Name(field))
					if descriptor == nil {
						t.Fatalf("unknown request field %s", field)
					}
					tc.request.ProtoReflect().Set(descriptor, value)
					assertLeaseServiceInvalid(t, tc)
				})
			}
		})
	}
}

func assertLeaseServiceInvalid(t *testing.T, tc *leaseServiceContract) {
	t.Helper()
	ctrl := gomock.NewController(t)
	svc := jobs.New(validator.New(), jobsmock.NewMockRepository(ctrl), jobsmock.NewMockCache(ctrl))
	before := proto.Clone(tc.request)
	got, err := tc.invoke(t.Context(), svc)
	if !proto.Equal(tc.request, before) {
		t.Fatal("validation changed the caller request")
	}
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("invalid request reached repository or wrong status: %v", err)
	}
	if got != nil && !reflect.ValueOf(got).IsZero() {
		t.Fatalf("invalid request returned data: %v", got)
	}
}

func assertLeaseServiceOutcome(t *testing.T, tc *leaseServiceContract, outcome string) {
	t.Helper()
	ctrl := gomock.NewController(t)
	repo := jobsmock.NewMockRepository(ctrl)
	svc := jobs.New(validator.New(), repo, jobsmock.NewMockCache(ctrl))
	ctx, cancel := context.WithTimeout(context.WithValue(t.Context(), leaseContextKey{}, "claim-owner"), time.Minute)
	defer cancel()
	var failure error
	if outcome == "repository failure" {
		failure = errors.New("lease storage failure")
	}
	if outcome == "canceled caller" {
		cancel()
		failure = context.Canceled
	}
	want := tc.expect(repo, func(got context.Context) {
		if got.Value(leaseContextKey{}) != "claim-owner" || got.Done() != ctx.Done() || !errors.Is(got.Err(), ctx.Err()) {
			t.Error("repository lost caller context or cancellation")
		}
		gotDeadline, gotOK := got.Deadline()
		wantDeadline, wantOK := ctx.Deadline()
		if gotOK != wantOK || !gotDeadline.Equal(wantDeadline) {
			t.Error("repository lost caller deadline")
		}
	}, failure)
	before := proto.Clone(tc.request)
	got, err := tc.invoke(ctx, svc)
	if !proto.Equal(tc.request, before) {
		t.Fatal("service changed the caller request")
	}
	if failure != nil {
		if !errors.Is(err, failure) {
			t.Fatalf("error=%v, want original %v", err, failure)
		}
		if got != nil && !reflect.ValueOf(got).IsZero() {
			t.Fatalf("failure returned a result: %v", got)
		}
	} else if err != nil || !leaseServiceResultsEqual(got, want) {
		t.Fatalf("result=%v err=%v, want %v", got, err, want)
	}
}

func leaseServiceResultsEqual(got, want any) bool {
	if message, ok := got.(proto.Message); ok {
		expected, ok := want.(proto.Message)
		return ok && proto.Equal(message, expected)
	}
	return reflect.DeepEqual(got, want)
}
