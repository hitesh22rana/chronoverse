//nolint:testpackage // Exercises the repository cursor guard without database access.
package workflows

import (
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestListWorkflowsRejectsMalformedCursor(t *testing.T) {
	t.Parallel()
	cases := []struct{ name, cursor, message string }{
		{"missing separator", "invalid", "expected two parts"},
		{"extra separator", "id$timestamp$extra", "expected two parts"},
		{"invalid timestamp", "id$not-a-date", "invalid timestamp"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			response, err := New(&Config{FetchLimit: 2}, nil).ListWorkflows(t.Context(), testUserID, tc.cursor, nil)
			if response != nil || status.Code(err) != codes.InvalidArgument || !strings.Contains(status.Convert(err).Message(), tc.message) {
				t.Fatalf("ListWorkflows = (%+v, %v), want nil and InvalidArgument containing %q", response, err, tc.message)
			}
		})
	}
}
