# Configuration

Chronoverse configuration is environment-variable driven. The compose files
provide working local defaults, but production deployments should replace
default credentials, keys, hostnames, retention settings, and resource sizing.

Kubernetes uses the same environment variables through Kustomize-generated
ConfigMaps and Kubernetes Secrets under `infra/k8s`.

## Compose Profiles

### Development: `compose.dev.yaml`

Development builds local images and exposes internal ports for debugging:

| Component | Host port | Notes |
| --- | ---: | --- |
| Dashboard | `3001` | Next.js dashboard, built with `NEXT_PUBLIC_API_URL=http://localhost:8080` |
| HTTP API | `8080` | Direct server access |
| OTLP gRPC | `4317` | OpenTelemetry collector endpoint |
| LGTM Grafana | — | Not host-published by default; use the loopback override or Kubernetes port-forward described in [Grafana access and Compose upgrades](#grafana-access-and-compose-upgrades). Anonymous access is disabled; development credentials default to `admin` / `chronoverse-local-grafana-password` |
| PostgreSQL | `5432` | TLS-enabled database |
| ClickHouse | `9440` | Secure native protocol |
| Redis | `6379` | TLS-enabled Redis |
| Meilisearch | `7700` | HTTPS |
| Kafka | `9094` | SSL broker listener |
| Kafka controller | `9093` | Controller listener |
| gRPC services | `50051`-`50055` | Users, workflows, jobs, notifications, analytics |

Development Kafka topic partition defaults are:

- `KAFKA_WORKFLOWS_TOPIC_PARTITIONS=2`
- `KAFKA_JOBS_TOPIC_PARTITIONS=2`
- `KAFKA_JOB_LOGS_TOPIC_PARTITIONS=2`
- `KAFKA_ANALYTICS_TOPIC_PARTITIONS=2`

### Production: `compose.prod.yaml`

Production uses published `ghcr.io/hitesh22rana/chronoverse/*:latest` images,
internal service networking, resource limits, and replicated workers. Host
exposure is intentionally small:

| Component | Host port | Notes |
| --- | ---: | --- |
| Nginx | `80` | Dashboard and `/api/...` reverse proxy |
| LGTM Grafana | — | Not host-published by default. Access via the opt-in Compose loopback override or `kubectl port-forward`; anonymous access is disabled and production credentials come from `GF_SECURITY_ADMIN_PASSWORD` (Compose, required) or `grafana-secret` (Kubernetes) |

### Grafana access and Compose upgrades

Compose keeps Grafana private by default. To expose the UI only on the Docker
host loopback interface, recreate `lgtm` with the opt-in override:

```sh
docker compose -f compose.dev.yaml -f compose.grafana.yaml up -d lgtm
# Production uses the same override and requires the production environment:
docker compose -f compose.prod.yaml -f compose.grafana.yaml up -d lgtm
```

The host port defaults to `3000`; set `GRAFANA_HOST_PORT` to choose another.
Kubernetes uses
`kubectl -n chronoverse port-forward svc/lgtm 3000:3000` instead.

Grafana applies `GF_SECURITY_ADMIN_PASSWORD` only when it first creates its
database. When upgrading an existing Compose `lgtm:/data` volume, first
recreate the container from the updated Compose file. This detaches the old
`/otel-lgtm` application volume so the pinned image supplies its hardened
startup script and binaries. Then run the repository helper, which resets the
database password from the container's configured
`GF_SECURITY_ADMIN_PASSWORD`, migrates a legacy stored admin login to the
configured `GF_SECURITY_ADMIN_USER`, and verifies anonymous `401` plus
authenticated `200` responses:

```sh
docker compose -f compose.dev.yaml up -d --force-recreate --no-deps lgtm
# For production, use compose.prod.yaml with its required environment instead.
scripts/grafana/reset-admin-password.sh
```

The helper assumes an old login of `admin` when the configured username does
not already authenticate. If an existing database uses another login and you
are changing it, supply that stored login explicitly:

```sh
GRAFANA_CURRENT_ADMIN_USER=old-login scripts/grafana/reset-admin-password.sh
```

Set `GRAFANA_CONTAINER` when the container is not named `lgtm`. Passwords are
fed to Grafana through standard input, so values beginning with `-` are not
parsed as CLI flags. Do not remove the `lgtm` volume as an authentication
migration: `/data` contains the Grafana database and dashboards together with
persisted Prometheus metrics, Loki logs, Tempo traces, and Pyroscope profiles.

Production Kafka topic partition defaults are:

- `KAFKA_WORKFLOWS_TOPIC_PARTITIONS=2`
- `KAFKA_JOBS_TOPIC_PARTITIONS=4`
- `KAFKA_JOB_LOGS_TOPIC_PARTITIONS=4`
- `KAFKA_ANALYTICS_TOPIC_PARTITIONS=2`

Workers are configured with compose resource reservations and two replicas for
the low/mid/high-resource worker groups.

| Worker group | Services | Limit | Reservation |
| --- | --- | --- | --- |
| Low | `scheduling-worker`, `analytics-processor`, `outbox-relay` | `0.25` CPU, `512M` memory | `0.1` CPU, `256M` memory |
| Mid | `workflow-worker`, `joblogs-processor` | `0.5` CPU, `2G` memory | `0.25` CPU, `1G` memory |
| High | `execution-worker` | `2` CPU, `2G` memory | `1` CPU, `1G` memory |

## Kubernetes Overlays

`infra/k8s` is organized as Kustomize:

- `base/` contains application services, workers, dashboard, Nginx, Docker
  proxy, PgBouncer, RBAC, network policy, PodDisruptionBudgets, Kafka topic
  initialization, KEDA consumer scaling, and shared ConfigMaps.
- `overlays/local/` adds in-cluster PostgreSQL, Redis, ClickHouse, Kafka,
  Meilisearch, LGTM, hostPath storage, and certificate bootstrap jobs.
- `overlays/production/` deploys self-hosted PostgreSQL, Redis, ClickHouse,
  Kafka, Meilisearch, runtime-agent, dynamic PVCs, service HPAs, production
  topic partitions, and production KEDA ceilings.

Common commands:

```sh
kubectl kustomize infra/k8s/overlays/local
kubectl kustomize infra/k8s/overlays/production
scripts/k8s/setup.sh --mode local
scripts/k8s/setup.sh --mode production
```

The setup script preserves valid, complete pre-created Secrets and generates missing
bootstrap material. Patch public URLs and allowed origins for your deployment
before exposing the HTTP entrypoint.

## Core Environment Groups

Every Go process accepts `ENV`; its code default is `development`. Compose and
Kubernetes set it explicitly for the selected topology. Treat it as a runtime
mode label rather than a substitute for configuring credentials, TLS, public
origins, or storage.

### Server

The public HTTP server reads:

- `SERVER_HOST`, `SERVER_PORT`
- `SERVER_REQUEST_TIMEOUT`, `SERVER_READ_TIMEOUT`,
  `SERVER_READ_HEADER_TIMEOUT`, `SERVER_IDLE_TIMEOUT`
- `SERVER_REQUEST_BODY_LIMIT`
- `SERVER_SESSION_EXPIRY`, `SERVER_CSRF_EXPIRY`,
  `SERVER_CSRF_HMAC_SECRET`
- `SERVER_HOST_URL`
- `SERVER_ALLOWED_ORIGINS`
- `SERVER_SAME_SITE_MODE`
- `CRYPTO_SECRET`

In development the dashboard calls the server directly. In production, Nginx
proxies `/api/` to the server and rewrites the path before forwarding.

### gRPC Servers and Clients

Each gRPC service uses:

- `GRPC_HOST`, `GRPC_PORT`, `GRPC_REQUEST_TIMEOUT`
- `GRPC_TLS_ENABLED`, `GRPC_TLS_CA_FILE`, `GRPC_TLS_CERT_FILE`,
  `GRPC_TLS_KEY_FILE`

Service clients use:

- `USERS_SERVICE_HOST`, `USERS_SERVICE_PORT`, `USERS_SERVICE_TLS_ENABLED`,
  `USERS_SERVICE_TLS_CA_FILE`
- `WORKFLOWS_SERVICE_HOST`, `WORKFLOWS_SERVICE_PORT`,
  `WORKFLOWS_SERVICE_TLS_ENABLED`, `WORKFLOWS_SERVICE_TLS_CA_FILE`
- `JOBS_SERVICE_HOST`, `JOBS_SERVICE_PORT`, `JOBS_SERVICE_TLS_ENABLED`,
  `JOBS_SERVICE_TLS_CA_FILE`
- `NOTIFICATIONS_SERVICE_HOST`, `NOTIFICATIONS_SERVICE_PORT`,
  `NOTIFICATIONS_SERVICE_TLS_ENABLED`, `NOTIFICATIONS_SERVICE_TLS_CA_FILE`
- `ANALYTICS_SERVICE_HOST`, `ANALYTICS_SERVICE_PORT`,
  `ANALYTICS_SERVICE_TLS_ENABLED`, `ANALYTICS_SERVICE_TLS_CA_FILE`
- `CLIENT_TLS_CERT_FILE`, `CLIENT_TLS_KEY_FILE` for mTLS client identity.

### PostgreSQL

Common settings:

- `POSTGRES_HOST`, `POSTGRES_PORT`, `POSTGRES_USER`, `POSTGRES_PASSWORD`,
  `POSTGRES_DB`
- `POSTGRES_MAX_CONNS`, `POSTGRES_MIN_CONNS`,
  `POSTGRES_MAX_CONN_LIFE`, `POSTGRES_MAX_CONN_IDLE`,
  `POSTGRES_DIAL_TIMEOUT`
- `POSTGRES_TLS_ENABLED`, `POSTGRES_TLS_CA_FILE`,
  `POSTGRES_TLS_CERT_FILE`, `POSTGRES_TLS_KEY_FILE`

PostgreSQL holds transactional state, idempotency records, job leases, and
outbox events. In Kubernetes, `POSTGRES_HOST=postgres` addresses PgBouncer and
`postgres-primary` addresses PostgreSQL directly for role bootstrap. Workload
ConfigMaps set explicit two- or four-connection maxima and allow a zero idle
minimum. Keep TLS and credential values environment-specific outside local
development.

### ClickHouse

Common settings:

- `CLICKHOUSE_HOSTS`, `CLICKHOUSE_DATABASE`, `CLICKHOUSE_USERNAME`,
  `CLICKHOUSE_PASSWORD`
- `CLICKHOUSE_MAX_OPEN_CONNS`, `CLICKHOUSE_MAX_IDLE_CONNS`,
  `CLICKHOUSE_CONN_MAX_LIFETIME`, `CLICKHOUSE_DIAL_TIMEOUT`
- `CLICKHOUSE_TLS_ENABLED`, `CLICKHOUSE_TLS_CA_FILE`,
  `CLICKHOUSE_TLS_CERT_FILE`, `CLICKHOUSE_TLS_KEY_FILE`

ClickHouse stores retained job logs. Analytics are stored in PostgreSQL, while
ClickHouse is only used for job logs when workflow log retention is enabled.

### Redis

Common settings:

- `REDIS_HOST`, `REDIS_PORT`, `REDIS_PASSWORD`, `REDIS_DB`
- `REDIS_POOL_SIZE`, `REDIS_MIN_IDLE_CONNS`
- `REDIS_READ_TIMEOUT`, `REDIS_WRITE_TIMEOUT`
- `REDIS_MAX_MEMORY`, `REDIS_EVICTION_POLICY`,
  `REDIS_EVICTION_POLICY_SAMPLE_SIZE`
- `REDIS_TLS_ENABLED`, `REDIS_TLS_CA_FILE`, `REDIS_TLS_CERT_FILE`,
  `REDIS_TLS_KEY_FILE`

Redis is used for sessions, cached reads, and live log stream state. Production
compose sets `REDIS_MAX_MEMORY=${REDIS_MAX_MEMORY:-768mb}` on every Redis
client process so one late-starting service does not reset Redis back to the
code default of `100mb`.

### Meilisearch

Common settings:

- `MEILISEARCH_URI`
- `MEILISEARCH_MASTER_KEY`
- `MEILISEARCH_TLS_ENABLED`, `MEILISEARCH_TLS_CA_FILE`,
  `MEILISEARCH_TLS_CERT_FILE`, `MEILISEARCH_TLS_KEY_FILE`

The compose defaults include a development master key. Replace it for any
shared or production environment.

### Kafka

Application services and workers use:

- `KAFKA_BROKERS`
- `KAFKA_CONSUMER_GROUP`
- `KAFKA_TLS_ENABLED`, `KAFKA_TLS_CA_FILE`, `KAFKA_TLS_CERT_FILE`,
  `KAFKA_TLS_KEY_FILE`

Topic initialization uses:

- `KAFKA_TOPIC_REPLICATION_FACTOR`
- `KAFKA_WORKFLOWS_TOPIC_PARTITIONS`
- `KAFKA_JOBS_TOPIC_PARTITIONS`
- `KAFKA_JOB_LOGS_TOPIC_PARTITIONS`
- `KAFKA_ANALYTICS_TOPIC_PARTITIONS`

Kafka auto topic creation is disabled in compose. `init-kafka-topics` creates or
expands the expected topics: `workflows`, `jobs`, `job_logs`, and `analytics`.
The Kubernetes overlays include the same topic initializer.

## Domain and Worker Settings

### Workflows Service

- `WORKFLOWS_SERVICE_CONFIG_FETCH_LIMIT`

Shared command-ledger cleanup is owned by the outbox relay and is independent
from published-outbox cleanup; logically expired keys can be replaced even if
cleanup is delayed.

### Jobs Service

- `JOBS_SERVICE_CONFIG_FETCH_LIMIT`
- `JOBS_SERVICE_CONFIG_LOGS_FETCH_LIMIT`
- `JOBS_SERVICE_RUNTIME_HEARTBEAT_TTL`
- `JOBS_SERVICE_RUNTIME_LOST_AFTER`
- `COMMAND_IDEMPOTENCY_EVENT_RETENTION`

The jobs service also needs workflows-service client settings so log endpoints
can enforce workflow retention policy. Runtime heartbeat settings control which
`runtime_nodes` are fresh enough for new container claims and when an expired
lease should be treated as owned by an unavailable runtime.

### Notifications Service

- `NOTIFICATIONS_SERVICE_CONFIG_FETCH_LIMIT`
- `COMMAND_IDEMPOTENCY_EVENT_RETENTION`

This caps the fetch size used by the notification list operation.

Client and random commands remain replayable for 24 hours. Automatic scheduling,
notification creation, and deterministic job cancellation use
`COMMAND_IDEMPOTENCY_EVENT_RETENTION`, which defaults to `336h` and must be at
least `168h`. Set it to at least the longest Kafka retention, published-outbox
redrive window, or supported manual event-redrive window, whichever is greater.

### Execution Worker Replay Safety

- `EXECUTION_WORKER_AWAITING_RECONCILIATION_LIMIT`

`0` uses normalized executor concurrency. A positive value must be at least
executor concurrency or the worker fails during startup. The bound applies to
claiming, active, and ambiguous handoffs; when full, Kafka receives retryable
backpressure instead of starting another workload.

### Runtime Agent

- `RUNTIME_AGENT_ID`
- `RUNTIME_AGENT_NODE_NAME`
- `RUNTIME_AGENT_DOCKER_ENDPOINT`
- `RUNTIME_AGENT_DOCKER_HEALTH_ENDPOINT`
- `RUNTIME_AGENT_DOCKER_ADVERTISE_HOST`
- `RUNTIME_AGENT_DOCKER_ADVERTISE_PORT` (default `2376`)
- `RUNTIME_AGENT_HEARTBEAT_INTERVAL`
- `RUNTIME_AGENT_MAX_CONCURRENCY`
- `DOCKER_HOST`

`runtime-agent` pings its local Docker endpoint, upserts a `READY` row into
PostgreSQL, then heartbeats Docker endpoint health and capacity. In Compose
there is one runtime named `local-docker` pointing at `tcp://docker-proxy:2376`.
In Kubernetes, run one agent as a sidecar beside each node-local Docker proxy
(`DaemonSet` on `chronoverse.io/docker-workloads=true` nodes) and register a
node-stable endpoint on `NODE_IP:2376` via `hostPort: 2376` — not a pod IP or
load-balanced `tcp://docker-proxy:2376` `ClusterIP`. The DaemonSet is per-node by design;
a `ClusterIP` would load-balance to a random backend and break the invariant
that `ClaimJob` (`internal/repository/jobs/lease.go:254`) selects one `runtime_nodes`
row and workers later dial its stored `runtime_endpoint` (`internal/repository/executor/executor.go:682`,
`internal/repository/jobs/lease.go:1063` recovery). The DaemonSet's HAProxy
binds `:2376 ssl crt /certs/docker-proxy/server.pem ca-file /certs/docker-proxy/ca.crt verify required`
plus the `X-Chronoverse-Docker-Proxy-Token` header allowlist and an exact
Docker method/path allowlist before forwarding to the host socket. Runtime-agent
health probes `tcp://127.0.0.1:2376` (loopback, no `hostPort` needed); the
advertised endpoint is constructed from
`RUNTIME_AGENT_DOCKER_ADVERTISE_HOST`/`PORT` with IPv6-safe brackets. Workers
and the agent use a bounded custom Docker HTTP transport with mTLS
(`DOCKER_PROXY_TLS_CA_FILE`/`CERT_FILE`/`KEY_FILE`, `ServerName docker-proxy`)
plus the token second factor. The CA bundle and client keypair are reloaded on
each new TLS handshake so staged certificate rotation does not leave cached
clients pinned to old files.

When mTLS is configured, the shared Docker client also normalizes a legacy
`tcp://<same-host>:2375` endpoint to
`tcp://<same-host>:2376`. The exact DNS name, IPv4 address, or bracketed IPv6
address is preserved. Runtime-agent applies this to its configured health
endpoint, and workers normalize persisted endpoints before the bounded
endpoint-cache lookup so old and new snapshots share one client. This is a
no-op when mTLS is not configured, for Unix sockets, and for every other port.
It lets jobs and workflow command snapshots created before the `2376`
migration finish without a database rewrite in Compose or Kubernetes.

Certificate identity is also authorization, not just authentication:

| Certificate role | Allowed Docker API operations |
| --- | --- |
| `runtime-agent` | ping and version only |
| `workflow-worker` | ping/version, image inspect/pull, and container logs/stop/delete for cancellation cleanup |
| `execution-worker` | workflow operations plus container inspect/create/start/wait and network inspect/create |

Unknown client subjects and cross-role operations are denied before the Docker
socket. Workload containers receive neither token nor certificates. The
execution-worker identity remains highly privileged by necessity: Docker
container creation against a host daemon is node-root-equivalent if that
identity and token are compromised. Keep execution workers inside the trusted
control plane and use rootless/isolated runtimes when the threat model requires
a smaller blast radius. The workflow role cannot create containers, but its
log/stop/delete cleanup calls are not tenant-scoped by HAProxy; a compromised
workflow identity can affect a known container ID on any reachable runtime.
Per-container authorization requires a purpose-built broker rather than direct
Docker API access. `hostPort` bypasses `NetworkPolicy`, so restrict TCP `2376`
at the infrastructure layer (node firewall / security group / CNI host policy).
Multi-node kind and similar Docker-container-based Kubernetes emulators can
make node host ports reachable only from pods on the same emulator node. In that
specific topology, use a pod-IP endpoint override as an emulator workaround; do
not use pod-IP runtime endpoints for real Kubernetes clusters where node IPs are
routable.
`RUNTIME_AGENT_ID` must be stable for the lifetime of that runtime node. Derive
it from durable node identity, such as the Kubernetes node name, hostname, or a
mounted identity file; do not generate a new random ID on every restart.
`last_heartbeat_at` is the last successful Docker-health heartbeat. If Docker
becomes unavailable while PostgreSQL is reachable, the agent marks the runtime
`UNHEALTHY` without refreshing that timestamp; a successful unhealthy update
stops new container claims immediately. If PostgreSQL is unavailable, the agent
exits and existing heartbeat TTL behavior applies. The agent marks itself
`DRAINING` on graceful shutdown; missed heartbeats make it ineligible for new
container job claims.

### Scheduling Worker

- `SCHEDULING_WORKER_POLL_INTERVAL`
- `SCHEDULING_WORKER_CONTEXT_TIMEOUT`
- `SCHEDULING_WORKER_BATCH_SIZE`

The batch size controls how many due workflows are scanned per polling pass.

### Workflow Worker

- `WORKFLOW_WORKER_IMAGE_PULL_LOCK_TTL`
- `WORKFLOW_WORKER_IMAGE_PULL_LOCK_WAIT_TIMEOUT`
- `WORKFLOW_WORKER_IMAGE_PULL_LOCK_RETRY_INTERVAL`

These settings coordinate Docker image pulls for replicated workflow workers
that share a runtime node. The lock is scoped by runtime node and exact image
string; Docker host is used as a fallback when a request omits an explicit
runtime scope. Compose defaults are `10m`, `10m`, and `500ms`.
Workflow workers do not
launch workload containers, so `EXECUTION_WORKER_WORKLOAD_CONTAINER_*` limits do
not apply to this image-pull path. For `CONTAINER` workflows, successful build
stores resolved image reference and digest as derived workflow metadata; the
payload remains user-authored configuration.

### Execution Worker

- `EXECUTION_WORKER_ID`
- `EXECUTION_WORKER_CONCURRENCY`
- `EXECUTION_WORKER_WORKLOAD_CONTAINER_MEMORY`
- `EXECUTION_WORKER_WORKLOAD_CONTAINER_CPUS`
- `EXECUTION_WORKER_WORKLOAD_CONTAINER_PIDS_LIMIT`
- `EXECUTION_WORKER_LEASE_DURATION`
- `EXECUTION_WORKER_LEASE_RENEW_INTERVAL`
- `EXECUTION_WORKER_SYSTEM_RETRY_LIMIT`
- `EXECUTION_WORKER_SYSTEM_RETRY_BACKOFF`
- `EXECUTION_WORKER_RECOVERY_INTERVAL`
- `EXECUTION_WORKER_RECOVERY_BATCH_SIZE`
- `EXECUTION_WORKER_JOB_LOG_BATCH_SIZE`
- `EXECUTION_WORKER_JOB_LOG_BATCH_INTERVAL`
- `EXECUTION_WORKER_JOB_LOG_PUBLISH_TIMEOUT`
- `EXECUTION_WORKER_JOB_LOG_PUBLISH_RETRIES`
- `EXECUTION_WORKER_JOB_LOG_PUBLISH_BACKOFF`
- `EXECUTION_WORKER_JOB_LOG_LIVE_TIMEOUT`
- `EXECUTION_WORKER_JOB_LOG_LIVE_BUFFER_SIZE`
- `EXECUTION_WORKER_IMAGE_PULL_LOCK_TTL`
- `EXECUTION_WORKER_IMAGE_PULL_LOCK_WAIT_TIMEOUT`
- `EXECUTION_WORKER_IMAGE_PULL_LOCK_RETRY_INTERVAL`

If `EXECUTION_WORKER_ID` is empty, the worker falls back to the container
hostname. `EXECUTION_WORKER_CONCURRENCY=0` means auto concurrency from
`GOMAXPROCS`, which is adjusted by `automaxprocs` from the worker container CPU
quota. Workload container memory, CPU, and PID settings apply to the Docker
containers launched by the worker; they are separate from the worker process
resource limit and from image pulls. Keep lease duration longer than the
renewal interval. Container execution uses the runtime endpoint
returned by `ClaimJob`, not the worker pod's own Docker host. Before creating a
container, the worker ensures the resolved image exists on that runtime daemon
under a Redis lock scoped to runtime node plus exact image string.

### Job Logs Processor

- `JOBLOGS_PROCESSOR_BATCH_JOB_LOGS_SIZE_LIMIT`
- `JOBLOGS_PROCESSOR_BATCH_JOB_LOGS_TIME_INTERVAL`

Tune these values together with ClickHouse and Meilisearch capacity.

### Analytics Processor

- `ANALYTICS_PROCESSOR_CLEANUP_ENABLED`
- `ANALYTICS_PROCESSOR_CLEANUP_INTERVAL`
- `ANALYTICS_PROCESSOR_CLEANUP_BATCH_SIZE`
- `ANALYTICS_PROCESSED_EVENTS_RETENTION`

Processed-event retention supports replay-safe analytics dedupe while limiting
metadata growth.

### Outbox Relay

- `OUTBOX_RELAY_WORKFLOW_ENABLED`
- `OUTBOX_RELAY_JOBS_ENABLED`
- `OUTBOX_RELAY_ANALYTICS_ENABLED`
- `OUTBOX_RELAY_BATCH_SIZE`
- `OUTBOX_RELAY_POLL_INTERVAL`
- `OUTBOX_RELAY_CONTEXT_TIMEOUT`
- `OUTBOX_RELAY_MAX_ATTEMPTS`
- `OUTBOX_RELAY_RETRY_BACKOFF`
- `OUTBOX_RELAY_PROCESSING_LEASE`
- `OUTBOX_RELAY_WORKER_ID`
- `OUTBOX_RELAY_CLEANUP_ENABLED`
- `OUTBOX_RELAY_CLEANUP_INTERVAL`
- `OUTBOX_RELAY_CLEANUP_BATCH_SIZE`
- `OUTBOX_RELAY_PUBLISHED_RETENTION`
- `OUTBOX_RELAY_IDEMPOTENCY_CLEANUP_MAX_BATCHES`

Compose sets `OUTBOX_RELAY_WORKER_ID` from the hostname when not provided. Keep
the processing lease longer than normal Kafka publish latency. Idempotency
cleanup deletes up to the configured number of batches per cleanup cycle and
stops early after a partial batch. The default is ten batches of 1,000 rows.

## Dashboard Settings

`NEXT_PUBLIC_API_URL` is injected at build time for the dashboard image.

- Development uses `http://localhost:8080`.
- Production uses the Nginx `/api` proxy in the generated Nginx config. If you
  build a custom dashboard image, set `NEXT_PUBLIC_API_URL` to the public API
  origin/path that browser clients should call.

## Secrets and Certificates

`init-certs` generates local certificates and auth keys under the shared `certs`
volume. This is convenient for compose-based development and demos, but
production environments should manage:

- PostgreSQL, ClickHouse, Redis, Meilisearch, Kafka, and gRPC certificates.
- Ed25519 auth key material (`auth.ed` and `auth.ed.pub`).
- `CRYPTO_SECRET` and `SERVER_CSRF_HMAC_SECRET`.
- Database passwords and `MEILISEARCH_MASTER_KEY`.
- Public hostnames, allowed origins, and same-site cookie policy.

Production startup rejects an empty CSRF HMAC secret, the known development
placeholder, and deployments that reuse one value for `CRYPTO_SECRET` and
`SERVER_CSRF_HMAC_SECRET`. Persist distinct random values in your secret
manager. The production Compose profile requires both variables explicitly.

Kubernetes production deployments may pre-create these Secrets to own their
credentials and trust material. `scripts/k8s/setup.sh` preserves valid, complete
operator-provided Secrets and generates missing bootstrap material; it rejects
partial Secrets, insecure server secret placeholders, reused server secrets,
and partial internal TLS trust chains:

- `postgres-secret`: `POSTGRES_USER`, `POSTGRES_PASSWORD`, `POSTGRES_DB`
- `postgres-app-secret`: the dedicated `chronoverse_app` non-superuser identity
  used by application workloads through PgBouncer
- `clickhouse-secret`: `CLICKHOUSE_PASSWORD`
- `meilisearch-secret`: `MEILISEARCH_MASTER_KEY`, `MEILI_MASTER_KEY`
- `kafka-tls-secret`: Kafka keystore, truststore, and key passwords
- `chronoverse-server-security`: `CRYPTO_SECRET`, `SERVER_CSRF_HMAC_SECRET`
- `chronoverse-auth`: `auth.ed`, `auth.ed.pub`
- `chronoverse-ca`: `ca.crt`
- `chronoverse-ingress-tls`: `tls.crt`, `tls.key`
- `chronoverse-client-tls`: `tls.crt`, `tls.key`
- `chronoverse-service-tls`: per-service gRPC certificate and key files
- `chronoverse-infra-tls`: PostgreSQL, Redis, ClickHouse, Kafka, and Meilisearch certificate/key pairs
- `chronoverse-kafka-tls`: Kafka keystore, truststore, and credential files
- `grafana-secret`: `GF_SECURITY_ADMIN_USER`, `GF_SECURITY_ADMIN_PASSWORD` (Grafana admin credentials; `setup.sh` generates for both local and production, preserves operator-provided values; compose production requires `GF_SECURITY_ADMIN_PASSWORD` via env)
- `docker-proxy-auth`: `DOCKER_PROXY_TOKEN` (256-bit haproxy token; `setup.sh` generates, consumed by `docker-proxy` and `runtime-agent`/`execution-worker`/`workflow-worker` via `X-Chronoverse-Docker-Proxy-Token`)
- `docker-proxy-ca`: `ca.crt` (docker-proxy mTLS CA; `setup.sh` generates per-mode)
- `docker-proxy-server`: `server.pem` (HAProxy server `tls.crt`+`tls.key` combined; `setup.sh` generates)
- `docker-proxy-client-runtime-agent` / `docker-proxy-client-workflow-worker` / `docker-proxy-client-execution-worker`: `tls.crt`, `tls.key` (separate role mTLS identities for `tcp://...:2376`; `setup.sh` generates certificates restricted to client authentication)

Kubernetes mounts the HAProxy server identity and runtime-agent client identity
as separate projected volumes. Worker pods receive only their own role Secret,
with an explicit UID/GID and `fsGroup` so non-root images can read `0440` keys.
The Docker proxy CA private key is never stored in a runtime Secret. Rotate an
existing Kubernetes deployment with the overlapping-CA workflow:

```sh
scripts/k8s/setup.sh --mode production --context <context> \
  --rotate-docker-proxy-certs
```

The setup flag requires an existing deployment and delegates to
`scripts/k8s/rotate-docker-proxy-certs.sh` after the manifest apply succeeds.
The standalone script performs the same rotation without reapplying manifests.

Compose generates a dedicated Docker proxy CA outside the shared `./certs`
tree. Runtime services mount only one of
`docker-proxy-certs/{server,runtime-agent,workflow-worker,execution-worker}`;
only `init-certs` can access the issuer directory. Docker proxy clients mount
their role at `/docker-proxy-certs`, separately from the read-only `/certs`
application-certificate mount. Do not nest the role mount below `/certs`:
Docker cannot create a child mountpoint after mounting that parent read-only.
`make compose/validate` checks this invariant in both Compose configurations.

For a maintenance-window rotation, stop the stack, regenerate all proxy
identities, and recreate it:

```sh
docker compose -f compose.prod.yaml down
docker compose -f compose.prod.yaml run --rm --no-deps \
  -e DOCKER_PROXY_ROTATE_CERTS=true init-certs
docker compose -f compose.prod.yaml up -d
```
