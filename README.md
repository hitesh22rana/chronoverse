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

**[Website](https://hitesh22rana.github.io/chronoverse/)** · **[Documentation](https://hitesh22rana.github.io/chronoverse/docs/)** · **[API reference](https://hitesh22rana.github.io/chronoverse/docs/api/reference/)**

## Features

- **Workflow management**: create, update, terminate, delete, search, and monitor workflows.
- **Scheduled and manual runs**: run workflows automatically by minute interval or trigger a job on demand.
- **Workflow kinds**:
  - `HEARTBEAT`: lightweight health-check workflow without execution logs.
  - `CONTAINER`: runs containerized workloads and can retain stdout/stderr logs.
- **Replay-safe execution**: idempotency keys, workflow generations, deterministic event keys, transactional outbox delivery, durable job leases, Redis-coordinated Docker image pulls, worker retries, and stale-event guards.
- **Runtime-aware Docker execution**: `runtime-agent` registers each Docker-capable node, `jobs-service` assigns runtime ownership during claim, and workers talk directly to the selected Docker endpoint for execution, logs, and cleanup.
- **Job execution lifecycle**: queued, running, completed, failed, and canceled jobs with automatic retry handling for system failures and safe explanations for terminal failures and cancellations.
- **Retained job logs**: ClickHouse-backed logs, Meilisearch-backed search, raw log download, stream filtering, shareable line selections, and Server-Sent Events for live output.
- **Retention controls**: per-workflow log retention with explicit behavior for non-log-producing or retention-disabled workflows.
- **Notifications and analytics**: user notifications, workflow/job analytics, generated log counts, and execution duration summaries.
- **Security by default in compose**: generated certificates, TLS/mTLS across infrastructure and gRPC services, CSRF-protected session cookies, and Ed25519 JWTs for service authorization.
- **Observability**: OpenTelemetry traces, metrics, and logs exported to the bundled Grafana OTEL LGTM stack.

## Architecture

Chronoverse uses a message-driven microservice architecture:

- **HTTP server** exposes the public REST API and mediates browser sessions, CSRF checks, and gRPC calls.
- **gRPC services** own user, workflow, job, notification, and analytics domains.
- **Kafka topics** carry workflow, job, log, and analytics events between workers.
- **PostgreSQL** stores transactional state, analytics, leases, runtime ownership, idempotency records, and outbox events.
- **ClickHouse** stores retained job logs.
- **Redis** stores sessions, cached reads, live log pub/sub state, and runtime-node-scoped image pull locks.
- **Meilisearch** indexes retained job logs for search.
- **Runtime agent + Docker socket proxy** register Docker-capable nodes and expose the node-local Docker API without mounting the Docker socket directly into workers.
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

### Runtime Plane

- `runtime-agent`: registers one Docker-capable runtime node in PostgreSQL, heartbeats Docker endpoint health/capacity, marks unhealthy when Docker is unavailable, and marks itself draining on graceful shutdown.
- `docker-proxy`: exposes the node-local Docker API that `execution-worker` and `workflow-worker` use after `jobs-service` returns runtime ownership.

### Workers and Jobs

- `scheduling-worker`: scans due workflows and creates replay-safe job dispatch events.
- `workflow-worker`: processes workflow build events, resolves container image digests, and performs owner-aware cleanup.
- `execution-worker`: claims leased jobs, routes container execution to the assigned runtime endpoint, renews leases, publishes logs, and recovers expired leases.
- `joblogs-processor`: batches log events into ClickHouse and Meilisearch when retention is enabled.
- `analytics-processor`: consumes workflow, job, and log events into analytics tables with dedupe.
- `outbox-relay`: publishes transactional outbox events to Kafka with processing leases, retries, dead handling, and cleanup.
- `database-migration`: applies PostgreSQL, ClickHouse, and Meilisearch schema/index migrations.

## Getting Started

### Prerequisites

- Docker
- Docker Compose
- `kubectl` for Kubernetes deployments
- OpenSSL for Kubernetes bootstrap; Java `keytool` when generating production Kafka TLS material
- kind when using the repository's `--create-kind` local-cluster workflow

The compose stacks build or pull all runtime services. Local Go, Node.js, Buf,
and lint tooling are only needed when developing the codebase directly.
Both compose files use one local runtime named `local-docker`; multi-node
deployments run one `runtime-agent` beside each node-local Docker proxy.
Runtime readiness is based on successful Docker health heartbeats; `UNHEALTHY`
means the agent is alive but its Docker endpoint is unusable, while `DRAINING`
means intentional shutdown or scale-down. `RUNTIME_AGENT_ID` must be stable per
runtime node across restarts, not randomly regenerated at startup.
Docker proxy traffic uses mTLS on `2376` plus a token and certificate-role API
ACLs. Compose and Kubernetes isolate the server, runtime-agent,
workflow-worker, and execution-worker private keys rather than sharing one
certificate directory. Kubernetes requires Docker Engine on every labeled
runtime node and routable node IPs; containerd-only clusters are not a supported
Docker-workflow runtime.

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
- LGTM Grafana: not host-published by default; OTLP gRPC remains available on
  `4317`. For local UI access, use
  `docker compose -f compose.dev.yaml -f compose.grafana.yaml up -d lgtm`,
  which binds Grafana to `127.0.0.1:${GRAFANA_HOST_PORT:-3000}`, or use
  `kubectl -n chronoverse port-forward svc/lgtm 3000:3000` with Kubernetes.
  Sign in with the development default `admin` /
  `chronoverse-local-grafana-password`. After upgrading an existing
  `lgtm:/data` volume, run
  `scripts/grafana/reset-admin-password.sh`; if its stored admin login is not
  `admin`, set `GRAFANA_CURRENT_ADMIN_USER` to that login.

### Production Stack

```sh
export CRYPTO_SECRET="$(openssl rand -hex 16)"
export SERVER_CSRF_HMAC_SECRET="$(openssl rand -hex 32)"
export GF_SECURITY_ADMIN_PASSWORD="$(openssl rand -hex 24)"
docker compose -f compose.prod.yaml up -d
```

Persist these values in your secret manager and reuse them across server
restarts. `CRYPTO_SECRET` and `SERVER_CSRF_HMAC_SECRET` must be distinct;
production startup rejects empty values, the known development placeholder,
and reuse of one value for both purposes.

Production uses published images, internal service networking, resource limits,
replicated workers, and a single Nginx entry point:

- Dashboard and proxied API: `http://localhost`
- API routes through Nginx: `/api/...`
- LGTM Grafana: not host-published by default. Use
  `docker compose -f compose.prod.yaml -f compose.grafana.yaml up -d lgtm` for
  an opt-in loopback binding, or
  `kubectl -n chronoverse port-forward svc/lgtm 3000:3000` for
  Kubernetes. Compose uses `GF_SECURITY_ADMIN_USER` /
  `GF_SECURITY_ADMIN_PASSWORD`; Kubernetes production uses `grafana-secret`.
  Run `scripts/grafana/reset-admin-password.sh` after upgrading an existing
  Compose `lgtm:/data` volume, with `GRAFANA_CURRENT_ADMIN_USER` set when the
  stored login is not the legacy default `admin`.

Before running a real deployment, replace development secrets, default
passwords, and generated local certificate assumptions with environment-specific
values. See [Configuration](./docs/configuration.md) and [Operations](./docs/operations.md).

### Kubernetes

Kubernetes manifests are available as Kustomize overlays:

```sh
scripts/k8s/setup.sh --mode local
```

For production:

```sh
scripts/k8s/setup.sh --mode production --context <context>
```

Rotate an existing Kubernetes Docker proxy PKI with overlapping CA trust during
a maintenance window:

```sh
scripts/k8s/setup.sh --mode production --context <context> \
  --rotate-docker-proxy-certs
```

The local strategy is a single-node, self-contained Kubernetes setup for
validation with kind/minikube-style clusters. The production strategy is the
self-hosted Chronoverse stack on your Kubernetes infrastructure: services,
workers, PostgreSQL, Redis, Kafka, ClickHouse, Meilisearch, runtime-agent, and
storage all run under your cluster. Application database traffic is bounded by
PgBouncer transaction pooling, while Kafka consumers scale from lag through
KEDA. KEDA is a platform prerequisite and is validated, not installed, by the
setup script. The setup script preserves valid, complete
operator-provided Secrets, rejects partial production TLS trust chains and
insecure or reused server secrets, and generates missing bootstrap material. See
[infra/k8s/README.md](./infra/k8s/README.md), [configuration](./docs/configuration.md),
and [operations](./docs/operations.md).

## API and Usage

The dashboard talks to the HTTP API using cookie sessions and CSRF protection.
External clients can use the same public routes documented in the
[HTTP API reference](https://hitesh22rana.github.io/chronoverse/docs/api/reference/).

Important API notes:

- Retry-prone mutations require an `Idempotency-Key` header.
- `CONTAINER` workflows can retain and search logs.
- `HEARTBEAT` workflows do not generate execution logs.
- Job list and detail responses include normalized, user-safe reason metadata for failed and canceled jobs.
- Retained log read/search APIs return HTTP `412 Precondition Failed` when retention is disabled for a workflow; live SSE streams report stream-open failures as `event: error`.
- Production Nginx exposes the API below `/api/`; development exposes the server directly.

## Development Commands

```sh
make tools
make dependencies
make generate
make mockgen
make test/short
make test/integration
make lint
make build/all
```

`make dependencies` regenerates protobuf stubs and runs `go mod tidy`; use
`make mockgen` after changing an interface with a `//go:generate` directive.
Kubernetes changes should also pass all four `make k8s/render/*` and
`make k8s/dry-run/*` targets plus both setup-script dry runs documented in the
[Kubernetes guide](https://hitesh22rana.github.io/chronoverse/docs/deployment/kubernetes/#validation).

Dashboard commands live in `dashboard/`:

```sh
npm ci
npm run dev:port
npm test
npm run build
npm run lint
```

Both frontend workspaces exact-pin direct dependencies and commit npm lockfiles;
use `npm ci` to reproduce the reviewed dependency graph.

Static-site commands live in `static/`; `npm run check` validates MDX and
OpenAPI, lints, type-checks, and performs the static export.

More operational commands and troubleshooting notes are in the
[operations guide](https://hitesh22rana.github.io/chronoverse/docs/operations/monitoring/).

## Documentation

The canonical documentation is built from MDX and OpenAPI in `static/` and
published as static HTML:

- [Chronoverse website](https://hitesh22rana.github.io/chronoverse/)
- [Documentation home](https://hitesh22rana.github.io/chronoverse/docs/)
- [Engineering architecture](https://hitesh22rana.github.io/chronoverse/docs/engineering/architecture/)
- [HTTP API reference](https://hitesh22rana.github.io/chronoverse/docs/api/reference/)
- [Deployment and configuration](https://hitesh22rana.github.io/chronoverse/docs/deployment/configuration/)
- [Kubernetes deployment](https://hitesh22rana.github.io/chronoverse/docs/deployment/kubernetes/)
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
