//nolint:testpackage // Tests the internal scheduling contracts.
package jobs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/pkg/idempotency"
	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
)

func TestScheduleInsertErrorClassification(t *testing.T) {
	t.Parallel()

	repo := &Repository{pg: &postgres.Postgres{}}
	cases := []struct {
		name       string
		err        error
		trigger    string
		generation int64
		code       codes.Code
		message    string
	}{
		{"deadline", fmt.Errorf("insert: %w", context.DeadlineExceeded), "MANUAL", 0, codes.DeadlineExceeded, "context deadline exceeded"},
		{"canceled", fmt.Errorf("insert: %w", context.Canceled), "AUTOMATIC", 3, codes.Canceled, "context canceled"},
		{"automatic guard", pgx.ErrNoRows, "AUTOMATIC", 3, codes.FailedPrecondition, "workflow generation mismatch"},
		{"manual guard", pgx.ErrNoRows, "MANUAL", 0, codes.NotFound, "workflow not found"},
		{"unguarded automatic", pgx.ErrNoRows, "AUTOMATIC", 0, codes.Internal, "failed to insert job"},
		{"database failure", errors.New("connection lost"), "MANUAL", 0, codes.Internal, "connection lost"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := repo.mapScheduleInsertError(tc.err, tc.trigger, tc.generation)
			if status.Code(err) != tc.code || !strings.Contains(status.Convert(err).Message(), tc.message) {
				t.Fatalf("classification = %v, want %v containing %q", err, tc.code, tc.message)
			}
		})
	}
}

func TestStoredScheduleCommandIdentity(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name             string
		trigger          string
		storedWorkflow   string
		storedUser       string
		storedTrigger    string
		storedGeneration sql.NullInt64
		code             codes.Code
	}{
		{"manual without generation", "MANUAL", "workflow", "owner", "MANUAL", sql.NullInt64{}, codes.OK},
		{"manual ignores generation", "MANUAL", "workflow", "owner", "MANUAL", sql.NullInt64{Int64: 9, Valid: true}, codes.OK},
		{"different workflow", "MANUAL", "other", "owner", "MANUAL", sql.NullInt64{}, codes.AlreadyExists},
		{"different owner", "MANUAL", "workflow", "other", "MANUAL", sql.NullInt64{}, codes.AlreadyExists},
		{"different trigger", "AUTOMATIC", "workflow", "owner", "MANUAL", sql.NullInt64{}, codes.AlreadyExists},
		{"automatic matches", "AUTOMATIC", "workflow", "owner", "AUTOMATIC", sql.NullInt64{Int64: 3, Valid: true}, codes.OK},
		{"automatic lacks generation", "AUTOMATIC", "workflow", "owner", "AUTOMATIC", sql.NullInt64{}, codes.AlreadyExists},
		{"different generation", "AUTOMATIC", "workflow", "owner", "AUTOMATIC", sql.NullInt64{Int64: 4, Valid: true}, codes.AlreadyExists},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			hash, err := idempotency.HashCanonical(scheduleJobHashFields("workflow", "owner", tc.trigger, 3))
			if err != nil {
				t.Fatal(err)
			}
			err = validateStoredScheduleCommand(hash, tc.storedWorkflow, tc.storedUser, tc.storedTrigger, tc.storedGeneration)
			if status.Code(err) != tc.code {
				t.Fatalf("stored identity = %v, want %v", err, tc.code)
			}
		})
	}
}
