package pat

import (
	"context"
	"math/rand"
	"sync"
	"time"
	"unsafe"

	"github.com/golang-jwt/jwt/v5"
	"go.opentelemetry.io/otel"
	otelcodes "go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/pkg/redis"
	svcpkg "github.com/hitesh22rana/chronoverse/internal/pkg/svc"
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

// audienceContextKey is the key for the audience in the context.
type audienceContextKey struct{}

// patContextKey is the key for the pat in the context.
type patContextKey struct{}

// audienceFromContext extracts the audience from the context.
func audienceFromContext(ctx context.Context) (string, error) {
	value := ctx.Value(audienceContextKey{})
	if value == nil {
		return "", status.Error(codes.FailedPrecondition, "audience is required")
	}

	audience, ok := value.(string)
	if !ok || audience == "" {
		return "", status.Error(codes.FailedPrecondition, "audience is required")
	}

	return audience, nil
}

// patFromContext extracts the pat from the context.
func patFromContext(ctx context.Context) (string, error) {
	value := ctx.Value(patContextKey{})
	if value == nil {
		return "", status.Error(codes.FailedPrecondition, "pat is required")
	}

	pat, ok := value.(string)
	if !ok || pat == "" {
		return "", status.Error(codes.FailedPrecondition, "pat is required")
	}

	return pat, nil
}

// WithAudience sets the audience in the context.
func WithAudience(ctx context.Context, audience string) context.Context {
	return context.WithValue(ctx, audienceContextKey{}, audience)
}

// WithPat sets the pat in the context.
func WithPat(ctx context.Context, pat string) context.Context {
	return context.WithValue(ctx, patContextKey{}, pat)
}

func init() {
	once.Do(func() {
		randSrc = rand.NewSource(time.Now().UnixNano())
	})
}

// TokenIssuer is the interface for issuing and validating pats.
type TokenIssuer interface {
	IssuePat(ctx context.Context, userID string) (string, error)
	RevokePat(ctx context.Context) (string, error)
	IsValidPat(ctx context.Context) (string, error)
}

// Options contains options for the pat issuer.
type Options struct {
	Issuer    string
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
	tp         trace.Tracer
	options    *Options
	redisStore *redis.Store
}

// New creates a new Pat instance.
func New(options *Options, redisStore *redis.Store) *Pat {
	serviceName := svcpkg.Info().GetServiceInfo()
	return &Pat{
		tp:         otel.Tracer(serviceName),
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
func (p *Pat) IssuePat(ctx context.Context, userID string) (pat string, err error) {
	ctx, span := p.tp.Start(ctx, "Pat.IssuePat")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	audience, err := audienceFromContext(ctx)
	if err != nil {
		return "", err
	}

	id := p.generateClaimsID()
	now := time.Now()

	claims := &Claims{
		ID: id,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    p.options.Issuer,
			Audience:  jwt.ClaimStrings{audience},
			ExpiresAt: jwt.NewNumericDate(now.Add(p.options.Expiry)),
			IssuedAt:  jwt.NewNumericDate(now),
			Subject:   userID,
		},
	}

	_pat := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signedPat, err := _pat.SignedString([]byte(p.options.JWTSecret))
	if err != nil {
		err = status.Errorf(codes.Internal, "failed to sign pat: %v", err)
		return "", err
	}

	return signedPat, nil
}

// RevokePat invalidates a pat.
func (p *Pat) RevokePat(ctx context.Context) (userID string, err error) {
	ctx, span := p.tp.Start(ctx, "Pat.RevokePat")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	// Extract the pat from the context
	pat, err := patFromContext(ctx)
	if err != nil {
		return "", err
	}

	// Add the pat to the blacklist with the expiration time
	// If the pat is valid, it will be invalidated when it expires
	// This is to prevent the pat from being used after it is revoked
	claims, err := p.getClaims(pat)
	if err != nil {
		return "", err
	}

	// check expiry
	if claims.ExpiresAt.Time.Before(time.Now()) {
		err = status.Error(codes.Unauthenticated, "pat expired")
		return "", err
	}

	// Check if the pat is already in the store
	var c Claims
	err = p.redisStore.Get(ctx, claims.ID, &c)
	if err != nil && status.Code(err) != codes.NotFound {
		return "", err
	}

	if err == nil {
		// If the pat is already in the store, it is already revoked
		err = status.Error(codes.Unauthenticated, "pat is already revoked")
		return "", err
	}

	// Add one second to the expiry time to ensure the pat is invalidated
	expiry := time.Until(claims.ExpiresAt.Time.Add(time.Second))

	// Set the pat in the store
	err = p.redisStore.Set(ctx, claims.ID, claims, expiry)
	if err != nil {
		err = status.Errorf(codes.Internal, "failed to add revoked pat to store: %v", err)
		return "", err
	}

	return claims.Subject, nil
}

// IsValidPat checks if a pat is valid.
func (p *Pat) IsValidPat(ctx context.Context) (userID string, err error) {
	ctx, span := p.tp.Start(ctx, "Pat.IsValidPat")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	// Extract the pat from the context
	pat, err := patFromContext(ctx)
	if err != nil {
		return "", err
	}

	claims, err := p.getClaims(pat)
	if err != nil {
		return "", err
	}

	// check expiry
	if claims.ExpiresAt.Time.Before(time.Now()) {
		err = status.Error(codes.Unauthenticated, "pat expired")
		return "", err
	}

	var c Claims

	// Check if the pat is in the store
	err = p.redisStore.Get(ctx, claims.ID, &c)
	if err != nil {
		// If the pat is not in the store, it is valid
		if status.Code(err) == codes.NotFound {
			return claims.Subject, nil
		}

		err = status.Errorf(codes.Internal, "failed to validate pat: %v", err)
		return "", err
	}

	// Since the pat is in the store, it is revoked
	err = status.Error(codes.Unauthenticated, "pat is revoked")
	return "", err
}
