//nolint:testpackage // Tests the internal filter validator directly.
package workflows

import (
	"testing"

	workflowsmodel "github.com/hitesh22rana/chronoverse/internal/model/workflows"
)

func TestValidateFiltersIntervalRange(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		filters *workflowsmodel.ListWorkflowsFilters
		wantErr bool
	}{
		{
			name:    "minimum only",
			filters: &workflowsmodel.ListWorkflowsFilters{IntervalMin: 5},
		},
		{
			name:    "maximum only",
			filters: &workflowsmodel.ListWorkflowsFilters{IntervalMax: 5},
		},
		{
			name:    "bounded range",
			filters: &workflowsmodel.ListWorkflowsFilters{IntervalMin: 5, IntervalMax: 10},
		},
		{
			name:    "equal bounds",
			filters: &workflowsmodel.ListWorkflowsFilters{IntervalMin: 5, IntervalMax: 5},
		},
		{
			name:    "reversed range",
			filters: &workflowsmodel.ListWorkflowsFilters{IntervalMin: 10, IntervalMax: 5},
			wantErr: true,
		},
		{
			name:    "negative minimum",
			filters: &workflowsmodel.ListWorkflowsFilters{IntervalMin: -1},
			wantErr: true,
		},
		{
			name:    "negative maximum",
			filters: &workflowsmodel.ListWorkflowsFilters{IntervalMax: -1},
			wantErr: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			err := validateFilters(test.filters)
			if test.wantErr && err == nil {
				t.Fatal("expected validation error")
			}
			if !test.wantErr && err != nil {
				t.Fatalf("unexpected validation error: %v", err)
			}
		})
	}
}
