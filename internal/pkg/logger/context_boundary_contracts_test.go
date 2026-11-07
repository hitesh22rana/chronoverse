//nolint:testpackage // Checks invalid context values under the package-owned key.
package logger

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFromContextIgnoresWrongValueType(t *testing.T) {
	logger := FromContext(context.WithValue(t.Context(), loggerKey{}, "not a logger"))
	require.NotNil(t, logger)
	require.False(t, logger.Core().Enabled(-1))
}
