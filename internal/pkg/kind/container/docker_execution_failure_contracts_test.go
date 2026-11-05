//nolint:testpackage // Tests shared terminal-failure publication and real SDK cleanup.
package container

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	dockerclient "github.com/docker/docker/client"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/pkg/terminalreason"
)

//nolint:gocyclo // Verifies publication ordering, cleanup requests, and terminal classification independently.
func TestExecutionFailurePublishesBeforeBestEffortStop(t *testing.T) {
	for _, tc := range []struct {
		name     string
		deadline bool
		response int
		want     codes.Code
	}{
		{"deadline", true, http.StatusNoContent, codes.DeadlineExceeded},
		{"canceled", false, http.StatusNoContent, codes.Canceled},
		{"deadline stop rejected", true, http.StatusInternalServerError, codes.DeadlineExceeded},
		{"canceled stop rejected", false, http.StatusInternalServerError, codes.Canceled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			var stops atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/containers/contract/stop") || r.URL.Query().Get("t") != "2" {
					t.Errorf("cleanup request = %s %s", r.Method, r.URL)
				}
				stops.Add(1)
				close(entered)
				<-release
				w.WriteHeader(tc.response)
				if tc.response != http.StatusNoContent {
					writeDockerTestResponse(t, w, `{"message":"stop rejected"}`)
				}
			}))
			defer server.Close()
			defer unblock()
			cli, err := dockerclient.NewClientWithOpts(dockerclient.WithHost(server.URL))
			if err != nil {
				t.Fatal(err)
			}
			defer cli.Close()
			workflow := &DockerWorkflow{Client: cli}
			var ctx context.Context
			var cancel context.CancelFunc
			if tc.deadline {
				ctx, cancel = context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
			} else {
				ctx, cancel = context.WithCancel(t.Context())
				cancel()
			}
			defer cancel()
			errs := make(chan error, 1)
			done := make(chan struct{})
			go func() {
				defer close(done)
				workflow.publishExecutionFailure(ctx, "contract", errs, errors.New("wait interrupted"))
			}()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("stop request did not start")
			}
			select {
			case terminal := <-errs:
				if status.Code(terminal) != tc.want {
					t.Fatalf("terminal error=%v", terminal)
				}
				reason, found := terminalreason.FromError(terminal)
				if tc.deadline && (!found || reason != terminalreason.TimeLimitExceeded) {
					t.Fatalf("deadline reason = %q %v", reason, found)
				}
				if !tc.deadline && found {
					t.Fatalf("cancel acquired failure reason %q", reason)
				}
			default:
				t.Fatal("terminal error unavailable while Stop blocked")
			}
			select {
			case <-done:
				t.Fatal("cleanup finished before Stop response")
			default:
			}
			unblock()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("cleanup did not finish after Stop response")
			}
			if stops.Load() != 1 {
				t.Fatalf("stop calls=%d", stops.Load())
			}
		})
	}
}

func TestOrdinaryExecutionWaitFailureDoesNotStopContainer(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	cli, err := dockerclient.NewClientWithOpts(dockerclient.WithHost(server.URL))
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	workflow := &DockerWorkflow{Client: cli}
	errs := make(chan error, 1)
	workflow.publishExecutionFailure(t.Context(), "contract", errs, errors.New("daemon wait failed"))
	terminal := <-errs
	if status.Code(terminal) != codes.Aborted || !strings.Contains(terminal.Error(), "daemon wait failed") || calls.Load() != 0 {
		t.Fatalf("wait failure=%v requests=%d", terminal, calls.Load())
	}
}

func TestExecuteInternalDeadlinePublishesBeforeStopCompletes(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	workflow := newRunningContainerWorkflow(t, runningContainerFixture{
		logStream: func(w http.ResponseWriter, r *http.Request) { flushDockerLogStream(w); <-r.Context().Done() },
		stop:      func() { close(entered); <-release },
	})
	_, logs, errs, err := workflow.Execute(t.Context(), 200*time.Millisecond, "alpine", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("execution deadline did not attempt Stop")
	}
	select {
	case terminal := <-errs:
		reason, found := terminalreason.FromError(terminal)
		if status.Code(terminal) != codes.DeadlineExceeded || !found || reason != terminalreason.TimeLimitExceeded {
			t.Fatalf("internal execution deadline = %v reason=%q", terminal, reason)
		}
	default:
		t.Fatal("deadline result unavailable while Stop blocked")
	}
	select {
	case _, open := <-logs:
		if !open {
			t.Fatal("execution channels closed before Stop completed")
		}
		t.Fatal("fixture unexpectedly published logs")
	default:
	}
	unblock()
	result := awaitDockerLogStream(t, logs, errs)
	if len(result.logs) != 0 || len(result.errs) != 0 {
		t.Fatalf("unexpected additional execution output: %+v", result)
	}
}
