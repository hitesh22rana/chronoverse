//nolint:testpackage // Verifies lease normalization and terminal timing contracts.
package jobs

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
)

func TestLeaseSecondsRoundsDownWithPositiveMinimum(t *testing.T) {
	for _, tc := range []struct {
		duration time.Duration
		want     int64
	}{
		{-time.Second, 1}, {0, 1}, {time.Nanosecond, 1}, {999 * time.Millisecond, 1}, {time.Second, 1}, {1999 * time.Millisecond, 1}, {2 * time.Second, 2},
	} {
		if got := leaseSeconds(tc.duration); got != tc.want {
			t.Errorf("leaseSeconds(%v)=%d, want %d", tc.duration, got, tc.want)
		}
	}
}

func TestTerminalExecutionDurationHandlesIncompleteAndReversedTimes(t *testing.T) {
	start := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	for _, tc := range []struct {
		name     string
		snapshot *terminalJobSnapshot
		want     uint64
	}{
		{name: "missing snapshot"},
		{name: "never started", snapshot: &terminalJobSnapshot{CompletedAt: start}},
		{name: "completion precedes start", snapshot: &terminalJobSnapshot{StartedAt: sql.NullTime{Time: start, Valid: true}, CompletedAt: start.Add(-time.Second)}},
		{name: "subsecond duration", snapshot: &terminalJobSnapshot{StartedAt: sql.NullTime{Time: start, Valid: true}, CompletedAt: start.Add(999 * time.Millisecond)}},
		{name: "whole seconds", snapshot: &terminalJobSnapshot{StartedAt: sql.NullTime{Time: start, Valid: true}, CompletedAt: start.Add(2500 * time.Millisecond)}, want: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.snapshot.executionDurationSeconds(); got != tc.want {
				t.Fatalf("duration=%d, want %d", got, tc.want)
			}
		})
	}
}

func TestMapJobLeaseReadErrorContracts(t *testing.T) {
	repo := &Repository{pg: &postgres.Postgres{}}
	for _, tc := range []struct {
		name    string
		err     error
		code    codes.Code
		message string
	}{
		{name: "success"},
		{name: "missing job", err: fmt.Errorf("query: %w", pgx.ErrNoRows), code: codes.NotFound, message: "read lease: job not found"},
		{name: "invalid identifier", err: &pgconn.PgError{Code: "22P02", Message: "invalid UUID"}, code: codes.InvalidArgument},
		{name: "database failure", err: &pgconn.PgError{Code: "08006", Message: "connection failure"}, code: codes.Internal},
		{name: "wrapped cancellation", err: fmt.Errorf("query: %w", context.Canceled), code: codes.Canceled},
		{name: "wrapped deadline", err: fmt.Errorf("query: %w", context.DeadlineExceeded), code: codes.DeadlineExceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := repo.mapJobLeaseReadError(tc.err, "read lease")
			if status.Code(err) != tc.code {
				t.Fatalf("error=%v, want %v", err, tc.code)
			}
			if tc.message != "" && status.Convert(err).Message() != tc.message {
				t.Fatalf("message=%q, want %q", status.Convert(err).Message(), tc.message)
			}
		})
	}
}

// The command helper accepts arbitrary fields; unsupported JSON values must be
// rejected before it attempts to reserve a database command.
func TestReserveJobCommandRejectsUnsupportedHashFieldsBeforeDatabase(t *testing.T) {
	hash, replay, err := reserveJobCommand(t.Context(), nil, "job", "complete", "command", map[string]any{"unsupported": make(chan int)})
	if hash != "" || replay || status.Code(err) != codes.Internal {
		t.Fatalf("hash=%q replay=%v error=%v", hash, replay, err)
	}
}
