//nolint:testpackage // Testing internal package for better error handling validation
package container

import (
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
