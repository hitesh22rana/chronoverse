package users

import (
	"context"
	"errors"
	"fmt"

	"go.opentelemetry.io/otel"
	otelcodes "go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/crypto/bcrypt"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/jackc/pgx/v5"

	usersmodel "github.com/hitesh22rana/chronoverse/internal/model/users"
	"github.com/hitesh22rana/chronoverse/internal/pkg/auth"
	"github.com/hitesh22rana/chronoverse/internal/pkg/commandidempotency"
	"github.com/hitesh22rana/chronoverse/internal/pkg/idempotency"
	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
	svcpkg "github.com/hitesh22rana/chronoverse/internal/pkg/svc"
)

// #nosec G101 -- This public bcrypt hash is only used to equalize login timing.
const dummyPasswordHash = "$2a$10$jKOlozu2Vfz.vNPHnOvOKuKh5B2gNH4aLNs4SGYZy5KoWvjxOstCq"

// Repository provides users repository.
type Repository struct {
	tp   trace.Tracer
	auth auth.IAuth
	pg   *postgres.Postgres
}

// New creates a new auth repository.
func New(auth auth.IAuth, pg *postgres.Postgres) *Repository {
	return &Repository{
		tp:   otel.Tracer(svcpkg.Info().GetName()),
		auth: auth,
		pg:   pg,
	}
}

// RegisterUser a new user.
//
//nolint:gocritic,gocyclo,nestif // Registration keeps hashing, replay verification, insertion, and commit ordering explicit.
func (r *Repository) RegisterUser(ctx context.Context, email, password, idempotencyKey string) (res *usersmodel.GetUserResponse, authToken string, err error) {
	ctx, span := r.tp.Start(ctx, "Repository.RegisterUser")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	// Hash password
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		if errors.Is(err, bcrypt.ErrPasswordTooLong) {
			err = status.Errorf(codes.InvalidArgument, "password too long: %v", err)
			return nil, "", err
		}

		err = status.Errorf(codes.Internal, "failed to hash password: %v", err)
		return nil, "", err
	}

	tx, err := r.pg.BeginTx(ctx)
	if err != nil {
		return nil, "", status.Errorf(codes.Internal, "failed to start registration transaction: %v", err)
	}
	//nolint:errcheck // Rollback is a no-op after commit.
	defer tx.Rollback(ctx)

	requestHash, err := idempotency.HashCanonical(map[string]string{"email": email})
	if err != nil {
		return nil, "", status.Errorf(codes.Internal, "failed to hash registration request: %v", err)
	}
	reservation, err := commandidempotency.Reserve(
		ctx,
		tx,
		"public",
		commandidempotency.OperationUserRegister,
		idempotencyKey,
		requestHash,
	)
	if err != nil {
		return nil, "", err
	}
	if reservation.Replay {
		var storedPassword string
		query := fmt.Sprintf(`
			SELECT id, email, password, notification_preference, created_at, updated_at
			FROM %s
			WHERE id = $1
			LIMIT 1;
		`, postgres.TableUsers)
		res = &usersmodel.GetUserResponse{}
		if err = tx.QueryRow(ctx, query, reservation.ResourceID).Scan(
			&res.ID,
			&res.Email,
			&storedPassword,
			&res.NotificationPreference,
			&res.CreatedAt,
			&res.UpdatedAt,
		); err != nil {
			return nil, "", status.Errorf(codes.Internal, "failed to load registration replay: %v", err)
		}
		if err = bcrypt.CompareHashAndPassword([]byte(storedPassword), []byte(password)); err != nil {
			if errors.Is(err, bcrypt.ErrMismatchedHashAndPassword) {
				return nil, "", status.Error(codes.AlreadyExists, "idempotency key was used with different credentials")
			}
			return nil, "", status.Errorf(codes.Internal, "failed to verify registration replay: %v", err)
		}
		if err = tx.Commit(ctx); err != nil {
			return nil, "", status.Errorf(codes.Internal, "failed to commit registration replay: %v", err)
		}
		authToken, err = r.auth.IssueToken(ctx, res.ID)
		return res, authToken, err
	}

	// Insert user into database.
	query := fmt.Sprintf(`
		INSERT INTO %s (email, password) 
		VALUES ($1, $2)
		RETURNING id, email, notification_preference, created_at, updated_at;
	`, postgres.TableUsers)
	args := []any{email, string(hashedPassword)}

	rows, err := tx.Query(ctx, query, args...)
	if errors.Is(err, context.DeadlineExceeded) {
		err = status.Error(codes.DeadlineExceeded, err.Error())
		return nil, "", err
	} else if errors.Is(err, context.Canceled) {
		err = status.Error(codes.Canceled, err.Error())
		return nil, "", err
	}

	res, err = pgx.CollectExactlyOneRow(rows, pgx.RowToAddrOfStructByName[usersmodel.GetUserResponse])
	if err != nil {
		// Check if the user already exists
		if r.pg.IsUniqueViolation(err) {
			err = status.Errorf(codes.AlreadyExists, "user already exists: %v", err)
			return nil, "", err
		} else if r.pg.IsNoRows(err) {
			err = status.Errorf(codes.NotFound, "user not found: %v", err)
			return nil, "", err
		} else if r.pg.IsInvalidTextRepresentation(err) {
			err = status.Errorf(codes.InvalidArgument, "invalid user ID: %v", err)
			return nil, "", err
		}

		err = status.Errorf(codes.Internal, "failed to fetch user: %v", err)
		return nil, "", err
	}

	if err = commandidempotency.Complete(
		ctx,
		tx,
		"public",
		commandidempotency.OperationUserRegister,
		idempotencyKey,
		requestHash,
		res.ID,
		res,
		false,
	); err != nil {
		return nil, "", err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, "", status.Errorf(codes.Internal, "failed to commit registration transaction: %v", err)
	}

	// Authentication material is issued only after the account and ledger commit.
	authToken, err = r.auth.IssueToken(ctx, res.ID)
	if err != nil {
		return nil, "", err
	}

	return res, authToken, nil
}

// LoginUser user.
func (r *Repository) LoginUser(ctx context.Context, email, pass string) (res *usersmodel.GetUserResponse, authToken string, err error) {
	ctx, span := r.tp.Start(ctx, "Repository.LoginUser")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	// Fetch user from database
	query := fmt.Sprintf(`
		SELECT id, email, password, notification_preference, created_at, updated_at
		FROM %s WHERE email = $1
		LIMIT 1;
	`, postgres.TableUsers)
	args := []any{email}

	rows, err := r.pg.Query(ctx, query, args...)
	if errors.Is(err, context.DeadlineExceeded) {
		err = status.Error(codes.DeadlineExceeded, err.Error())
		return nil, "", err
	} else if errors.Is(err, context.Canceled) {
		err = status.Error(codes.Canceled, err.Error())
		return nil, "", err
	}

	loginUserResponse, err := pgx.CollectExactlyOneRow(rows, pgx.RowToAddrOfStructByName[usersmodel.LoginUserData])
	if err != nil {
		if r.pg.IsNoRows(err) {
			// Keep missing-user attempts on the same expensive path as invalid passwords.
			compareErr := bcrypt.CompareHashAndPassword([]byte(dummyPasswordHash), []byte(pass))
			if compareErr != nil && !errors.Is(compareErr, bcrypt.ErrMismatchedHashAndPassword) {
				return nil, "", status.Errorf(codes.Internal, "failed to verify password: %v", compareErr)
			}
			err = invalidCredentialsError()
			return nil, "", err
		} else if r.pg.IsInvalidTextRepresentation(err) {
			err = status.Errorf(codes.InvalidArgument, "invalid user ID: %v", err)
			return nil, "", err
		}

		err = status.Errorf(codes.Internal, "failed to fetch user: %v", err)
		return nil, "", err
	}

	// Validate password
	if err = bcrypt.CompareHashAndPassword([]byte(loginUserResponse.Password), []byte(pass)); err != nil {
		if errors.Is(err, bcrypt.ErrMismatchedHashAndPassword) {
			return nil, "", invalidCredentialsError()
		}

		err = status.Errorf(codes.Internal, "failed to verify password: %v", err)
		return nil, "", err
	}

	// Issue authToken
	authToken, err = r.auth.IssueToken(ctx, loginUserResponse.ID)
	if err != nil {
		return nil, "", err
	}

	res = &usersmodel.GetUserResponse{
		ID:                     loginUserResponse.ID,
		Email:                  loginUserResponse.Email,
		NotificationPreference: loginUserResponse.NotificationPreference,
		CreatedAt:              loginUserResponse.CreatedAt,
		UpdatedAt:              loginUserResponse.UpdatedAt,
	}

	return res, authToken, nil
}

func invalidCredentialsError() error {
	return status.Error(codes.Unauthenticated, "invalid email or password")
}

// GetUser fetches user by ID.
func (r *Repository) GetUser(ctx context.Context, id string) (res *usersmodel.GetUserResponse, err error) {
	ctx, span := r.tp.Start(ctx, "Repository.GetUser")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	query := fmt.Sprintf(`
		SELECT id, email, notification_preference, created_at, updated_at
		FROM %s WHERE id = $1
		LIMIT 1;
	`, postgres.TableUsers)
	args := []any{id}

	rows, err := r.pg.Query(ctx, query, args...)
	if errors.Is(err, context.DeadlineExceeded) {
		err = status.Error(codes.DeadlineExceeded, err.Error())
		return nil, err
	} else if errors.Is(err, context.Canceled) {
		err = status.Error(codes.Canceled, err.Error())
		return nil, err
	}

	res, err = pgx.CollectExactlyOneRow(rows, pgx.RowToAddrOfStructByName[usersmodel.GetUserResponse])
	if err != nil {
		if r.pg.IsNoRows(err) {
			err = status.Errorf(codes.NotFound, "user not found: %v", err)
			return nil, err
		} else if r.pg.IsInvalidTextRepresentation(err) {
			err = status.Errorf(codes.InvalidArgument, "invalid user ID: %v", err)
			return nil, err
		}

		err = status.Errorf(codes.Internal, "failed to fetch user: %v", err)
		return nil, err
	}

	return res, nil
}

// UpdateUser updates the user.
func (r *Repository) UpdateUser(ctx context.Context, id, notificationPreference string) (err error) {
	ctx, span := r.tp.Start(ctx, "Repository.UpdateUser")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	query := fmt.Sprintf(`
		UPDATE %s
		SET notification_preference = $1
		WHERE id = $2 AND notification_preference IS DISTINCT FROM $1;
	`, postgres.TableUsers)
	args := []any{notificationPreference, id}

	// Execute the query
	ct, err := r.pg.Exec(ctx, query, args...)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			err = status.Error(codes.DeadlineExceeded, err.Error())
			return err
		} else if errors.Is(err, context.Canceled) {
			err = status.Error(codes.Canceled, err.Error())
			return err
		}

		if r.pg.IsNoRows(err) {
			err = status.Errorf(codes.NotFound, "user not found: %v", err)
			return err
		} else if r.pg.IsInvalidTextRepresentation(err) {
			err = status.Errorf(codes.InvalidArgument, "invalid user ID: %v", err)
			return err
		}

		err = status.Errorf(codes.Internal, "failed to update user: %v", err)
		return err
	}

	if ct.RowsAffected() == 0 {
		var exists bool
		if lookupErr := r.pg.QueryRow(ctx, fmt.Sprintf(`SELECT EXISTS (SELECT 1 FROM %s WHERE id = $1)`, postgres.TableUsers), id).Scan(&exists); lookupErr != nil {
			return status.Errorf(codes.Internal, "failed to check user preference state: %v", lookupErr)
		}
		if !exists {
			return status.Error(codes.NotFound, "user not found")
		}
	}

	return nil
}
