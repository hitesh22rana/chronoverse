package databasemigration

import (
	"context"
	"errors"
	"strings"
	"time"

	_ "github.com/golang-migrate/migrate/v4/database/clickhouse"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/meilisearch/meilisearch-go"
	"go.opentelemetry.io/otel"
	otelcodes "go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	clickhousepkg "github.com/hitesh22rana/chronoverse/internal/pkg/clickhouse"
	meilisearchpkg "github.com/hitesh22rana/chronoverse/internal/pkg/meilisearch"
	postgrespkg "github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
	svcpkg "github.com/hitesh22rana/chronoverse/internal/pkg/svc"
)

const (
	meiliSearchTaskTimeout     = 1 * time.Minute
	meiliSearchPollingDuration = 2 * time.Second
)

// Config holds the database migration configuration.
type Config struct {
	PostgresDSN       string
	ClickHouseDSN     string
	MeiliSearchClient meilisearch.ServiceManager
}

// Repository provides database migration repository.
type Repository struct {
	tp  trace.Tracer
	cfg *Config
}

// New creates a new database migration repository.
func New(cfg *Config) *Repository {
	return &Repository{
		tp:  otel.Tracer(svcpkg.Info().GetName()),
		cfg: cfg,
	}
}

// MigratePostgres migrates the PostgreSQL database.
//
//nolint:dupl // This function is similar to MigrateClickHouse but for PostgreSQL.
func (r *Repository) MigratePostgres(ctx context.Context) (err error) {
	_, span := r.tp.Start(ctx, "Repository.MigratePostgres")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	// IOFS source instance for embedded migrations
	sourceInstance, _err := iofs.New(postgrespkg.MigrationsFS, "migrations")
	if _err != nil {
		err = status.Errorf(codes.Internal, "failed to create IOFS source instance: %v", _err)
		return err
	}

	// Migrate instance for postgres
	m, _err := migrate.NewWithSourceInstance("iofs", sourceInstance, r.cfg.PostgresDSN)
	if _err != nil {
		err = status.Errorf(codes.Internal, "failed to create migrate instance: %v", _err)
		return err
	}

	// Execute migration
	if _err := m.Up(); _err != nil && !errors.Is(_err, migrate.ErrNoChange) {
		err = status.Errorf(codes.Internal, "failed to run postgres migration: %v", _err)
		return err
	}

	return nil
}

// MigrateClickHouse migrates the ClickHouse database.
//
//nolint:dupl // This function is similar to MigratePostgres but for ClickHouse.
func (r *Repository) MigrateClickHouse(ctx context.Context) (err error) {
	_, span := r.tp.Start(ctx, "Repository.MigrateClickHouse")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	// IOFS source instance for embedded migrations
	sourceInstance, _err := iofs.New(clickhousepkg.MigrationsFS, "migrations")
	if _err != nil {
		err = status.Errorf(codes.Internal, "failed to create IOFS source instance: %v", _err)
		return err
	}

	// Migrate instance for clickhouse
	m, _err := migrate.NewWithSourceInstance("iofs", sourceInstance, r.cfg.ClickHouseDSN)
	if _err != nil {
		err = status.Errorf(codes.Internal, "failed to create migrate instance: %v", _err)
		return err
	}

	// Execute migration
	if _err := m.Up(); _err != nil && !errors.Is(_err, migrate.ErrNoChange) {
		err = status.Errorf(codes.Internal, "failed to run clickhouse migration: %v", _err)
		return err
	}

	return nil
}

// SetupMeiliSearch setups the MeiliSearch database.
func (r *Repository) SetupMeiliSearch(ctx context.Context) (err error) {
	_, span := r.tp.Start(ctx, "Repository.SetupMeiliSearch")
	defer func() {
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
	}()

	createIndexTaskIds := make([]int64, 0, len(meilisearchpkg.Indexes))
	for _, index := range meilisearchpkg.Indexes {
		createIndexTask, _err := r.cfg.MeiliSearchClient.CreateIndexWithContext(
			ctx,
			&meilisearch.IndexConfig{
				Uid:        index.Name,
				PrimaryKey: index.PrimaryKey,
			},
		)
		if _err != nil {
			err = status.Errorf(codes.Internal, "failed to create meilisearch index %v", _err)
			return err
		}

		createIndexTaskIds = append(createIndexTaskIds, createIndexTask.TaskUID)
	}

	if _err := r.waitForTaskCompletion(ctx, createIndexTaskIds); _err != nil {
		err = _err
		return err
	}

	updateSearchableTaskIds := make([]int64, 0, len(meilisearchpkg.Indexes))
	for _, index := range meilisearchpkg.Indexes {
		meiliSearchIndex := r.cfg.MeiliSearchClient.Index(index.Name)
		updateSearchableTask, _err := meiliSearchIndex.UpdateSearchableAttributesWithContext(
			ctx,
			&index.Searchable,
		)
		if _err != nil {
			err = status.Errorf(codes.Internal, "failed to update searchable index %v", _err)
			return err
		}

		updateSearchableTaskIds = append(updateSearchableTaskIds, updateSearchableTask.TaskUID)
	}

	if _err := r.waitForTaskCompletion(ctx, updateSearchableTaskIds); _err != nil {
		err = _err
		return err
	}

	updateFilterableTaskIds := make([]int64, 0, len(meilisearchpkg.Indexes))
	for _, index := range meilisearchpkg.Indexes {
		meiliSearchIndex := r.cfg.MeiliSearchClient.Index(index.Name)
		updateFilterableTask, _err := meiliSearchIndex.UpdateFilterableAttributesWithContext(
			ctx,
			&index.Filterable,
		)
		if _err != nil {
			err = status.Errorf(codes.Internal, "failed to update searchable index %v", _err)
			return err
		}

		updateFilterableTaskIds = append(updateFilterableTaskIds, updateFilterableTask.TaskUID)
	}

	if _err := r.waitForTaskCompletion(ctx, updateFilterableTaskIds); _err != nil {
		err = _err
		return err
	}

	updateSortableTaskIds := make([]int64, 0, len(meilisearchpkg.Indexes))
	for _, index := range meilisearchpkg.Indexes {
		meiliSearchIndex := r.cfg.MeiliSearchClient.Index(index.Name)
		updateSortableTask, _err := meiliSearchIndex.UpdateSortableAttributesWithContext(
			ctx,
			&index.Sortable,
		)
		if _err != nil {
			err = status.Errorf(codes.Internal, "failed to update searchable index %v", _err)
			return err
		}

		updateSortableTaskIds = append(updateSortableTaskIds, updateSortableTask.TaskUID)
	}

	err = r.waitForTaskCompletion(ctx, updateSortableTaskIds)
	return err
}

func (r *Repository) waitForTaskCompletion(ctx context.Context, taskIds []int64) error {
	if len(taskIds) == 0 {
		return nil
	}

	ticker := time.NewTicker(meiliSearchPollingDuration)
	defer ticker.Stop()

	timeout := time.After(meiliSearchTaskTimeout)
	for {
		select {
		case <-timeout:
			return status.Errorf(codes.DeadlineExceeded, "timeout waiting for Meilisearch tasks: %v", taskIds)
		case <-ticker.C:
			tasks, err := r.cfg.MeiliSearchClient.GetTasksWithContext(
				ctx,
				&meilisearch.TasksQuery{
					UIDS:  taskIds,
					Limit: int64(len(taskIds)),
				},
			)
			if err != nil {
				return status.Errorf(codes.Internal, "failed to get create meilisearch index info %v", err)
			}

			allDone := true
			//nolint:gocritic,exhaustive // It's how implemented in the library.
			for _, task := range tasks.Results {
				switch task.Status {
				case "succeeded":
				case "failed":
					if strings.Contains(task.Error.Code, "index_already_exists") {
						return nil
					}

					return status.Errorf(codes.Internal, "meilisearch task %d failed: %v", task.UID, task.Error)
				case "canceled":
					return status.Errorf(codes.Internal, "meilisearch task %d was canceled", task.UID)
				default:
					allDone = false
				}
			}

			if allDone {
				return nil
			}
		}
	}
}
