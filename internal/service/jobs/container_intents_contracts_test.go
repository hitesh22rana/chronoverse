package jobs_test

import (
	"context"
	"strings"
	"testing"

	"github.com/go-playground/validator/v10"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	"github.com/hitesh22rana/chronoverse/internal/pkg/auth"
	jobsservice "github.com/hitesh22rana/chronoverse/internal/service/jobs"
	jobsmock "github.com/hitesh22rana/chronoverse/internal/service/jobs/mock"
	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"
)

const intentContractID = "00000000-0000-4000-8000-000000000015"

type intentServiceCall struct {
	name, operation string
	call            func(context.Context, *jobspb.ContainerIntentCommand) (proto.Message, error)
	list, claim     bool
}

func intentServiceCalls(s *jobsservice.Service) []intentServiceCall {
	return []intentServiceCall{
		{name: "ensure", operation: "ensure", call: func(c context.Context, r *jobspb.ContainerIntentCommand) (proto.Message, error) {
			return s.EnsureContainerIntent(c, r)
		}},
		{name: "get", operation: "get", call: func(c context.Context, r *jobspb.ContainerIntentCommand) (proto.Message, error) {
			return s.GetContainerIntent(c, r)
		}},
		{name: "create", operation: "reserve_create", call: func(c context.Context, r *jobspb.ContainerIntentCommand) (proto.Message, error) {
			return s.ReserveContainerCreate(c, r)
		}},
		{name: "start", operation: "reserve_start", call: func(c context.Context, r *jobspb.ContainerIntentCommand) (proto.Message, error) {
			return s.ReserveContainerStart(c, r)
		}},
		{name: "close", operation: "close", call: func(c context.Context, r *jobspb.ContainerIntentCommand) (proto.Message, error) {
			return s.CloseContainerIntent(c, r)
		}},
		{name: "observe", operation: "observe", call: func(c context.Context, r *jobspb.ContainerIntentCommand) (proto.Message, error) {
			return s.ObserveContainer(c, r)
		}},
		{name: "report", operation: "report", call: func(c context.Context, r *jobspb.ContainerIntentCommand) (proto.Message, error) {
			return s.ReportContainerInspection(c, r)
		}},
		{name: "redrive", operation: "redrive", call: func(c context.Context, r *jobspb.ContainerIntentCommand) (proto.Message, error) {
			return s.RedriveContainerIntent(c, r)
		}},
		{name: "claim", list: true, claim: true, call: func(c context.Context, r *jobspb.ContainerIntentCommand) (proto.Message, error) {
			return s.ClaimContainerIntents(c, r)
		}},
		{name: "list", list: true, call: func(c context.Context, r *jobspb.ContainerIntentCommand) (proto.Message, error) {
			return s.ListContainerIntents(c, r)
		}},
	}
}

func validIntentServiceRequest() *jobspb.ContainerIntentCommand {
	return &jobspb.ContainerIntentCommand{
		JobId: intentContractID, IntentId: intentContractID, Token: intentContractID, LeaseToken: "live-lease", Attempt: 2,
		WorkflowGeneration: 7, RuntimeInstance: "original-daemon", RequestHash: "frozen", ContainerId: strings.Repeat("a", 64),
		Outcome: "ERROR", ErrorCode: "Unavailable", ErrorMessage: "original diagnostic", Stage: "CREATE", Reason: "daemon restored", Generation: 3, BatchSize: 5,
	}
}

//nolint:gocyclo // One public RPC matrix preserves authority evidence and repository failure semantics.
func TestContainerIntentServicePreservesAuthorityAndRepositoryFailures(t *testing.T) {
	for _, failure := range []bool{false, true} {
		for index := range intentServiceCalls(jobsservice.New(validator.New(), nil, nil)) {
			repo := jobsmock.NewMockRepository(gomock.NewController(t))
			s := jobsservice.New(validator.New(), repo, nil)
			call := intentServiceCalls(s)[index]
			name := call.name + "/success"
			if failure {
				name = call.name + "/repository failure"
			}
			t.Run(name, func(t *testing.T) {
				ctx := auth.WithSubject(auth.WithRole(t.Context(), auth.RoleAdmin.String()), "internal/executor")
				record := &jobsmodel.ContainerIntent{
					ID:                 intentContractID,
					JobID:              intentContractID,
					RuntimeInstance:    "original-daemon",
					WorkflowGeneration: 7,
					Generation:         3,
					Closed:             true,
					HeldCapacity:       true,
					Stage:              "CREATE",
					ImageReference:     "image@sha256:digest",
					InspectionErrors:   2,
				}
				check := func(command *jobsmodel.ContainerIntentCommand) {
					if command.JobID != intentContractID ||
						command.IntentID != intentContractID ||
						command.WorkflowGeneration != 7 ||
						command.LeaseToken != "live-lease" ||
						command.RuntimeInstance != "original-daemon" ||
						command.ErrorMessage != "original diagnostic" {
						t.Fatalf("lost authority/diagnostic: %+v", command)
					}
				}
				if call.list {
					repo.EXPECT().ListContainerIntents(gomock.Any(), gomock.Any(), call.claim).DoAndReturn(func(_ context.Context, c *jobsmodel.ContainerIntentCommand, _ bool) ([]*jobsmodel.ContainerIntent, error) {
						check(c)
						if failure {
							return nil, status.Error(codes.Unavailable, "repository unavailable")
						}
						return []*jobsmodel.ContainerIntent{record}, nil
					})
				} else {
					repo.EXPECT().UpdateContainerIntent(gomock.Any(), call.operation, gomock.Any()).DoAndReturn(func(
						_ context.Context,
						_ string,
						c *jobsmodel.ContainerIntentCommand,
					) (*jobsmodel.ContainerIntentResult, error) {
						check(c)
						if failure {
							return nil, status.Error(codes.Unavailable, "repository unavailable")
						}
						return &jobsmodel.ContainerIntentResult{Intent: record, Granted: true}, nil
					})
				}
				response, err := call.call(ctx, validIntentServiceRequest())
				if failure {
					if status.Code(err) != codes.Unavailable {
						t.Fatalf("repository error changed: %v", err)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				var got *jobspb.ContainerIntent
				if call.list {
					list, ok := response.(*jobspb.ListContainerIntentsResponse)
					if !ok {
						t.Fatalf("wrong listing response: %T", response)
					}
					got = list.GetIntents()[0]
				} else {
					result, ok := response.(*jobspb.ContainerIntentResponse)
					if !ok {
						t.Fatalf("wrong intent response: %T", response)
					}
					got = result.GetIntent()
				}
				if got.GetId() != intentContractID ||
					got.GetRuntimeInstance() != "original-daemon" ||
					got.GetWorkflowGeneration() != 7 ||
					!got.GetClosed() ||
					!got.GetHeldCapacity() ||
					got.GetInspectionErrors() != 2 {
					t.Fatalf("lost response evidence: %v", got)
				}
			})
		}
	}
}

func TestContainerIntentServiceRejectsMalformedCommandsBeforeStorage(t *testing.T) {
	for _, tc := range []struct {
		name, method string
		change       func(*jobspb.ContainerIntentCommand)
	}{
		{"batch below zero", "list", func(r *jobspb.ContainerIntentCommand) { r.BatchSize = -1 }},
		{"batch too large", "list", func(r *jobspb.ContainerIntentCommand) { r.BatchSize = 501 }},
		{"invalid token", "list", func(r *jobspb.ContainerIntentCommand) { r.Token = "invalid" }},
		{"long stage", "list", func(r *jobspb.ContainerIntentCommand) { r.Stage = strings.Repeat("s", 33) }},
		{"long outcome", "list", func(r *jobspb.ContainerIntentCommand) { r.Outcome = strings.Repeat("o", 33) }},
		{"invalid job", "list", func(r *jobspb.ContainerIntentCommand) { r.JobId = "invalid" }},
		{"invalid intent", "list", func(r *jobspb.ContainerIntentCommand) { r.IntentId = "invalid" }},
		{"long diagnostic", "list", func(r *jobspb.ContainerIntentCommand) { r.ErrorMessage = strings.Repeat("e", 8193) }},
		{"long reason", "list", func(r *jobspb.ContainerIntentCommand) { r.Reason = strings.Repeat("r", 2049) }},
		{"long runtime", "list", func(r *jobspb.ContainerIntentCommand) { r.RuntimeInstance = strings.Repeat("r", 257) }},
		{"long fingerprint", "list", func(r *jobspb.ContainerIntentCommand) { r.RequestHash = strings.Repeat("f", 257) }},
		{"missing lease", "create", func(r *jobspb.ContainerIntentCommand) { r.LeaseToken = "" }},
		{"missing lookup", "get", func(r *jobspb.ContainerIntentCommand) { r.IntentId = ""; r.JobId = "" }},
		{"missing materialization", "observe", func(r *jobspb.ContainerIntentCommand) { r.ContainerId = "" }},
		{"missing inspection token", "report", func(r *jobspb.ContainerIntentCommand) { r.Token = "" }},
		{"unknown inspection outcome", "report", func(r *jobspb.ContainerIntentCommand) { r.Outcome = "unsupported" }},
		{"missing repair reason", "redrive", func(r *jobspb.ContainerIntentCommand) { r.Reason = " " }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := jobsservice.New(validator.New(), jobsmock.NewMockRepository(gomock.NewController(t)), nil)
			ctx := auth.WithSubject(auth.WithRole(t.Context(), auth.RoleAdmin.String()), "internal/executor")
			req := validIntentServiceRequest()
			tc.change(req)
			for _, call := range intentServiceCalls(s) {
				if call.name == tc.method {
					_, err := call.call(ctx, req)
					if status.Code(err) != codes.InvalidArgument {
						t.Fatalf("malformed command reached storage: %v", err)
					}
				}
			}
		})
	}
	for _, method := range []string{"ensure", "get", "claim", "list", "redrive"} {
		t.Run(method+"/nil", func(t *testing.T) {
			s := jobsservice.New(validator.New(), jobsmock.NewMockRepository(gomock.NewController(t)), nil)
			ctx := auth.WithSubject(auth.WithRole(t.Context(), auth.RoleAdmin.String()), "internal/executor")
			for _, call := range intentServiceCalls(s) {
				if call.name == method {
					_, err := call.call(ctx, nil)
					if status.Code(err) != codes.InvalidArgument {
						t.Fatalf("nil command reached storage: %v", err)
					}
				}
			}
		})
	}
}

func TestContainerIntentDiscoveryAndRedriveRequireVerifiedPrincipal(t *testing.T) {
	for _, subject := range []string{"", "internal/workflow"} {
		for _, method := range []string{"ensure", "claim", "redrive"} {
			if method == "redrive" && subject != "" {
				continue
			}
			t.Run(method+"/"+subject, func(t *testing.T) {
				s := jobsservice.New(validator.New(), jobsmock.NewMockRepository(gomock.NewController(t)), nil)
				ctx := auth.WithRole(t.Context(), auth.RoleAdmin.String())
				if subject != "" {
					ctx = auth.WithSubject(ctx, subject)
				}
				for _, call := range intentServiceCalls(s) {
					if call.name == method {
						_, err := call.call(ctx, validIntentServiceRequest())
						if err == nil {
							t.Fatal("unverified principal reached storage")
						}
					}
				}
			})
		}
	}
}
