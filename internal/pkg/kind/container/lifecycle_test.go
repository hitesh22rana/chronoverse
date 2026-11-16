//nolint:testpackage // Test the real SDK adapter at its HTTP boundary.
package container

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/pkg/terminalreason"
)

func createResponseBody(status int, id string) string {
	if status == http.StatusCreated {
		return `{"Id":"` + id + `"}`
	}
	return `{"message":"create failed"}`
}

//nolint:gocyclo // One status matrix proves create never starts and keeps each failure classification distinct.
func TestCreateIntentUsesOneNamedMutationWithoutStart(t *testing.T) {
	for _, response := range []int{http.StatusCreated, http.StatusBadRequest, http.StatusNotFound, http.StatusInternalServerError, http.StatusConflict} {
		t.Run(http.StatusText(response), func(t *testing.T) {
			var creates, starts atomic.Int32
			daemon := newNetworkLifecycleTestDaemon(t, true)
			id := strings.Repeat("a", 64)
			workflow := newNetworkTestWorkflow(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if strings.HasSuffix(r.URL.Path, "/containers/create") {
					creates.Add(1)
					if r.URL.Query().Get("name") != "cv-owned" {
						t.Errorf("lost creation name: %s", r.URL)
					}
					var config struct {
						Labels     map[string]string
						HostConfig struct {
							ReadonlyRootfs bool
							NetworkMode    string
						}
					}
					if err := json.NewDecoder(r.Body).Decode(&config); err != nil {
						t.Error(err)
					}
					if config.Labels["chronoverse.intent_id"] != "intent" || config.Labels["chronoverse.job_id"] != "job" || len(config.Labels) != 3 {
						t.Errorf("ownership labels: %v", config.Labels)
					}
					if !config.HostConfig.ReadonlyRootfs || config.HostConfig.NetworkMode != DefaultWorkloadNetwork {
						t.Error("isolation lost")
					}
					// A pruned network after a failed request must not authorize a resend.
					daemon.networkExists.Store(false)
					w.WriteHeader(response)
					writeDockerTestResponse(t, w, createResponseBody(response, id))
					return
				}
				if strings.HasSuffix(r.URL.Path, "/start") {
					starts.Add(1)
					t.Error("Create started a container")
					return
				}
				daemon.ServeHTTP(w, r)
			}))
			got, err := workflow.Create(t.Context(), CreateRequest{Name: "cv-owned", IntentID: "intent", JobID: "job", Attempt: 1, Image: "alpine:3", Timeout: time.Second, Env: []string{"SECRET=private"}})
			if creates.Load() != 1 || starts.Load() != 0 {
				t.Fatalf("create/start=%d/%d", creates.Load(), starts.Load())
			}
			if response == http.StatusCreated {
				if err != nil || got != id {
					t.Fatalf("create result %q %v", got, err)
				}
				return
			}
			if err == nil || got != "" {
				t.Fatalf("failed create result %q %v", got, err)
			}
			want := "UNKNOWN"
			switch response {
			case http.StatusBadRequest:
				want = "REJECTED"
			case http.StatusNotFound:
				want = "SYSTEM"
			case http.StatusConflict:
				want = "CONFLICT"
			}
			if CreationOutcome(err) != want {
				t.Errorf("outcome %s want %s: %v", CreationOutcome(err), want, err)
			}
			reason, tagged := terminalreason.FromError(err)
			if want != "REJECTED" && (!tagged || reason != terminalreason.SystemError) {
				t.Errorf("infrastructure cause lost: %v", err)
			}
		})
	}
}

func TestRuntimeInstanceAndStartIdentityBoundary(t *testing.T) {
	var starts atomic.Int32
	workflow := newNetworkTestWorkflow(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/info"):
			writeDockerTestResponse(t, w, `{"ID":"original-daemon"}`)
		case strings.HasSuffix(r.URL.Path, "/start"):
			starts.Add(1)
			w.WriteHeader(http.StatusNotFound)
			writeDockerTestResponse(t, w, `{"message":"container absent"}`)
		default:
			t.Errorf("unexpected request %s", r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	instance, err := workflow.RuntimeInstance(context.Background())
	if err != nil || instance != "original-daemon" {
		t.Fatalf("runtime identity %q %v", instance, err)
	}
	for _, id := range []string{"cv-name", "short-id", strings.Repeat("A", 64)} {
		_, _, err = workflow.Start(t.Context(), time.Second, id)
		if status.Code(err) != codes.InvalidArgument {
			t.Errorf("nonimmutable start accepted %q: %v", id, err)
		}
	}
	if starts.Load() != 0 {
		t.Fatal("name-shaped start reached Docker")
	}
	_, _, err = workflow.Start(t.Context(), time.Second, strings.Repeat("a", 64))
	if err == nil || starts.Load() != 1 {
		t.Fatalf("start retry or lost error: %d %v", starts.Load(), err)
	}
	if reason, ok := terminalreason.FromError(err); !ok || reason != terminalreason.SystemError {
		t.Fatalf("start absence must be infrastructure: %v", err)
	}
}

func TestContainerCleanupRejectsMutableNamesAndIDPrefixes(t *testing.T) {
	workflow := &DockerWorkflow{}
	for _, id := range []string{"", "cv-container-name", "0123456789ab", strings.Repeat("A", 64)} {
		// A nil client proves invalid targets are rejected before any daemon call.
		if err := workflow.Remove(t.Context(), id); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("Remove(%q): %v", id, err)
		}
		if err := workflow.Terminate(t.Context(), id); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("Terminate(%q): %v", id, err)
		}
	}
}
