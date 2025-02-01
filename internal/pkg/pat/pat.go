package pat

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"time"

	"github.com/hitesh22rana/chronoverse/internal/pkg/redis"
)

const (
	patPrefix     = "pat:"
	tokenLength   = 32
	defaultExpiry = 24 * time.Hour
)

// PatMetaData stores essential pat information
type PatMetaData struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// PatOptions contains options for the Pat
type PatOptions struct {
	Expiry time.Duration
}

// Pat is a simple token-based authentication system
type Pat struct {
	options    *PatOptions
	redisStore *redis.RedisStore
}

// New creates a new Pat instance
func New(options *PatOptions, redisStore *redis.RedisStore) *Pat {
	// Validate options
	if options == nil {
		options = &PatOptions{}
	}

	if options.Expiry == 0 {
		options.Expiry = defaultExpiry
	}

	return &Pat{
		options:    options,
		redisStore: redisStore,
	}
}

// GeneratePat creates a new pat and stores it in the store
func (p *Pat) GeneratePat(ctx context.Context, id string) (string, error) {
	// Create seed from current time and ID
	seed := fmt.Sprintf("%d:%s", time.Now().UnixNano(), id)

	// Generate random bytes
	tokenBytes := make([]byte, tokenLength)
	if _, err := rand.Read(tokenBytes); err != nil {
		return "", fmt.Errorf("failed to generate token: %v", err)
	}

	// Combine seed and random bytes
	combined := append([]byte(seed), tokenBytes...)

	// Generate final PAT using base64 encoding
	pat := base64.URLEncoding.EncodeToString(combined)

	// Create metadata
	metadata := PatMetaData{
		ID:        id,
		CreatedAt: time.Now(),
		ExpiresAt: time.Now().Add(defaultExpiry),
	}

	// Store in Redis
	key := fmt.Sprintf("%s%s", patPrefix, pat)
	if err := p.redisStore.Set(ctx, key, metadata, defaultExpiry); err != nil {
		return "", fmt.Errorf("failed to store pat: %v", err)
	}

	return pat, nil
}

// ValidatePat checks if a pat is valid and returns its metadata
func (p *Pat) ValidatePat(ctx context.Context, pat string) (*PatMetaData, error) {
	// Get metadata
	key := fmt.Sprintf("%s%s", patPrefix, pat)
	var metadata PatMetaData
	if err := p.redisStore.Get(ctx, key, &metadata); err != nil {
		return nil, fmt.Errorf("invalid pat: %v", err)
	}

	// Check expiration
	if time.Now().After(metadata.ExpiresAt) {
		return nil, fmt.Errorf("pat expired")
	}

	return &metadata, nil
}

// RevokePat invalidates a pat
func (p *Pat) RevokePat(ctx context.Context, pat string) error {
	// Remove pat metadata
	key := fmt.Sprintf("%s%s", patPrefix, pat)
	if err := p.redisStore.Delete(ctx, key); err != nil {
		return fmt.Errorf("failed to delete pat: %v", err)
	}

	return nil
}

// IsValidPat provides a simple validity check
func (p *Pat) IsValidPat(ctx context.Context, token string) bool {
	_, err := p.ValidatePat(ctx, token)
	return err == nil
}
