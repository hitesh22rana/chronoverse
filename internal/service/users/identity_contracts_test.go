//nolint:testpackage // Verifies login error normalization.
package users

import (
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestLoginErrorNormalizationMasksIdentityLookupOnly(t *testing.T) {
	t.Parallel()
	for _, code := range []codes.Code{codes.NotFound, codes.InvalidArgument} {
		err := normalizeLoginError(status.Error(code, "private identity detail"))
		require.Equal(t, codes.Unauthenticated, status.Code(err))
		require.Equal(t, "invalid email or password", status.Convert(err).Message())
	}
	for _, code := range []codes.Code{codes.Internal, codes.Unavailable, codes.Unauthenticated} {
		original := status.Error(code, "original failure")
		require.Same(t, original, normalizeLoginError(original))
	}
	require.NoError(t, normalizeLoginError(nil))
}
