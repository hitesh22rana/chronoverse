package runtime

import (
	"context"
	"encoding/json"
	"time"

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
	return r.upsert(ctx, runtimemodel.NodeStatusReady)
}

// Heartbeat refreshes this runtime node heartbeat.
func (r *Repository) Heartbeat(ctx context.Context) error {
	ctx, span := r.tp.Start(ctx, "runtime.Repository.Heartbeat")
	defer span.End()

	query := `
        UPDATE runtime_nodes
        SET status = 'READY',
            last_heartbeat_at = now() AT TIME ZONE 'utc',
            updated_at = now() AT TIME ZONE 'utc'
        WHERE id = $1;
    `
	ct, err := r.pg.Exec(ctx, query, r.cfg.ID)
	if err != nil {
		return status.Errorf(codes.Internal, "failed to heartbeat runtime node: %v", err)
	}
	if ct.RowsAffected() == 0 {
		return r.RegisterReady(ctx)
	}
	return nil
}

// MarkDraining marks this runtime node as draining.
func (r *Repository) MarkDraining(ctx context.Context) error {
	return r.upsert(ctx, runtimemodel.NodeStatusDraining)
}

func (r *Repository) upsert(ctx context.Context, nodeStatus string) error {
	ctx, span := r.tp.Start(ctx, "runtime.Repository.Upsert")
	defer span.End()

	metadata, err := json.Marshal(map[string]string{
		"registered_by": svcpkg.Info().GetName(),
	})
	if err != nil {
		return status.Errorf(codes.Internal, "failed to marshal runtime metadata: %v", err)
	}

	query := `
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
            $4,
            now() AT TIME ZONE 'utc',
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
            last_heartbeat_at = EXCLUDED.last_heartbeat_at,
            max_concurrency = EXCLUDED.max_concurrency,
            metadata = EXCLUDED.metadata,
            updated_at = EXCLUDED.updated_at;
    `
	if _, err := r.pg.Exec(ctx, query, r.cfg.ID, r.cfg.NodeName, r.cfg.DockerEndpoint, nodeStatus, r.cfg.MaxConcurrency, metadata); err != nil {
		return status.Errorf(codes.Internal, "failed to upsert runtime node: %v", err)
	}
	return nil
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
