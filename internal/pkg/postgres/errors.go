package postgres

import (
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const (
	sqlStateUniqueViolation           = "23505"
	sqlStateInvalidTextRepresentation = "22P02"
)

// IsUniqueViolation reports whether err is a PostgreSQL unique violation.
func (db *Postgres) IsUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}

	return pgErr.Code == sqlStateUniqueViolation
}

// IsNoRows reports whether err means the query returned no rows.
func (db *Postgres) IsNoRows(err error) bool {
	return errors.Is(err, pgx.ErrNoRows)
}

// IsInvalidTextRepresentation reports whether err is a PostgreSQL invalid-text-representation error.
func (db *Postgres) IsInvalidTextRepresentation(err error) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}

	return pgErr.Code == sqlStateInvalidTextRepresentation
}
