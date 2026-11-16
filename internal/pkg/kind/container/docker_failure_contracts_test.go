//nolint:testpackage // Exercises daemon failures through the production HTTP client.
package container

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/network"
	dockerclient "github.com/moby/moby/client"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/pkg/terminalreason"
)

//nolint:gocyclo // Crosses two operations with independent daemon outcomes and request contracts.
func TestDockerRemovalAndStopDaemonFailures(t *testing.T) {
	for _, op := range []string{"remove", "stop"} {
		for _, tc := range []struct {
			name       string
			httpStatus int
			want       codes.Code
		}{{"gone", 404, codes.OK}, {"daemon rejection", 500, codes.Aborted}, {"success", 204, codes.OK}} {
			t.Run(op+"/"+tc.name, func(t *testing.T) {
				var calls atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if strings.HasSuffix(r.URL.Path, "/_ping") {
						w.WriteHeader(http.StatusOK)
						return
					}
					calls.Add(1)
					if op == "remove" && (r.Method != http.MethodDelete || r.URL.Query().Get("force") != "1" || r.URL.Query().Get("v") != "1") {
						t.Errorf("remove request = %s %s", r.Method, r.URL)
					}
					if op == "stop" && (r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/stop") || r.URL.Query().Get("t") == "") {
						t.Errorf("stop request = %s %s", r.Method, r.URL)
					}
					w.WriteHeader(tc.httpStatus)
					if tc.httpStatus != 204 {
						writeDockerTestResponse(t, w, `{"message":"daemon rejected operation"}`)
					}
				}))
				defer server.Close()
				cli, err := dockerclient.New(dockerclient.WithHost(server.URL), dockerclient.WithAPIVersion("1.51"))
				if err != nil {
					t.Fatal(err)
				}
				defer cli.Close()
				workflow := &DockerWorkflow{Client: cli}
				if op == "remove" {
					err = workflow.Remove(t.Context(), strings.Repeat("a", 64))
				} else {
					err = workflow.Terminate(t.Context(), strings.Repeat("a", 64))
				}
				if status.Code(err) != tc.want || calls.Load() != 1 {
					t.Fatalf("%s = %v, calls=%d", op, err, calls.Load())
				}
			})
		}
	}
}

func TestDockerInspectAdditionalErrorClasses(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want codes.Code
	}{{context.DeadlineExceeded, codes.DeadlineExceeded}, {context.Canceled, codes.Canceled}, {cerrdefs.ErrInternal, codes.Internal}} {
		if err := dockerImageInspectError(tc.err); status.Code(err) != tc.want {
			t.Fatalf("inspect error = %v, want %v", err, tc.want)
		}
	}
}

func TestResolveImageDigestFailureStages(t *testing.T) {
	const digest = "docker.io/library/alpine@sha256:4444444444444444444444444444444444444444444444444444444444444444"
	for _, tc := range []struct {
		name                       string
		buildStatus, inspectStatus int
		digests                    string
		want                       codes.Code
		resolved                   string
	}{
		{"build rejection", 500, 200, "[]", codes.Internal, ""},
		{"second inspect rejection", 200, 500, "[]", codes.Internal, ""},
		{"missing digest", 200, 200, "[]", codes.FailedPrecondition, ""},
		{"matching digest", 200, 200, `["` + digest + `"]`, codes.OK, digest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var inspections atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/_ping") {
					return
				}
				if !strings.Contains(r.URL.Path, "/images/") {
					t.Errorf("unexpected request %s", r.URL)
					http.Error(w, "unexpected", http.StatusInternalServerError)
					return
				}
				code := tc.buildStatus
				if inspections.Add(1) > 1 {
					code = tc.inspectStatus
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(code)
				if code == 200 {
					writeDockerTestResponse(t, w, `{"Id":"sha256:config","RepoDigests":`+tc.digests+`}`)
				} else {
					writeDockerTestResponse(t, w, `{"message":"inspect rejected"}`)
				}
			}))
			defer server.Close()
			cli, err := dockerclient.New(dockerclient.WithHost(server.URL), dockerclient.WithAPIVersion("1.51"))
			if err != nil {
				t.Fatal(err)
			}
			defer cli.Close()
			workflow := &DockerWorkflow{Client: cli}
			image, resolved, err := workflow.ResolveImageDigest(t.Context(), "alpine:latest")
			if image != "alpine:latest" || resolved != tc.resolved || status.Code(err) != tc.want {
				t.Fatalf("resolution = %q %q %v", image, resolved, err)
			}
		})
	}
}

func TestImageBuildPullFailures(t *testing.T) {
	for _, tc := range []struct {
		name       string
		pullStatus int
		truncate   bool
		want       codes.Code
	}{
		{"pull rejected", 404, false, codes.NotFound},
		{"registry authorization denied", 401, false, codes.Unauthenticated},
		{"invalid image request", 400, false, codes.InvalidArgument},
		{"registry permission denied", 403, false, codes.PermissionDenied},
		{"daemon overloaded", 429, false, codes.ResourceExhausted},
		{"daemon failure", 500, false, codes.Internal},
		{"daemon unavailable", 503, false, codes.Unavailable},
		{"pull stream interrupted", 200, true, codes.Unavailable},
		{"pull completed", 200, false, codes.OK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.HasSuffix(r.URL.Path, "/_ping"):
				case strings.HasSuffix(r.URL.Path, "/json"):
					w.WriteHeader(http.StatusNotFound)
					writeDockerTestResponse(t, w, `{"message":"image absent"}`)
				case strings.HasSuffix(r.URL.Path, "/images/create"):
					if tc.truncate {
						w.Header().Set("Content-Length", "1000")
					}
					w.WriteHeader(tc.pullStatus)
					writeDockerTestResponse(t, w, `{"status":"pulling"}`)
				default:
					t.Errorf("unexpected request %s", r.URL)
					w.WriteHeader(http.StatusInternalServerError)
				}
			}))
			defer server.Close()
			cli, err := dockerclient.New(dockerclient.WithHost(server.URL), dockerclient.WithAPIVersion("1.51"))
			if err != nil {
				t.Fatal(err)
			}
			defer cli.Close()
			workflow := &DockerWorkflow{Client: cli}
			err = workflow.Build(t.Context(), "alpine:latest")
			if status.Code(err) != tc.want {
				t.Fatalf("Build = %v want %v", err, tc.want)
			}
			_, userFailure := terminalreason.FromError(err)
			wantUserFailure := tc.pullStatus == 404 || tc.pullStatus == 401 || tc.pullStatus == 400 || tc.pullStatus == 403
			if userFailure != wantUserFailure {
				t.Fatalf("pull error user classification = %v, want %v: %v", userFailure, wantUserFailure, err)
			}
		})
	}
}

func TestImageExistsDistinguishesAbsenceAndFailure(t *testing.T) {
	for _, tc := range []struct {
		code   int
		exists bool
		want   codes.Code
	}{{200, true, codes.OK}, {404, false, codes.OK}, {500, false, codes.Internal}} {
		t.Run(fmt.Sprint(tc.code), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/_ping") {
					return
				}
				w.WriteHeader(tc.code)
				if tc.code == 200 {
					writeDockerTestResponse(t, w, `{"Id":"sha256:config"}`)
				} else {
					writeDockerTestResponse(t, w, `{"message":"inspect failure"}`)
				}
			}))
			defer server.Close()
			cli, err := dockerclient.New(dockerclient.WithHost(server.URL), dockerclient.WithAPIVersion("1.51"))
			if err != nil {
				t.Fatal(err)
			}
			defer cli.Close()
			workflow := &DockerWorkflow{Client: cli}
			exists, err := workflow.ImageExists(t.Context(), "alpine")
			if exists != tc.exists || status.Code(err) != tc.want {
				t.Fatalf("ImageExists = %v %v", exists, err)
			}
		})
	}
}

func TestDockerUnavailableDaemonContracts(t *testing.T) {
	for _, op := range []string{"remove", "stop", "build", "exists", "logs", "healthy", "execute"} {
		t.Run(op, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
			host := server.URL
			server.Close()
			cli, err := dockerclient.New(dockerclient.WithHost(host), dockerclient.WithAPIVersion("1.51"))
			if err != nil {
				t.Fatal(err)
			}
			defer cli.Close()
			workflow := &DockerWorkflow{Client: cli}
			switch op {
			case "remove":
				err = workflow.Remove(t.Context(), strings.Repeat("a", 64))
			case "stop":
				err = workflow.Terminate(t.Context(), strings.Repeat("a", 64))
			case "build":
				err = workflow.Build(t.Context(), "alpine")
			case "exists":
				var exists bool
				exists, err = workflow.ImageExists(t.Context(), "alpine")
				if exists {
					t.Fatal("unavailable image reported present")
				}
			case "logs":
				logs, errs, logErr := workflow.Logs(t.Context(), "contract")
				err = logErr
				if logs != nil || errs != nil {
					t.Fatal("failed logs returned active channels")
				}
			case "healthy":
				err = workflow.Healthy(t.Context())
			case "execute":
				_, _, _, err = workflow.ExecuteTestFixture(t.Context(), time.Minute, "alpine", nil, nil)
			}
			want := codes.Internal
			if op == "remove" {
				want = codes.Unavailable
			}
			if status.Code(err) != want {
				t.Fatalf("%s = %v", op, err)
			}
		})
	}
}

func TestTerminateDaemonDisappearsAfterHealthCheck(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/_ping") {
			t.Errorf("unexpected request %s", r.URL)
		}
		if err := r.Body.Close(); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	transport := &http.Transport{DisableKeepAlives: true}
	calls := 0
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		calls++
		if calls > 1 {
			return nil, &net.OpError{Op: "dial", Net: network, Err: syscall.ECONNREFUSED}
		}
		return (&net.Dialer{}).DialContext(ctx, network, address)
	}
	defer transport.CloseIdleConnections()
	cli, err := dockerclient.New(dockerclient.WithHost(server.URL), dockerclient.WithAPIVersion("1.51"), dockerclient.WithHTTPClient(&http.Client{Transport: transport}))
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	workflow := &DockerWorkflow{Client: cli}
	if err := workflow.Terminate(t.Context(), strings.Repeat("a", 64)); status.Code(err) != codes.Unavailable {
		t.Fatalf("Terminate = %v", err)
	}
}

type pendingInspectTransport struct{ http.RoundTripper }

func (transport pendingInspectTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if strings.HasSuffix(req.URL.Path, "/json") {
		// Keep the daemon request pending so HTTP cancellation cannot win Build's select.
		req = req.Clone(context.WithoutCancel(req.Context()))
	}
	return transport.RoundTripper.RoundTrip(req)
}

func TestBuildCancellationWhileSharedPullIsPending(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		t.Run(fmt.Sprint(deadline), func(t *testing.T) {
			entered, release := make(chan struct{}), make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/_ping") {
					return
				}
				close(entered)
				<-release
				w.Header().Set("Content-Type", "application/json")
				writeDockerTestResponse(t, w, `{"Id":"sha256:config"}`)
			}))
			defer server.Close()
			defer close(release)
			httpClient := server.Client()
			httpClient.Transport = pendingInspectTransport{httpClient.Transport}
			cli, err := dockerclient.New(dockerclient.WithHost(server.URL), dockerclient.WithAPIVersion("1.51"), dockerclient.WithHTTPClient(httpClient))
			if err != nil {
				t.Fatal(err)
			}
			defer cli.Close()
			workflow := &DockerWorkflow{Client: cli}
			var ctx context.Context
			var cancel context.CancelFunc
			if deadline {
				ctx, cancel = context.WithTimeout(t.Context(), 250*time.Millisecond)
			} else {
				ctx, cancel = context.WithCancel(t.Context())
			}
			defer cancel()
			result := make(chan error, 1)
			go func() { result <- workflow.Build(ctx, "alpine") }()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("image inspect did not start")
			}
			if !deadline {
				cancel()
			}
			select {
			case err := <-result:
				want := codes.Canceled
				if deadline {
					want = codes.DeadlineExceeded
				}
				if status.Code(err) != want {
					t.Fatalf("Build = %v want %v", err, want)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("Build did not honor cancellation")
			}
		})
	}
}

func TestWorkloadNetworkRejectsInvalidConfiguredSubnet(t *testing.T) {
	workflow := &DockerWorkflow{workloadSubnet: "invalid CIDR"}
	inspected := &network.Inspect{
		Name: DefaultWorkloadNetwork, Driver: workloadNetworkDriver,
		Options: map[string]string{workloadNetworkICCOption: workloadNetworkICCOff, workloadNetworkBridgeNameOption: workloadNetworkBridgeName},
	}
	if err := workflow.validateWorkloadNetwork(DefaultWorkloadNetwork, inspected); status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "invalid CIDR") {
		t.Fatalf("invalid subnet accepted: %v", err)
	}
}

type pullFaultTransport struct {
	http.RoundTripper
	failure error
}

func (t pullFaultTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if strings.HasSuffix(req.URL.Path, "/images/create") {
		return nil, t.failure
	}
	return t.RoundTripper.RoundTrip(req)
}

func TestImagePullTransportFailureKeepsInfrastructureCategory(t *testing.T) {
	for _, tc := range []struct {
		name    string
		failure error
		code    codes.Code
	}{
		{"cancellation", context.Canceled, codes.Canceled},
		{"deadline", context.DeadlineExceeded, codes.DeadlineExceeded},
		{"overload", cerrdefs.ErrResourceExhausted, codes.ResourceExhausted},
		{"connection closed", net.ErrClosed, codes.Unavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/_ping") {
					return
				}
				w.WriteHeader(http.StatusNotFound)
				writeDockerTestResponse(t, w, `{"message":"image absent"}`)
			}))
			defer server.Close()
			httpClient := server.Client()
			httpClient.Transport = pullFaultTransport{RoundTripper: httpClient.Transport, failure: tc.failure}
			cli, err := dockerclient.New(dockerclient.WithHost(server.URL), dockerclient.WithAPIVersion("1.51"), dockerclient.WithHTTPClient(httpClient))
			if err != nil {
				t.Fatal(err)
			}
			defer cli.Close()
			err = (&DockerWorkflow{Client: cli}).Build(t.Context(), "image")
			if status.Code(err) != tc.code {
				t.Fatalf("transport failure = %v, want %s", err, tc.code)
			}
			if _, user := terminalreason.FromError(err); user {
				t.Fatalf("transport failure became a user error: %v", err)
			}
		})
	}
}

type uncanceledPullTransport struct{ http.RoundTripper }

func (t uncanceledPullTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if strings.HasSuffix(req.URL.Path, "/images/create") {
		req = req.Clone(context.WithoutCancel(req.Context()))
	}
	return t.RoundTripper.RoundTrip(req)
}

func TestSharedPullRetainsCancellationDespiteLateRegistryRejection(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/_ping") {
			return
		}
		if strings.HasSuffix(r.URL.Path, "/images/create") {
			close(entered)
			<-release
		}
		w.WriteHeader(http.StatusNotFound)
		writeDockerTestResponse(t, w, `{"message":"image absent"}`)
	}))
	defer server.Close()
	var unblock sync.Once
	defer unblock.Do(func() { close(release) })
	httpClient := server.Client()
	httpClient.Transport = uncanceledPullTransport{httpClient.Transport}
	cli, err := dockerclient.New(dockerclient.WithHost(server.URL), dockerclient.WithAPIVersion("1.51"), dockerclient.WithHTTPClient(httpClient))
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	workflow := &DockerWorkflow{Client: cli}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	ownerDone := make(chan error, 1)
	go func() { ownerDone <- workflow.Build(ctx, "image") }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("pull did not start")
	}
	// Join the real in-flight singleflight result before the initiating caller cancels.
	follower := workflow.pullGroup.DoChan("image", func() (any, error) { return nil, fmt.Errorf("unexpected second pull") })
	cancel()
	unblock.Do(func() { close(release) })
	select {
	case result := <-follower:
		if status.Code(result.Err) != codes.Canceled {
			t.Fatalf("shared canceled request = %v", result.Err)
		}
		if _, user := terminalreason.FromError(result.Err); user {
			t.Fatalf("caller cancellation became image rejection: %v", result.Err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("shared pull did not finish")
	}
	select {
	case err := <-ownerDone:
		if status.Code(err) != codes.Canceled {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("owner did not finish")
	}
}
