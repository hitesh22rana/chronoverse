//nolint:testpackage // Faults cross the runtime/jobs boundary that owns send authority.
package executor

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	containerpkg "github.com/hitesh22rana/chronoverse/internal/pkg/kind/container"
	"github.com/hitesh22rana/chronoverse/internal/pkg/terminalreason"
	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"
)

type intentFaultAuth struct {
	fakeAuth
	calls, failAt int
}

func (a *intentFaultAuth) IssueToken(context.Context, string, ...string) (string, error) {
	a.calls++
	if a.calls == a.failAt {
		return "", status.Error(codes.Unavailable, "signing unavailable")
	}
	return "token", nil
}

type intentFaultJobs struct {
	*scriptedJobsClient
	fault   string
	reports []*jobspb.ContainerIntentCommand
}

func (j *intentFaultJobs) EnsureContainerIntent(_ context.Context, req *jobspb.ContainerIntentCommand, _ ...grpc.CallOption) (*jobspb.ContainerIntentResponse, error) {
	result := fakeIntent(req)
	switch j.fault {
	case "missing intent":
		result.Intent.Id = ""
	case "closed intent":
		result.Intent.Closed = true
	case "changed instance":
		result.Intent.RuntimeInstance = "replacement"
	case "changed node":
		result.Intent.RuntimeNodeId = "replacement"
	}
	return result, nil
}

func (j *intentFaultJobs) ReserveContainerCreate(_ context.Context, req *jobspb.ContainerIntentCommand, _ ...grpc.CallOption) (*jobspb.ContainerIntentResponse, error) {
	if j.fault == "create grant error" {
		return nil, status.Error(codes.Unavailable, "grant reply lost")
	}
	result := fakeIntent(req)
	result.Granted = j.fault != "create grant consumed"
	return result, nil
}

func (j *intentFaultJobs) ReserveContainerStart(_ context.Context, req *jobspb.ContainerIntentCommand, _ ...grpc.CallOption) (*jobspb.ContainerIntentResponse, error) {
	result := fakeIntent(req)
	result.Granted = j.fault != "start grant consumed"
	return result, nil
}

func (j *intentFaultJobs) CloseContainerIntent(_ context.Context, req *jobspb.ContainerIntentCommand, _ ...grpc.CallOption) (*jobspb.ContainerIntentResponse, error) {
	j.reports = append(j.reports, req)
	return fakeIntent(req), nil
}

type intentFaultRuntime struct {
	observedContainerSvc
	instanceFn                                           func(int) (string, error)
	inspectFn                                            func(int, string) (*containerpkg.State, error)
	createErr, startErr                                  error
	instanceCalls, inspectCalls, createCalls, startCalls int
}

func (c *intentFaultRuntime) RuntimeInstance(context.Context) (string, error) {
	c.instanceCalls++
	if c.instanceFn != nil {
		return c.instanceFn(c.instanceCalls)
	}
	return "test-daemon", nil
}

func (c *intentFaultRuntime) Inspect(ctx context.Context, id string) (*containerpkg.State, error) {
	c.inspectCalls++
	if c.inspectFn != nil {
		return c.inspectFn(c.inspectCalls, id)
	}
	return c.observedContainerSvc.Inspect(ctx, id)
}

//nolint:gocritic // The production interface freezes CreateRequest by value.
func (c *intentFaultRuntime) Create(ctx context.Context, req containerpkg.CreateRequest) (string, error) {
	c.createCalls++
	id, err := c.observedContainerSvc.Create(ctx, req)
	if c.createErr != nil {
		return "", c.createErr
	}
	return id, err
}

func (c *intentFaultRuntime) Start(ctx context.Context, d time.Duration, id string) (logs <-chan *jobsmodel.JobLog, errs <-chan error, err error) {
	c.startCalls++
	if c.startErr != nil {
		return nil, nil, c.startErr
	}
	return c.observedContainerSvc.Start(ctx, d, id)
}

//nolint:gocyclo // Each stage refusal must independently prevent Start and preserve its failure diagnostic.
func TestContainerIntentRefusalsNeverReachStart(t *testing.T) {
	for _, name := range []string{
		"authorization",
		"missing instance",
		"instance error",
		"missing intent",
		"closed intent",
		"changed instance",
		"changed node",
		"create grant error",
		"create grant consumed",
		"typed Create rejection",
		"failed resolution",
		"failed verification",
		"missing created object",
		"pre-start inspect failure",
		"start grant consumed",
		"Start failure",
	} {
		t.Run(name, func(t *testing.T) {
			runtime := &intentFaultRuntime{}
			scripted := &scriptedJobsClient{}
			auth := &intentFaultAuth{}
			fixture := newClaimedRunFixtureWithAuth(scripted, &scriptedWorkflowsClient{}, nil, runtime, auth)
			jobs := &intentFaultJobs{scriptedJobsClient: scripted, fault: name}
			fixture.repo.svc.Jobs = jobs
			wantCode := codes.Aborted
			wantCreates, wantStarts := 0, 0
			switch name {
			case "authorization":
				auth.failAt = 1
				wantCode = codes.Unavailable
			case "missing instance":
				runtime.instanceFn = func(int) (string, error) { return "", nil }
				wantCode = codes.Unavailable
			case "instance error":
				runtime.instanceFn = func(int) (string, error) { return "", status.Error(codes.Unavailable, "daemon down") }
				wantCode = codes.Unavailable
			case "create grant error":
				wantCode = codes.Unavailable
			case "typed Create rejection":
				runtime.createErr = terminalreason.Wrap(terminalreason.ExecutionFailed, status.Error(codes.InvalidArgument, "invalid workload"))
				wantCode = codes.InvalidArgument
				wantCreates = 1
			case "failed resolution":
				runtime.createErr = status.Error(codes.Unavailable, "lost create reply")
				runtime.inspectFn = func(int, string) (*containerpkg.State, error) {
					return nil, status.Error(codes.PermissionDenied, "inspect denied")
				}
				wantCode = codes.PermissionDenied
				wantCreates = 1
			case "failed verification":
				runtime.inspectFn = func(int, string) (*containerpkg.State, error) {
					return nil, status.Error(codes.PermissionDenied, "inspect denied")
				}
				wantCode = codes.PermissionDenied
				wantCreates = 1
			case "missing created object":
				runtime.inspectFn = func(int, string) (*containerpkg.State, error) {
					return nil, status.Error(codes.NotFound, "object disappeared")
				}
				wantCode = codes.Unavailable
				wantCreates = 1
			case "pre-start inspect failure":
				runtime.inspectFn = func(n int, id string) (*containerpkg.State, error) {
					if n == 2 {
						return nil, status.Error(codes.Unavailable, "pre-start inspection failed")
					}
					return runtime.observedContainerSvc.Inspect(t.Context(), id)
				}
				wantCode = codes.Unavailable
				wantCreates = 1
			case "start grant consumed":
				wantCreates = 1
			case "Start failure":
				runtime.startErr = status.Error(codes.Unavailable, "start transport failed")
				wantCode = codes.Unavailable
				wantCreates = 1
				wantStarts = 1
			}
			_, err := fixture.repo.executeContainerWorkflow(t.Context(), runtime, claimedJobID, claimedLeaseToken, "runtime-1", 1, containerWorkflow())
			if status.Code(err) != wantCode || runtime.createCalls != wantCreates || runtime.startCalls != wantStarts {
				t.Fatalf("unsafe stage progression: creates=%d starts=%d error=%v", runtime.createCalls, runtime.startCalls, err)
			}
			if wantCreates > 0 && (len(jobs.reports) != 1 || jobs.reports[0].GetErrorMessage() == "") {
				t.Fatalf("failure lost durable reporting: %+v", jobs.reports)
			}
		})
	}
}

func TestContainerObservationRequiresStableIdentityAndOwnership(t *testing.T) {
	for _, name := range []string{"first identity failure", "first identity changed", "confirmation failure", "foreign labels"} {
		t.Run(name, func(t *testing.T) {
			runtime := &intentFaultRuntime{}
			switch name {
			case "first identity failure":
				runtime.instanceFn = func(int) (string, error) { return "", status.Error(codes.Unavailable, "info failed") }
			case "first identity changed":
				runtime.instanceFn = func(int) (string, error) { return "replacement", nil }
			case "confirmation failure":
				runtime.instanceFn = func(n int) (string, error) {
					if n == 2 {
						return "", status.Error(codes.Unavailable, "confirmation failed")
					}
					return "test-daemon", nil
				}
			case "foreign labels":
				runtime.inspectFn = func(int, string) (*containerpkg.State, error) {
					return &containerpkg.State{ID: "foreign", Labels: map[string]string{"chronoverse.intent_id": "another"}}, nil
				}
			}
			fixture := newClaimedRunFixture(&scriptedJobsClient{}, &scriptedWorkflowsClient{}, nil, runtime)
			id, err := fixture.repo.resolveIntentContainer(t.Context(), runtime, &jobspb.ContainerIntent{Name: "cv-owned", Id: "intent", RuntimeInstance: "test-daemon"})
			if status.Code(err) != codes.Unavailable || id != "" || runtime.startCalls != 0 {
				t.Fatalf("unverified container adopted: id=%q error=%v", id, err)
			}
		})
	}
}

func TestContainerFailureDiagnosticsAndForeignContextErrors(t *testing.T) {
	long := strings.Repeat("界", 3000)
	if got := containerDiagnostic(status.Error(codes.Unavailable, long)); len([]rune(got)) != 2048 {
		t.Fatalf("unbounded diagnostic: %d", len([]rune(got)))
	}
	for _, cause := range []error{context.Canceled, fmt.Errorf("remote request: %w", context.DeadlineExceeded)} {
		_, err := callContainerIntent(t.Context(), func(context.Context, *jobspb.ContainerIntentCommand, ...grpc.CallOption) (*jobspb.ContainerIntentResponse, error) {
			return nil, cause
		}, &jobspb.ContainerIntentCommand{})
		if status.Code(err) != status.FromContextError(cause).Code() || t.Context().Err() != nil {
			t.Fatalf("remote context classification or caller ownership changed: %v", err)
		}
	}
}
