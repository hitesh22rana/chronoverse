//nolint:testpackage // Testing internal package for better error handling validation
package container

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	cerrdefs "github.com/containerd/errdefs"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestDockerImageInspectErrorMapsDockerErrorClasses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		code codes.Code
	}{
		{
			name: "invalid argument",
			err:  cerrdefs.ErrInvalidArgument.WithMessage("invalid reference format"),
			code: codes.InvalidArgument,
		},
		{
			name: "unavailable",
			err:  cerrdefs.ErrUnavailable.WithMessage("docker daemon unavailable"),
			code: codes.Unavailable,
		},
		{
			name: "fallback",
			err:  errors.New("unexpected inspect failure"),
			code: codes.Aborted,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := dockerImageInspectError(tt.err)
			if status.Code(err) != tt.code {
				t.Fatalf("dockerImageInspectError() code = %s, want %s: %v", status.Code(err), tt.code, err)
			}
		})
	}
}

func TestResolveImageDigestKeepsAlreadyDigestedReference(t *testing.T) {
	t.Parallel()

	const imageRef = "registry.example.com/app@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	var inspectCalls atomic.Int32
	var pullCalls atomic.Int32
	pulled := atomic.Bool{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/_ping":
			w.Header().Set("API-Version", "1.51")
			writeDockerTestResponse(t, w, "OK")
		case strings.Contains(r.URL.Path, "/images/") && strings.HasSuffix(r.URL.Path, "/json"):
			inspectCalls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			if !pulled.Load() {
				w.WriteHeader(http.StatusNotFound)
				writeDockerTestResponse(t, w, `{"message":"No such image"}`)
				return
			}
			writeDockerTestResponse(t, w, `{"Id":"sha256:config","RepoDigests":[]}`)
		case strings.Contains(r.URL.Path, "/images/create"):
			pullCalls.Add(1)
			pulled.Store(true)
			w.Header().Set("Content-Type", "application/json")
			writeDockerTestResponse(t, w, `{"status":"pulled"}`)
		case strings.Contains(r.URL.Path, "/networks/"):
			// Constructor ensures the workload network: report it as existing.
			w.Header().Set("Content-Type", "application/json")
			writeDockerTestResponse(t, w, `{"Name":"chronoverse-workloads","Driver":"bridge","Options":{"com.docker.network.bridge.enable_icc":"false"}}`)
		default:
			t.Fatalf("unexpected Docker API request: %s %s", r.Method, r.URL.String())
		}
	}))
	t.Cleanup(server.Close)

	w, err := NewDockerWorkflow(WithDockerHost(server.URL))
	if err != nil {
		t.Fatalf("NewDockerWorkflow() error = %v", err)
	}
	t.Cleanup(func() {
		_ = w.Close()
	})

	resolvedRef, resolvedDigest, err := w.ResolveImageDigest(context.Background(), imageRef)
	if err != nil {
		t.Fatalf("ResolveImageDigest() error = %v", err)
	}
	if resolvedRef != imageRef {
		t.Fatalf("resolved image ref = %q, want %q", resolvedRef, imageRef)
	}
	if resolvedDigest != imageRef {
		t.Fatalf("resolved image digest = %q, want %q", resolvedDigest, imageRef)
	}
	if got := pullCalls.Load(); got != 1 {
		t.Fatalf("image pull calls = %d, want 1", got)
	}
	if got := inspectCalls.Load(); got < 2 {
		t.Fatalf("image inspect calls = %d, want at least 2", got)
	}
}

func writeDockerTestResponse(t *testing.T, w http.ResponseWriter, body string) {
	t.Helper()
	if _, err := w.Write([]byte(body)); err != nil {
		t.Errorf("failed to write Docker test response: %v", err)
	}
}

func TestMatchingRepositoryDigestReturnsDigestForRequestedRepository(t *testing.T) {
	t.Parallel()

	const want = "registry.example.com/app@sha256:2222222222222222222222222222222222222222222222222222222222222222"
	got, err := matchingRepositoryDigest("registry.example.com/app:latest", []string{
		"",
		"other.example.com/app@sha256:1111111111111111111111111111111111111111111111111111111111111111",
		want,
	})
	if err != nil {
		t.Fatalf("matchingRepositoryDigest() error = %v", err)
	}
	if got != want {
		t.Fatalf("matchingRepositoryDigest() = %q, want %q", got, want)
	}
}

func TestMatchingRepositoryDigestSkipsMalformedCandidates(t *testing.T) {
	t.Parallel()

	const want = "registry.example.com/app@sha256:3333333333333333333333333333333333333333333333333333333333333333"
	got, err := matchingRepositoryDigest("registry.example.com/app:latest", []string{
		"not a digest",
		"registry.example.com/app@sha256:not-hex",
		"other.example.com/app@sha256:2222222222222222222222222222222222222222222222222222222222222222",
		want,
	})
	if err != nil {
		t.Fatalf("matchingRepositoryDigest() error = %v", err)
	}
	if got != want {
		t.Fatalf("matchingRepositoryDigest() = %q, want %q", got, want)
	}
}

func TestMatchingRepositoryDigestSupportsDockerHubShorthand(t *testing.T) {
	t.Parallel()

	const want = "docker.io/library/alpine@sha256:4444444444444444444444444444444444444444444444444444444444444444"
	got, err := matchingRepositoryDigest("alpine:latest", []string{want})
	if err != nil {
		t.Fatalf("matchingRepositoryDigest() error = %v", err)
	}
	if got != want {
		t.Fatalf("matchingRepositoryDigest() = %q, want %q", got, want)
	}
}

func TestMatchingRepositoryDigestFailsWithoutMatchingRepoDigest(t *testing.T) {
	t.Parallel()

	_, err := matchingRepositoryDigest("registry.example.com/app:latest", []string{
		"",
		"other.example.com/app@sha256:5555555555555555555555555555555555555555555555555555555555555555",
	})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("matchingRepositoryDigest() code = %s, want %s: %v", status.Code(err), codes.FailedPrecondition, err)
	}
}

func TestMatchingRepositoryDigestFailsWithoutRepoDigest(t *testing.T) {
	t.Parallel()

	_, err := matchingRepositoryDigest("local-app:dev", []string{"", ""})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("matchingRepositoryDigest() code = %s, want %s: %v", status.Code(err), codes.FailedPrecondition, err)
	}
}

func TestMatchingRepositoryDigestFailsForInvalidRequestedImage(t *testing.T) {
	t.Parallel()

	_, err := matchingRepositoryDigest("bad@@image", []string{
		"registry.example.com/app@sha256:6666666666666666666666666666666666666666666666666666666666666666",
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("matchingRepositoryDigest() code = %s, want %s: %v", status.Code(err), codes.InvalidArgument, err)
	}
}
