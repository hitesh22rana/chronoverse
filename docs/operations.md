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
- LGTM: `http://localhost:3000`
- gRPC: `localhost:50051` through `localhost:50055`

Development builds local service images and exposes internal infrastructure
ports for debugging.

### Production

```sh
export CRYPTO_SECRET="$(openssl rand -hex 16)"
export SERVER_CSRF_HMAC_SECRET="$(openssl rand -hex 32)"
docker compose -f compose.prod.yaml up -d
```

Persist these two distinct values in your secret manager and reuse them across
server restarts.

Useful endpoints:

- Dashboard: `http://localhost`
- API through Nginx: `http://localhost/api/...`
- LGTM: `http://localhost:3000`

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
Meilisearch, application services, workers, runtime-agent, and CPU/memory
HorizontalPodAutoscalers. Production autoscaling requires metrics-server or an
equivalent `autoscaling/v2` resource metrics provider.

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
Docker-capable node. Official overlays register `tcp://$(NODE_IP):2375` so
running job cleanup survives proxy pod restarts on the same node. Multi-node
kind and other Docker-container-based Kubernetes emulators can have a specific
hostPort routing limitation where pods on one emulator node cannot reach another
emulator node's host port; if you choose that topology, use a pod-IP endpoint
override as an emulator-only workaround. `workflow-worker` and
`execution-worker` do not need the Docker node label; they can schedule anywhere
with network access to TCP `2375` on registered runtime endpoints.

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

1. `init-certs` generates the local CA, service certificates, client
   certificates, Kafka keystore/truststore files, and auth keys.
2. PostgreSQL, ClickHouse, Redis, Meilisearch, Kafka, LGTM, and Docker proxy
   start with TLS-enabled configuration.
3. `init-kafka-topics` creates or expands Kafka topics.
4. `init-database-migration` applies PostgreSQL migrations, ClickHouse
   migrations, and Meilisearch index setup.
5. gRPC services start after migrations and their dependencies are healthy.
6. Workers start after dependent services and topics are ready.
7. The dashboard starts after backend services.
8. Production Nginx starts after dashboard and server.

Kubernetes does not provide Compose-style dependency ordering for long-running
Deployments. The overlays include explicit Jobs for certificate bootstrap
where local, Kafka topic initialization, and database migration. Application
containers are expected to retry transient dependency failures, while readiness
and liveness probes decide when pods receive traffic or are restarted. The local
overlay uses init containers only for prerequisites that must exist before the
process starts, such as generated certificate files and hostPath permissions.
Operators should still inspect bootstrap Jobs before scaling application
workloads.

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
make test
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
- `make test` runs Go tests with the race detector.
- `make build/all` builds all Go services and workers, including `outbox-relay`.

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
but should not be treated as a substitute for capacity planning.

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
- `OUTBOX_RELAY_WORKFLOW_ENABLED`, `OUTBOX_RELAY_JOBS_ENABLED`, and
  `OUTBOX_RELAY_ANALYTICS_ENABLED` can disable topic groups when needed.

If relay throughput is low, inspect Kafka publish latency and PostgreSQL query
latency before reducing the poll interval.

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
expected keys.

### Kubernetes Readiness Problems

- Run `kubectl -n chronoverse get pods,job` and find the first failing pod or
  incomplete Job.
- Inspect `init-kafka-topics` and `database-migration` before application logs.
- Confirm the production StorageClass, Ingress hostname/TLS certificate,
  `SERVER_HOST_URL`, `SERVER_FRONTEND_URL`, allowed origins, credentials, and
  generated or operator-provided Secrets match the target cluster.
- Confirm Docker-capable nodes expose Docker Engine at `/var/run/docker.sock`
  and have the `chronoverse.io/docker-workloads=true` label.
- Confirm workers can reach runtime node IPs on TCP `2375`; NetworkPolicy,
  node firewall rules, or security groups may block hostPort traffic.
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
