# Chronoverse

![chronoverse](./.github/assets/chronoverse.png)

**Distributed job scheduler and orchestrator for your own infrastructure.**

Chronoverse runs scheduled and manual workflows across a Docker-backed execution
fleet. It combines an HTTP dashboard/API, gRPC microservices, Kafka workers,
transactional persistence, retained/searchable job logs, notifications, and
analytics into one self-hosted stack.

[![Go Report Card](https://goreportcard.com/badge/github.com/hitesh22rana/chronoverse)](https://goreportcard.com/report/github.com/hitesh22rana/chronoverse)
[![License](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Ask DeepWiki](https://deepwiki.com/badge.svg)](https://deepwiki.com/hitesh22rana/chronoverse)

## Features

- **Workflow management**: create, update, terminate, delete, search, and monitor workflows.
- **Scheduled and manual runs**: run workflows automatically by minute interval or trigger a job on demand.
- **Workflow kinds**:
  - `HEARTBEAT`: lightweight health-check workflow without execution logs.
  - `CONTAINER`: runs containerized workloads and can retain stdout/stderr logs.
- **Replay-safe execution**: idempotency keys, workflow generations, deterministic event keys, transactional outbox delivery, durable job leases, Redis-coordinated Docker image pulls, worker retries, and stale-event guards.
- **Job execution lifecycle**: queued, running, completed, failed, and canceled jobs with automatic retry handling for system failures.
- **Retained job logs**: ClickHouse-backed logs, Meilisearch-backed search, raw log download, stream filtering, and Server-Sent Events for live output.
- **Retention controls**: per-workflow log retention with explicit behavior for non-log-producing or retention-disabled workflows.
- **Notifications and analytics**: user notifications, workflow/job analytics, retained log counts, and execution duration summaries.
- **Security by default in compose**: generated certificates, TLS/mTLS across infrastructure and gRPC services, CSRF-protected session cookies, and Ed25519 JWTs for service authorization.
- **Observability**: OpenTelemetry traces, metrics, and logs exported to the bundled Grafana OTEL LGTM stack.

## Architecture

Chronoverse uses a message-driven microservice architecture:

- **HTTP server** exposes the public REST API and mediates browser sessions, CSRF checks, and gRPC calls.
- **gRPC services** own user, workflow, job, notification, and analytics domains.
- **Kafka topics** carry workflow, job, log, and analytics events between workers.
- **PostgreSQL** stores transactional state, analytics, leases, idempotency records, and outbox events.
- **ClickHouse** stores retained job logs.
- **Redis** stores sessions, cached reads, live log pub/sub state, and workflow-worker image pull locks.
- **Meilisearch** indexes retained job logs for search.
- **Docker socket proxy** lets execution workers run containers without mounting the Docker socket directly.
- **LGTM** provides local OpenTelemetry collection and dashboards.

For the full event flow and replay-safety model, see the
[engineering documentation](https://hitesh22rana.github.io/chronoverse/docs/engineering/architecture/).

## Components

### Services

- `server`: HTTP API gateway for the dashboard and external clients.
- `users-service`: user accounts, login, token issuance, and preferences.
- `workflows-service`: workflow definitions, build status, generation checks, and cleanup.
- `jobs-service`: scheduling, job state, log reads/search, live log streams, leases, and retry state.
- `notifications-service`: notification creation and read-state management.
- `analytics-service`: user and workflow analytics reads.

### Workers and Jobs

- `scheduling-worker`: scans due workflows and creates replay-safe job dispatch events.
- `workflow-worker`: processes workflow build events and prepares executable workflow configuration.
- `execution-worker`: claims leased jobs, runs containers, renews leases, publishes logs, and recovers expired leases.
- `joblogs-processor`: batches log events into ClickHouse and Meilisearch when retention is enabled.
- `analytics-processor`: consumes workflow, job, and log events into analytics tables with dedupe.
- `outbox-relay`: publishes transactional outbox events to Kafka with processing leases, retries, dead handling, and cleanup.
- `database-migration`: applies PostgreSQL, ClickHouse, and Meilisearch schema/index migrations.

## Getting Started

### Prerequisites

- Docker
- Docker Compose

The compose stacks build or pull all runtime services. Local Go, Node.js, Buf,
and lint tooling are only needed when developing the codebase directly.

### Development Stack

```sh
git clone https://github.com/hitesh22rana/chronoverse.git
cd chronoverse
docker compose -f compose.dev.yaml up -d
```

Development defaults expose internal ports for debugging:

- Dashboard: `http://localhost:3001`
- HTTP API: `http://localhost:8080`
- gRPC services: `50051` through `50055`
- PostgreSQL: `5432`
- ClickHouse TLS: `9440`
- Redis TLS: `6379`
- Meilisearch HTTPS: `7700`
- Kafka SSL/controller: `9094` / `9093`
- LGTM dashboard: `http://localhost:3000`

### Production Stack

```sh
docker compose -f compose.prod.yaml up -d
```

Production uses published images, internal service networking, resource limits,
replicated workers, and a single Nginx entry point:

- Dashboard and proxied API: `http://localhost`
- API routes through Nginx: `/api/...`
- LGTM dashboard: `http://localhost:3000`

Before running a real deployment, replace development secrets, default
passwords, and generated local certificate assumptions with environment-specific
values. See [Configuration](./docs/configuration.md) and [Operations](./docs/operations.md).

## API and Usage

The dashboard talks to the HTTP API using cookie sessions and CSRF protection.
External clients can use the same public routes documented in the
[HTTP API reference](https://hitesh22rana.github.io/chronoverse/docs/api/reference/).

Important API notes:

- Retry-prone mutations require an `Idempotency-Key` header.
- `CONTAINER` workflows can retain and search logs.
- `HEARTBEAT` workflows do not generate execution logs.
- Retained log read/search APIs return HTTP `412 Precondition Failed` when retention is disabled for a workflow; live SSE streams report stream-open failures as `event: error`.
- Production Nginx exposes the API below `/api/`; development exposes the server directly.

## Development Commands

```sh
make tools
make generate
make test/short
make test
make lint
make build/all
```

Dashboard commands live in `dashboard/`:

```sh
npm install
npm run dev:port
npm run build
npm run lint
```

More operational commands and troubleshooting notes are in the
[operations guide](https://hitesh22rana.github.io/chronoverse/docs/operations/monitoring/).

## Documentation

The canonical documentation is built from MDX and OpenAPI in `static/` and
published as static HTML:

- [Documentation home](https://hitesh22rana.github.io/chronoverse/docs/)
- [Engineering architecture](https://hitesh22rana.github.io/chronoverse/docs/engineering/architecture/)
- [HTTP API reference](https://hitesh22rana.github.io/chronoverse/docs/api/reference/)
- [Deployment and configuration](https://hitesh22rana.github.io/chronoverse/docs/deployment/configuration/)
- [Operations](https://hitesh22rana.github.io/chronoverse/docs/operations/monitoring/)

The concise Markdown files under `docs/` remain useful for repository-local
reading, while the static site is the complete product and engineering reference.

## Contributing

Contributions are welcome.

1. Fork the repository.
2. Create a feature branch.
3. Make the change with tests and documentation where relevant.
4. Run the applicable checks.
5. Open a pull request.

## License

Chronoverse is licensed under the MIT License. See [LICENSE](./LICENSE).

## Acknowledgments

- [Franz-go](https://github.com/twmb/franz-go)
- [Docker SDK for Go](https://github.com/moby/moby)
- [OpenTelemetry Go](https://github.com/open-telemetry/opentelemetry-go)
- [Docker OTEL LGTM](https://github.com/grafana/docker-otel-lgtm)
