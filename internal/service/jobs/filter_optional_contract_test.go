//nolint:testpackage // Checks the validator's explicit optional-filter contract.
package jobs

import "testing"

func TestListJobFiltersMayBeOmitted(t *testing.T) {
	t.Parallel()
	if err := validateListJobsFilters(nil); err != nil {
		t.Fatalf("omitted filters: %v", err)
	}
}
