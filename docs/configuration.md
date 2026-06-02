# Configuration

Chronoverse configuration is environment-variable driven. The compose files
provide working local defaults, but production deployments should replace
default credentials, keys, hostnames, retention settings, and resource sizing.

## Compose Profiles

### Development: `compose.dev.yaml`

Development builds local images and exposes internal ports for debugging:

| Component | Host port | Notes |
| --- | ---: | --- |
| Dashboard | `3001` | Next.js dashboard, built with `NEXT_PUBLIC_API_URL=http://localhost:8080` |
| HTTP API | `8080` | Direct server access |
| LGTM | `3000` | Observability UI |
| OTLP gRPC | `4317` | OpenTelemetry collector endpoint |
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
| LGTM | `3000` | Observability UI |

Production Kafka topic partition defaults are:

- `KAFKA_WORKFLOWS_TOPIC_PARTITIONS=2`
- `KAFKA_JOBS_TOPIC_PARTITIONS=4`
- `KAFKA_JOB_LOGS_TOPIC_PARTITIONS=4`
- `KAFKA_ANALYTICS_TOPIC_PARTITIONS=2`

Workers are configured with compose resource reservations and two replicas for
the low/high-resource worker groups.

## Core Environment Groups

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
outbox events. Keep its TLS and credential values environment-specific outside
local development.

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

Redis is used for sessions, cached reads, and live log stream state.

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

## Domain and Worker Settings

### Workflows Service

- `WORKFLOWS_SERVICE_CONFIG_FETCH_LIMIT`
- `WORKFLOWS_CLEANUP_ENABLED`
- `WORKFLOWS_CLEANUP_INTERVAL`
- `WORKFLOWS_CLEANUP_BATCH_SIZE`

Cleanup removes old workflow-related idempotency/outbox state according to the
service implementation.

### Jobs Service

- `JOBS_SERVICE_CONFIG_FETCH_LIMIT`
- `JOBS_SERVICE_CONFIG_LOGS_FETCH_LIMIT`

The jobs service also needs workflows-service client settings so log endpoints
can enforce workflow retention policy.

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
that share a Docker daemon. The lock is scoped by Docker host and exact image
string. Compose defaults are `10m`, `10m`, and `500ms`.

### Execution Worker

- `EXECUTION_WORKER_ID`
- `EXECUTION_WORKER_CONCURRENCY`
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

If `EXECUTION_WORKER_ID` is empty, the worker falls back to the container
hostname. Keep lease duration longer than the renewal interval.

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

Compose sets `OUTBOX_RELAY_WORKER_ID` from the hostname when not provided. Keep
the processing lease longer than normal Kafka publish latency.

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
