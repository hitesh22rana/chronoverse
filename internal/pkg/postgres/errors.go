package postgres

import (
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// IsUniqueViolation checks if the error is a unique violation error.
func (db *Postgres) IsUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}

	return pgErr.Code == "23505"
}

// IsNoRows checks if the error is a no rows error.
func (db *Postgres) IsNoRows(err error) bool {
	return errors.Is(err, pgx.ErrNoRows)
}
