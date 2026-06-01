# Operations

This guide covers day-to-day commands, startup behavior, health checks, tuning,
and common troubleshooting paths for the compose-based Chronoverse stack.

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
docker compose -f compose.prod.yaml up -d
```

Useful endpoints:

- Dashboard: `http://localhost`
- API through Nginx: `http://localhost/api/...`
- LGTM: `http://localhost:3000`

Production uses published images, internal service ports, generated TLS
configuration, resource limits, and replicated worker settings.

### Stop and Inspect

```sh
docker compose -f compose.dev.yaml ps
docker compose -f compose.dev.yaml logs -f server
docker compose -f compose.dev.yaml down
```

Use `compose.prod.yaml` in the same commands for the production stack.

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

## Health Checks

Compose includes health checks for infrastructure and gRPC services:

- PostgreSQL uses `psql` with TLS client certs.
- ClickHouse uses `clickhouse-client --secure`.
- Redis uses `redis-cli --tls`.
- Meilisearch uses HTTPS with cert/key/CA paths.
- gRPC services use `grpc-health-probe` with TLS and service auth headers.

If a service is stuck in `starting`, inspect the logs for that service and the
dependency immediately before it in the startup order.

## Build, Test, and Lint

Repository commands:

```sh
make tools
make generate
make test/short
make test
make lint
make lint/fix
make build/all
```

Important notes:

- `make generate` requires `buf`.
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

### Execution Workers

- `EXECUTION_WORKER_CONCURRENCY` controls parallel job execution.
- `EXECUTION_WORKER_LEASE_DURATION` and
  `EXECUTION_WORKER_LEASE_RENEW_INTERVAL` control lease ownership.
- `EXECUTION_WORKER_SYSTEM_RETRY_LIMIT` and
  `EXECUTION_WORKER_SYSTEM_RETRY_BACKOFF` control infrastructure retry behavior.
- `EXECUTION_WORKER_RECOVERY_INTERVAL` and
  `EXECUTION_WORKER_RECOVERY_BATCH_SIZE` control expired lease recovery.
- `EXECUTION_WORKER_JOB_LOG_*` settings tune log batching, publish retries, live
  publish timeout, and live log buffer size.

Keep the lease duration comfortably above the renewal interval. Increase
concurrency only when Docker host capacity, Kafka partitions, and downstream
services can support it.

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
