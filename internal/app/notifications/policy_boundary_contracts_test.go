//nolint:testpackage // Verifies internal method qualification.
package notifications

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNotificationPolicyRequiresQualifiedMethod(t *testing.T) {
	t.Parallel()
	for _, method := range []string{"", "CreateNotification", "/CreateNotification", "/notifications.Service/ListNotifications"} {
		require.False(t, isInternalAPI(method))
	}
	require.True(t, isInternalAPI("/notifications.Service/CreateNotification"))
}
