//nolint:testpackage // Exercises durable lease state with shared integration fixtures.
package jobs

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/pkg/commandidempotency"
	"github.com/hitesh22rana/chronoverse/internal/pkg/testkit"
)

func TestIntegrationRenewLeaseUsesDatabaseClockAndPreservesOwnership(t *testing.T) {
	ctx := t.Context()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)
	fixture := seedClaimedJob(ctx, t, pg, repo)
	setLeaseMinutes(ctx, t, pg, fixture.JobID, activeFixtureMinutes)
	before := readLeaseOwnership(ctx, t, pg, fixture.JobID)
	var earliest time.Time
	if err := pg.QueryRow(ctx, `SELECT clock_timestamp() AT TIME ZONE 'utc'`).Scan(&earliest); err != nil {
		t.Fatal(err)
	}
	expires, err := repo.RenewJobLease(ctx, fixture.JobID, fixture.LeaseToken, 61*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	after := readLeaseOwnership(ctx, t, pg, fixture.JobID)
	var latest time.Time
	if clockErr := pg.QueryRow(ctx, `SELECT clock_timestamp() AT TIME ZONE 'utc'`).Scan(&latest); clockErr != nil {
		t.Fatal(clockErr)
	}
	if !after.LeaseExpiresAt.Valid || !after.LastHeartbeatAt.Valid || !expires.Equal(after.LeaseExpiresAt.Time) {
		t.Fatalf("returned expiry %v does not match durable renewal: %+v", expires, after)
	}
	if duration := after.LeaseExpiresAt.Time.Sub(after.LastHeartbeatAt.Time); duration != 61*time.Second {
		t.Fatalf("database renewal interval = %s, want 61s", duration)
	}
	if heartbeat := after.LastHeartbeatAt.Time; heartbeat.Before(earliest) || heartbeat.After(latest) {
		t.Fatalf("heartbeat %v is outside database renewal window [%v, %v]", heartbeat, earliest, latest)
	}
	want := before
	want.LeaseExpiresAt, want.LastHeartbeatAt = after.LeaseExpiresAt, after.LastHeartbeatAt
	if after != want {
		t.Fatalf("renewal changed ownership: before=%+v after=%+v", before, after)
	}
}

func TestIntegrationRenewLeaseRejectsLostAuthorityWithoutMutation(t *testing.T) {
	for _, scenario := range []string{"wrong token", "expired lease", "released job", "canceled caller", "invalid ID"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := t.Context()
			pg := testkit.Postgres(t)
			repo := newTestRepository(t)
			fixture := seedClaimedJob(ctx, t, pg, repo)
			setLeaseMinutes(ctx, t, pg, fixture.JobID, activeFixtureMinutes)
			callCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			jobID, token, want := fixture.JobID, fixture.LeaseToken, codes.FailedPrecondition
			switch scenario {
			case "wrong token":
				token += "-stale"
			case "expired lease":
				setLeaseMinutes(ctx, t, pg, fixture.JobID, -1)
			case "released job":
				releaseForRetry(t, repo, fixture.JobID, fixture.LeaseToken, fixtureTag())
			case "canceled caller":
				cancel()
				want = codes.Canceled
			case "invalid ID":
				jobID, want = "not-a-uuid", codes.InvalidArgument
			}
			before := readLeaseOwnership(ctx, t, pg, fixture.JobID)
			expires, err := repo.RenewJobLease(callCtx, jobID, token, time.Minute)
			if !expires.IsZero() || status.Code(err) != want {
				t.Fatalf("renewal = (%v, %v), want zero expiry and %s", expires, err, want)
			}
			if after := readLeaseOwnership(ctx, t, pg, fixture.JobID); after != before {
				t.Fatalf("rejected renewal changed state: before=%+v after=%+v", before, after)
			}
		})
	}
}

func TestIntegrationAttachContainerRejectsUnownedCommands(t *testing.T) {
	ctx := t.Context()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)
	fixture := seedClaimedJob(ctx, t, pg, repo)
	before := readLeaseOwnership(ctx, t, pg, fixture.JobID)
	if !before.RuntimeNodeID.Valid || before.RuntimeNodeID.String == "" {
		t.Fatal("claimed container job has no runtime node")
	}
	const containerID = "owner-contract-container"
	command := "attach-" + fixtureTag()
	for _, rejection := range []string{"stale token", "different runtime", "canceled caller"} {
		t.Run(rejection, func(t *testing.T) {
			callCtx, cancel := context.WithCancel(t.Context())
			defer cancel()
			token, node, want := fixture.LeaseToken, before.RuntimeNodeID.String, codes.FailedPrecondition
			switch rejection {
			case "stale token":
				token += "-stale"
			case "different runtime":
				node += "-other"
			case "canceled caller":
				cancel()
				want = codes.Canceled
			}
			err := repo.AttachJobContainer(callCtx, fixture.JobID, token, containerID, node, command)
			if status.Code(err) != want {
				t.Fatalf("attach = %v, want %s", err, want)
			}
			if after := readLeaseOwnership(ctx, t, pg, fixture.JobID); after != before {
				t.Fatalf("rejected attach changed state: before=%+v after=%+v", before, after)
			}
			if _, exists := readJobCommand(ctx, t, pg, fixture.JobID, commandidempotency.OperationJobAttachContainer, command); exists {
				t.Fatal("rejected attach left a command reservation")
			}
		})
	}
	if err := repo.AttachJobContainer(ctx, fixture.JobID, fixture.LeaseToken, containerID, before.RuntimeNodeID.String, command); err != nil {
		t.Fatalf("rejected attempts poisoned the command identity: %v", err)
	}
	if after := readLeaseOwnership(ctx, t, pg, fixture.JobID); !after.ContainerID.Valid || after.ContainerID.String != containerID {
		t.Fatalf("valid retry did not attach its container: %+v", after)
	}
}

func TestIntegrationAttachContainerReplaysOriginalCommand(t *testing.T) {
	ctx := t.Context()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)
	fixture := seedClaimedJob(ctx, t, pg, repo)
	before := readLeaseOwnership(ctx, t, pg, fixture.JobID)
	if !before.RuntimeNodeID.Valid || before.RuntimeNodeID.String == "" {
		t.Fatal("claimed container job has no runtime node")
	}
	const containerID = "replay-contract-container"
	command := "attach-" + fixtureTag()
	if err := repo.AttachJobContainer(ctx, fixture.JobID, fixture.LeaseToken, containerID, before.RuntimeNodeID.String, command); err != nil {
		t.Fatal(err)
	}
	want := before
	want.ContainerID = sql.NullString{String: containerID, Valid: true}
	if after := readLeaseOwnership(ctx, t, pg, fixture.JobID); after != want {
		t.Fatalf("attach changed fields other than container ID: got=%+v want=%+v", after, want)
	}
	stored, exists := readJobCommand(ctx, t, pg, fixture.JobID, commandidempotency.OperationJobAttachContainer, command)
	if !exists || stored.Status != "COMPLETED" {
		t.Fatalf("accepted attach has no completed command: %+v", stored)
	}
	if err := repo.AttachJobContainer(ctx, fixture.JobID, fixture.LeaseToken, containerID, before.RuntimeNodeID.String, command); err != nil {
		t.Fatalf("identical attach replay: %v", err)
	}
	if after := readLeaseOwnership(ctx, t, pg, fixture.JobID); after != want {
		t.Fatal("identical attach replay changed the job")
	}
	conflict := repo.AttachJobContainer(ctx, fixture.JobID, fixture.LeaseToken, containerID+"-replacement", before.RuntimeNodeID.String, command)
	if status.Code(conflict) != codes.AlreadyExists {
		t.Fatalf("changed attach command = %v, want AlreadyExists", conflict)
	}
	if after := readLeaseOwnership(ctx, t, pg, fixture.JobID); after != want {
		t.Fatal("conflicting attach replay replaced the container")
	}
	if err := repo.AttachJobContainer(ctx, fixture.JobID, fixture.LeaseToken, containerID, before.RuntimeNodeID.String, command); err != nil {
		t.Fatalf("conflicting replay corrupted the original command identity: %v", err)
	}
}
