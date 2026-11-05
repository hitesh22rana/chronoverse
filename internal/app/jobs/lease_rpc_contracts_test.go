package jobs_test

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"

	"github.com/hitesh22rana/chronoverse/internal/app/jobs"
	jobsmock "github.com/hitesh22rana/chronoverse/internal/app/jobs/mock"
	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	"github.com/hitesh22rana/chronoverse/internal/pkg/auth"
	authmock "github.com/hitesh22rana/chronoverse/internal/pkg/auth/mock"
	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"
)

const (
	leaseContractJobID       = "00000000-0000-4000-8000-000000000001"
	leaseContractWorkflowID  = "00000000-0000-4000-8000-000000000002"
	leaseContractUserID      = "00000000-0000-4000-8000-000000000003"
	leaseContractSecondJobID = "00000000-0000-4000-8000-000000000004"
)

func leaseRPCConnection(t *testing.T, svc jobs.Service, role auth.Role) *grpc.ClientConn {
	t.Helper()
	ctrl := gomock.NewController(t)
	credentials := authmock.NewMockIAuth(ctrl)
	credentials.EXPECT().ValidateToken(gomock.Any(), auth.ServiceNameJobs).DoAndReturn(
		func(ctx context.Context, _ string) (context.Context, *jwt.Token, error) {
			if token, err := auth.ExtractAuthorizationTokenFromMetadata(ctx); err != nil || token != "validated-token" {
				t.Errorf("authentication received token=%q err=%v", token, err)
			}
			return auth.WithAudience(auth.WithRole(ctx, string(role)), auth.ServiceNameJobs), &jwt.Token{Valid: true}, nil
		},
	)
	server := jobs.New(t.Context(), &jobs.Config{Deadline: time.Second}, credentials, svc)
	listener := bufconn.Listen(1024 * 1024)
	stopped := make(chan error, 1)
	go func() { stopped <- server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		if closeErr := listener.Close(); closeErr != nil {
			t.Error(closeErr)
		}
		if serveErr := <-stopped; serveErr != nil && !errors.Is(serveErr, grpc.ErrServerStopped) {
			t.Error(serveErr)
		}
	})
	conn, err := grpc.NewClient("passthrough:///lease-contract", grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return listener.DialContext(ctx) }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := conn.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	return conn
}

type leaseRPCContract struct {
	method   string
	request  proto.Message
	response proto.Message
	expect   func(*jobsmock.MockService, func(context.Context, proto.Message), error)
}

func leaseRPCContracts() []leaseRPCContract {
	expires := time.Date(2026, 5, 4, 12, 13, 14, 123456789, time.FixedZone("offset", 3600))
	expired := &jobsmodel.ExpiredJobLease{
		ID: leaseContractJobID, WorkflowID: leaseContractWorkflowID, UserID: leaseContractUserID, LeaseToken: "lease", Trigger: "AUTOMATIC",
		ScheduledAt: expires, Attempts: 4, LogRetention: true, ContainerID: sql.NullString{String: "container", Valid: true},
		LeasedBy: sql.NullString{String: "worker", Valid: true}, RuntimeNodeID: sql.NullString{String: "runtime", Valid: true},
		RuntimeEndpoint: sql.NullString{String: "tcp://runtime:2376", Valid: true}, RuntimeUnavailable: true,
	}
	second := &jobsmodel.ExpiredJobLease{
		ID: leaseContractSecondJobID, WorkflowID: leaseContractWorkflowID, UserID: leaseContractUserID, LeaseToken: "second-lease",
		Trigger: "MANUAL", ScheduledAt: expires.Add(time.Second), Attempts: 1,
	}
	claim := &jobspb.ClaimJobResponse{Claimed: true, Id: leaseContractJobID, WorkflowId: leaseContractWorkflowID, UserId: leaseContractUserID, LeaseToken: "lease", Attempts: 4}
	node := &jobspb.GetReadyRuntimeNodeResponse{RuntimeNodeId: "runtime", RuntimeEndpoint: "tcp://runtime:2376"}
	nodes := &jobspb.ListReadyRuntimeNodesResponse{Nodes: []*jobspb.GetReadyRuntimeNodeResponse{node}}
	return []leaseRPCContract{
		{
			method: "ClaimJob",
			request: &jobspb.ClaimJobRequest{
				Id:                   leaseContractJobID,
				WorkflowId:           leaseContractWorkflowID,
				WorkerId:             "worker",
				ProcessInstanceId:    "123e4567-e89b-12d3-a456-426614174000",
				CommandId:            "claim",
				LeaseDurationSeconds: 31,
				DispatchAttempt:      3,
			},
			response: claim,
			expect: func(svc *jobsmock.MockService, check func(context.Context, proto.Message), failure error) {
				svc.EXPECT().ClaimJob(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, req *jobspb.ClaimJobRequest) (*jobspb.ClaimJobResponse, error) {
					check(ctx, req)
					if failure != nil {
						return nil, failure
					}
					return claim, nil
				})
			},
		},
		{
			method:   "GetReadyRuntimeNode",
			request:  &jobspb.GetReadyRuntimeNodeRequest{},
			response: node,
			expect: func(svc *jobsmock.MockService, check func(context.Context, proto.Message), failure error) {
				svc.EXPECT().GetReadyRuntimeNode(gomock.Any(),
					gomock.Any()).DoAndReturn(func(ctx context.Context,
					req *jobspb.GetReadyRuntimeNodeRequest) (*jobspb.GetReadyRuntimeNodeResponse,
					error,
				) {
					check(ctx, req)
					if failure != nil {
						return nil, failure
					}
					return node, nil
				})
			},
		},
		{
			method:   "ListReadyRuntimeNodes",
			request:  &jobspb.ListReadyRuntimeNodesRequest{},
			response: nodes,
			expect: func(svc *jobsmock.MockService, check func(context.Context, proto.Message), failure error) {
				svc.EXPECT().ListReadyRuntimeNodes(gomock.Any(),
					gomock.Any()).DoAndReturn(func(ctx context.Context,
					req *jobspb.ListReadyRuntimeNodesRequest) (*jobspb.ListReadyRuntimeNodesResponse,
					error,
				) {
					check(ctx, req)
					if failure != nil {
						return nil, failure
					}
					return nodes, nil
				})
			},
		},
		{
			method: "RenewJobLease",
			request: &jobspb.RenewJobLeaseRequest{
				Id:                   leaseContractJobID,
				LeaseToken:           "lease",
				LeaseDurationSeconds: 31,
			},
			response: &jobspb.RenewJobLeaseResponse{LeaseExpiresAt: expires.UTC().Format(time.RFC3339Nano)},
			expect: func(svc *jobsmock.MockService, check func(context.Context, proto.Message), failure error) {
				svc.EXPECT().RenewJobLease(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, req *jobspb.RenewJobLeaseRequest) (time.Time, error) {
					check(ctx, req)
					if failure != nil {
						return time.Time{}, failure
					}
					return expires, nil
				})
			},
		},
		{
			method: "AttachJobContainer",
			request: &jobspb.AttachJobContainerRequest{
				Id:            leaseContractJobID,
				LeaseToken:    "lease",
				ContainerId:   "container",
				RuntimeNodeId: "runtime",
				CommandId:     "attach",
			},
			response: &jobspb.AttachJobContainerResponse{},
			expect: func(svc *jobsmock.MockService, check func(context.Context, proto.Message), failure error) {
				svc.EXPECT().AttachJobContainer(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, req *jobspb.AttachJobContainerRequest) error {
					check(ctx, req)
					return failure
				})
			},
		},
		{
			method: "CompleteJob",
			request: &jobspb.CompleteJobRequest{
				Id:         leaseContractJobID,
				LeaseToken: "lease",
				CommandId:  "complete",
			},
			response: &jobspb.CompleteJobResponse{},
			expect: func(svc *jobsmock.MockService, check func(context.Context, proto.Message), failure error) {
				svc.EXPECT().CompleteJob(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, req *jobspb.CompleteJobRequest) error { check(ctx, req); return failure })
			},
		},
		{
			method: "FailJob",
			request: &jobspb.FailJobRequest{
				Id:                 leaseContractJobID,
				LeaseToken:         "lease",
				CommandId:          "fail",
				FailureKind:        "SYSTEM",
				ErrorCode:          "unavailable",
				ErrorMessage:       "runtime lost",
				TerminalReasonCode: "SYSTEM_ERROR",
			},
			response: &jobspb.FailJobResponse{},
			expect: func(svc *jobsmock.MockService, check func(context.Context, proto.Message), failure error) {
				svc.EXPECT().FailJob(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, req *jobspb.FailJobRequest) error { check(ctx, req); return failure })
			},
		},
		{
			method: "CancelClaimedJob",
			request: &jobspb.CancelClaimedJobRequest{
				Id:                 leaseContractJobID,
				LeaseToken:         "lease",
				CommandId:          "cancel",
				TerminalReasonCode: "WORKFLOW_TERMINATED",
			},
			response: &jobspb.CancelClaimedJobResponse{},
			expect: func(svc *jobsmock.MockService, check func(context.Context, proto.Message), failure error) {
				svc.EXPECT().CancelClaimedJob(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, req *jobspb.CancelClaimedJobRequest) error { check(ctx, req); return failure })
			},
		},
		{
			method: "ReleaseJobForRetry",
			request: &jobspb.ReleaseJobForRetryRequest{
				Id:            leaseContractJobID,
				LeaseToken:    "lease",
				CommandId:     "retry",
				NextAttemptAt: expires.Format(time.RFC3339Nano),
				ErrorCode:     "retry",
				ErrorMessage:  "temporary",
			},
			response: &jobspb.ReleaseJobForRetryResponse{},
			expect: func(svc *jobsmock.MockService, check func(context.Context, proto.Message), failure error) {
				svc.EXPECT().ReleaseJobForRetry(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, req *jobspb.ReleaseJobForRetryRequest) error {
					check(ctx, req)
					return failure
				})
			},
		},
		{
			method: "RecoverExpiredJobLeases",
			request: &jobspb.RecoverExpiredJobLeasesRequest{
				BatchSize:            7,
				WorkerId:             "worker",
				ProcessInstanceId:    "123e4567-e89b-12d3-a456-426614174000",
				CommandId:            "recover",
				LeaseDurationSeconds: 31,
			},
			response: &jobspb.RecoverExpiredJobLeasesResponse{Jobs: []*jobspb.ExpiredJobLease{{
				Id:                 leaseContractJobID,
				WorkflowId:         leaseContractWorkflowID,
				UserId:             leaseContractUserID,
				LeaseToken:         "lease",
				Trigger:            "AUTOMATIC",
				ScheduledAt:        expires.Format(time.RFC3339Nano),
				Attempts:           4,
				LogRetention:       true,
				ContainerId:        "container",
				LeasedBy:           "worker",
				RuntimeNodeId:      "runtime",
				RuntimeEndpoint:    "tcp://runtime:2376",
				RuntimeUnavailable: true,
			}, {
				Id: leaseContractSecondJobID, WorkflowId: leaseContractWorkflowID, UserId: leaseContractUserID, LeaseToken: "second-lease", Trigger: "MANUAL",
				ScheduledAt: expires.Add(time.Second).Format(time.RFC3339Nano), Attempts: 1,
			}}},
			expect: func(svc *jobsmock.MockService, check func(context.Context, proto.Message), failure error) {
				svc.EXPECT().RecoverExpiredJobLeases(gomock.Any(),
					gomock.Any()).DoAndReturn(func(ctx context.Context,
					req *jobspb.RecoverExpiredJobLeasesRequest) ([]*jobsmodel.ExpiredJobLease,
					error,
				) {
					check(ctx, req)
					if failure != nil {
						return nil, failure
					}
					return []*jobsmodel.ExpiredJobLease{expired, second}, nil
				})
			},
		},
	}
}

func TestLeaseRPCContracts(t *testing.T) {
	for _, tc := range leaseRPCContracts() {
		for _, outcome := range []string{"success", "service failure", "non-admin with forged metadata"} {
			t.Run(tc.method+"/"+outcome, func(t *testing.T) {
				assertLeaseRPCOutcome(t, &tc, outcome)
			})
		}
	}
}

func assertLeaseRPCOutcome(t *testing.T, tc *leaseRPCContract, outcome string) {
	t.Helper()
	ctrl := gomock.NewController(t)
	svc := jobsmock.NewMockService(ctrl)
	seen := make(chan context.Context, 1)
	role := auth.RoleAdmin
	var failure error
	if outcome == "non-admin with forged metadata" {
		role = auth.RoleUser
	} else {
		if outcome == "service failure" {
			failure = status.Error(codes.Aborted, "lease ownership changed")
		}
		tc.expect(svc, func(ctx context.Context, req proto.Message) {
			assertLeaseRPCInvocation(ctx, t, tc.request, req, seen)
		}, failure)
	}
	conn := leaseRPCConnection(t, svc, role)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	ctx = auth.WithAuthorizationTokenInMetadata(ctx, "validated-token")
	ctx = metadata.AppendToOutgoingContext(ctx, "role", string(auth.RoleAdmin))
	wantResponse := proto.Clone(tc.response)
	response := tc.response.ProtoReflect().New().Interface()
	err := conn.Invoke(ctx, "/jobs.JobsService/"+tc.method, tc.request, response)
	if outcome == "non-admin with forged metadata" {
		if status.Code(err) != codes.PermissionDenied {
			t.Fatalf("non-admin error=%v", err)
		}
		return
	}
	if failure != nil {
		if status.Code(err) != status.Code(failure) || status.Convert(err).Message() != status.Convert(failure).Message() {
			t.Fatalf("error=%v, want %v", err, failure)
		}
	} else if err != nil || !proto.Equal(response, wantResponse) {
		t.Fatalf("response=%v err=%v, want %v", response, err, wantResponse)
	}
	select {
	case got := <-seen:
		if !errors.Is(got.Err(), context.Canceled) {
			t.Errorf("service context not released after return: %v", got.Err())
		}
	default:
		t.Fatal("service was not invoked")
	}
}

func assertLeaseRPCInvocation(ctx context.Context, t *testing.T, want, got proto.Message, seen chan<- context.Context) {
	t.Helper()
	if !proto.Equal(got, want) {
		t.Errorf("service request=%v, want %v", got, want)
	}
	deadline, ok := ctx.Deadline()
	if !ok || time.Until(deadline) <= 0 || time.Until(deadline) > time.Second {
		t.Error("service context lacks bounded server deadline")
	}
	seen <- ctx
}
