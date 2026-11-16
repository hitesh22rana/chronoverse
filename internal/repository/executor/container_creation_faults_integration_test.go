//nolint:testpackage // Proves durable authority across real PostgreSQL and Docker boundaries.
package executor

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	containerpkg "github.com/hitesh22rana/chronoverse/internal/pkg/kind/container"
	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
	"github.com/hitesh22rana/chronoverse/internal/pkg/testkit"
	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"
)

type creationFaultRuntime struct {
	*containerpkg.DockerWorkflow
	mode            string
	creates, starts int
	request         containerpkg.CreateRequest
	id              string
}

//nolint:gocritic // Preserve the immutable production adapter request boundary.
func (r *creationFaultRuntime) Create(ctx context.Context, req containerpkg.CreateRequest) (string, error) {
	r.creates++
	r.request = req
	if r.mode == "late Create" {
		return "", status.Error(codes.Unavailable, "Create response lost before allocation became visible")
	}
	id, err := r.DockerWorkflow.Create(ctx, req)
	r.id = id
	if err == nil && r.mode == "process death" {
		if _, writeErr := os.Stdout.WriteString("CV015_CREATED\n"); writeErr != nil {
			return "", writeErr
		}
		<-ctx.Done()
		return "", ctx.Err()
	}
	if err == nil && r.mode == "Create reply lost" {
		return "", status.Error(codes.Unavailable, "Create response lost")
	}
	return id, err
}

func (r *creationFaultRuntime) Start(ctx context.Context, timeout time.Duration, id string) (logs <-chan *jobsmodel.JobLog, errs <-chan error, err error) {
	r.starts++
	return r.DockerWorkflow.Start(ctx, timeout, id)
}

type creationFaultJobs struct {
	*durableJobsClient
	mode string
}

func lostIntentReply(mode, op string, result *jobspb.ContainerIntentResponse, err error) (*jobspb.ContainerIntentResponse, error) {
	if err == nil && mode == op {
		return nil, status.Error(codes.Unavailable, op+" committed but response lost")
	}
	return result, err
}

func (j *creationFaultJobs) EnsureContainerIntent(ctx context.Context, req *jobspb.ContainerIntentCommand, _ ...grpc.CallOption) (*jobspb.ContainerIntentResponse, error) {
	res, err := j.durableJobsClient.EnsureContainerIntent(ctx, req)
	return lostIntentReply(j.mode, "Ensure reply lost", res, err)
}

func (j *creationFaultJobs) ObserveContainer(ctx context.Context, req *jobspb.ContainerIntentCommand, _ ...grpc.CallOption) (*jobspb.ContainerIntentResponse, error) {
	res, err := j.durableJobsClient.ObserveContainer(ctx, req)
	return lostIntentReply(j.mode, "Observe reply lost", res, err)
}

func (j *creationFaultJobs) ReserveContainerStart(ctx context.Context, req *jobspb.ContainerIntentCommand, _ ...grpc.CallOption) (*jobspb.ContainerIntentResponse, error) {
	res, err := j.durableJobsClient.ReserveContainerStart(ctx, req)
	return lostIntentReply(j.mode, "Start grant reply lost", res, err)
}

func (j *creationFaultJobs) AttachJobContainer(ctx context.Context, req *jobspb.AttachJobContainerRequest, _ ...grpc.CallOption) (*jobspb.AttachJobContainerResponse, error) {
	err := j.repo.AttachJobContainer(ctx, req.GetId(), req.GetLeaseToken(), req.GetContainerId(), req.GetRuntimeNodeId(), req.GetCommandId())
	if err == nil && j.mode == "Attach reply lost" {
		err = status.Error(codes.Unavailable, "attachment committed but response lost")
	}
	return &jobspb.AttachJobContainerResponse{}, err
}

func (j *creationFaultJobs) ReportContainerInspection(ctx context.Context, req *jobspb.ContainerIntentCommand, _ ...grpc.CallOption) (*jobspb.ContainerIntentResponse, error) {
	return j.updateContainerIntent(ctx, "report", req)
}

func TestIntegrationContainerCreationLostRepliesRemainRecoverable(t *testing.T) {
	if testing.Short() {
		t.Skip("requires PostgreSQL and Docker")
	}
	daemon, err := containerpkg.NewDockerWorkflow(containerpkg.WithDockerHost("unix:///var/run/docker.sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer daemon.Client.Close()
	if err = daemon.Build(t.Context(), "alpine:3.24.2"); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"success", "Ensure reply lost", "Create reply lost", "Observe reply lost", "Attach reply lost", "Start grant reply lost", "late Create", "process death"} {
		t.Run(mode, func(t *testing.T) { testContainerCreationFault(t, daemon, mode) })
	}
}

//nolint:gocyclo // Each assertion follows one real authority/physical-cleanup lifecycle.
func testContainerCreationFault(t *testing.T, daemon *containerpkg.DockerWorkflow, mode string) {
	t.Helper()
	ctx := t.Context()
	pg := testkit.Postgres(t)
	jobs, workflows := newDurableRepositories(pg)
	fixture := seedDurableClaim(ctx, t, pg, jobs, "COMPLETED", 1)
	node := "creation-" + uuid.NewString()
	if _, err := pg.Exec(ctx, `INSERT INTO runtime_nodes(id,node_name,docker_endpoint,status,last_heartbeat_at,max_concurrency)
 VALUES($1,$1,$2,'READY',clock_timestamp() AT TIME ZONE 'utc',1)`, node, daemon.DockerHost()); err != nil {
		t.Fatal(err)
	}
	if _, err := pg.Exec(ctx, `UPDATE workflows SET kind='CONTAINER',payload='{"image":"alpine:3.24.2","cmd":["true"],"timeout":"5s"}' WHERE id=$1`, fixture.WorkflowID); err != nil {
		t.Fatal(err)
	}
	if _, err := pg.Exec(ctx, "UPDATE jobs SET runtime_node_id=$2,runtime_endpoint=$3 WHERE id=$1", fixture.JobID, node, daemon.DockerHost()); err != nil {
		t.Fatal(err)
	}
	runtime := &creationFaultRuntime{DockerWorkflow: daemon, mode: mode}
	t.Cleanup(func() {
		if runtime.id != "" {
			cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := daemon.Remove(cleanup, runtime.id); err != nil {
				t.Error(err)
			}
		}
	})
	repo := newDurableExecutor(fixture, jobs, workflows, nil, &rejectingContainerSvc{})
	client := &creationFaultJobs{durableJobsClient: &durableJobsClient{repo: jobs}, mode: mode}
	repo.svc.Jobs = client
	repo.svc.CsvcForEndpoint = func(string, string) (ContainerSvc, error) { return runtime, nil }
	workflow := containerWorkflow()
	workflow.Id, workflow.UserId, workflow.Generation = fixture.WorkflowID, fixture.UserID, 1
	workflow.ResolvedImageDigest = "alpine:3.24.2"
	var runErr error
	if mode == "process death" {
		killContainerCreationChild(t, fixture, node)
	} else {
		_, runErr = repo.executeContainerIntent(ctx, runtime, fixture.JobID, fixture.LeaseToken, node, 1, workflow, containerpkg.CreateRequest{
			Image: "alpine:3.24.2", Cmd: []string{"true"}, Timeout: 5 * time.Second,
		})
	}

	wantSuccess := mode == "success" || mode == "Create reply lost"
	if mode != "process death" && (runErr == nil) != wantSuccess {
		t.Fatalf("execution result: %v", runErr)
	}
	intentResult, err := jobs.UpdateContainerIntent(ctx, "get", &jobsmodel.ContainerIntentCommand{JobID: fixture.JobID, Attempt: 1})
	if err != nil {
		t.Fatal(err)
	}
	intent := intentResult.Intent
	switch {
	case mode == "process death":
		if intent.Closed || intent.ContainerID != "" || !intent.CreateReserved || intent.StartReserved {
			t.Fatalf("hard death did not preserve uncertain creation: %+v", intent)
		}
		state, inspectErr := daemon.Inspect(ctx, intent.Name)
		if inspectErr != nil {
			t.Fatal(inspectErr)
		}
		runtime.id = state.ID
		if _, err = pg.Exec(ctx, "UPDATE jobs SET lease_expires_at=(clock_timestamp() AT TIME ZONE 'utc')-interval '1 second' WHERE id=$1", fixture.JobID); err != nil {
			t.Fatal(err)
		}
		recovered, recoveryErr := jobs.RecoverExpiredJobLeases(ctx, 1, "recovery", uuid.NewString(), uuid.NewString(), time.Minute)
		if recoveryErr != nil || len(recovered) != 1 || recovered[0].ID != fixture.JobID {
			t.Fatalf("claim killed executor: %+v %v", recovered, recoveryErr)
		}
		err = repo.recoverExpiredLease(ctx, recovered[0].ToProto())
	case wantSuccess:
		err = jobs.CompleteJob(ctx, fixture.JobID, fixture.LeaseToken, uuid.NewString())
	default:
		err = jobs.ReleaseJobForRetry(ctx, fixture.JobID, fixture.LeaseToken, time.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano), "Unavailable", "fault recovery", uuid.NewString())
	}
	if err != nil {
		t.Fatal(err)
	}
	if mode == "Ensure reply lost" {
		if runtime.creates != 0 || runtime.starts != 0 {
			t.Fatal("failed Ensure reply granted a mutation")
		}
		return
	}
	if mode == "late Create" {
		runtime.id, err = daemon.Create(ctx, runtime.request)
		if err != nil {
			t.Fatal(err)
		}
	}
	if mode == "late Create" || mode == "process death" {
		if _, err = pg.Exec(ctx, "UPDATE container_creation_intents SET next_inspection_at='1970-01-01' WHERE id=$1", intent.ID); err != nil {
			t.Fatal(err)
		}
		claimed, claimErr := jobs.ListContainerIntents(ctx, &jobsmodel.ContainerIntentCommand{BatchSize: 1}, true)
		if claimErr != nil || len(claimed) != 1 || claimed[0].ID != intent.ID {
			t.Fatalf("claim late tombstone: %+v %v", claimed, claimErr)
		}
		client.mode = ""
		repo.inspectContainerIntent(ctx, containerIntentProto(claimed[0]))
	}
	wantStarts := 0
	if wantSuccess {
		wantStarts = 1
	}
	if mode != "process death" && (runtime.creates != 1 || runtime.starts != wantStarts) {
		t.Fatalf("mutation replay: creates=%d starts=%d", runtime.creates, runtime.starts)
	}
	var obligations int
	if err = pg.QueryRow(ctx, "SELECT count(*) FROM outbox_events WHERE payload->'ContainerCleanup'->>'ContainerID'=$1", runtime.id).Scan(&obligations); err != nil || obligations != 1 {
		t.Fatalf("physical obligation missing: count=%d error=%v", obligations, err)
	}
	if !wantSuccess {
		state, inspectErr := daemon.Inspect(ctx, runtime.id)
		if inspectErr != nil || state.Status != "created" {
			t.Fatalf("uncertain path executed workload: %+v %v", state, inspectErr)
		}
	}
	// Use the production cleanup path, including the original-instance receipt.
	if err = repo.cleanupContainer(ctx, runtime, fixture.JobID, runtime.id); err != nil {
		t.Fatal(err)
	}
	completed, err := jobs.GetContainerCleanupReceipt(ctx, fixture.JobID, runtime.id, intent.RuntimeInstance, intent.ID)
	if err != nil || !completed {
		t.Fatalf("physical removal lost proof: completed=%v error=%v", completed, err)
	}
}

// TestContainerCreationCrashChild runs only in the controlled subprocess below.
func TestContainerCreationCrashChild(t *testing.T) {
	dsn := os.Getenv("CV015_CRASH_DSN")
	if dsn == "" {
		t.Skip("controlled child process only")
	}
	parsed, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg := parsed.ConnConfig
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	pg, err := postgres.New(ctx, &postgres.Config{
		Host: cfg.Host, Port: int(cfg.Port), User: cfg.User, Password: cfg.Password, Database: cfg.Database,
		TLSConfig: &postgres.TLSConfig{},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer pg.Close()
	jobs, workflows := newDurableRepositories(pg)
	fixture := &durableFixture{JobID: os.Getenv("CV015_CRASH_JOB"), LeaseToken: os.Getenv("CV015_CRASH_LEASE"), WorkflowID: os.Getenv("CV015_CRASH_WORKFLOW")}
	repo := newDurableExecutor(fixture, jobs, workflows, nil, &rejectingContainerSvc{})
	daemon, err := containerpkg.NewDockerWorkflow(containerpkg.WithDockerHost("unix:///var/run/docker.sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer daemon.Client.Close()
	runtime := &creationFaultRuntime{DockerWorkflow: daemon, mode: "process death"}
	workflow := containerWorkflow()
	workflow.Id, workflow.Generation = fixture.WorkflowID, 1
	_, err = repo.executeContainerIntent(ctx, runtime, fixture.JobID, fixture.LeaseToken, os.Getenv("CV015_CRASH_NODE"), 1, workflow, containerpkg.CreateRequest{
		Image: "alpine:3.24.2", Cmd: []string{"true"}, Timeout: 5 * time.Second,
	})
	t.Fatalf("child survived the crash boundary: %v", err)
}

func killContainerCreationChild(t *testing.T, fixture *durableFixture, node string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, exe, "-test.run=^TestContainerCreationCrashChild$", "-test.count=1")
	child.Env = append(os.Environ(),
		"CV015_CRASH_DSN="+testkit.PostgresDSN(t), "CV015_CRASH_JOB="+fixture.JobID,
		"CV015_CRASH_LEASE="+fixture.LeaseToken, "CV015_CRASH_WORKFLOW="+fixture.WorkflowID, "CV015_CRASH_NODE="+node)
	var stderr bytes.Buffer
	child.Stderr = &stderr
	stdout, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = child.Start(); err != nil {
		t.Fatal(err)
	}
	cleanupChild := func() {
		if child.ProcessState == nil {
			if killErr := child.Process.Kill(); killErr != nil && !errors.Is(killErr, os.ErrProcessDone) {
				t.Error(killErr)
			}
			_ = child.Wait() //nolint:errcheck // The caller reports the failure; wait joins stderr copying.
		}
	}
	defer cleanupChild()
	ready := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			if scanner.Text() == "CV015_CREATED" {
				ready <- nil
				return
			}
		}
		if scanErr := scanner.Err(); scanErr != nil {
			ready <- scanErr
			return
		}
		ready <- errors.New("child exited before Create boundary")
	}()
	select {
	case scanErr := <-ready:
		if scanErr != nil {
			// Wait joins stderr copying before reading its buffer.
			cleanupChild()
			t.Fatalf("child failed before Create boundary: %s (%v)", stderr.String(), scanErr)
		}
	case <-ctx.Done():
		t.Fatal("child did not reach Create boundary")
	}
	if err = child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err = child.Wait(); err == nil {
		t.Fatal("child was not terminated")
	}
}
