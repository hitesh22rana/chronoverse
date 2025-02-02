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

// Repository provides authentication operations.
type Repository struct {
	tokenIssuer   *pat.Pat
	postgresStore *postgres.Postgres
}

// New creates a new auth repository.
func New(tokenIssuer *pat.Pat, postgresStore *postgres.Postgres) *Repository {
	return &Repository{
		tokenIssuer:   tokenIssuer,
		postgresStore: postgresStore,
	}
}

// Register a new user.
func (r *Repository) Register(ctx context.Context, email, password string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, defaultTimeout)
	defer cancel()

	// Hash password
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", status.Errorf(codes.Internal, "failed to hash password: %v", err)
	}

	// Start transaction
	tx, err := r.postgresStore.BeginTx(ctx)
	if err != nil {
		return "", status.Errorf(codes.Internal, "failed to start transaction: %v", err)
	}
	// rollback if not committed
	defer func() {
		err = tx.Rollback(ctx)
	}()

	// Insert user into database
	query := fmt.Sprintf(`
		INSERT INTO %s (email, password, created_at, updated_at) 
		VALUES ($1, $2, $3, $3)
		RETURNING id`, userTable)

	var ID string
	err = tx.QueryRow(ctx, query, email, string(hashedPassword), time.Now()).Scan(&ID)
	if err != nil {
		// Check if the user already exists
		if r.postgresStore.IsUniqueViolation(err) {
			return "", status.Errorf(codes.AlreadyExists, "user already exists: %v", err)
		}

		return "", status.Errorf(codes.Internal, "failed to insert user: %v", err)
	}

	// Generate PAT
	token, err := r.tokenIssuer.GeneratePat(ctx, ID)
	if err != nil {
		return "", status.Errorf(codes.Internal, "failed to generate token: %v", err)
	}

	// Commit transaction
	if err = tx.Commit(ctx); err != nil {
		return "", status.Errorf(codes.Internal, "failed to commit transaction: %v", err)
	}

	return token, nil
}

// Login user.
func (r *Repository) Login(ctx context.Context, email, pass string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, defaultTimeout)
	defer cancel()

	// Fetch user from database
	var (
		ID       string
		password string
	)
	query := fmt.Sprintf(`SELECT id, password FROM %s WHERE email = $1`, userTable)
	err := r.postgresStore.QueryRow(ctx, query, email).Scan(&ID, &password)
	if err != nil {
		if r.postgresStore.IsNoRows(err) {
			return "", status.Errorf(codes.NotFound, "user not found: %v", err)
		}

		return "", status.Errorf(codes.Internal, "failed to fetch user: %v", err)
	}

	// Validate password
	if err = bcrypt.CompareHashAndPassword([]byte(password), []byte(pass)); err != nil {
		return "", status.Errorf(codes.FailedPrecondition, "invalid password: %v", err)
	}

	// Generate PAT
	token, err := r.tokenIssuer.GeneratePat(ctx, ID)
	if err != nil {
		return "", status.Errorf(codes.Internal, "failed to generate token: %v", err)
	}

	return token, nil
}

// Logout user.
func (r *Repository) Logout(ctx context.Context, token string) error {
	ctx, cancel := context.WithTimeout(ctx, defaultTimeout)
	defer cancel()

	// Revoke the token
	if err := r.tokenIssuer.RevokePat(ctx, token); err != nil {
		return status.Errorf(codes.Internal, "failed to revoke token: %v", err)
	}

	return nil
}

// ValidateToken validates the token.
func (r *Repository) ValidateToken(ctx context.Context, token string) error {
	ctx, cancel := context.WithTimeout(ctx, defaultTimeout)
	defer cancel()

	// Validate the token
	if ok := r.tokenIssuer.IsValidPat(ctx, token); !ok {
		return status.Errorf(codes.Unauthenticated, "invalid token %s", token)
	}

	return nil
}
