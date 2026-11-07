//nolint:testpackage // Checks the forwarding contract without a repository goroutine.
package executor

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

type lifecycleRepository struct {
	calls   int
	observe func(context.Context)
	err     error
}

func (r *lifecycleRepository) Run(ctx context.Context) error { r.calls++; r.observe(ctx); return r.err }

func TestRunForwardsContextAndOriginalError(t *testing.T) {
	for _, cause := range []error{nil, errors.New("repository failed"), context.Canceled} {
		var observed context.Context
		repo := &lifecycleRepository{err: cause, observe: func(ctx context.Context) { observed = ctx }}
		err := New(repo).Run(t.Context())
		require.ErrorIs(t, err, cause)
		require.Equal(t, 1, repo.calls)
		require.Equal(t, t.Context(), observed)
	}
}
