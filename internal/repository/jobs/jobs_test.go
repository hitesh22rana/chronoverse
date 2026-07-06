//nolint:testpackage // Tests unexported cursor helpers directly.
package jobs

import (
	"regexp"
	"strings"
	"testing"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
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

func TestReleaseJobForRetryQueryCarriesPreviousRuntimeOwner(t *testing.T) {
	query := releaseJobForRetryQuery()

	assertContains(t, query, "SELECT id, runtime_node_id")
	assertContains(t, query, "FOR UPDATE")
	assertContains(t, query, "RETURNING target.runtime_node_id AS previous_runtime_node_id")
	assertContains(t, query, "released.previous_runtime_node_id IS NOT NULL")
	assertContains(t, query, "rn.id = released.previous_runtime_node_id")
}

func TestRecoverExpiredJobLeasesQueryPrefersStoredRuntimeEndpoint(t *testing.T) {
	query := recoverExpiredJobLeasesQuery()

	assertContains(t, query, "COALESCE(NULLIF(j.runtime_endpoint, ''), rn.docker_endpoint) AS runtime_endpoint")
}

func assertContains(t *testing.T, value, want string) {
	t.Helper()

	if !strings.Contains(value, want) {
		t.Fatalf("expected query to contain %q:\n%s", want, value)
	}
}

func TestEncodeJobLogsCursorEmpty(t *testing.T) {
	if got := encodeJobLogsCursor(jobLogsCursor{}); got != "" {
		t.Fatalf("expected empty cursor, got %q", got)
	}
}

func TestNewJobLogsHighlightToken(t *testing.T) {
	got, err := newJobLogsHighlightToken()
	if err != nil {
		t.Fatalf("expected token generation to succeed: %v", err)
	}

	if !regexp.MustCompile(`^[a-f0-9]{32}$`).MatchString(got) {
		t.Fatalf("unexpected highlight token format: %q", got)
	}
}

func TestNewJobLogsSearchRequestUsesTokenScopedHighlightTags(t *testing.T) {
	const token = "0123456789abcdef0123456789abcdef"

	got := newJobLogsSearchRequest("job_id = \"job_id\"", token, 201, jobsmodel.SearchJobLogsOptions{})

	if got.HighlightPreTag != "__CV_HL_START_"+token+"__" {
		t.Fatalf("unexpected highlight pre tag: %q", got.HighlightPreTag)
	}
	if got.HighlightPostTag != "__CV_HL_END_"+token+"__" {
		t.Fatalf("unexpected highlight post tag: %q", got.HighlightPostTag)
	}
	if got.Limit != 201 {
		t.Fatalf("unexpected limit: got %d want %d", got.Limit, 201)
	}
	if len(got.AttributesToHighlight) != 1 || got.AttributesToHighlight[0] != "message" {
		t.Fatalf("unexpected attributes to highlight: %+v", got.AttributesToHighlight)
	}
}

func TestNewJobLogsSearchRequestSupportsAscendingSort(t *testing.T) {
	got := newJobLogsSearchRequest("job_id = \"job_id\"", "", 201, jobsmodel.SearchJobLogsOptions{
		SortOrder: jobsmodel.JobLogsSortOrderAsc,
	})

	if len(got.Sort) != 2 || got.Sort[0] != "sequence_num:asc" || got.Sort[1] != "id:asc" {
		t.Fatalf("unexpected sort: %+v", got.Sort)
	}
}

func TestNewJobLogsSearchRequestCanDisableHighlights(t *testing.T) {
	got := newJobLogsSearchRequest("job_id = \"job_id\"", "", 201, jobsmodel.SearchJobLogsOptions{
		DisableHighlight: true,
	})

	if len(got.AttributesToHighlight) != 0 {
		t.Fatalf("unexpected attributes to highlight: %+v", got.AttributesToHighlight)
	}
	if got.HighlightPreTag != "" || got.HighlightPostTag != "" {
		t.Fatalf("unexpected highlight tags: pre=%q post=%q", got.HighlightPreTag, got.HighlightPostTag)
	}
}
