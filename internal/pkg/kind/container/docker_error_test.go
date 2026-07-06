//nolint:testpackage // Testing internal package for better error handling validation
package container

import (
	"context"
	"errors"
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

	const imageRef = "registry.example.com/app@sha256:0123456789abcdef"
	w := &DockerWorkflow{}

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
}

func TestFirstRepositoryDigestReturnsFirstNonEmptyDigest(t *testing.T) {
	t.Parallel()

	got, err := firstRepositoryDigest("registry.example.com/app:latest", []string{
		"",
		"registry.example.com/app@sha256:abc",
		"registry.example.com/app@sha256:def",
	})
	if err != nil {
		t.Fatalf("firstRepositoryDigest() error = %v", err)
	}
	if got != "registry.example.com/app@sha256:abc" {
		t.Fatalf("firstRepositoryDigest() = %q", got)
	}
}

func TestFirstRepositoryDigestFailsWithoutRepoDigest(t *testing.T) {
	t.Parallel()

	_, err := firstRepositoryDigest("local-app:dev", []string{"", ""})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("firstRepositoryDigest() code = %s, want %s: %v", status.Code(err), codes.FailedPrecondition, err)
	}
}
