//nolint:testpackage // Quota test uses the shared Redis container like other integration tests.
package workflow

import (
	"fmt"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/pkg/testkit"
)

func TestIntegrationCheckImageQuotaEnforcesDistinctLimit(t *testing.T) {
	rdb := testkit.Redis(t)
	cfg := ImageQuotaConfig{MaxDistinct: 2, TTL: time.Hour}
	userID := fmt.Sprintf("quota-user-%s", t.Name())
	ctx := t.Context()

	for _, image := range []string{"quota-img-a:1", "quota-img-b:1", "quota-img-a:1"} {
		if err := CheckImageQuota(ctx, rdb, userID, image, cfg); err != nil {
			t.Fatalf("CheckImageQuota(%q) error = %v", image, err)
		}
	}

	// Third distinct image exceeds the quota of two (re-adding an existing one
	// above does not, proving the count is distinct, not total).
	err := CheckImageQuota(ctx, rdb, userID, "quota-img-c:1", cfg)
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("CheckImageQuota() code = %s, want %s: %v", status.Code(err), codes.FailedPrecondition, err)
	}
}

func TestIntegrationCheckImageQuotaDisabledWhenZero(t *testing.T) {
	rdb := testkit.Redis(t)

	if err := CheckImageQuota(t.Context(), rdb, "quota-user-disabled", "any:1", ImageQuotaConfig{}); err != nil {
		t.Fatalf("CheckImageQuota() error = %v", err)
	}
}
