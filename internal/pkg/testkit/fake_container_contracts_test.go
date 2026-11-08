//nolint:testpackage // Exercises the shared test double's concurrency contract.
package testkit

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestFakeContainerServiceRejectsUnsupportedOperations(t *testing.T) {
	fake := NewFakeContainerSvc(0)
	require.Equal(t, "tcp://fake:2375", fake.DockerHost())
	exists, err := fake.ImageExists(t.Context(), "image")
	require.NoError(t, err)
	require.False(t, exists)
	ref, digest, err := fake.ResolveImageDigest(t.Context(), "image")
	require.Empty(t, ref)
	require.Empty(t, digest)
	require.Equal(t, codes.Unimplemented, status.Code(err))
	id, logs, errs, err := fake.Execute(t.Context(), time.Second, "image", nil, nil)
	require.Empty(t, id)
	require.Nil(t, logs)
	require.Nil(t, errs)
	require.Equal(t, codes.Unimplemented, status.Code(err))
	logs, errs, err = fake.Logs(t.Context(), "id")
	require.Nil(t, logs)
	require.Nil(t, errs)
	require.Equal(t, codes.Unimplemented, status.Code(err))
	state, err := fake.Inspect(t.Context(), "id")
	require.Nil(t, state)
	require.Equal(t, codes.Unimplemented, status.Code(err))
	require.Equal(t, codes.Unimplemented, status.Code(fake.Remove(t.Context(), "id")))
	require.Equal(t, codes.Unimplemented, status.Code(fake.Terminate(t.Context(), "id")))
}

func TestFakeContainerCancellationUnwindsInflightBuilds(t *testing.T) {
	fake := NewFakeContainerSvc(time.Hour)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	require.ErrorIs(t, fake.Build(ctx, "image"), context.Canceled)
	require.Empty(t, fake.CompletedBuilds())
	require.Equal(t, 1, fake.MaxConcurrentBuilds("image"))
	fake.BuildDelay = 0
	require.NoError(t, fake.Build(t.Context(), "image"))
	require.Equal(t, 1, fake.MaxConcurrentBuilds("image"))
	snapshot := fake.CompletedBuilds()
	snapshot[0] = "changed"
	require.Equal(t, []string{"image"}, fake.CompletedBuilds())
}
