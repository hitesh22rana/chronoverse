//nolint:testpackage // Exercise the actual Docker lifecycle state behind recovery policy.
package container

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

//nolint:gocyclo // One real Docker lifecycle proves created-never-executed ordering end to end.
func TestIntegrationDockerCreatedContainerIsNeverAnExecutedSuccess(t *testing.T) {
	if testing.Short() {
		t.Skip("requires local Docker")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	workflow, err := NewDockerWorkflow(WithDockerHost("unix:///var/run/docker.sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer workflow.Client.Close()
	instance, err := workflow.RuntimeInstance(ctx)
	if err != nil {
		t.Fatal(err)
	}
	const image = "alpine:3.24.2"
	if err = workflow.Build(ctx, image); err != nil {
		t.Fatal(err)
	}
	identity := uuid.NewString()
	id, err := workflow.Create(ctx, CreateRequest{
		Name: "cv-created-" + identity, IntentID: identity, JobID: uuid.NewString(), Attempt: 1,
		Image: image, Cmd: []string{"sh", "-c", "echo executed"}, Timeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if removeErr := workflow.Remove(cleanup, id); removeErr != nil {
			t.Error(removeErr)
		}
	}()
	state, err := workflow.Inspect(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != "created" || state.Running || state.ExitCode != 0 || state.ID != id || state.Labels["chronoverse.intent_id"] != identity {
		t.Fatalf("unexpected unstarted lifecycle: %+v", state)
	}
	// This is the exact stop/replay/remove path a never-started recovery needs.
	if err = workflow.Terminate(ctx, id); err != nil {
		t.Fatalf("stop unstarted container: %v", err)
	}
	replayCtx, stopReplay := context.WithTimeout(ctx, 5*time.Second)
	defer stopReplay()
	logs, errs, err := workflow.Logs(replayCtx, id)
	if err != nil {
		t.Fatalf("open unstarted replay: %v", err)
	}
	for logs != nil || errs != nil {
		select {
		case _, ok := <-logs:
			if !ok {
				logs = nil
			} else {
				t.Fatal("unstarted workload produced output")
			}
		case replayErr, ok := <-errs:
			if !ok {
				errs = nil
			} else if replayErr != nil {
				t.Fatalf("unstarted replay: %v", replayErr)
			}
		case <-replayCtx.Done():
			t.Fatalf("unstarted replay did not finish: %v", replayCtx.Err())
		}
	}
	if err = workflow.Remove(ctx, id); err != nil {
		t.Fatal(err)
	}
	confirmed, err := workflow.RuntimeInstance(ctx)
	if err != nil || confirmed != instance {
		t.Fatalf("cleanup runtime changed: %q/%q %v", instance, confirmed, err)
	}
	if _, err = workflow.Inspect(ctx, id); err == nil {
		t.Fatal("unstarted container survived removal")
	}
}
