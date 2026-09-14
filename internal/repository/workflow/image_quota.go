package workflow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ImageQuotaConfig bounds distinct registry images per user over a sliding
// window, slowing disk-fill abuse without a database migration. Zero
// MaxDistinct disables the quota.
type ImageQuotaConfig struct {
	MaxDistinct int
	TTL         time.Duration
}

// CheckImageQuota records image for userID and refuses over-quota users with
// FailedPrecondition (fail-fast: retrying cannot free quota). Keys and
// members are hashed so user IDs and image refs never land in Redis raw.
func CheckImageQuota(ctx context.Context, locks workflowLockStore, userID, image string, cfg ImageQuotaConfig) error {
	if cfg.MaxDistinct <= 0 {
		return nil
	}
	if cfg.TTL <= 0 {
		return status.Error(codes.Internal, "image quota TTL must be positive")
	}
	count, err := locks.TrackDistinct(ctx, imageQuotaKey(userID), sha256Hex(image), cfg.TTL)
	if err != nil {
		return err
	}
	if count > int64(cfg.MaxDistinct) {
		return status.Errorf(codes.FailedPrecondition, "distinct image quota exceeded (%d)", cfg.MaxDistinct)
	}
	return nil
}

func imageQuotaKey(userID string) string {
	return "container:image-quota:" + sha256Hex(userID)
}

func sha256Hex(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
