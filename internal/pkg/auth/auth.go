//go:generate mockgen -source=$GOFILE -package=$GOPACKAGE -destination=./mock/$GOFILE

package auth

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ed25519"
	"errors"
	"os"
	"slices"
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
	Expiry = time.Minute * 15 // 15 minutes

	// clockSkewLeeway tolerates the small clock differences that are normal
	// between Kubernetes nodes. Without it, a token issued on a node whose
	// clock is slightly ahead can fail its nbf check on another node.
	clockSkewLeeway   = 30 * time.Second
	jwtExpiryClaim    = "exp"
	jwtNotBeforeClaim = "nbf"
	jwtIssuerClaim    = "iss"
	jwtAudienceClaim  = "aud"
	jwtSubjectClaim   = "sub"
	jwtRoleClaim      = "role"

	// authorizationMetadataKey is the key for the token in the metadata.
	authorizationMetadataKey = "Authorization"
)

// ServiceName values identify JWT issuers and audiences.
const (
	ServiceNameServer        = "server"
	ServiceNameUsers         = "users-service"
	ServiceNameWorkflows     = "workflows-service"
	ServiceNameJobs          = "jobs-service"
	ServiceNameNotifications = "notifications-service"
	ServiceNameAnalytics     = "analytics-service"
)

// Role is the role of the audience.
type Role string

const (
	// RoleAdmin is the admin role.
	RoleAdmin Role = "admin"

	// RoleUser is the user role.
	RoleUser Role = "user"
)

func (r Role) String() string {
	return string(r)
}

// audienceContextKey is the key for the audience in the context.
type audienceContextKey struct{}

// tokenContextKey is the key for the pat in the context.
type tokenContextKey struct{}

// roleContextKey is the key for the role in the context.
type roleContextKey struct{}

// subjectContextKey is the key for the subject in the context.
type subjectContextKey struct{}

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

// subjectFromContext extracts the subject from the context.
func subjectFromContext(ctx context.Context) (string, error) {
	value := ctx.Value(subjectContextKey{})
	if value == nil {
		return "", status.Error(codes.FailedPrecondition, "subject is required")
	}

	subject, ok := value.(string)
	if !ok || subject == "" {
		return "", status.Error(codes.FailedPrecondition, "subject is required")
	}

	return subject, nil
}

// WithAudience sets the audience in the context.
func WithAudience(ctx context.Context, audience string) context.Context {
	return context.WithValue(ctx, audienceContextKey{}, audience)
}

// WithRole sets the role in the context.
func WithRole(ctx context.Context, role string) context.Context {
	return context.WithValue(ctx, roleContextKey{}, role)
}

// WithSubject sets the subject in the context.
func WithSubject(ctx context.Context, subject string) context.Context {
	return context.WithValue(ctx, subjectContextKey{}, subject)
}

// WithAuthorizationToken sets the authorization token in the context.
func WithAuthorizationToken(ctx context.Context, token string) context.Context {
	return context.WithValue(ctx, tokenContextKey{}, token)
}

// WithAuthorizationTokenInMetadata sets the authorization token in the metadata for outgoing requests.
func WithAuthorizationTokenInMetadata(ctx context.Context, token string) context.Context {
	md, ok := metadata.FromOutgoingContext(ctx)
	if ok {
		md = md.Copy()
		md.Delete(authorizationMetadataKey)
		ctx = metadata.NewOutgoingContext(ctx, md)
	}
	return metadata.AppendToOutgoingContext(ctx, authorizationMetadataKey, "Bearer "+token)
}

// WithInternalServiceAuthorization issues an admin token for the caller service and
// attaches it as the bearer header. The audiences list MUST contain every service
// the receiver chain is expected to authorize against; the receiver's
// ValidateToken validates that its own service name is in the list.
func WithInternalServiceAuthorization(ctx context.Context, issuer IAuth, subject string, audiences ...string) (context.Context, error) {
	if len(audiences) == 0 {
		return nil, status.Error(codes.InvalidArgument, "receiver audience is required")
	}

	ctx = WithRole(ctx, RoleAdmin.String())

	token, err := issuer.IssueToken(ctx, subject, audiences...)
	if err != nil {
		return nil, err
	}

	ctx = WithAuthorizationTokenInMetadata(ctx, token)

	return ctx, nil
}

// WithSetAuthorizationTokenInHeaders sets the authorization token in the headers for clients.
func WithSetAuthorizationTokenInHeaders(token string) metadata.MD {
	return metadata.Pairs(authorizationMetadataKey, "Bearer "+token)
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

// ExtractRoleFromContext extracts the role placed in the context by ValidateToken.
// Server-side authorization MUST use this helper, never the metadata header.
func ExtractRoleFromContext(ctx context.Context) (string, error) {
	return roleFromContext(ctx)
}

// ExtractAudienceFromContext extracts the audience placed in the context by
// ValidateToken (the only safe source for authenticated RPCs).
func ExtractAudienceFromContext(ctx context.Context) (string, error) {
	return audienceFromContext(ctx)
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

// IsInternalService checks if the request is from an internal service by
// reading the role from the JWT-validated context.
func IsInternalService(ctx context.Context) bool {
	role, err := roleFromContext(ctx)
	return err == nil && role == RoleAdmin.String()
}

// IAuth is the interface for the Auth service.
type IAuth interface {
	IssueToken(ctx context.Context, subject string, audiences ...string) (token string, err error)
	ValidateToken(ctx context.Context, expectedAudience string) (ctx2 context.Context, token *jwt.Token, err error)
}

// Auth is responsible for issuing and validating jwt tokens.
type Auth struct {
	issuer     string
	privateKey crypto.PrivateKey
	publicKey  crypto.PublicKey
	kid        string
	bundle     map[string]*bundleEntry
	bundlePath string
	tp         trace.Tracer
}

// New creates a new Auth instance.
//
//nolint:gocyclo // key load + bundle resolution + kid selection is linear, split would add indirection
func New() (*Auth, error) {
	issuer := svcpkg.Info().GetName()
	privateKeyPath := svcpkg.Info().GetAuthPrivateKeyPath()
	publicKeyPath := svcpkg.Info().GetAuthPublicKeyPath()

	privateKeyBytes, err := os.ReadFile(privateKeyPath)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to read private key: %v", err)
	}

	privateKey, err := jwt.ParseEdPrivateKeyFromPEM(privateKeyBytes)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to parse private key: %v", err)
	}

	publicKeyBytes, err := os.ReadFile(publicKeyPath)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to read public key: %v", err)
	}

	publicKey, err := jwt.ParseEdPublicKeyFromPEM(publicKeyBytes)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to parse public key: %v", err)
	}

	edPub, ok := publicKey.(ed25519.PublicKey)
	if !ok {
		return nil, status.Errorf(codes.Internal, "public key is not ed25519")
	}
	// Verify private and public correspond — prevents signer restart with
	// mismatched pair (e.g. rotation mv of auth.ed succeeded but auth.ed.pub
	// failed). Without this, IssueToken would sign with new private while kid
	// (derived from old public) selects old key for verifiers.
	if edPriv, ok := privateKey.(ed25519.PrivateKey); ok {
		if derivedPub, ok := edPriv.Public().(ed25519.PublicKey); ok {
			if !bytes.Equal(derivedPub, edPub) {
				return nil, status.Errorf(codes.Internal, "private and public key mismatch")
			}
		}
	}
	kid := kidForKey(issuer, edPub)

	bundle, bundlePath, bundleErr := findAndLoadBundle(publicKeyPath)
	if bundleErr != nil {
		return nil, status.Errorf(codes.Internal, "failed to load trusted bundle %s: %v", bundlePath, bundleErr)
	}
	for candKid, entry := range bundle {
		if entry.Iss != issuer {
			continue
		}
		if candPub, ok := entry.PublicKey.(ed25519.PublicKey); ok && len(candPub) == len(edPub) {
			match := true
			for i := range candPub {
				if candPub[i] != edPub[i] {
					match = false
					break
				}
			}
			if match {
				kid = candKid
				break
			}
		}
	}

	return &Auth{
		issuer:     issuer,
		privateKey: privateKey,
		publicKey:  publicKey,
		kid:        kid,
		bundle:     bundle,
		bundlePath: bundlePath,
		tp:         otel.Tracer(svcpkg.Info().GetName()),
	}, nil
}

// IssueToken issues a new token with the given subject. audiences controls
// the JWT "aud" claim and must name at least one receiver. The role placed in
// context (see WithRole) is stamped into the signed "role" claim.
func (a *Auth) IssueToken(ctx context.Context, subject string, audiences ...string) (token string, err error) {
	ctx, span := a.tp.Start(ctx, "Auth.IssueToken")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	if len(audiences) == 0 {
		return "", status.Error(codes.InvalidArgument, "receiver audience is required")
	}

	role, err := roleFromContext(ctx)
	if err != nil {
		return "", err
	}
	now := time.Now()
	_token := jwt.NewWithClaims(&jwt.SigningMethodEd25519{}, jwt.MapClaims{
		jwtAudienceClaim:  audiences,
		jwtNotBeforeClaim: now.Unix(),
		"iat":             now.Unix(),
		jwtExpiryClaim:    now.Add(Expiry).Unix(),
		jwtIssuerClaim:    a.issuer,
		jwtSubjectClaim:   subject,
		jwtRoleClaim:      role,
	})
	if a.kid != "" {
		_token.Header["kid"] = a.kid
		// Ensure alg is EdDSA per RFC; jwt library sets it via SigningMethod
		_token.Header["alg"] = jwt.SigningMethodEdDSA.Alg()
	}

	signed, err := _token.SignedString(a.privateKey)
	if err != nil {
		return "", status.Errorf(codes.Internal, "failed to sign token: %v", err)
	}

	return signed, nil
}

func isOnlyTokenExpired(err error) bool {
	if !errors.Is(err, jwt.ErrTokenExpired) {
		return false
	}

	var onlyExpiration func(error) bool
	onlyExpiration = func(current error) bool {
		//nolint:errorlint // Leaf identity is required; errors.Is would hide sibling validation failures.
		if current == jwt.ErrTokenExpired || current == jwt.ErrTokenInvalidClaims {
			return true
		}
		if joined, ok := current.(interface{ Unwrap() []error }); ok {
			errs := joined.Unwrap()
			if len(errs) == 0 {
				return false
			}
			for _, inner := range errs {
				if !onlyExpiration(inner) {
					return false
				}
			}
			return true
		}
		if inner := errors.Unwrap(current); inner != nil {
			return onlyExpiration(inner)
		}
		return false
	}

	return onlyExpiration(err)
}

// ValidateToken validates the token carried in context, enforces the issuer
// and audience claims, and writes the validated role/audience/subject into
// the returned context for downstream authorization. expectedAudience MUST
// match the receiver's own service name — typically svcpkg.Info().GetName().
//
// The audience claim may be a string or a string slice; either form is
// accepted as long as expectedAudience appears in the list.
//
//nolint:gocyclo // ValidateToken handles kid-bound bundle lookup and claim validation in one linear flow.
func (a *Auth) ValidateToken(ctx context.Context, expectedAudience string) (outCtx context.Context, token *jwt.Token, err error) {
	ctx, span := a.tp.Start(ctx, "Auth.ValidateToken")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	if expectedAudience == "" {
		return ctx, nil, status.Error(codes.InvalidArgument, "expected audience is required")
	}

	tokenString, err := tokenFromContext(ctx)
	if err != nil {
		return ctx, nil, err
	}

	var kidFromHeader string
	parsed, perr := jwt.Parse(
		tokenString,
		func(token *jwt.Token) (any, error) {
			if _, ok := token.Method.(*jwt.SigningMethodEd25519); !ok {
				return nil, status.Error(codes.Unauthenticated, "invalid signing method")
			}
			if a.bundle != nil {
				if khStr, ok := token.Header["kid"].(string); ok {
					kidFromHeader = khStr
				}
				if kidFromHeader == "" {
					return nil, status.Error(codes.Unauthenticated, "missing kid")
				}
				entry, ok := a.bundle[kidFromHeader]
				if !ok {
					return nil, status.Error(codes.Unauthenticated, "untrusted kid")
				}
				return entry.PublicKey, nil
			}
			return a.publicKey, nil
		},
		jwt.WithLeeway(clockSkewLeeway),
		jwt.WithAudience(expectedAudience),
		jwt.WithExpirationRequired(),
	)
	expired := isOnlyTokenExpired(perr)
	if perr != nil && !expired {
		return ctx, nil, status.Errorf(codes.Unauthenticated, "failed to parse token: %v", perr)
	}
	if parsed == nil {
		return ctx, nil, status.Error(codes.Unauthenticated, "invalid token")
	}
	claims, ok := parsed.Claims.(jwt.MapClaims)
	if !ok {
		return ctx, nil, status.Error(codes.Unauthenticated, "invalid token claims")
	}
	issuer, issuerErr := claims.GetIssuer()
	if issuerErr != nil || !TrustedIssuer(issuer) {
		return ctx, nil, status.Error(codes.Unauthenticated, "token has untrusted issuer")
	}
	if kidFromHeader != "" {
		if entry, found := a.bundle[kidFromHeader]; found && entry.Iss != issuer {
			return ctx, nil, status.Error(codes.Unauthenticated, "kid issuer mismatch")
		}
	}
	roleVal, ok := claims[jwtRoleClaim].(string)
	if !ok || roleVal == "" {
		return ctx, nil, status.Error(codes.Unauthenticated, "token missing role claim")
	}
	subVal, ok := claims[jwtSubjectClaim].(string)
	if !ok || subVal == "" {
		return ctx, nil, status.Error(codes.Unauthenticated, "token missing subject claim")
	}
	if expired {
		return ctx, nil, status.Error(codes.DeadlineExceeded, "token is expired")
	}
	outCtx = WithAudience(ctx, expectedAudience)
	outCtx = WithRole(outCtx, roleVal)
	outCtx = WithSubject(outCtx, subVal)
	return outCtx, parsed, nil
}

// Keep trustedIssuers in sync with cmd/<svc>/main.go build ldflags.
var trustedIssuers = []string{
	ServiceNameServer,
	ServiceNameUsers,
	ServiceNameWorkflows,
	ServiceNameJobs,
	ServiceNameNotifications,
	ServiceNameAnalytics,
	"scheduling-worker",
	"workflow-worker",
	"execution-worker",
	"runtime-agent",
	"joblogs-processor",
	"analytics-processor",
	"outbox-relay",
}

// GatewayAudiences returns the audience set stamped into tokens the
// gateway (server) mints or forwards. Every service the gateway may
// forward a call to is included so the receiver can ValidateToken with
// its own service name and the JWT remains valid. Adding a new gRPC
// service requires updating trustedIssuers and GatewayAudiences.
func GatewayAudiences() []string {
	return []string{
		ServiceNameServer,
		ServiceNameUsers,
		ServiceNameWorkflows,
		ServiceNameJobs,
		ServiceNameNotifications,
		ServiceNameAnalytics,
	}
}

// TrustedIssuer reports whether iss is a known platform service identity.
func TrustedIssuer(iss string) bool {
	return slices.Contains(trustedIssuers, iss)
}
