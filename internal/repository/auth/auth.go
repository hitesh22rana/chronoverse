package auth

import (
	"context"
	"fmt"
	"time"

	"golang.org/x/crypto/bcrypt"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/pkg/pat"
	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
)

const (
	userTable      = "users"
	defaultTimeout = 5 * time.Second
)

// AuthRepository provides authentication operations
type AuthRepository struct {
	tokenIssuer   *pat.Pat
	postgresStore *postgres.Postgres
}

// New creates a new auth repository
func New(tokenIssuer *pat.Pat, postgresStore *postgres.Postgres) *AuthRepository {
	return &AuthRepository{
		tokenIssuer:   tokenIssuer,
		postgresStore: postgresStore,
	}
}

// Register a new user
func (a *AuthRepository) Register(ctx context.Context, email string, password string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, defaultTimeout)
	defer cancel()

	// Hash password
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", status.Errorf(codes.Internal, "failed to hash password: %v", err)
	}

	// Start transaction
	tx, err := a.postgresStore.BeginTx(ctx)
	if err != nil {
		return "", status.Errorf(codes.Internal, "failed to start transaction: %v", err)
	}
	// rollback if not committed
	defer tx.Rollback(ctx)

	// Insert user into database
	query := fmt.Sprintf(`
		INSERT INTO %s (email, password, created_at, updated_at) 
		VALUES ($1, $2, $3, $3)
		RETURNING id`, userTable)

	var ID string
	err = tx.QueryRow(ctx, query, email, string(hashedPassword), time.Now()).Scan(&ID)

	if err != nil {
		// Check if the user already exists
		if a.postgresStore.IsUniqueViolation(err) {
			return "", status.Errorf(codes.AlreadyExists, "user already exists: %v", err)
		}

		return "", status.Errorf(codes.Internal, "failed to insert user: %v", err)
	}

	// Generate PAT
	token, err := a.tokenIssuer.GeneratePat(ctx, ID)
	if err != nil {
		return "", status.Errorf(codes.Internal, "failed to generate token: %v", err)
	}

	// Commit transaction
	if err = tx.Commit(ctx); err != nil {
		return "", status.Errorf(codes.Internal, "failed to commit transaction: %v", err)
	}

	return token, nil
}

// Login user
func (a *AuthRepository) Login(ctx context.Context, email string, pass string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, defaultTimeout)
	defer cancel()

	// Fetch user from database
	var (
		ID       string
		password string
	)
	query := fmt.Sprintf(`SELECT id, password FROM %s WHERE email = $1`, userTable)
	err := a.postgresStore.QueryRow(ctx, query, email).Scan(&ID, &password)
	if err != nil {
		if a.postgresStore.IsNoRows(err) {
			return "", status.Errorf(codes.NotFound, "user not found: %v", err)
		}

		return "", status.Errorf(codes.Internal, "failed to fetch user: %v", err)
	}

	// Validate password
	if err := bcrypt.CompareHashAndPassword([]byte(password), []byte(pass)); err != nil {
		return "", status.Errorf(codes.FailedPrecondition, "invalid password: %v", err)
	}

	// Generate PAT
	token, err := a.tokenIssuer.GeneratePat(ctx, ID)
	if err != nil {
		return "", status.Errorf(codes.Internal, "failed to generate token: %v", err)
	}

	return token, nil
}

// Logout user
func (a *AuthRepository) Logout(ctx context.Context, token string) error {
	ctx, cancel := context.WithTimeout(ctx, defaultTimeout)
	defer cancel()

	// Revoke the token
	if err := a.tokenIssuer.RevokePat(ctx, token); err != nil {
		return status.Errorf(codes.Internal, "failed to revoke token: %v", err)
	}

	return nil
}

// ValidateToken validates the token
func (a *AuthRepository) ValidateToken(ctx context.Context, token string) error {
	ctx, cancel := context.WithTimeout(ctx, defaultTimeout)
	defer cancel()

	// Validate the token
	if _, err := a.tokenIssuer.ValidatePat(ctx, token); err != nil {
		return status.Errorf(codes.Unauthenticated, "invalid token: %v", err)
	}

	return nil
}
