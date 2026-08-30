# Architecture

Chronoverse is a distributed scheduler built around synchronous gRPC services
and asynchronous Kafka workers. The public HTTP server and dashboard handle
user-facing workflows; background workers handle scheduling, builds, execution,
log delivery, notifications, analytics, and replay-safe event publication.

## Runtime Topology

### Entry Points

- `dashboard` is the Next.js UI. Compose development exposes it on host port
  `3001`; Compose production puts it behind Nginx on port `80`.
- `server` is the public HTTP API. In development it is exposed on host port
  `8080`; in production it is only reachable inside the compose network and is
  proxied by Nginx under `/api/`.
- In Compose, `nginx` exists in production only. It proxies dashboard traffic to
  `dashboard:3000`, rewrites `/api/...` to `server:8080`, and disables buffering
  for job log SSE routes.

In Kubernetes, `infra/k8s` provides Kustomize overlays. The local overlay
deploys the same entry points inside a single namespace. The production overlay
deploys the application, workers, Nginx, Docker proxy, Kafka topic initializer,
migration and role-bootstrap jobs, PgBouncer, PostgreSQL, Redis, Kafka,
ClickHouse, Meilisearch, LGTM, dynamic PVCs, service HorizontalPodAutoscalers,
and KEDA Kafka-lag scaling. It is a self-hosted topology; operators own
the StorageClass, Secret lifecycle, ingress hostnames, runtime-node preparation,
backups, and production sizing.

### Domain Services

- `users-service` owns users, authentication, authorization metadata, and
  notification preferences.
- `workflows-service` owns workflow definitions, workflow generations, build
  status, termination/deletion rules, cleanup, and workflow list filters.
- `jobs-service` owns scheduled jobs, manual job creation, job status, job
  leases, runtime assignment, log reads, log search, raw log download, and live
  log streams.
- `notifications-service` owns notification listing and marking notifications as
  read.
- `analytics-service` reads user and workflow analytics.

These services expose gRPC ports `50051` through `50055` inside the stack. The
development compose file also exposes those ports on the host for direct
debugging. Kubernetes exposes the five domain services through headless Services
inside the `chronoverse` namespace. Their DNS records return ready Pod IPs so
the gRPC clients' `round_robin` policy can balance calls across resolved
replicas. The HTTP `server` and `dashboard` retain ordinary ClusterIP Services.

### Workers

- `scheduling-worker` scans PostgreSQL for workflows that are due and creates
  job dispatch work.
- `workflow-worker` consumes workflow events, builds Docker execution metadata,
  resolves container image digests, and updates workflow build state.
- `execution-worker` consumes job events, claims durable leases, runs containers,
  streams/publishes logs through the assigned runtime endpoint, renews leases,
  and recovers expired leases.
- `joblogs-processor` consumes log events and batches retained logs into
  ClickHouse and Meilisearch.
- `analytics-processor` consumes workflow, job, and log events and updates
  analytics tables.
- `outbox-relay` claims transactional outbox rows and publishes them to Kafka.
- `database-migration` applies PostgreSQL migrations, ClickHouse migrations, and
  Meilisearch index setup before application services start.

## Data Stores and Infrastructure

- **PgBouncer** transaction-pools application connections and bounds aggregate
  PostgreSQL backend usage as replicas scale and roll.
- **PostgreSQL** stores users, workflows, jobs, runtime nodes, analytics,
  idempotency keys, outbox events, leases, retry state, and transactional
  metadata.
- **Kafka** carries asynchronous events on the `workflows`, `jobs`, `job_logs`,
  and `analytics` topics. Topic creation is explicit; auto-create is disabled.
- **ClickHouse** stores retained job logs.
- **Redis** stores HTTP sessions, cached service reads, live log
  publish/subscribe state, and runtime-node-scoped image pull locks.
- **Meilisearch** indexes retained job logs for search.
- **Runtime agent** registers each Docker-capable node and heartbeats Docker
  endpoint health/capacity into PostgreSQL.
- **Docker socket proxy** exposes an mTLS- and token-authenticated node-local
  Docker API. Certificate-role ACLs limit runtime-agent to health, workflow-worker
  to image and cancellation-cleanup operations, and execution-worker to the
  container/network execution surface. Server and role private keys are mounted
  separately.
- **LGTM** receives OpenTelemetry data and exposes local dashboards.

The Kubernetes local overlay includes these infrastructure systems for
single-node validation. The Kubernetes production overlay is self-hosted:
PostgreSQL, Redis, Kafka, ClickHouse, Meilisearch, runtime-agent, services, and
workers are deployed together on the user's Kubernetes infrastructure.

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
5. `workflow-worker` consumes the workflow event, validates the container
   payload when needed, resolves the image digest, stores resolved image
   metadata on the workflow row, and updates the workflow build status through
   `workflows-service`.
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
   current state, and runtime availability are valid. `CONTAINER` jobs receive a
   fresh `READY` runtime node whose last heartbeat reflects a successful Docker
   health check; `HEARTBEAT` jobs do not.
3. The worker creates a Docker client for the returned runtime endpoint, ensures
   the resolved image digest exists on that runtime under a runtime-node-scoped
   image pull lock, runs the image, attaches the container ID with
   `runtime_node_id`, and renews the lease while the job runs.
4. Logs are emitted both for live streaming and durable processing when retention
   is enabled.
5. Completion, user failures, system failures, retries, and cancellations are
   recorded through lease-token-protected job APIs.
6. Expired running leases are recovered by workers using the job's stored
   runtime owner and endpoint. If the runtime is unavailable, recovery releases
   or fails the job according to retry policy instead of guessing another node.

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
  have not changed. Resolved image references and digests are derived workflow
  metadata and are not part of the user-authored build hash.
- **Image pull locks** prevent replicated workers that share a runtime daemon
  from cold-pulling the same image concurrently. Locks are scoped by runtime
  node and image, with Docker host fallback when runtime scope is omitted.
- **Runtime ownership** records `runtime_node_id` and `runtime_endpoint` on
  running container jobs so execution, logs, termination, deletion, and lease
  recovery target the Docker daemon that owns the container.
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
- The Kubernetes local overlay generates development certificates into a local
  certificate PVC and mirrors only the CA and generic Kafka client identity
  into narrowly managed Secrets for KEDA. Production mounts Kubernetes Secrets
  for auth keys, CA material, client TLS, service TLS, infrastructure TLS,
  Kafka stores, and datastore credentials. The setup script preserves valid,
  complete operator-provided Secrets and can generate a missing fallback set;
  partial internal TLS trust chains are rejected.
- PostgreSQL, ClickHouse, Redis, Meilisearch, Kafka, and gRPC services run with
  TLS or mTLS in compose.
- The HTTP server uses encrypted session cookies, constant-time CSRF HMAC
  verification, browser security response headers, and service JWT metadata for
  downstream gRPC calls.
- Production startup rejects the known development secret placeholder and
  rejects reuse of one value for encryption and CSRF signing.
- Services and workers export OpenTelemetry data to `lgtm:4317`.
