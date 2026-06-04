//nolint:testpackage // Tests unexported cursor helpers directly.
package jobs

import (
	"testing"
)

func TestJobLogsCursorRoundTrip(t *testing.T) {
	want := jobLogsCursor{
		SequenceNum: 42,
		Stream:      "stdout",
		EventID:     "log:job:stdout:42",
	}

	encoded := encodeJobLogsCursor(want)
	if encoded == "" {
		t.Fatal("expected encoded cursor")
	}

	got, err := extractDataFromGetJobLogsCursor(encoded)
	if err != nil {
		t.Fatalf("expected cursor to decode: %v", err)
	}
	if got != want {
		t.Fatalf("unexpected cursor: got %+v want %+v", got, want)
	}
}

func TestEncodeJobLogsCursorEmpty(t *testing.T) {
	if got := encodeJobLogsCursor(jobLogsCursor{}); got != "" {
		t.Fatalf("expected empty cursor, got %q", got)
	}
}
