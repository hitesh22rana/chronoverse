package pat

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/pkg/redis"
)

const (
	patPrefix     = "pat:"
	tokenLength   = 32
	defaultExpiry = 24 * time.Hour
)

// MetaData stores essential pat information.
type MetaData struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Options contains options for the Pat.
type Options struct {
	Expiry time.Duration
}

// Pat is a simple token-based authentication system.
type Pat struct {
	options    *Options
	redisStore *redis.Store
}

// New creates a new Pat instance.
func New(options *Options, redisStore *redis.Store) *Pat {
	// Validate options
	if options == nil {
		options = &Options{}
	}

	if options.Expiry == 0 {
		options.Expiry = defaultExpiry
	}

	return &Pat{
		options:    options,
		redisStore: redisStore,
	}
}

// GeneratePat creates a new pat and stores it in the store.
func (p *Pat) GeneratePat(ctx context.Context, id string) (string, error) {
	// Create seed from current time and ID
	seed := fmt.Sprintf("%d:%s", time.Now().UnixNano(), id)

	// Generate random bytes
	tokenBytes := make([]byte, tokenLength)
	if _, err := rand.Read(tokenBytes); err != nil {
		return "", status.Errorf(codes.Internal, "failed to generate random bytes: %v", err)
	}

	// Combine seed and random bytes
	combined := append([]byte(seed), tokenBytes...)

	// Generate final PAT using base64 encoding
	pat := base64.URLEncoding.EncodeToString(combined)

	// Create metadata
	metadata := MetaData{
		ID:        id,
		CreatedAt: time.Now(),
		ExpiresAt: time.Now().Add(defaultExpiry),
	}

	// Store in Redis
	key := fmt.Sprintf("%s%s", patPrefix, pat)
	return pat, p.redisStore.Set(ctx, key, metadata, defaultExpiry)
}

// ValidatePat checks if a pat is valid and returns its metadata.
func (p *Pat) ValidatePat(ctx context.Context, pat string) (*MetaData, error) {
	// Get metadata
	key := fmt.Sprintf("%s%s", patPrefix, pat)
	var metadata MetaData
	if err := p.redisStore.Get(ctx, key, &metadata); err != nil {
		return nil, status.Errorf(codes.Unauthenticated, "invalid pat: %v", err)
	}

	// Check expiration
	if time.Now().After(metadata.ExpiresAt) {
		return nil, status.Errorf(codes.Unauthenticated, "pat expired")
	}

	return &metadata, nil
}

// RevokePat invalidates a pat.
func (p *Pat) RevokePat(ctx context.Context, pat string) error {
	// Remove pat metadata
	key := fmt.Sprintf("%s%s", patPrefix, pat)
	return p.redisStore.Delete(ctx, key)
}

// IsValidPat checks if a pat is valid by comparing the token with the metadata ID.
func (p *Pat) IsValidPat(ctx context.Context, token string) error {
	metadata, err := p.ValidatePat(ctx, token)
	if err != nil {
		return err
	}

	// Decode the base64 encoded token
	decodedToken, err := base64.URLEncoding.DecodeString(token)
	if err != nil {
		return status.Errorf(codes.Internal, "failed to decode token: %v", err)
	}

	// Check if the token is valid
	if string(decodedToken)[20:56] != metadata.ID {
		return status.Errorf(codes.Unauthenticated, "invalid token")
	}

	return nil
}
