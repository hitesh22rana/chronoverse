//nolint:testpackage // Tests unexported cache-key builders directly.
package jobs

import (
	"strings"
	"testing"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
)

func TestJobLogsCacheKeysAreFixedSize(t *testing.T) {
	t.Parallel()

	huge := strings.Repeat("a", 1000000)
	keys := map[string]string{
		"read":   jobLogsCacheKey("user1", "job1", huge, "stdout", jobsmodel.JobLogsSortOrderDesc),
		"search": searchJobLogsCacheKey("user1", "job1", huge, huge, "stdout", jobsmodel.JobLogsSortOrderDesc, false),
	}
	for name, key := range keys {
		if len(key) > 100 {
			t.Fatalf("%s cache key length = %d, want fixed-size", name, len(key))
		}
	}
	if keys["read"] == keys["search"] {
		t.Fatal("read and search cache keys collide")
	}
	read := jobLogsCacheKey("user1", "job1", "cursor", "stdout", jobsmodel.JobLogsSortOrderDesc)
	if again := jobLogsCacheKey("user1", "job1", "cursor", "stdout", jobsmodel.JobLogsSortOrderDesc); again != read {
		t.Fatal("cache key is not deterministic")
	}
	if same := jobLogsCacheKey("user1", "job1", "other", "stdout", jobsmodel.JobLogsSortOrderDesc); same == read {
		t.Fatal("different cursors produced the same cache key")
	}
}
