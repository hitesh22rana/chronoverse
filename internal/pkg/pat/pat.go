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

// userFromPat extracts the user ID from a pat.
func userFromPat(pat string) (string, error) {
	// Decode the base64 encoded pat
	decodedPat, err := base64.URLEncoding.DecodeString(pat)
	if err != nil {
		return "", status.Errorf(codes.Internal, "failed to decode token: %v", err)
	}

	if len(decodedPat) < 56 {
		return "", status.Errorf(codes.Internal, "invalid token")
	}

	return string(decodedPat)[20:56], nil
}

// IssuePat issues a new pat and stores it in the store.
func (p *Pat) IssuePat(ctx context.Context, id string) (string, error) {
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

// RevokePat invalidates a pat.
func (p *Pat) RevokePat(ctx context.Context, pat string) (string, error) {
	// Extract the user ID from the token
	userID, err := userFromPat(pat)
	if err != nil {
		return "", err
	}

	// Remove pat metadata
	key := fmt.Sprintf("%s%s", patPrefix, pat)
	err = p.redisStore.Delete(ctx, key)
	if err != nil {
		return "", status.Errorf(codes.Internal, "failed to delete pat: %v", err)
	}

	return userID, nil
}

// IsValidPat checks if a pat is valid.
func (p *Pat) IsValidPat(ctx context.Context, pat string) (string, error) {
	key := fmt.Sprintf("%s%s", patPrefix, pat)
	var metadata MetaData
	if err := p.redisStore.Get(ctx, key, &metadata); err != nil {
		return "", status.Errorf(codes.Unauthenticated, "invalid pat: %v", err)
	}

	// Check expiration
	if time.Now().After(metadata.ExpiresAt) {
		return "", status.Errorf(codes.Unauthenticated, "pat expired")
	}

	// Extract the user ID from the token
	userID, err := userFromPat(pat)
	if err != nil {
		return "", err
	}

	// Check if the token is valid
	if userID != metadata.ID {
		return "", status.Errorf(codes.Unauthenticated, "invalid token")
	}

	return userID, nil
}
