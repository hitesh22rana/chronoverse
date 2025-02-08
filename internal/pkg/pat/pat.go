//go:generate go run go.uber.org/mock/mockgen@v0.5.0 -source=$GOFILE -package=$GOPACKAGE -destination=./mock/$GOFILE

package pat

import (
	"context"
	"math/rand"
	"sync"
	"time"
	"unsafe"

	"github.com/golang-jwt/jwt/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/pkg/redis"
)

const letterBytes = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
const (
	letterIdxBits  = 6                    // 6 bits to represent a letter index
	letterIdxMask  = 1<<letterIdxBits - 1 // All 1-bits, as many as letterIdxBits
	letterIdxMax   = 63 / letterIdxBits   // # of letter indices fitting in 63 bits
	claimsIDLength = 32                   // length of the claims ID
)

var (
	randSrc rand.Source
	once    sync.Once
)

func init() {
	once.Do(func() {
		randSrc = rand.NewSource(time.Now().UnixNano())
	})
}

// TokenIssuer is the interface for issuing and validating pats.
type TokenIssuer interface {
	IssuePat(ctx context.Context, userID string) (string, error)
	RevokePat(ctx context.Context, pat string) (string, error)
	IsValidPat(ctx context.Context, pat string) (string, error)
}

// Options contains options for the pat issuer.
type Options struct {
	Issuer    string
	Audience  string
	JWTSecret string
	Expiry    time.Duration
}

// Claims represents the claims in the pat.
type Claims struct {
	ID string `json:"id"`
	jwt.RegisteredClaims
}

// Pat is responsible for issuing and validating pats.
type Pat struct {
	options    *Options
	redisStore *redis.Store
}

// New creates a new Pat instance.
func New(options *Options, redisStore *redis.Store) *Pat {
	return &Pat{
		options:    options,
		redisStore: redisStore,
	}
}

// generateClaimsID generates a random alphanumeric string of length 32.
// this is used to identify the pat stored in the store.
func (p *Pat) generateClaimsID() string {
	b := make([]byte, claimsIDLength)
	// A src.Int63() generates 63 random bits, enough for letterIdxMax characters!
	for i, cache, remain := claimsIDLength-1, randSrc.Int63(), letterIdxMax; i >= 0; {
		if remain == 0 {
			cache, remain = randSrc.Int63(), letterIdxMax
		}
		if idx := int(cache & letterIdxMask); idx < len(letterBytes) {
			b[i] = letterBytes[idx]
			i--
		}
		cache >>= letterIdxBits
		remain--
	}

	return *(*string)(unsafe.Pointer(&b))
}

// getCliams extracts the claims from the pat.
func (p *Pat) getClaims(pat string) (*Claims, error) {
	c := &Claims{}
	claims, err := jwt.ParseWithClaims(pat, c, func(_ *jwt.Token) (interface{}, error) {
		return []byte(p.options.JWTSecret), nil
	})
	if err != nil {
		return nil, status.Errorf(codes.Unauthenticated, "invalid pat: %v", err)
	}

	if _, ok := claims.Method.(*jwt.SigningMethodHMAC); !ok {
		return nil, status.Errorf(codes.Unauthenticated, "invalid signing method")
	}

	return c, nil
}

// IssuePat issues a new pat and stores it in the store.
func (p *Pat) IssuePat(_ context.Context, userID string) (string, error) {
	id := p.generateClaimsID()
	now := time.Now()

	claims := &Claims{
		ID: id,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    p.options.Issuer,
			Audience:  jwt.ClaimStrings{p.options.Audience},
			ExpiresAt: jwt.NewNumericDate(now.Add(p.options.Expiry)),
			IssuedAt:  jwt.NewNumericDate(now),
			Subject:   userID,
		},
	}

	pat := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signedPat, err := pat.SignedString([]byte(p.options.JWTSecret))
	if err != nil {
		return "", status.Errorf(codes.Internal, "failed to sign pat: %v", err)
	}

	return signedPat, nil
}

// RevokePat invalidates a pat.
func (p *Pat) RevokePat(ctx context.Context, pat string) (string, error) {
	// Add the pat to the blacklist with the expiration time
	// If the pat is valid, it will be invalidated when it expires
	// This is to prevent the pat from being used after it is revoked
	claims, err := p.getClaims(pat)
	if err != nil {
		return "", err
	}

	// check expiry
	if claims.RegisteredClaims.ExpiresAt.Time.Before(time.Now()) {
		return "", status.Error(codes.Unauthenticated, "pat expired")
	}

	// Check if the pat is already in the store
	var c Claims
	err = p.redisStore.Get(ctx, claims.ID, &c)
	if err != nil && status.Code(err) != codes.NotFound {
		return "", err
	}

	if err == nil {
		// If the pat is already in the store, it is already revoked
		return "", status.Error(codes.Unauthenticated, "pat is already revoked")
	}

	// Add one second to the expiry time to ensure the pat is invalidated
	expiry := time.Until(claims.ExpiresAt.Time.Add(time.Second))

	// Set the pat in the store
	err = p.redisStore.Set(ctx, claims.ID, claims, expiry)
	if err != nil {
		return "", status.Errorf(codes.Internal, "failed to add revoked pat to store: %v", err)
	}

	return claims.Subject, nil
}

// IsValidPat checks if a pat is valid.
func (p *Pat) IsValidPat(ctx context.Context, pat string) (string, error) {
	claims, err := p.getClaims(pat)
	if err != nil {
		return "", err
	}

	// check expiry
	if claims.RegisteredClaims.ExpiresAt.Time.Before(time.Now()) {
		return "", status.Error(codes.Unauthenticated, "pat expired")
	}

	var c Claims

	// Check if the pat is in the store
	err = p.redisStore.Get(ctx, claims.ID, &c)
	if err != nil {
		// If the pat is not in the store, it is valid
		if status.Code(err) == codes.NotFound {
			return claims.RegisteredClaims.Subject, nil
		}

		return "", status.Errorf(codes.Internal, "failed to validate pat: %v", err)
	}

	// Since the pat is in the store, it is revoked
	return "", status.Error(codes.Unauthenticated, "pat is already revoked")
}
