# Architecture

Chronoverse is a distributed scheduler built around synchronous gRPC services
and asynchronous Kafka workers. The public HTTP server and dashboard handle
user-facing workflows; background workers handle scheduling, builds, execution,
log delivery, notifications, analytics, and replay-safe event publication.

## Runtime Topology

### Entry Points

- `dashboard` is the Next.js UI. In development it is exposed on host port
  `3001`; in production it sits behind Nginx on port `80`.
- `server` is the public HTTP API. In development it is exposed on host port
  `8080`; in production it is only reachable inside the compose network and is
  proxied by Nginx under `/api/`.
- `nginx` exists in production only. It proxies dashboard traffic to
  `dashboard:3000`, rewrites `/api/...` to `server:8080`, and disables buffering
  for job log SSE routes.

### Domain Services

- `users-service` owns users, authentication, authorization metadata, and
  notification preferences.
- `workflows-service` owns workflow definitions, workflow generations, build
  status, termination/deletion rules, cleanup, and workflow list filters.
- `jobs-service` owns scheduled jobs, manual job creation, job status, job
  leases, log reads, log search, raw log download, and live log streams.
- `notifications-service` owns notification listing and marking notifications as
  read.
- `analytics-service` reads user and workflow analytics.

These services expose gRPC ports `50051` through `50055` inside the stack. The
development compose file also exposes those ports on the host for direct
debugging.

### Workers

- `scheduling-worker` scans PostgreSQL for workflows that are due and creates
  job dispatch work.
- `workflow-worker` consumes workflow events, builds Docker execution metadata,
  and updates workflow build state.
- `execution-worker` consumes job events, claims durable leases, runs containers,
  streams/publishes logs, renews leases, and recovers expired leases.
- `joblogs-processor` consumes log events and batches retained logs into
  ClickHouse and Meilisearch.
- `analytics-processor` consumes workflow, job, and log events and updates
  analytics tables.
- `outbox-relay` claims transactional outbox rows and publishes them to Kafka.
- `database-migration` applies PostgreSQL migrations, ClickHouse migrations, and
  Meilisearch index setup before application services start.

## Data Stores and Infrastructure

- **PostgreSQL** stores users, workflows, jobs, analytics, idempotency keys,
  outbox events, leases, retry state, and transactional metadata.
- **Kafka** carries asynchronous events on the `workflows`, `jobs`, `job_logs`,
  and `analytics` topics. Topic creation is explicit; auto-create is disabled.
- **ClickHouse** stores retained job logs.
- **Redis** stores HTTP sessions, cached service reads, and live log
  publish/subscribe state.
- **Meilisearch** indexes retained job logs for search.
- **Docker socket proxy** exposes a narrow Docker API surface to execution
  workers for container lifecycle and log access.
- **LGTM** receives OpenTelemetry data and exposes local dashboards.

## Event Flow

### Workflow Create and Update

1. A client sends `POST /workflows` or `PUT /workflows/{workflow_id}` with an
   `Idempotency-Key` header.
2. The HTTP server validates session and CSRF state, then calls
   `workflows-service`.
3. `workflows-service` writes workflow state in PostgreSQL, records the
   idempotency key, updates the workflow generation/build hash as needed, and
   creates outbox events in the same transaction.
4. `outbox-relay` claims pending outbox rows and publishes workflow events to
   Kafka.
5. `workflow-worker` consumes the workflow event, builds workflow execution
   metadata, and updates the workflow build status through `workflows-service`.
6. Notification and analytics events are published through the same durable
   event path.

### Scheduling and Manual Runs

- Automatic scheduling is driven by `scheduling-worker`. It scans due workflows,
  uses workflow generation guards, and creates replay-safe job events.
- Manual scheduling is driven by `POST /workflows/{workflow_id}/jobs/schedule`
  and also requires `Idempotency-Key`.
- Job dispatch events include trigger metadata (`AUTOMATIC` or `MANUAL`) and
  dispatch-attempt data so repeated processing does not create duplicate work.

### Job Execution

1. `execution-worker` consumes job events and calls `jobs-service` to claim the
   job.
2. `jobs-service` grants a lease only when the job, workflow, dispatch attempt,
   and current state are valid.
3. The worker starts the container through the Docker proxy, attaches the
   container ID, and renews the lease while the job runs.
4. Logs are emitted both for live streaming and durable processing when retention
   is enabled.
5. Completion, user failures, system failures, retries, and cancellations are
   recorded through lease-token-protected job APIs.
6. Expired running leases are recovered by workers and either retried, failed, or
   cleaned up according to the job state.

### Logs, Search, and Analytics

- `CONTAINER` workflows can retain stdout/stderr logs when `log_retention` is
  enabled.
- `HEARTBEAT` workflows do not generate execution logs.
- Running jobs can stream live logs over Server-Sent Events.
- Completed retained logs are read from ClickHouse and searched through
  Meilisearch.
- `joblogs-processor` writes deterministic log event IDs so replayed log events
  do not double-count or duplicate retained logs.
- `analytics-processor` deduplicates processed events and cleans old processed
  event records.

## Replay-Safety Model

Chronoverse treats duplicate events, worker restarts, and partial failures as
expected operating conditions.

- **Idempotency keys** are required for workflow create/update and manual job
  scheduling. Retrying the same mutation with the same key should not duplicate
  the command.
- **Transactional outbox events** are written alongside state changes and later
  published by `outbox-relay`. This prevents state changes from being committed
  without their matching Kafka event.
- **Outbox processing leases** let multiple relay replicas work safely. Failed
  publication attempts are retried with backoff and eventually marked dead.
- **Workflow generations** guard stale build, scheduling, reschedule,
  termination, and deletion events.
- **Build hashes** avoid unnecessary rebuild work when workflow execution inputs
  have not changed.
- **Deterministic event keys** deduplicate workflow, job, notification,
  analytics, and log side effects.
- **Durable job leases** ensure only one execution worker owns a running job at a
  time. Lease tokens are required for status, container, completion, failure, and
  retry operations.
- **Partition-aware Kafka workers** preserve per-partition ordering and commit
  only after the selected retry/commit policy permits it.
- **Cleanup loops** remove old outbox and processed-event records after their
  retention windows.

## Security and Observability

- Compose generates a local CA, service certificates, client certificates, and
  Ed25519 auth keys through `init-certs`.
- PostgreSQL, ClickHouse, Redis, Meilisearch, Kafka, and gRPC services run with
  TLS or mTLS in compose.
- The HTTP server uses encrypted session cookies, CSRF cookies, and service JWT
  metadata for downstream gRPC calls.
- Services and workers export OpenTelemetry data to `lgtm:4317`.
