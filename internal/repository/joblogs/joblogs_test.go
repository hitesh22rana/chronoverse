//nolint:testpackage // Tests unexported batching behavior without widening production API.
package joblogs

import "testing"

func TestPrependQueueData(t *testing.T) {
	t.Parallel()

	first := &queueData{}
	second := &queueData{}
	third := &queueData{}

	got := prependQueueData([]*queueData{third}, []*queueData{first, second})
	if len(got) != 3 {
		t.Fatalf("prependQueueData() = %v, want length 3", got)
	}
	if got[0] != first || got[1] != second || got[2] != third {
		t.Fatalf("prependQueueData() = %v, want [%v, %v, %v]", got, first, second, third)
	}
}
