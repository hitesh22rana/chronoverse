package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	runtimemodel "github.com/hitesh22rana/chronoverse/internal/model/runtime"
	"github.com/hitesh22rana/chronoverse/internal/pkg/postgres"
	svcpkg "github.com/hitesh22rana/chronoverse/internal/pkg/svc"
)

// Config configures runtime node registration.
type Config struct {
	ID             string
	NodeName       string
	DockerEndpoint string
	MaxConcurrency int32
}

// Repository provides runtime node persistence.
type Repository struct {
	tp  trace.Tracer
	cfg Config
	pg  *postgres.Postgres
}

// New creates a runtime repository.
func New(cfg Config, pg *postgres.Postgres) *Repository {
	if cfg.MaxConcurrency <= 0 {
		cfg.MaxConcurrency = 1
	}
	return &Repository{
		tp:  otel.Tracer(svcpkg.Info().GetName()),
		cfg: cfg,
		pg:  pg,
	}
}

// RegisterReady upserts this runtime node as ready.
func (r *Repository) RegisterReady(ctx context.Context) error {
	return r.upsert(ctx, runtimemodel.NodeStatusReady, true)
}

// Heartbeat refreshes this runtime node heartbeat.
func (r *Repository) Heartbeat(ctx context.Context) error {
	ctx, span := r.tp.Start(ctx, "runtime.Repository.Heartbeat")
	defer span.End()

	tx, err := r.pg.BeginTx(ctx)
	if err != nil {
		return status.Errorf(codes.Internal, "failed to start runtime heartbeat transaction: %v", err)
	}
	defer func() {
		//nolint:errcheck // Commit closes the transaction on success.
		_ = tx.Rollback(ctx)
	}()

	if lockErr := r.lockRuntimeNodeTx(ctx, tx); lockErr != nil {
		if errors.Is(lockErr, pgx.ErrNoRows) {
			if rollbackErr := tx.Rollback(ctx); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
				return status.Errorf(codes.Internal, "failed to rollback missing runtime heartbeat transaction: %v", rollbackErr)
			}
			return r.RegisterReady(ctx)
		}
		return status.Errorf(codes.Internal, "failed to lock runtime node for heartbeat: %v", lockErr)
	}

	query := `
        UPDATE runtime_nodes
        SET status = 'READY',
            last_heartbeat_at = now() AT TIME ZONE 'utc',
            updated_at = now() AT TIME ZONE 'utc'
        WHERE id = $1;
    `
	ct, err := tx.Exec(ctx, query, r.cfg.ID)
	if err != nil {
		return status.Errorf(codes.Internal, "failed to heartbeat runtime node: %v", err)
	}
	if ct.RowsAffected() == 0 {
		if rollbackErr := tx.Rollback(ctx); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			return status.Errorf(codes.Internal, "failed to rollback empty runtime heartbeat transaction: %v", rollbackErr)
		}
		return r.RegisterReady(ctx)
	}
	if err := r.reconcileRunningJobsTx(ctx, tx); err != nil {
		return status.Errorf(codes.Internal, "failed to reconcile runtime running jobs: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return status.Errorf(codes.Internal, "failed to commit runtime heartbeat transaction: %v", err)
	}
	return nil
}

// MarkDraining marks this runtime node as draining.
func (r *Repository) MarkDraining(ctx context.Context) error {
	return r.upsert(ctx, runtimemodel.NodeStatusDraining, true)
}

// MarkUnhealthy marks this runtime node as unhealthy without refreshing its last successful Docker heartbeat.
func (r *Repository) MarkUnhealthy(ctx context.Context) error {
	return r.upsert(ctx, runtimemodel.NodeStatusUnhealthy, false)
}

func (r *Repository) upsert(ctx context.Context, nodeStatus string, refreshHeartbeat bool) error {
	ctx, span := r.tp.Start(ctx, "runtime.Repository.Upsert")
	defer span.End()

	metadata, err := json.Marshal(map[string]string{
		"registered_by": svcpkg.Info().GetName(),
	})
	if err != nil {
		return status.Errorf(codes.Internal, "failed to marshal runtime metadata: %v", err)
	}

	tx, err := r.pg.BeginTx(ctx)
	if err != nil {
		return status.Errorf(codes.Internal, "failed to start runtime upsert transaction: %v", err)
	}
	defer func() {
		//nolint:errcheck // Commit closes the transaction on success.
		_ = tx.Rollback(ctx)
	}()

	query := upsertRuntimeNodeQuery()
	if _, err := tx.Exec(
		ctx,
		query,
		r.cfg.ID,
		r.cfg.NodeName,
		r.cfg.DockerEndpoint,
		nodeStatus,
		r.cfg.MaxConcurrency,
		metadata,
		refreshHeartbeat,
		staleRuntimeHeartbeatAt(),
	); err != nil {
		return status.Errorf(codes.Internal, "failed to upsert runtime node: %v", err)
	}
	if err := r.lockRuntimeNodeTx(ctx, tx); err != nil {
		return status.Errorf(codes.Internal, "failed to lock runtime node after upsert: %v", err)
	}
	if err := r.reconcileRunningJobsTx(ctx, tx); err != nil {
		return status.Errorf(codes.Internal, "failed to reconcile runtime running jobs: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return status.Errorf(codes.Internal, "failed to commit runtime upsert transaction: %v", err)
	}
	return nil
}

func staleRuntimeHeartbeatAt() time.Time {
	return time.Unix(0, 0).UTC()
}

func upsertRuntimeNodeQuery() string {
	return `
        INSERT INTO runtime_nodes (
            id,
            node_name,
            docker_endpoint,
            status,
            last_heartbeat_at,
            max_concurrency,
            running_jobs,
            metadata,
            created_at,
            updated_at
        ) VALUES (
            $1,
            $2,
            $3,
            $4::runtime_node_status,
            CASE WHEN $7 THEN now() AT TIME ZONE 'utc' ELSE $8::timestamp END,
            $5,
            0,
            $6,
            now() AT TIME ZONE 'utc',
            now() AT TIME ZONE 'utc'
        )
        ON CONFLICT (id) DO UPDATE
        SET node_name = EXCLUDED.node_name,
            docker_endpoint = EXCLUDED.docker_endpoint,
            status = EXCLUDED.status,
            last_heartbeat_at = CASE
                WHEN $7 THEN EXCLUDED.last_heartbeat_at
                ELSE runtime_nodes.last_heartbeat_at
            END,
            max_concurrency = EXCLUDED.max_concurrency,
            metadata = EXCLUDED.metadata,
            updated_at = EXCLUDED.updated_at;
    `
}

func (r *Repository) lockRuntimeNodeTx(ctx context.Context, tx pgx.Tx) error {
	var id string
	return tx.QueryRow(ctx, lockRuntimeNodeQuery(), r.cfg.ID).Scan(&id)
}

func lockRuntimeNodeQuery() string {
	return `
        SELECT id
        FROM runtime_nodes
        WHERE id = $1
        FOR UPDATE;
    `
}

func (r *Repository) reconcileRunningJobsTx(ctx context.Context, tx pgx.Tx) error {
	_, err := tx.Exec(ctx, reconcileRunningJobsQuery(), r.cfg.ID)
	return err
}

func reconcileRunningJobsQuery() string {
	return `
        UPDATE runtime_nodes AS rn
        SET running_jobs = (
                SELECT COUNT(*)::int
                FROM jobs AS j
                WHERE j.status = 'RUNNING'
                    AND j.runtime_node_id = rn.id
            ),
            updated_at = now() AT TIME ZONE 'utc'
        WHERE rn.id = $1;
    `
}

// RunHeartbeats heartbeats until the context is canceled.
func (r *Repository) RunHeartbeats(ctx context.Context, interval time.Duration) error {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	if err := r.RegisterReady(ctx); err != nil {
		return err
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := r.Heartbeat(ctx); err != nil {
				return err
			}
		}
	}
}
