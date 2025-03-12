package auth

import (
	"context"
	"crypto"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"go.opentelemetry.io/otel"
	otelcodes "go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	svcpkg "github.com/hitesh22rana/chronoverse/internal/pkg/svc"
)

const (
	// Expiry is the expiry time for the jwt token.
	// For security reasons, the token should expire in a short time.
	Expiry = time.Minute

	// audienceMetadataKey is the key for the audience in the metadata.
	audienceMetadataKey = "Audience"

	// authorizationMetadataKey is the key for the token in the metadata.
	authorizationMetadataKey = "Authorization"

	// roleMetadataKey is the key for the role in the metadata.
	roleMetadataKey = "Role"
)

// Role is the role of the audience.
type Role string

const (
	// RoleAdmin is the admin role.
	RoleAdmin Role = "admin"

	// RoleUser is the user role.
	RoleUser Role = "user"
)

// audienceContextKey is the key for the audience in the context.
type audienceContextKey struct{}

// tokenContextKey is the key for the pat in the context.
type tokenContextKey struct{}

// roleContextKey is the key for the role in the context.
type roleContextKey struct{}

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

// tokenFromContext extracts the token from the context.
func tokenFromContext(ctx context.Context) (string, error) {
	value := ctx.Value(tokenContextKey{})
	if value == nil {
		return "", status.Error(codes.FailedPrecondition, "token is required")
	}

	token, ok := value.(string)
	if !ok || token == "" {
		return "", status.Error(codes.FailedPrecondition, "token is required")
	}

	return token, nil
}

// roleFromContext extracts the role from the context.
func roleFromContext(ctx context.Context) (string, error) {
	value := ctx.Value(roleContextKey{})
	if value == nil {
		return "", status.Error(codes.FailedPrecondition, "role is required")
	}

	role, ok := value.(string)
	if !ok || role == "" {
		return "", status.Error(codes.FailedPrecondition, "role is required")
	}

	return role, nil
}

// WithAudience sets the audience in the context.
func WithAudience(ctx context.Context, audience string) context.Context {
	return context.WithValue(ctx, audienceContextKey{}, audience)
}

// WithRole sets the role in the context.
func WithRole(ctx context.Context, role string) context.Context {
	return context.WithValue(ctx, roleContextKey{}, role)
}

// WithAuthorizationToken sets the authorization token in the context.
func WithAuthorizationToken(ctx context.Context, token string) context.Context {
	return context.WithValue(ctx, tokenContextKey{}, token)
}

// WithAudienceInMetadata sets the audience in the metadata for incoming requests.
func WithAudienceInMetadata(ctx context.Context, audience string) context.Context {
	return metadata.AppendToOutgoingContext(ctx, audienceMetadataKey, audience)
}

// WithRoleInMetadata sets the role in the metadata for incoming requests.
func WithRoleInMetadata(ctx context.Context, role Role) context.Context {
	return metadata.AppendToOutgoingContext(ctx, roleMetadataKey, string(role))
}

// WithAuthorizationTokenInMetadata sets the authorization token in the metadata for incoming requests.
func WithAuthorizationTokenInMetadata(ctx context.Context, token string) context.Context {
	return metadata.AppendToOutgoingContext(ctx, authorizationMetadataKey, "Bearer "+token)
}

// WithSetAuthorizationTokenInHeaders sets the authorization token in the headers for clients.
func WithSetAuthorizationTokenInHeaders(token string) metadata.MD {
	return metadata.Pairs(authorizationMetadataKey, "Bearer "+token)
}

// ExtractAudienceFromMetadata extracts the audience from the metadata.
func ExtractAudienceFromMetadata(ctx context.Context) (string, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return "", status.Error(codes.NotFound, "metadata is required")
	}

	audience := md.Get(audienceMetadataKey)
	if len(audience) == 0 {
		return "", status.Error(codes.FailedPrecondition, "audience is required")
	}

	return audience[0], nil
}

// ExtractAuthorizationTokenFromMetadata extracts the authorization token from the metadata.
func ExtractAuthorizationTokenFromMetadata(ctx context.Context) (string, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return "", status.Error(codes.NotFound, "metadata is required")
	}

	data := md.Get(authorizationMetadataKey)
	if len(data) == 0 {
		return "", status.Error(codes.FailedPrecondition, "missing authorization token")
	}

	parts := strings.Split(data[0], " ")
	if len(parts) < 2 || parts[0] != "Bearer" {
		return "", status.Error(codes.FailedPrecondition, "missing authorization token")
	}

	return parts[1], nil
}

// ExtractRoleFromMetadata extracts the role from the metadata.
func ExtractRoleFromMetadata(ctx context.Context) (string, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return "", status.Error(codes.NotFound, "metadata is required")
	}

	role := md.Get(roleMetadataKey)
	if len(role) == 0 {
		return "", status.Error(codes.FailedPrecondition, "role is required")
	}

	return role[0], nil
}

// ExtractAuthorizationTokenFromHeaders extracts the authorization token from the headers.
func ExtractAuthorizationTokenFromHeaders(headers metadata.MD) (string, error) {
	data := headers.Get(authorizationMetadataKey)
	if len(data) == 0 {
		return "", status.Error(codes.FailedPrecondition, "missing authorization token")
	}

	parts := strings.Split(data[0], " ")
	if len(parts) < 2 || parts[0] != "Bearer" {
		return "", status.Error(codes.FailedPrecondition, "missing authorization token")
	}

	return parts[1], nil
}

// Auth is responsible for issuing and validating jwt tokens.
type Auth struct {
	issuer     string
	privateKey crypto.PrivateKey
	publicKey  crypto.PublicKey
	tp         trace.Tracer
}

// New creates a new Auth instance.
func New() (*Auth, error) {
	privateKeyBytes, err := os.ReadFile(svcpkg.Info().GetAuthPrivateKeyPath())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to read private key: %v", err)
	}

	privateKey, err := jwt.ParseEdPrivateKeyFromPEM(privateKeyBytes)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to parse private key: %v", err)
	}

	publicKeyBytes, err := os.ReadFile(svcpkg.Info().GetAuthPublicKeyPath())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to read public key: %v", err)
	}

	publicKey, err := jwt.ParseEdPublicKeyFromPEM(publicKeyBytes)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to parse public key: %v", err)
	}

	return &Auth{
		issuer:     svcpkg.Info().GetName(),
		privateKey: privateKey,
		publicKey:  publicKey,
		tp:         otel.Tracer(svcpkg.Info().GetName()),
	}, nil
}

// IssueToken issues a new token with the given subject.
func (a *Auth) IssueToken(ctx context.Context, subject string) (token string, err error) {
	ctx, span := a.tp.Start(ctx, "Auth.IssueToken")
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

	role, err := roleFromContext(ctx)
	if err != nil {
		return "", err
	}

	now := time.Now()
	_token := jwt.NewWithClaims(&jwt.SigningMethodEd25519{}, jwt.MapClaims{
		"aud": audience,
		"nbf": now.Unix(),
		"iat": now.Unix(),
		"exp": now.Add(Expiry).Unix(),
		"iss": a.issuer,
		"sub": subject,

		// role is the role of the audience
		"role": role,
	})

	token, err = _token.SignedString(a.privateKey)
	if err != nil {
		err = status.Errorf(codes.Internal, "failed to sign token: %v", err)
		return "", err
	}

	return token, nil
}

// ValidateToken validates and returns the token.
func (a *Auth) ValidateToken(ctx context.Context) (token *jwt.Token, err error) {
	ctx, span := a.tp.Start(ctx, "Auth.ValidateToken")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	// Extract the token from the context
	tokenString, err := tokenFromContext(ctx)
	if err != nil {
		return nil, err
	}

	token, err = jwt.Parse(
		tokenString,
		func(token *jwt.Token) (any, error) {
			if _, ok := token.Method.(*jwt.SigningMethodEd25519); !ok {
				return nil, status.Error(codes.Unauthenticated, "invalid signing method")
			}

			return a.publicKey, nil
		})
	if err != nil {
		// check if the token is expired
		// if the token is expired, return an error with code DeadlineExceeded.
		if errors.Is(err, jwt.ErrTokenExpired) {
			err = status.Error(codes.DeadlineExceeded, "token is expired")
			return nil, err
		}

		err = status.Errorf(codes.Unauthenticated, "failed to parse token: %v", err)
		return nil, err
	}

	return token, nil
}
