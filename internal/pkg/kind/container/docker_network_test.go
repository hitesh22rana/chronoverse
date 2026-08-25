//nolint:testpackage // Tests fail-closed validation around the unexported network ensure helper.
package container

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestValidateWorkloadNetworkNameRejectsReservedModes(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"", " bridge", "bridge", "HOST", "none", "default", "nat", "container", "container:peer", platformNetwork} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if err := validateWorkloadNetworkName(name); status.Code(err) != codes.FailedPrecondition {
				t.Fatalf("validateWorkloadNetworkName(%q) code = %s, want %s: %v", name, status.Code(err), codes.FailedPrecondition, err)
			}
		})
	}
}

func TestValidateWorkloadNetworkNameRejectsProxyUnsafeNames(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"foo:bar", "a@b", "net name", "-leading", "network/name"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if err := validateWorkloadNetworkName(name); status.Code(err) != codes.FailedPrecondition {
				t.Fatalf("validateWorkloadNetworkName(%q) code = %s, want %s: %v", name, status.Code(err), codes.FailedPrecondition, err)
			}
		})
	}
}

func TestValidateWorkloadNetworkNameAcceptsProxySafeNames(t *testing.T) {
	t.Parallel()

	for _, name := range []string{DefaultWorkloadNetwork, "Workloads_2.0", "1-network"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if err := validateWorkloadNetworkName(name); err != nil {
				t.Fatalf("validateWorkloadNetworkName(%q) error = %v", name, err)
			}
		})
	}
}

func TestValidateWorkloadNetworkRequiresIsolatedBridge(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		inspected network.Inspect
		wantCode  codes.Code
	}{
		{
			name: "isolated bridge",
			inspected: network.Inspect{
				Name:    DefaultWorkloadNetwork,
				Driver:  "bridge",
				Options: map[string]string{workloadNetworkICCOption: "false"},
			},
			wantCode: codes.OK,
		},
		{
			name: "unexpected name",
			inspected: network.Inspect{
				Name:    "other-workloads",
				Driver:  "bridge",
				Options: map[string]string{workloadNetworkICCOption: "false"},
			},
			wantCode: codes.FailedPrecondition,
		},
		{
			name: "platform network",
			inspected: network.Inspect{
				Name:    platformNetwork,
				Driver:  "bridge",
				Options: map[string]string{workloadNetworkICCOption: "false"},
			},
			wantCode: codes.FailedPrecondition,
		},
		{
			name: "overlay driver",
			inspected: network.Inspect{
				Name:    DefaultWorkloadNetwork,
				Driver:  "overlay",
				Options: map[string]string{workloadNetworkICCOption: "false"},
			},
			wantCode: codes.FailedPrecondition,
		},
		{
			name: "icc enabled",
			inspected: network.Inspect{
				Name:    DefaultWorkloadNetwork,
				Driver:  "bridge",
				Options: map[string]string{workloadNetworkICCOption: "true"},
			},
			wantCode: codes.FailedPrecondition,
		},
		{
			name: "icc option absent",
			inspected: network.Inspect{
				Name:   DefaultWorkloadNetwork,
				Driver: "bridge",
			},
			wantCode: codes.FailedPrecondition,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateWorkloadNetwork(DefaultWorkloadNetwork, &tt.inspected)
			if status.Code(err) != tt.wantCode {
				t.Fatalf("validateWorkloadNetwork() code = %s, want %s: %v", status.Code(err), tt.wantCode, err)
			}
		})
	}
}

func TestEnsureWorkloadNetworkValidatesExistingNetwork(t *testing.T) {
	t.Parallel()

	var createCalls atomic.Int32
	workflow := newNetworkTestWorkflow(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			createCalls.Add(1)
		}
		w.Header().Set("Content-Type", "application/json")
		writeDockerTestResponse(t, w, `{"Name":"chronoverse-workloads","Driver":"bridge","Options":{"com.docker.network.bridge.enable_icc":"true"}}`)
	}))

	err := workflow.ensureWorkloadNetwork(context.Background())
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("ensureWorkloadNetwork() code = %s, want %s: %v", status.Code(err), codes.FailedPrecondition, err)
	}
	if got := createCalls.Load(); got != 0 {
		t.Fatalf("network create calls = %d, want 0", got)
	}
}

func TestEnsureWorkloadNetworkValidatesCreatedNetwork(t *testing.T) {
	t.Parallel()

	var inspectCalls atomic.Int32
	workflow := newNetworkTestWorkflow(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/networks/"):
			if inspectCalls.Add(1) == 1 {
				w.WriteHeader(http.StatusNotFound)
				writeDockerTestResponse(t, w, `{"message":"network not found"}`)
				return
			}
			writeDockerTestResponse(t, w, `{"Name":"chronoverse-workloads","Driver":"bridge","Options":{"com.docker.network.bridge.enable_icc":"false"}}`)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/networks/create"):
			w.WriteHeader(http.StatusCreated)
			writeDockerTestResponse(t, w, `{"Id":"network-id"}`)
		default:
			t.Fatalf("unexpected Docker API request: %s %s", r.Method, r.URL.String())
		}
	}))

	if err := workflow.ensureWorkloadNetwork(context.Background()); err != nil {
		t.Fatalf("ensureWorkloadNetwork() error = %v", err)
	}
	if got := inspectCalls.Load(); got != 2 {
		t.Fatalf("network inspect calls = %d, want 2", got)
	}
}

func TestEnsureWorkloadNetworkValidatesRaceWinner(t *testing.T) {
	t.Parallel()

	var inspectCalls atomic.Int32
	workflow := newNetworkTestWorkflow(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/networks/"):
			if inspectCalls.Add(1) == 1 {
				w.WriteHeader(http.StatusNotFound)
				writeDockerTestResponse(t, w, `{"message":"network not found"}`)
				return
			}
			writeDockerTestResponse(t, w, `{"Name":"chronoverse-workloads","Driver":"bridge","Options":{"com.docker.network.bridge.enable_icc":"true"}}`)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/networks/create"):
			w.WriteHeader(http.StatusConflict)
			writeDockerTestResponse(t, w, `{"message":"network already exists"}`)
		default:
			t.Fatalf("unexpected Docker API request: %s %s", r.Method, r.URL.String())
		}
	}))

	err := workflow.ensureWorkloadNetwork(context.Background())
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("ensureWorkloadNetwork() code = %s, want %s: %v", status.Code(err), codes.FailedPrecondition, err)
	}
}

func newNetworkTestWorkflow(t *testing.T, handler http.Handler) *DockerWorkflow {
	t.Helper()

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	cli, err := client.NewClientWithOpts(client.WithHost(server.URL), client.WithVersion("1.51"))
	if err != nil {
		t.Fatalf("create Docker client: %v", err)
	}
	t.Cleanup(func() {
		_ = cli.Close()
	})

	return &DockerWorkflow{Client: cli, workloadNetwork: DefaultWorkloadNetwork}
}

type networkLifecycleTestDaemon struct {
	t                      *testing.T
	networkExists          atomic.Bool
	networkCreateCalls     atomic.Int32
	containerCreateCalls   atomic.Int32
	unsafeNetwork          bool
	pruneDuringFirstCreate bool
	failNetworkCreate      bool
}

func newNetworkLifecycleTestDaemon(t *testing.T, networkExists bool) *networkLifecycleTestDaemon {
	t.Helper()

	daemon := &networkLifecycleTestDaemon{t: t}
	daemon.networkExists.Store(networkExists)

	return daemon
}

func (d *networkLifecycleTestDaemon) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch {
	case (r.Method == http.MethodGet || r.Method == http.MethodHead) && strings.HasSuffix(r.URL.Path, "/_ping"):
		w.Header().Set("API-Version", "1.51")
		w.WriteHeader(http.StatusOK)
	case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/networks/"):
		d.handleNetworkInspect(w)
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/networks/create"):
		d.handleNetworkCreate(w)
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/containers/create"):
		d.handleContainerCreate(w)
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/containers/container-1/start"):
		w.WriteHeader(http.StatusInternalServerError)
		writeDockerTestResponse(d.t, w, `{"message":"stop before runtime goroutines start"}`)
	default:
		d.t.Errorf("unexpected Docker API request: %s %s", r.Method, r.URL.String())
		w.WriteHeader(http.StatusInternalServerError)
	}
}

func (d *networkLifecycleTestDaemon) handleNetworkInspect(w http.ResponseWriter) {
	if !d.networkExists.Load() {
		w.WriteHeader(http.StatusNotFound)
		writeDockerTestResponse(d.t, w, `{"message":"network chronoverse-workloads not found"}`)
		return
	}

	icc := workloadNetworkICCOff
	if d.unsafeNetwork {
		icc = "true"
	}
	writeDockerTestResponse(d.t, w, `{"Name":"chronoverse-workloads","Driver":"bridge","Options":{"`+workloadNetworkICCOption+`":"`+icc+`"}}`)
}

func (d *networkLifecycleTestDaemon) handleNetworkCreate(w http.ResponseWriter) {
	d.networkCreateCalls.Add(1)
	if d.failNetworkCreate {
		w.WriteHeader(http.StatusInternalServerError)
		writeDockerTestResponse(d.t, w, `{"message":"network creation failed"}`)
		return
	}

	d.networkExists.Store(true)
	w.WriteHeader(http.StatusCreated)
	writeDockerTestResponse(d.t, w, `{"Id":"net-1"}`)
}

func (d *networkLifecycleTestDaemon) handleContainerCreate(w http.ResponseWriter) {
	createCall := d.containerCreateCalls.Add(1)
	if d.pruneDuringFirstCreate && createCall == 1 {
		// Model a prune after pre-create validation but before Docker resolves
		// HostConfig.NetworkMode.
		d.networkExists.Store(false)
		w.WriteHeader(http.StatusNotFound)
		writeDockerTestResponse(d.t, w, `{"message":"network chronoverse-workloads not found"}`)
		return
	}
	if !d.networkExists.Load() {
		d.t.Error("ContainerCreate called before the workload network was recreated")
		w.WriteHeader(http.StatusNotFound)
		writeDockerTestResponse(d.t, w, `{"message":"network chronoverse-workloads not found"}`)
		return
	}

	w.WriteHeader(http.StatusCreated)
	writeDockerTestResponse(d.t, w, `{"Id":"container-1","Warnings":null}`)
}

func TestExecuteRecreatesPrunedWorkloadNetwork(t *testing.T) {
	daemon := newNetworkLifecycleTestDaemon(t, false)
	workflow := newNetworkTestWorkflow(t, daemon)

	id, logsCh, errsCh, err := workflow.Execute(context.Background(), time.Second, "alpine:3", []string{"echo", "hi"}, nil)
	if status.Code(err) != codes.Aborted {
		t.Fatalf("Execute() code = %s, want %s: %v", status.Code(err), codes.Aborted, err)
	}
	if id != "container-1" {
		t.Errorf("container id = %q, want container-1", id)
	}
	if logsCh != nil || errsCh != nil {
		t.Fatal("Execute() returned log channels after ContainerStart failed")
	}
	if got := daemon.networkCreateCalls.Load(); got != 1 {
		t.Errorf("network create calls = %d, want 1", got)
	}
	if got := daemon.containerCreateCalls.Load(); got != 1 {
		t.Errorf("container create calls = %d, want 1", got)
	}
}

func TestExecuteRejectsReplacedWorkloadNetwork(t *testing.T) {
	daemon := newNetworkLifecycleTestDaemon(t, true)
	daemon.unsafeNetwork = true
	workflow := newNetworkTestWorkflow(t, daemon)

	id, logsCh, errsCh, err := workflow.Execute(context.Background(), time.Second, "alpine:3", []string{"echo", "hi"}, nil)
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("Execute() code = %s, want %s: %v", status.Code(err), codes.FailedPrecondition, err)
	}
	if id != "" || logsCh != nil || errsCh != nil {
		t.Fatalf("Execute() = (%q, %v, %v), want empty results", id, logsCh, errsCh)
	}
	if got := daemon.containerCreateCalls.Load(); got != 0 {
		t.Errorf("container create calls = %d, want 0", got)
	}
}

func TestExecuteRetriesWhenWorkloadNetworkIsPrunedDuringCreate(t *testing.T) {
	daemon := newNetworkLifecycleTestDaemon(t, true)
	daemon.pruneDuringFirstCreate = true
	workflow := newNetworkTestWorkflow(t, daemon)

	id, _, _, err := workflow.Execute(context.Background(), time.Second, "alpine:3", []string{"echo", "hi"}, nil)
	if status.Code(err) != codes.Aborted {
		t.Fatalf("Execute() code = %s, want %s: %v", status.Code(err), codes.Aborted, err)
	}
	if id != "container-1" {
		t.Errorf("container id = %q, want container-1", id)
	}
	if got := daemon.networkCreateCalls.Load(); got != 1 {
		t.Errorf("network create calls = %d, want 1", got)
	}
	if got := daemon.containerCreateCalls.Load(); got != 2 {
		t.Errorf("container create calls = %d, want 2", got)
	}
}

func TestExecuteReturnsWorkloadNetworkRecreationFailure(t *testing.T) {
	daemon := newNetworkLifecycleTestDaemon(t, true)
	daemon.pruneDuringFirstCreate = true
	daemon.failNetworkCreate = true
	workflow := newNetworkTestWorkflow(t, daemon)

	id, logsCh, errsCh, err := workflow.Execute(context.Background(), time.Second, "alpine:3", []string{"echo", "hi"}, nil)
	if status.Code(err) != codes.Internal {
		t.Fatalf("Execute() code = %s, want %s: %v", status.Code(err), codes.Internal, err)
	}
	if id != "" || logsCh != nil || errsCh != nil {
		t.Fatalf("Execute() = (%q, %v, %v), want empty results", id, logsCh, errsCh)
	}
	if got := daemon.containerCreateCalls.Load(); got != 1 {
		t.Errorf("container create calls = %d, want 1", got)
	}
}
