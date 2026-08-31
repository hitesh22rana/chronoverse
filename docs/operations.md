# Operations

This guide covers day-to-day commands, startup behavior, health checks, tuning,
and common troubleshooting paths for Compose and Kubernetes Chronoverse stacks.

## Running the Stack

### Development

```sh
docker compose -f compose.dev.yaml up -d
```

Useful endpoints:

- Dashboard: `http://localhost:3001`
- API: `http://localhost:8080`
- LGTM Grafana: not host-published by default; run
  `docker compose -f compose.dev.yaml -f compose.grafana.yaml up -d lgtm` for
  loopback-only access on `http://127.0.0.1:${GRAFANA_HOST_PORT:-3000}`
- gRPC: `localhost:50051` through `localhost:50055`

Development builds local service images and exposes internal infrastructure
ports for debugging.

### Production

```sh
export CRYPTO_SECRET="$(openssl rand -hex 16)"
export SERVER_CSRF_HMAC_SECRET="$(openssl rand -hex 32)"
export GF_SECURITY_ADMIN_PASSWORD="$(openssl rand -hex 24)"
docker compose -f compose.prod.yaml up -d
```

Persist these values in your secret manager and reuse them across server
restarts. `CRYPTO_SECRET` and `SERVER_CSRF_HMAC_SECRET` must be distinct.

Useful endpoints:

- Dashboard: `http://localhost`
- API through Nginx: `http://localhost/api/...`
- LGTM Grafana: not host-published by default; add `compose.grafana.yaml` for
  opt-in loopback-only access

Production uses published images, internal service ports, generated TLS
configuration, resource limits, and replicated worker settings.

### Kubernetes

```sh
scripts/k8s/setup.sh --mode local
scripts/k8s/setup.sh --mode production
```

The local strategy is self-contained for single-node validation and runs one
replica per app deployment. The production strategy is self-hosted on your
Kubernetes infrastructure and includes PostgreSQL, Redis, Kafka, ClickHouse,
Meilisearch, PgBouncer, application services, workers, runtime-agent,
CPU/memory HorizontalPodAutoscalers for services, and KEDA Kafka-lag scaling
for consumers. Production service autoscaling requires metrics-server or an
equivalent `autoscaling/v2` resource metrics provider. KEDA is a platform
prerequisite for both overlays; `setup.sh` checks its CRD and external metrics
API but does not install it. Label the platform-managed namespace serving the
external metrics API with `chronoverse.io/keda-kafka-access=true`; the Kafka
NetworkPolicy uses that stable label so KEDA installations are not restricted
to a namespace literally named `keda`. Setup discovers and validates the
serving namespace before applying Chronoverse.

Kubernetes does not include a generic `kubectl` command to create a cluster.
Create the cluster with your lifecycle tool, such as kind, minikube, kubeadm, or
managed Kubernetes provisioning, then apply the Kustomize overlay with
`kubectl`.

Container workflows require Docker-capable runtime nodes. Before applying the
overlay, make sure every node that should own Docker containers exposes Docker
Engine at `/var/run/docker.sock` and has this label:

```sh
kubectl label node <node-name> chronoverse.io/docker-workloads=true
```

For kind, the Docker socket mount, certificate mount, and node label must be
configured when the cluster is created. The repository includes a single-node
local example:

```sh
kind create cluster --name chronoverse --config infra/k8s/overlays/local/kind-cluster.yaml
kubectl config use-context kind-chronoverse
scripts/k8s/setup.sh --mode local
```

Docker Desktop's built-in `docker-desktop` Kubernetes context is not sufficient
for Docker-backed workers because its node does not expose Docker Engine at
`/var/run/docker.sock` to pods. Use kind with the checked-in config, or use a
cluster whose nodes really provide that socket.

The `docker-proxy` DaemonSet runs one `runtime-agent` sidecar per labeled
Docker-capable node. Official overlays construct an IPv4/IPv6-safe node endpoint on `2376` via
`hostPort:2376` so running job cleanup survives proxy pod restarts on the same
node. Health probes use `tcp://127.0.0.1:2376` (loopback) while the advertised
endpoint remains node-stable. The proxy binds
`:2376 ssl crt /certs/docker-proxy/server.pem ca-file /certs/docker-proxy/ca.crt verify required`
plus the `X-Chronoverse-Docker-Proxy-Token` and exact certificate-role and
method/path ACLs. Runtime-agent receives health APIs only, workflow-worker gets
image and cancellation-cleanup calls, and execution-worker gets the execution
surface. The server and each role use separate private-key mounts; workload
containers receive neither token nor certificate. Multi-node kind and other Docker-container-based Kubernetes emulators
can have a specific hostPort routing limitation where pods on one emulator node
cannot reach another emulator node's host port; if you choose that topology,
use a pod-IP endpoint override as an emulator-only workaround.
`workflow-worker` and `execution-worker` do not need the Docker node label;
they can schedule anywhere with network access to TCP `2376` on registered
runtime endpoints.

When upgrading an existing installation from plaintext `2375`, rebuild and
restart `runtime-agent`, `workflow-worker`, and `execution-worker` together.
With Docker proxy mTLS configured, runtime-agent normalizes its configured
health endpoint and both workers preserve the stored runtime host while
normalizing legacy `tcp://<host>:2375` snapshots to port `2376`. Workers do
this before endpoint-cache lookup. Current runtime registrations already use
`2376`, and historical jobs or idempotency snapshots therefore need no manual
database rewrite. Plaintext Docker endpoints remain unchanged when proxy TLS
is disabled.

The execution-worker identity remains node-root-equivalent if its key and token
are compromised because it can create containers on a host Docker daemon. Role
authorization prevents runtime-agent or workflow-worker credentials from
inheriting that create privilege, but workflow cleanup credentials can still
read logs and stop/delete a known container ID. Docker is not a tenant security
boundary; per-container policy requires a narrower execution broker.

### Docker Proxy Certificate Rotation

Kubernetes rotation is integrated into setup and stages overlapping trust:

```sh
scripts/k8s/setup.sh --mode production --context <context> \
  --rotate-docker-proxy-certs
```

This requires an existing deployment. The standalone
`scripts/k8s/rotate-docker-proxy-certs.sh --context <context>` command skips the
manifest apply. Both rotate the three clients before the server and remove the
old CA only after all identities trust the replacement. Run in a maintenance
window; a single-node hostPort DaemonSet briefly interrupts Docker calls while
its pod restarts.

Compose rotation requires a stopped stack because it replaces the dedicated
issuer and every mounted role directory as one atomic set:

```sh
docker compose -f compose.prod.yaml down
docker compose -f compose.prod.yaml run --rm --no-deps \
  -e DOCKER_PROXY_ROTATE_CERTS=true init-certs
docker compose -f compose.prod.yaml up -d
```

### Stop and Inspect

```sh
docker compose -f compose.dev.yaml ps
docker compose -f compose.dev.yaml logs -f server
docker compose -f compose.dev.yaml down
```

Use `compose.prod.yaml` in the same commands for the production stack.

For Kubernetes:

```sh
kubectl -n chronoverse get pods
kubectl -n chronoverse get deploy,sts,job
kubectl -n chronoverse logs deploy/server
```

## Startup Order

1. `init-certs` generates the local service CA, service certificates, client
   certificates, Kafka keystore/truststore files, auth keys, and a separate
   role-isolated Docker proxy PKI.
2. `init-keda-secrets` mirrors the local CA and generic Kafka client identity
   from the certificate PVC into the two Kubernetes Secrets used by KEDA.
3. PostgreSQL, ClickHouse, Redis, Meilisearch, Kafka, LGTM, and Docker proxy
   start with TLS-enabled configuration.
4. `init-postgres-app-role` creates or updates the dedicated non-superuser
   application role.
5. `init-kafka-topics` creates or expands Kafka topics.
6. `database-migration` connects directly to `postgres-primary` for its
   session-level advisory lock, then applies PostgreSQL migrations, ClickHouse
   migrations, and Meilisearch index setup.
7. gRPC services become ready after their dependencies are healthy.
8. Workers become ready after dependent services and topics are available.
9. The dashboard and Nginx expose the application.

Kubernetes does not provide Compose-style dependency ordering for long-running
Deployments. The overlays include explicit Jobs for certificate bootstrap
where local, Kafka topic initialization, and database migration. Application
containers are expected to retry transient dependency failures, while readiness
and liveness probes decide when pods receive traffic or are restarted. The local
overlay uses init containers only for prerequisites that must exist before the
process starts, such as generated certificate files and hostPath permissions.
The setup entrypoint waits for every bootstrap Job, forces a post-Kafka KEDA
reconciliation, waits for PgBouncer, rolls the Docker proxy DaemonSet, and then
restarts and waits for stateless application Deployments. Direct Kustomize
users must inspect those Jobs and perform equivalent rollout checks before
scaling application workloads.

### Resolving Failed Migrations

Both migration runners use the same dirty-flag bookkeeping (golang-migrate for
PostgreSQL, an equivalent native runner for ClickHouse): a migration that fails
partway leaves its `schema_migrations` row marked dirty, and every later run
refuses to continue rather than re-applying partially executed DDL. To recover,
inspect the failed migration's statements, restore the schema to a consistent
state, clear the dirty flag (`migrate force` for PostgreSQL, deleting the row
for ClickHouse), and restart `init-database-migration`.

## Maintenance-Window Upgrades

Schema-changing releases must be deployed as an offline cutover. Chronoverse
does not support mixed application versions across an idempotency-ledger
migration.

1. Take and verify PostgreSQL and ClickHouse backups.
2. Stop public traffic and mutation producers first. While the existing outbox
   relay and workflow worker are still running, wait until terminal job outbox
   rows are published and the workflow worker's consumer lag reaches zero. This
   drain is required before migration 11 can safely seed completed terminal
   identities. Then stop the server, scheduling and execution workers, workflow
   workers, processors, and the outbox relay. Allow in-flight requests and
   database transactions to finish before continuing.
3. Run the new release's `database-migration` image while PostgreSQL remains
   available. Migration preflight checks run before destructive schema changes;
   an unexpired in-progress command, malformed legacy identity, or normalized-key
   collision aborts the migration for operator reconciliation.
4. Verify the schema version and migration logs, then delete Redis keys matching
   `workflow:*` and `workflows:*` before starting any application process from
   the new release. Migration-time failure-threshold reconciliation updates
   PostgreSQL directly and cannot invalidate Redis atomically. Do not use
   `FLUSHDB`: Redis also contains sessions and coordination state. Use an
   approved `SCAN` plus `UNLINK` procedure for both patterns and verify both
   scans are empty before continuing.
5. Start domain services, then the outbox relay and workers while public traffic
   remains stopped. Confirm health, consumer progress, outbox publication, and
   runtime registration.
6. Run an approved canary that exercises workflow create replay, changed-input
   conflict, update, scheduling, termination, deletion, and deterministic outbox
   keys. Remove its fixtures.
7. Start the server and restore public traffic.

For Compose, stop the mutation paths before replacing images:

```sh
docker compose -f compose.prod.yaml stop \
  nginx server scheduling-worker workflow-worker execution-worker \
  joblogs-processor analytics-processor outbox-relay
docker compose -f compose.prod.yaml run --rm init-database-migration
```

The normal migration executable applies upgrades only. A rollback requires an
operator-reviewed migration invocation or a verified database restore; do not
assume that restarting old images reverses the schema.

### Rollback

Keep traffic, workers, processors, and outbox publication stopped. Roll back the
database schema before starting old binaries, verify the restored schema and
data, then start the old release as one version. The legacy schema cannot
represent completed terminal identities, non-workflow command-ledger records,
or every reused manual command key. Migration 11 restores one legacy workflow
row for every accepted canonical or raw workflow-ID operation/hash pair, so
supported workflow-update retries remain idempotent after rollback. The
remaining documented identity loss still requires explicit operator acceptance.
Prefer restoring the pre-upgrade backup when that loss is unacceptable.

## Health Checks

Compose includes health checks for infrastructure and gRPC services:

- PostgreSQL uses `psql` with TLS client certs.
- ClickHouse uses `clickhouse-client --secure`.
- Redis uses `redis-cli --tls`.
- Meilisearch uses HTTPS with cert/key/CA paths.
- gRPC services use `grpc-health-probe` with TLS and service auth headers.

If a service is stuck in `starting`, inspect the logs for that service and the
dependency immediately before it in the startup order.

Kubernetes uses readiness and liveness probes for app services and local
infrastructure where practical. Use `kubectl describe pod` to inspect probe
failures and missing Secret or ConfigMap references.

## Build, Test, and Lint

Repository commands:

```sh
make tools
make dependencies
make generate
make mockgen
make test/short
make test/integration
make lint
make lint/fix
make build/all
```

Important notes:

- `make generate` requires `buf`.
- `make dependencies` regenerates protobuf stubs and runs `go mod tidy -v`.
- `make mockgen` installs the configured tools and runs every `//go:generate`
  directive.
- `make tools` installs Go tooling into `./.bin`.
- `make test/short` runs every unit suite with the race detector; Docker-backed
  integration tests self-skip under `-short`.
- `make build/all` builds all Go services and workers, including `outbox-relay`.

### Integration Tests

Every Docker-backed suite is an `*_integration_test.go` file with `TestIntegration*`
test names: repository packages under `internal/repository`, the command ledger in
`internal/pkg/commandidempotency` (both provisioned with Testcontainers), and the
container workflow tests in `internal/pkg/kind/container` (which drive the host
Docker daemon directly).

```sh
make test/integration   # race detector + real containers; needs a Docker daemon
```

Key facts:

- **Bootstrap**: the shared `internal/pkg/testkit` package starts one container
  per service on first use, applies migrations and index setup, and terminates
  everything after the package test binary finishes. Each repository package
  declares which services it needs in its `TestMain`, for example
  `testkit.Run(m, testkit.WithPostgres(), testkit.WithKafka())`. Fixture helpers
  such as `testkit.SeedUser` and `testkit.SeedWorkflow` insert the rows most
  schemas depend on, so tests never repeat the same SQL.
- **Single source of truth**: testkit reuses the production client constructors
  and migration runners from the sibling packages under `internal/pkg` —
  `postgres.Migrate`, `clickhouse.Migrate`, `meilisearch.SetupIndexes`, and
  `kafka.EnsureTopics` — so integration tests exercise the exact code paths the
  running services use, and the embedded migrations under
  `internal/pkg/postgres/migrations` and `internal/pkg/clickhouse/migrations`
  are never re-implemented in tests.
- **Gating**: integration tests self-skip under `-short` (so `make test/short`
  and plain `go test ./...` stay fast) and when Docker is unavailable.
  `make test/integration` runs the full suite with the race detector.

Dashboard commands:

```sh
cd dashboard
npm install
npm run dev:port
npm run build
npm run lint
npm run lint:fix
```

The dashboard `dev:port` script runs on port `3001`, matching the dev compose
dashboard port.

Static documentation commands:

```sh
cd static
npm ci
npm run validate:docs
npm run check
```

`npm run check` includes documentation validation, lint, type checking, and the
static Next.js export.

## Operational Tuning

### Kafka Topic Partitions

Tune before starting the stack or before topic initialization:

- `KAFKA_WORKFLOWS_TOPIC_PARTITIONS`
- `KAFKA_JOBS_TOPIC_PARTITIONS`
- `KAFKA_JOB_LOGS_TOPIC_PARTITIONS`
- `KAFKA_ANALYTICS_TOPIC_PARTITIONS`
- `KAFKA_TOPIC_REPLICATION_FACTOR`

Kafka auto topic creation is disabled. The init job can expand partition counts
but should not be treated as a substitute for capacity planning. Kubernetes
base uses 2/4/4/2 partitions for workflows/jobs/job_logs/analytics; production
overrides these to 6/12/12/6 and applies matching KEDA consumer ceilings.

### PostgreSQL connection pools

Kubernetes applications connect through the `postgres` PgBouncer Service in
transaction mode; PostgreSQL itself is available privately as
`postgres-primary`. Two production PgBouncer replicas are each limited to 25
backend connections, preserving half of PostgreSQL's 100 slots for bootstrap,
migrations, administration, and faults. Application pools are explicitly
budgeted at two or four connections per pod with zero idle minimum except for
outbox-relay's single warm connection. Do not raise these values without load
testing pool wait time, transaction duration, database CPU/memory, and locks.

### Scheduler

- `SCHEDULING_WORKER_POLL_INTERVAL` controls how often due workflows are scanned.
- `SCHEDULING_WORKER_CONTEXT_TIMEOUT` bounds each scan.
- `SCHEDULING_WORKER_BATCH_SIZE` controls how many workflows can be processed per
  pass.

Lower intervals increase scheduling responsiveness and database load.

### Workflow Workers

- `WORKFLOW_WORKER_IMAGE_PULL_LOCK_TTL` controls how long a worker owns an image
  pull lock before renewal.
- `WORKFLOW_WORKER_IMAGE_PULL_LOCK_WAIT_TIMEOUT` controls how long another
  worker waits for the same runtime node and image before retrying the Kafka
  record.
- `WORKFLOW_WORKER_IMAGE_PULL_LOCK_RETRY_INTERVAL` controls Redis polling while
  waiting for a held image pull lock.

Increase the TTL and wait timeout for large images or slow registries. Reduce
the retry interval only when Redis can support the extra polling. Locks are
scoped to runtime node plus exact image string; Docker host is used as a fallback
when a request omits an explicit runtime scope. Different
runtime nodes may still pull the same image in parallel, so registry-wide rate
limits need separate capacity planning. Workflow workers do not launch workload
containers, so execution-worker workload container limits do not apply to image
pulls.

Runtime-agent heartbeats represent Docker endpoint health. A successful
`UNHEALTHY` update stops new container claims immediately when the Docker proxy
or daemon is unavailable. If PostgreSQL is unavailable, runtime-agent exits and
the existing runtime heartbeat TTL controls when the last `READY` row becomes
ineligible.

### Execution Workers

- `EXECUTION_WORKER_CONCURRENCY` controls parallel job execution. `0` or unset
  uses auto concurrency from `GOMAXPROCS`.
- `EXECUTION_WORKER_WORKLOAD_CONTAINER_MEMORY`,
  `EXECUTION_WORKER_WORKLOAD_CONTAINER_CPUS`, and
  `EXECUTION_WORKER_WORKLOAD_CONTAINER_PIDS_LIMIT` bound each Docker workload
  container launched by the execution worker.
- `EXECUTION_WORKER_LEASE_DURATION` and
  `EXECUTION_WORKER_LEASE_RENEW_INTERVAL` control lease ownership.
- `EXECUTION_WORKER_SYSTEM_RETRY_LIMIT` and
  `EXECUTION_WORKER_SYSTEM_RETRY_BACKOFF` control infrastructure retry behavior.
- `EXECUTION_WORKER_RECOVERY_INTERVAL` and
  `EXECUTION_WORKER_RECOVERY_BATCH_SIZE` control expired lease recovery.
- `EXECUTION_WORKER_JOB_LOG_*` settings tune log batching, publish retries, live
  publish timeout, and live log buffer size.
- `EXECUTION_WORKER_IMAGE_PULL_LOCK_*` settings coordinate cold image pulls on
  the selected runtime node before Docker container creation.

Keep the lease duration comfortably above the renewal interval. Increase
concurrency only when Docker host capacity, per-workload resource limits, Kafka
partitions, and downstream services can support it. These per-workload limits
are applied only when execution-worker creates Docker job containers. Execution
image pulls use the same runtime-node-scoped lock model as workflow-worker
digest resolution: workers sharing one runtime daemon serialize the same image
pull, while different runtime nodes may pull independently.

### Outbox Relay

- `OUTBOX_RELAY_BATCH_SIZE` and `OUTBOX_RELAY_POLL_INTERVAL` control throughput
  and database polling pressure.
- `OUTBOX_RELAY_PROCESSING_LEASE` controls how long a relay owns claimed rows.
- `OUTBOX_RELAY_MAX_ATTEMPTS` and `OUTBOX_RELAY_RETRY_BACKOFF` control retry and
  dead-event behavior.
- `OUTBOX_RELAY_CLEANUP_*` and `OUTBOX_RELAY_PUBLISHED_RETENTION` control cleanup
  of published events.
- `OUTBOX_RELAY_IDEMPOTENCY_CLEANUP_MAX_BATCHES` bounds shared-ledger cleanup per
  cycle. The default ten batches of 1,000 rows can drain about 960,000 expired
  records per day at the default 15-minute interval while keeping each cycle
  bounded.
- `OUTBOX_RELAY_WORKFLOW_ENABLED`, `OUTBOX_RELAY_JOBS_ENABLED`, and
  `OUTBOX_RELAY_ANALYTICS_ENABLED` can disable topic groups when needed.

If relay throughput is low, inspect Kafka publish latency and PostgreSQL query
latency before reducing the poll interval.

Client and random command results expire after 24 hours. Automatic scheduling,
notification creation, and deterministic cancellation use
`COMMAND_IDEMPOTENCY_EVENT_RETENTION`, defaulting to `336h` with a hard minimum
of `168h`. Configure it no shorter than the longest Kafka retention,
published-outbox redrive window, or supported manual event-redrive window.
Automatic jobs and notifications retain their deterministic keys in domain
rows, so an exact replay after ledger expiry reconstructs the ledger without a
duplicate resource; changed payloads still conflict. Cancellation remains
effect-idempotent after expiry, but its original cleanup snapshot is replayable
only during this configured window. Workflow terminal-effect identities are
removed with their owning workflow rather than by time-based ledger cleanup.

### Logs and Search

- `JOBLOGS_PROCESSOR_BATCH_JOB_LOGS_SIZE_LIMIT`
- `JOBLOGS_PROCESSOR_BATCH_JOB_LOGS_TIME_INTERVAL`
- ClickHouse connection pool settings.
- Meilisearch capacity and master key configuration.

Large log volumes should be matched with enough Kafka `job_logs` partitions and
ClickHouse insertion capacity.

### Analytics Cleanup

- `ANALYTICS_PROCESSOR_CLEANUP_ENABLED`
- `ANALYTICS_PROCESSOR_CLEANUP_INTERVAL`
- `ANALYTICS_PROCESSOR_CLEANUP_BATCH_SIZE`
- `ANALYTICS_PROCESSED_EVENTS_RETENTION`

Processed-event retention supports replay dedupe. Avoid setting it too low for
the longest replay window you expect to tolerate.

## Troubleshooting

### TLS or Certificate Failures

- Confirm `init-certs` completed successfully.
- Confirm the `certs` volume exists and is mounted by the failing service.
- For gRPC services, verify `GRPC_TLS_*` paths and client `*_SERVICE_TLS_CA_FILE`
  paths match the generated files.
- For Kafka, inspect the generated keystore/truststore files in the Kafka certs
  mount.
- For Docker proxy failures, verify the dedicated `docker-proxy-certs` role
  mount in Compose or the `docker-proxy-ca`/`server`/matching `client-*` Secret
  in Kubernetes. A `403` means token or role authorization failed; a TLS alert
  means the issuer, EKU, hostname, or mounted keypair failed authentication.
- Compose clients mount their role at `/docker-proxy-certs`, not below the
  read-only `/certs` mount. Run `make compose/validate` to detect invalid nested
  mount targets and incomplete role-specific TLS wiring.

For local development, recreating the stack and cert volume can clear stale
certificate state:

```sh
docker compose -f compose.dev.yaml down -v
docker compose -f compose.dev.yaml up -d
```

This deletes local data volumes.

For Kubernetes production, verify that `chronoverse-auth`, `chronoverse-ca`,
`chronoverse-client-tls`, `chronoverse-service-tls`, and
`chronoverse-kafka-tls` exist in the `chronoverse` namespace and contain the
expected keys. Also verify the atomic Docker proxy set: `docker-proxy-ca`,
`docker-proxy-server`, and all three `docker-proxy-client-*` Secrets.

### Kubernetes Readiness Problems

- Run `kubectl -n chronoverse get pods,job` and find the first failing pod or
  incomplete Job.
- Inspect `init-kafka-topics` and `database-migration` before application logs.
- Confirm the production StorageClass, Ingress hostname/TLS certificate,
  `SERVER_HOST_URL`, `SERVER_FRONTEND_URL`, allowed origins, credentials, and
  generated or operator-provided Secrets match the target cluster.
- Confirm Docker-capable nodes expose Docker Engine at `/var/run/docker.sock`
  and have the `chronoverse.io/docker-workloads=true` label.
- Confirm workers can reach runtime node IPs on TCP `2376` (`hostPort` bypasses
  `NetworkPolicy` — restrict `2376` at infra layer); the proxy requires
  `docker-proxy-ca`/`server`/`client-*` Secrets plus `docker-proxy-auth`
  `DOCKER_PROXY_TOKEN` (mTLS `verify required` + token second factor — missing
  or mismatched material fails closed and workers cannot ping `127.0.0.1:2376`
  or `$(NODE_IP):2376`).
- In kind, recreate the cluster with
  `infra/k8s/overlays/local/kind-cluster.yaml` if `docker-proxy` reports
  `/var/run/docker.sock is not a socket file`.
- If `kubectl config current-context` returns `docker-desktop`, switch to the
  `kind-chronoverse` context or another Docker-capable cluster.

### Compose Readiness Problems

- Run `docker compose -f compose.dev.yaml ps` and find the first unhealthy or
  restarting dependency.
- Inspect dependency logs before application logs.
- Check that database migration completed before services started.
- Check `init-kafka-topics` output if workers are not receiving events.

### Missing Logs

Expected no-log cases:

- `HEARTBEAT` workflows do not produce logs.
- Workflows with `log_retention=false` do not persist searchable/downloadable
  logs.
- Retained log read/search endpoints return `412 Precondition Failed` when
  retention is disabled; live SSE streams report the same condition as
  `event: error`.

Unexpected no-log cases:

- Confirm the job is a `CONTAINER` workflow.
- Confirm the workflow has `log_retention=true`.
- Confirm `joblogs-processor` is running.
- Check ClickHouse health and Meilisearch health.
- Inspect `job_logs` Kafka topic activity.

### SSE Live Logs Do Not Stream

- Development clients should call
  `/workflows/{workflow_id}/jobs/{job_id}/events` on the API origin.
- Production clients should call
  `/api/workflows/{workflow_id}/jobs/{job_id}/events` through Nginx.
- Confirm the job is still `RUNNING`; the stream endpoint is intended for live
  running jobs.
- Confirm the proxy path disables buffering. The included production Nginx config
  has a dedicated SSE location.

### Duplicate or Stale Events

Chronoverse is designed to tolerate replay. Check these areas before treating a
duplicate Kafka event as data corruption:

- Mutation clients should reuse the same `Idempotency-Key` only for retries of
  the same action.
- `outbox-relay` should be running so committed outbox rows are published.
- Workflow generation mismatch errors usually mean a stale event was ignored.
- Job lease precondition failures usually mean another worker owns the job or
  the lease expired.
- Analytics and log processors dedupe deterministic event IDs and processed
  event records.

### Dashboard Cannot Reach API

- Development dashboard builds with `NEXT_PUBLIC_API_URL=http://localhost:8080`.
- Production should use the Nginx `/api` proxy.
- Check `SERVER_ALLOWED_ORIGINS` when browser CORS requests fail.
- Confirm session and CSRF cookies are scoped to the expected host and same-site
  mode.
