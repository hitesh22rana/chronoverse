//nolint:testpackage // Pins internal RPC method classification.
package workflows

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestInternalWorkflowPolicyRequiresQualifiedMethod(t *testing.T) {
	t.Parallel()
	for _, method := range []string{"", "UpdateWorkflowBuildStatus", "/UpdateWorkflowBuildStatus", "/workflows.Service/GetWorkflow"} {
		require.False(t, isInternalAPI(method))
	}
	require.True(t, isInternalAPI("/workflows.Service/UpdateWorkflowBuildStatus"))
}
