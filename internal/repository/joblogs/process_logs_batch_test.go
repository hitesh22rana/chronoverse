//nolint:testpackage // Tests unexported batch-dedupe helper directly.
package joblogs

import (
	"testing"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
)

func TestDedupeLogsInBatch(t *testing.T) {
	logs := []*jobsmodel.JobLogEvent{
		{EventKey: "log-1", Message: "first"},
		{EventKey: "log-1", Message: "duplicate"},
		{EventKey: "log-2", Message: "second"},
		{Message: "without-key"},
	}

	deduped := dedupeLogsInBatch(logs)
	if len(deduped) != 3 {
		t.Fatalf("expected 3 logs after dedupe, got %d", len(deduped))
	}
	if deduped[0].Message != "first" {
		t.Fatalf("expected first copy to be retained, got %q", deduped[0].Message)
	}
	if deduped[1].EventKey != "log-2" {
		t.Fatalf("expected second unique event key, got %q", deduped[1].EventKey)
	}
	if deduped[2].Message != "without-key" {
		t.Fatalf("expected log without event key to be retained, got %q", deduped[2].Message)
	}
}
