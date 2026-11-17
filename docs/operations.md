# Operations

This guide covers day-to-day commands, startup behavior, health checks, tuning,
and common troubleshooting paths for Compose and Kubernetes Chronoverse stacks.

## Docker SDK Compatibility

Container workers and test helpers use the supported `github.com/moby/moby/client`
and `github.com/moby/moby/api` modules. The client negotiates Docker Engine API
versions automatically, with a minimum supported API of `1.40`.

If an older daemon reports "client version too new", check its supported API
version with `docker version` and upgrade the daemon if it is below `1.40`.
Negotiation failures can surface as a daemon-side version mismatch rather than a
clear minimum-version error.

Operators must keep the Docker daemon patched separately; an SDK migration does
not patch the host engine. The `go.mod` in `golang-migrate` carries the deprecated
`github.com/docker/docker` module as an indirect requirement, pulled in by its
PostgreSQL-driver tests via upstream `dhui/dktest`. Chronoverse source and compiled
package dependencies no longer import that SDK. Upstream test-helper migration
and module cleanup are separate follow-ups; no fork, module replacement or
advisory suppression is used here.

## Running the Stack

### Development

```sh
scripts/compose/init-env.sh
scripts/compose/up.sh
```

Compose automatically reloads the gitignored `.env` file in later shells.
Preserve it while existing encrypted sessions must remain valid.

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
export POSTGRES_PASSWORD="$(openssl rand -hex 24)"
export CLICKHOUSE_PASSWORD="$(openssl rand -hex 24)"
export MEILI_MASTER_KEY="$(openssl rand -hex 32)"
export CRYPTO_SECRET="$(openssl rand -hex 16)"
export SERVER_CSRF_HMAC_SECRET="$(openssl rand -hex 32)"
export GF_SECURITY_ADMIN_PASSWORD="$(openssl rand -hex 24)"
export KAFKA_SSL_KEYSTORE_PASSWORD="$(openssl rand -hex 32)"
export KAFKA_SSL_TRUSTSTORE_PASSWORD="$(openssl rand -hex 32)"
COMPOSE_FILE=compose.prod.yaml scripts/compose/up.sh
```

Set all eight values in the shell or copy `.env.example` to `.env` and fill it.
Persist them in your secret manager and reuse them across server restarts.
`CRYPTO_SECRET` and `SERVER_CSRF_HMAC_SECRET` must be distinct.

Useful endpoints:

- Dashboard: `http://localhost`
- API through Nginx: `http://localhost/api/...`
- LGTM Grafana: not host-published by default; add `compose.grafana.yaml` for
  opt-in loopback-only access

Production uses published images, internal service ports, generated TLS
configuration, resource limits, and replicated worker settings.

### Public HTTPS and Session Cookies

Chronoverse supports HTTP for local or operator-managed deployments. For a
public production endpoint, terminate HTTPS at your reverse proxy or load
balancer using your own hostname and certificate. The internal TLS settings
above do not secure the browser-to-Nginx connection; the production Compose
file publishes plain HTTP on port 80.

Set `SERVER_HOST_URL` to the browser-facing HTTPS origin, even when the TLS
terminator forwards HTTP internally. Set `SERVER_ALLOWED_ORIGINS` to the exact
dashboard origin (scheme, hostname, and port when nonstandard). For a dashboard
and API served together at `https://chronoverse.example.com`, create a local
`compose.https.yaml` override:

```yaml
services:
  server:
    environment:
      SERVER_HOST_URL: https://chronoverse.example.com
      SERVER_ALLOWED_ORIGINS: "https://chronoverse.example.com,"
```

With the production secrets above configured, start with both files:

```sh
docker compose -f compose.prod.yaml -f compose.https.yaml run --rm init-certs
docker compose -f compose.prod.yaml -f compose.https.yaml up -d
```

Exporting `SERVER_HOST_URL` in the shell alone does not inject it into the
server container; the override supplies the environment entry missing from the
base Compose file. Route the HTTPS proxy to Nginx so `/api/` keeps its existing
path rewrite, and restrict direct access to the backend HTTP port to the proxy.
Redirect public HTTP requests to HTTPS. For Kubernetes, configure the public
Ingress TLS endpoint and the server environment with the same public origin.

The application derives the session and CSRF cookies' `Secure` attribute and
HSTS header from the scheme in `SERVER_HOST_URL`, not from forwarded headers.
An HTTPS terminator alone therefore does not enable these settings. Browsers
send `Secure` cookies over HTTPS; setting an HTTPS public URL while accessing
the application over ordinary HTTP prevents those cookies from being sent.
If you intentionally use HTTP, configure an HTTP public URL and matching
allowed origin. Cookies then lack transport encryption, so an on-path observer
can capture and replay sessions. Transport security is the operator's
responsibility; Chronoverse does not enforce HTTPS.

### Kubernetes

```sh
scripts/k8s/setup.sh --mode local
scripts/k8s/setup.sh --mode production --context <context>
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
Docker-capable node. Official overlays use `hostNetwork` and construct an
IPv4/IPv6-safe node endpoint on `2376`, so running job cleanup survives proxy
pod restarts on the same node and cross-node workers avoid CNI host-port DNAT.
Health probes use `tcp://127.0.0.1:2376` (loopback) while the advertised
endpoint remains node-stable. The proxy binds
`:2376 ssl crt /certs/docker-proxy/server.pem ca-file /certs/docker-proxy/ca.crt verify required`
plus the `X-Chronoverse-Docker-Proxy-Token` and exact certificate-role and
method/path ACLs. Runtime-agent receives health APIs only, workflow-worker gets
image and cancellation-cleanup calls, and execution-worker gets the execution
surface. The server and each role use separate private-key mounts; workload
containers receive neither token nor certificate. Multi-node kind and other
Docker-container-based Kubernetes emulators can have topology-specific routing
limitations; use a pod-IP endpoint override only for emulator-only validation.
`workflow-worker` and `execution-worker` do not need the Docker node label;
they can schedule anywhere with network access to TCP `2376` on registered
runtime endpoints.

Runtime-agent registers the Docker proxy endpoint on port `2376`. Workers use
the registered runtime host and port for jobs and command snapshots. Update the
runtime-agent configuration when changing a listener address.

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
window; a single-node host-networked DaemonSet briefly interrupts Docker calls while
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

## Database Migrations

Run `database-migration` before starting application services. It applies pending
PostgreSQL and ClickHouse migrations in dependency order and configures
Meilisearch indexes. Confirm that the job completes successfully before
proceeding with the rollout.

Before schema-changing releases, take and verify database backups and review
migration compatibility with the running application. Pause traffic and workers
when a migration requires exclusive access or changes the schema expected by
running services.

### Rollback

The migration executable applies upgrades only. Restarting an earlier application
image does not reverse database changes. Review the release's down migrations
before using them, run them in reverse dependency order, and account for any data
they remove. Keep affected application services stopped until the schema and
application versions are compatible. Restore a verified backup when rollback
requires recovering removed data.

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

## Dependency Security Maintenance

`.github/dependabot.yml` groups Go and frontend security update PRs and disables
routine version-update PRs. Both `/dashboard` and `/static` npm lockfiles are
covered. Security updates are event-driven; the weekly schedule does not delay
security fixes. Review and merge update PRs through the normal CI checks.

The file does not enable repository security settings. An administrator must enable
Dependabot alerts and Dependabot security updates in GitHub's repository security
settings. With an appropriately authorized GitHub CLI session, the equivalent is:

```sh
gh api --method PUT repos/hitesh22rana/chronoverse/vulnerability-alerts
gh api --method PUT repos/hitesh22rana/chronoverse/automated-security-fixes
```

Verify the settings after merging this configuration; enabling automatic fixes
before it reaches the default branch may create separate, ungrouped PRs. No
automatic merging is configured. Review Go toolchain and Docker base-image
security releases separately; manifest dependency updates do not rebuild existing
binaries or patch an operator's Docker daemon.

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
npm ci
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
PgBouncer generates its one allowed database mapping from
`postgres-app-secret.POSTGRES_DB`; the admin and application PostgreSQL Secrets
must name the same database, and setup rejects a mismatch before apply.

### Scheduler

- `SCHEDULING_WORKER_POLL_INTERVAL` controls how often due workflows are scanned.
- `SCHEDULING_WORKER_CONTEXT_TIMEOUT` bounds each scan.
- `SCHEDULING_WORKER_BATCH_SIZE` controls how many workflows can be processed per pass.

Lower intervals increase scheduling responsiveness and database load.

### Workflow Workers

- `WORKFLOW_WORKER_IMAGE_PULL_LOCK_TTL` controls how long an image-pull
  lease remains valid after acquisition or renewal.
- `WORKFLOW_WORKER_IMAGE_PULL_LOCK_WAIT_TIMEOUT` controls how long another
  worker waits for the same runtime node and image before retrying the Kafka
  record.
- `WORKFLOW_WORKER_IMAGE_PULL_LOCK_RETRY_INTERVAL` controls Redis polling while
  waiting for a held image pull lock.

Workflow and execution image-pull locks renew every `TTL/3`, including while
rechecking image availability after acquisition. A zero TTL selects the default
`10m`; explicit TTLs must be at least `3ms` and are rounded down to whole
milliseconds to match Redis expiry precision. Zero wait timeout and retry
interval select their defaults. Negative values for any of these three durations
fail startup.

Expiry is tracked conservatively from acquisition and renewal request start.
Each extension deadline is the earliest of `now + TTL/3`, `now + 5s`, and the
tracked expiry minus `TTL/3`. If that safe window is exhausted, protected work
is canceled with a renewal-budget error. A store error remains distinct from
`renewed=false` ownership loss. Choose TTLs with enough margin for Redis latency
and worker scheduling delays; the minimum is a precision limit, not a recommended
production lease.

Cleanup cancels and joins renewal; an in-flight extension remains subject to its
I/O deadline. Release then uses a separate detached five-second deadline and
remains best effort. Release errors preserve the build result; a renewal failure
can turn apparent build success into a retryable coordination error. A retry can
reuse the cached image. Independent build failures are retained, caller-driven
cancellation keeps the caller's error, and renewal-driven cancellation reports
the renewal failure.

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

### Container Creation and Reconciliation

Container execution persists an immutable intent before sending Docker Create.
Each attempt has its own name and original runtime instance. Create and Start
permissions are single-use: a lost permission response does not grant another
transmission. The container is observed and attached before Start, and Start
always targets its full immutable ID. Both Docker proxy deployments route these
mutations through a backend that does not retry accepted requests.

A Create timeout is an unknown outcome, not proof that no container exists.
Execution workers resolve the intent name using verified ownership labels and
persist each observed ID. Jobs-service trusts the authenticated execution role to
verify ownership labels; PostgreSQL also prevents one daemon/container identity
from belonging to multiple intents. Closed intents remain discoverable after job,
workflow and owner deletion. A later materialization is cleanup-only and receives its own
immutable cleanup obligation; a receipt for another ID does not cover it.

Expired-lease recovery never completes a `created` container from its zero exit
code. It cleans that object and applies system recovery. Only an observed `exited`
container with a committed Start reservation can settle from its exit code and
retained logs. An infrastructure Create fault retains its original diagnostic
and uses the system retry budget; a typed invalid workload remains a user failure.
Network repair uses a new attempt rather than resending Create for the same intent.
Intent/grant/observation and attachment RPCs each have a five-second deadline;
Create and Start mutation I/O are capped at thirty seconds. These budgets do not
shorten the workload timeout or prove that a timed-out daemon mutation did not run.

A workflow cannot admit another container job while a prior Start-reserved ID
lacks original-instance removal evidence, even if the prior job is terminal.
The scheduler defers blocked jobs without spending execution attempts or creating
repeated dispatches. Runtime occupancy includes durable unresolved capacity holds;
heartbeats cannot erase them, and duplicate receipts cannot release capacity twice.
HTTP workflows do not acquire the container admission gate. Admission, attachment
and settlement transactions lock the workflow row before job, intent and runtime
rows, serializing those transactions within that workflow. Settlement triggers close intents and recompute the node’s
occupancy before committing; runtime heartbeats use the same accounting model.
High job turnover on one workflow or runtime can contend on these locks and
repeat node-wide counts. Measure that contention before increasing concurrency.
Unrelated workflows retain independent workflow locks.

Discovery has one reserved worker, separate from expired-lease recovery. Each
recovery cycle processes at most ten discovery items, bounded by
`EXECUTION_WORKER_RECOVERY_BATCH_SIZE`. Two durable scheduling lanes, `URGENT`
and `SURVEILLANCE`, share claim opportunities equally and lend unused capacity.
The 15-second pass interval, maximum ten sequential claims per pass, healthy
five-minute rearm with jitter, error backoff, 45-second ownership lease,
30-second inspection budget and five-second failure-report/identity-read budgets
are unchanged; no new cadence, concurrency, API or GC setting is introduced. The
equal split is a fairness policy, not a latency guarantee: a slow inspection can
still delay the other lane, and overload postpones recovery.

Each claim is obtained immediately before its inspection. One short database
transaction locks a singleton discovery turn, selects half the requested batch
(rounded up) from the preferred lane, fills the rest from the other lane, then
borrows any remainder back. Existing closed, non-quarantined, due,
expired-inspection and `SKIP LOCKED` predicates and fresh tokens/leases remain.
The final response interleaves the ordered lane slices, starting with the
preferred lane when nonempty, and the turn advances opposite that first served
lane. An empty claim leaves preference unchanged. Tokens and the turn commit
atomically; the turn is never held across remote inspection, and a lost commit
authorization grants no fallback. First served means `response[0]`, not
allocation or `UPDATE RETURNING` order.

The marker is a scheduling hint, never proof, admission or mutation authority.
Open intents stay ineligible. First closure sets the marker urgent and the due
clock to closure time; duplicate closure preserves both. A new unreceipted
materialization promotes to urgent, while duplicate observation, ensure, receipt
or closure does not reset due or backoff. A successful fenced `ABSENT`/`OBSERVED`
report may downgrade only when the intent has no active hold and no
materialization lacking an exact job/container/runtime/intent receipt. Errors and
interruptions preserve the marker and existing backoff/quarantine; recording a
receipt or releasing a hold keeps it until a later successful inspection.
Inspection errors back off and quarantine after three consecutive reported
failures. Quarantined intents are excluded from automatic discovery claims until
administrative redrive; quarantine does not release workflow admission gates or
capacity holds. Redrive returns the row to urgent and due now, retains the last
inspection diagnostic until an inspection reports success or a new failure, and
does not reopen execution authority. Worker shutdown releases the inspection
lease without spending or resetting that budget; repeated interruptions can postpone
discovery indefinitely.

Claim selection uses the lane/due index and materializes candidate IDs before
updating leases. Healthy reports use full-key receipt probes for the current
intent. Recheck both query plans after PostgreSQL or predicate/index changes,
including generic plans and intents with many materializations; indexed selection
alone does not guarantee an efficient update or receipt join.

Use the internal-admin `ListContainerIntents` and `GetContainerIntent` RPCs to
inspect original routing, image reference, creation outcome, inspection error,
next due time, quarantine and capacity holds. `RedriveContainerIntent` requires
the expected inspection generation and a repair reason. Jobs-service records
the authenticated principal and generation transition in
`container_intent_redrives`; no caller-supplied actor is trusted. Never use SQL
to manufacture a receipt or clear a hold.

Monitor `chronoverse.container_intents.discovery_due` and
`chronoverse.container_intents.oldest_due` (seconds), which carry only a bounded
`lane` label with `URGENT` and `SURVEILLANCE` series, plus the global
`chronoverse.container_intents.quarantined` and
`chronoverse.container_intents.capacity_holds`. Successful collection always
reports both lanes, as an explicit zero for an empty lane, and never an unlabeled
total beside them. `oldest_due` keeps the scheduled-due clock: a due row may still
hold an inspection lease, so it is not the same as immediately claimable waiting
age; inspect active leases separately and use the maximum lane age. Collection
failure omits the sample rather than fabricating a healthy zero. Each replica
reads the same database state; aggregate with the maximum, not the sum.
These jobs-service gauges read persisted state with a five-second collection
budget and carry no job, intent or runtime identity labels.
Holds also exist during live execution; a nonzero hold count alone is not a failure.
Alert on quarantine and overdue inspection, and investigate holds that outlive their
execution. The following queries provide per-lane and per-runtime diagnosis
without secret-bearing request data:

```sql
SELECT quarantined, inspection_urgent,
       count(*) AS intents,
       count(*) FILTER (WHERE NOT quarantined AND next_inspection_at <= (clock_timestamp() AT TIME ZONE 'utc')) AS due,
       count(*) FILTER (WHERE NOT quarantined AND inspection_until > (clock_timestamp() AT TIME ZONE 'utc')) AS leased,
       min(next_inspection_at) FILTER (WHERE NOT quarantined) AS oldest_due
FROM container_creation_intents
WHERE closed_at IS NOT NULL
GROUP BY quarantined, inspection_urgent;

SELECT h.runtime_node_id, h.runtime_instance, h.intent_id, h.job_id,
       i.start_reserved_at,
       (clock_timestamp() AT TIME ZONE 'utc') - i.start_reserved_at AS hold_age,
       i.closed_at, i.quarantined
FROM runtime_capacity_holds h
JOIN container_creation_intents i ON i.id = h.intent_id
WHERE h.released_at IS NULL
ORDER BY i.start_reserved_at;

SELECT intent_id, expected_generation, generation, actor, reason, created_at
FROM container_intent_redrives
ORDER BY created_at DESC
LIMIT 100;
```

Quarantine requires diagnosis and repair before redrive. Confirm the original
runtime is reachable, its instance identity matches, and the cleanup role can
read `/info` (including versioned API paths). That read exposes privileged daemon
metadata; workflow workers still cannot list or inspect containers or start work.
An identity mismatch or denied identity read cannot produce a usable receipt.

If the original runtime is permanently lost, admission and held capacity can
remain blocked indefinitely. There is no force-run or destroyed-runtime
attestation bypass. Restore the original instance and establish physical absence,
or design and review an explicit retirement procedure before releasing evidence.
Do not age out discovery tombstones: a prior granted Create can arrive after an
earlier absence observation. A previously granted Start can also arrive after
cancellation while the runtime is unreachable; only completed full-ID removal
closes that execution path. Runtime identity checks assume a stable authenticated
binding and do not fence concurrently live cloned daemon identities.

### Durable Container Cleanup

Job terminal transitions and retry detachment atomically enqueue an immutable
`CONTAINER_CLEANUP` outbox event containing verified materialization/intent identity,
original runtime instance and routing, attempt and log-retention policy. Workflow deletion preserves these snapshots
before cascading job deletion. Cleanup does not require the workflow or job row
to remain present, and repeated events target the original container ID.

For retained storage, grouped receipt/backlog counts and capacity planning, see
[Retained cleanup data](#retained-cleanup-data).

Workflow workers stop the container, replay retained logs and remove it. Each
attempt has a three-minute deadline, including the existing two-minute replay
budget. Replay failures preserve the container for retry. Failed attempts remain
uncommitted until completion or durable quarantine; a restart resumes from
Kafka with the persisted execution budget. Removal in progress remains retryable;
absence on the verified original runtime is successful cleanup. Executor inline removal is best effort with a detached
ten-second deadline; the durable event remains responsible for recovery if that
attempt fails or the executor exits after settlement.

Jobs-service stores a durable completion receipt for each exact job/container/runtime-instance
identity after Docker confirms removal or absence on the original daemon. Workflow cleanup and executor
inline cleanup check receipts before contacting Docker and record completion
after removal. A receipt read or write failure is a cleanup failure, not an
acknowledgement. These internal RPCs require the jobs-service admin role. Attachment and receipt
APIs, and both database columns, enforce the same 256-character container-ID
limit.

Expired-container recovery checks receipts before resolving a runtime. It
records confirmed absence from inspection, and successful removal, before
settling the recovered lease. Lease-owned recovery operations have a
three-minute attempt deadline and preserve caller/lease cancellation; receipt
I/O and the shared removal step have ten-second budgets. Cleanup following an
accepted terminal settlement retains its separate detached ten-second budget.

Published cleanup events are retained until a matching receipt exists, even
past the normal published-event retention window. This preserves unfinished
cleanup snapshots if Kafka retention expires. On its normal publication cycle,
the relay requeues due unfinished published cleanup when no execution lease or
retry delay is active. Successful initial publication schedules recovery after
`OUTBOX_RELAY_REPLAY_INTERVAL` (default five minutes). Each successful replay
wave doubles the next interval, capped by `OUTBOX_RELAY_REPLAY_MAX_INTERVAL`
(default one hour): five, ten, twenty, forty, sixty minutes, then sixty minutes
repeatedly. Replay rounds and due times survive relay restarts; rounds saturate
without imposing a lifetime replay limit. Transport retries remain in their
current wave. Execution generation, attempts and failure budget are unchanged.
Completed and quarantined cleanup is excluded; publication `DEAD` requires
administrative repair rather than automatic budget renewal.

Workflow publication allocates its total batch fairly between initial deliveries
and replay, borrowing unused slots in either direction. A durable database turn
also alternates batch-one allocations and the first send of larger batches across
replicas and restarts. Selected records are explicitly interleaved before Kafka
I/O; the scheduling row is locked only during the short claim transaction.
Per-key unpublished predecessor barriers, including `DEAD` and delayed `FAILED`
rows, still apply across both classes. Fairness applies to eligible independent
keys, not a fixed per-event latency under overload. Other topics poll independently.

Republishing does not require consumer-lag data. The interval cap bounds the
scheduled wait after successful publication, not delivery or physical-cleanup
latency: overload, active leases/backoff, key blockers, unavailable services,
disabled relays, repeated crashes, quarantine and `DEAD` can postpone recovery.
A broker-accepted send followed by a crash or failed database acknowledgement can
still be sent again after the transport lease, before its normal recovery
interval. This is at-least-once delivery, not a broker-wide duplicate bound.
Monitor Kafka backlog, retained work and relay throughput. Other published event
types retain their usual retention behavior.

An accepted audited administrative redrive resets pacing to round zero for its
new cleanup generation. Configure matching replay intervals across replicas; the
successful publisher determines the next persisted due time. Configuration
changes affect future successful publications, not existing due timestamps. Both
intervals require whole milliseconds and `1ms <= interval <= maximum`. Programmatic
zero intervals select defaults; explicit negative or fractional-millisecond
values are rejected.

#### Published-outbox pruning

Recheck both lane query plans after PostgreSQL upgrades or changes to their
predicates/indexes. Lane transactions disable bitmap scans locally to avoid
sorting the full eligible backlog before the candidate limit. Validate candidate
index access, target-row PK access and bounded receipt PK probes at representative
cardinalities; a materialized CTE alone does not prevent a full receipt scan.
Physical pages, locked-row skips and IO remain outside a strict work bound.

Published-outbox retention uses two candidate-limited lanes: ordinary published
events and completed container cleanup. Before any lane work, a call reserves a
durable maintenance turn and commits that reservation. The turn alternates the
preferred lane across replicas and restarts; an empty or failed lane still
advances it. The turn lock is not held during deletion. A busy reservation skips that maintenance
call and is logged at debug level; aligned replica schedules can reduce achieved
pruning throughput. Replica count does not guarantee proportional capacity.

A call selects at most `OUTBOX_RELAY_CLEANUP_BATCH_SIZE` candidate identities. The
preferred lane is offered half rounded up; the other lane receives the batch size
minus already charged candidates, and the preferred lane gets at most one final borrowed
operation. A failed lane is not reentered in the same call. Each lane locks its
candidates before checking proof, then deletes by candidate ID and commits
independently. Ordinary candidates include missing or null actions. Cleanup
candidates require `cleanup_state = 'COMPLETED'` and an exact receipt match on
job, container, original runtime instance and creation intent. Job IDs are parsed
as UUIDs only when valid; intent comparison is textual. Missing or mismatched
proof retains the row.

A call can return a known committed delete count together with an error when one
lane fails and another commits, in either order. The relay logs that count with
the error. Successful lane logs report selected, deleted and proof-mismatch
counts, including
zero-deletion cycles. Failed lanes log the full charged allowance, zero known
committed deletions and whether the commit may be uncertain. `selected_known`
distinguishes observed selections from unavailable counts; a failed commit can
retain an observed selection count without proving deletion. If a commit acknowledgement is
uncertain,
the count is a lower bound; the next cycle finds any remaining candidates. Lanes
commit independently, so one lane's failure or rollback does not undo another
lane's confirmed deletions.

A `COMPLETED` row without exact proof is corruption, not deletion authority. It
fails closed and can block cleanup-lane progress while ordinary pruning continues.
Inspect expired cleanup candidates lacking proof (replace the interval with the
configured `OUTBOX_RELAY_PUBLISHED_RETENTION`):

```sql
SELECT events.id, events.published_at, events.payload->>'JobID' AS job_id,
       events.payload->'ContainerCleanup'->>'ContainerID' AS container_id,
       events.payload->'ContainerCleanup'->>'RuntimeInstance' AS runtime_instance,
       events.payload->'ContainerCleanup'->>'IntentID' AS intent_id,
       events.cleanup_state, events.cleanup_generation
FROM outbox_events events
WHERE events.status = 'PUBLISHED'
  AND events.published_at < now() - interval '168h'
  AND events.payload->>'Action' = 'CONTAINER_CLEANUP'
  AND events.cleanup_state = 'COMPLETED'
  AND NOT EXISTS (
      SELECT 1
      FROM container_cleanup_receipts receipt
      WHERE receipt.job_id = CASE
              WHEN pg_input_is_valid(events.payload->>'JobID', 'uuid')
              THEN (events.payload->>'JobID')::uuid
            END
        AND receipt.container_id = events.payload->'ContainerCleanup'->>'ContainerID'
        AND receipt.runtime_instance = events.payload->'ContainerCleanup'->>'RuntimeInstance'
        AND receipt.intent_id::text = events.payload->'ContainerCleanup'->>'IntentID'
  )
ORDER BY events.published_at, events.id
LIMIT 100;
```

Receipt-present rows in a non-`COMPLETED` state, including `NULL`, should not be
produced by completion writers. They stay outside automatic pruning and are an
invariant violation to preserve for diagnosis:

```sql
SELECT events.id, events.status, events.published_at, events.cleanup_state,
       events.payload->>'JobID' AS job_id,
       events.payload->'ContainerCleanup'->>'ContainerID' AS container_id,
       events.payload->'ContainerCleanup'->>'RuntimeInstance' AS runtime_instance,
       events.payload->'ContainerCleanup'->>'IntentID' AS intent_id
FROM outbox_events events
JOIN container_cleanup_receipts receipt
  ON receipt.job_id = CASE
         WHEN pg_input_is_valid(events.payload->>'JobID', 'uuid')
         THEN (events.payload->>'JobID')::uuid
       END
 AND receipt.container_id = events.payload->'ContainerCleanup'->>'ContainerID'
 AND receipt.runtime_instance = events.payload->'ContainerCleanup'->>'RuntimeInstance'
 AND receipt.intent_id::text = events.payload->'ContainerCleanup'->>'IntentID'
WHERE events.payload->>'Action' = 'CONTAINER_CLEANUP'
  AND events.cleanup_state IS DISTINCT FROM 'COMPLETED'
ORDER BY events.published_at, events.id
LIMIT 100;
```

Receipts survive job/workflow deletion and published-outbox pruning. Enqueueing
cleanup consults receipts, and a previously queued delivery also checks them,
so completed cleanup does not depend on its former runtime remaining available.
Receipts have no automatic expiry; do not prune them with outbox retention.
Safe receipt garbage collection requires proving that source references and
possible redeliveries are gone. A crash after removal but before receipt commit
can still require another Docker confirmation; this is not exactly-once cleanup.

#### Cleanup quarantine and redrive

Cleanup events use workflow-key ordering. Before contacting Docker, a worker
acquires a token-bound execution attempt in the retained outbox row. Each
cleanup generation permits three failed or abandoned attempts. The four-minute
attempt lease covers the three-minute cleanup deadline plus reporting time;
a worker crash or lost acquisition response consumes that attempt once its
lease expires. Concurrent deliveries wait for the active lease. Failed attempts
wait 30 seconds, then 60 seconds, before another acquisition. Caller cancellation
is reported with a detached five-second deadline and returns the attempt without
spending the failure budget. If reporting is unavailable, the Kafka record stays
retryable and the lease eventually expires. Repeated successfully reported
caller cancellations can therefore retry indefinitely without quarantine; the
three-attempt bound applies to failures and abandoned attempts, not interruptions.

After exhaustion, the worker persists `QUARANTINED` before acknowledging the
record, allowing later workflow events to proceed. Quarantine does not mean the
container was removed: the snapshot remains retained until a completion receipt
exists. Workers log quarantines with workflow, job, container, generation and
attempt count. Inspect outstanding work with read-only SQL:

```sql
SELECT kafka_key AS workflow_id, payload->>'JobID' AS job_id,
       payload->'ContainerCleanup'->>'ContainerID' AS container_id,
       cleanup_state, cleanup_generation, cleanup_attempts,
       cleanup_error_code, cleanup_failed_at, cleanup_lease_until,
       cleanup_next_attempt_at, status AS publication_status
FROM outbox_events
WHERE cleanup_state IS NOT NULL AND cleanup_state <> 'COMPLETED'
ORDER BY cleanup_failed_at NULLS LAST, created_at;
```

Jobs-service exports these gauges from committed cleanup state, with a
five-second collection deadline and no job, container, actor or runtime labels:

| Metric | Meaning |
| --- | --- |
| `chronoverse.container_cleanup.outstanding` | Unfinished cleanup rows, including quarantined work and failed publication |
| `chronoverse.container_cleanup.quarantined` | Current execution quarantines requiring repair/redrive |
| `chronoverse.container_cleanup.publication_dead` | Unfinished cleanup whose publication is `DEAD` |
| `chronoverse.container_cleanup.oldest_due` | Seconds overdue for cleanup execution; active cleanup leases, future execution backoff and quarantined work are excluded |

`oldest_due` measures cleanup that can execute, not how late the next replay
publication is. A record already in Kafka can run while `cleanup_replay_due_at`
is still in the future. Replay pacing therefore does not pause or reset this
age; it controls republication cadence without gating a delivery already in
Kafka. Active execution leases and execution retry backoff still suppress the
due age. If no usable delivery remains, the next execution may depend on an
eligible replay. Cross-check `cleanup_replay_due_at`, publication state and
consumer progress when diagnosing overdue cleanup; publication `DEAD` still
requires operator repair.

These are backlog gauges, not delivery counters: duplicate records and worker or
exporter restarts do not create additional work. Completion removes the row from
all four measurements. Collection failure omits samples rather than reporting a
healthy zero; alert on telemetry loss as well. Each jobs-service replica reads the
same database totals, so use the maximum across replicas, not their sum.

Alert on sustained nonzero quarantine or publication-dead counts. Alert on oldest
due age exceeding the normal delivery-latency budget; five minutes sustained for
five minutes is an initial example to tune against the deployment's latency.
Outstanding count alone is not a failure. Use the query above to identify affected
work and distinguish backoff, a live lease, publication failure and quarantine.

Repair the runtime/configuration issue, then call the internal jobs-service RPC
with an admin token for the jobs-service audience and the observed generation:

```sh
grpcurl -H "authorization: Bearer $JOBS_ADMIN_TOKEN" \
  -d '{
    "job_id":"<job UUID>",
    "container_id":"<container ID>",
    "runtime_instance":"<original daemon ID>",
    "intent_id":"<intent UUID>",
    "generation":"1",
    "reason":"original daemon connectivity restored"
  }' \
  "$JOBS_SERVICE_ADDRESS" jobs.JobsService/RedriveContainerCleanup
```

The repair reason is required (maximum 2048 bytes). The audit actor comes only
from the validated admin JWT subject, never from a request field or metadata.
A shared service principal identifies that credential, not the individual human
using it. Use distinct authenticated principals when individual attribution is
required. Each accepted generation transition writes its actor, reason, expected
and new generation, immutable cleanup identity and database timestamp in the same
transaction as requeueing. A failed transaction writes neither change; retrying
an old generation does not create a second audit. History survives source deletion
and outbox pruning, without an automatic expiry policy:

```sql
SELECT job_id, container_id, runtime_instance, intent_id,
       expected_generation, generation, actor, reason, created_at
FROM container_cleanup_redrives
ORDER BY created_at DESC, event_key, generation
LIMIT 100;
```

Use the deployment's TLS credentials when connecting. Redrive increments the
cleanup generation, resets its execution/publication budgets and requeues the
original snapshot. This is an explicit administrative override for unfinished
work, not only quarantined cleanup: it also accepts `READY`, `RETRY` (overriding
backoff), and `PROCESSING` with an expired lease. It refuses a live execution
lease. Repeating a request with the old generation returns `STALE` without
resetting the new budget; late Kafka
deliveries and reports from that generation also do no work. A completion receipt
returns `COMPLETED` without requeueing, even after outbox pruning. The RPC cannot
change the original runtime routing. Execution quarantine is separate from
publication `DEAD`: the same explicit redrive can requeue a retained cleanup
whose publication failed, while unrelated DEAD events retain the relay's
existing recovery procedure. Monitor both states. Redrive cannot repair a
`COMPLETED` row that lacks exact receipt proof or manufacture removal proof; do
not issue raw `cleanup_state` updates or insert receipts without confirmed
removal. Persistent malformed identity needs a separate repair design.

Recovery depends on the original runtime
being reachable and on container identity having been durably attached; unknown
containers from an interrupted create/attach operation require separate runtime
reconciliation. These deadlines depend on Docker honoring request contexts.

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

Expired-lease recovery treats missing containers, unavailable runtimes and
runtime-client setup failures as infrastructure failures. They retain their
original diagnostic code and settle as `SYSTEM` / `SYSTEM_ERROR` when the system
retry limit is exhausted. An observed non-zero workload exit remains `USER` /
`NON_ZERO_EXIT`; confirmed container absence is cleanup evidence, not a workload
failure diagnosis.

Lease duration must be a whole number of seconds between `1s` and
`2147483647s`, matching the jobs RPC representation. Renewal interval must be
positive and leave a positive renewal RPC budget below the lease duration.
Invalid explicit timing fails startup rather than being truncated, defaulted or
silently rewritten. When shortening the lease duration, configure its renewal
interval as well. An unset environment interval keeps its `10s` default; it is
not derived from the lease duration. Only programmatic executor configuration
with a zero interval derives `duration/3` when the default interval no longer
fits the configured lease.

Each initial lease proof and periodic renewal has an RPC budget of the smaller
of five seconds and one third of the configured gap between lease duration and
renewal interval; an earlier caller deadline still applies. Renewal failure
cancels the operation with its cause. Normal execution and expired-lease
recovery both cancel and join their renewal supervisor before returning. A late
renewal failure does not override an already accepted settlement. These bounds
rely on the jobs client honoring contexts; process pauses and downstream fencing
remain separate concerns.

Validation enforces wire precision and a positive renewal budget; it does not
impose a minimum operational budget. A near-equal lease duration and interval
can pass validation yet time out during initial proof or renewal. Leave enough
budget for observed jobs-service latency and scheduling delays; the `30s`/`10s`
defaults provide a five-second RPC budget.

Keep the lease duration comfortably above the renewal interval. Increase
concurrency only when Docker host capacity, per-workload resource limits, Kafka
partitions, and downstream services can support it. Measure queue delay before
adding capacity. These per-workload limits
are applied only when execution-worker creates Docker job containers. Execution
image pulls use the same runtime-node-scoped lock model as workflow-worker
digest resolution: workers sharing one runtime daemon serialize the same image
pull, while different runtime nodes may pull independently. See
[Workflow Workers](#workflow-workers) for the shared image-pull TTL, renewal and
cleanup budgets.

Retry contract: `SYSTEM_RETRY_LIMIT=3` with `SYSTEM_RETRY_BACKOFF=30s`
(`30s, 60s, 120s`, capped at `240s`) applies only to infrastructure codes
(`Aborted`, `Canceled`, `Internal`, `ResourceExhausted`, `Unavailable`,
`DeadlineExceeded`) and explicitly tagged infrastructure failures that retain
another diagnostic code, including transient Docker ping failures. Unexpected heartbeat endpoint HTTP
statuses, including `429`, remain `USER` `UNEXPECTED_STATUS_CODE` terminal
failures without retry: the probed endpoint throttling us is endpoint behavior,
not platform infrastructure. After exhaustion the
job persists as terminal `FAILED` (`SYSTEM_ERROR` for infrastructure,
user reason otherwise). `ReleaseJobForRetry` moves a concurrently terminated
workflow's job to `CANCELED` (`WORKFLOW_TERMINATED`) instead of `PENDING`, and
the scheduler re-checks `terminated_at` at `UPDATE` time, so termination cannot
leave orphan `PENDING` rows. Distinguish delays via logs: `queue_delay`
(scheduled-to-claim), `execution_delay` (claim-to-finish), `retry_delay`
(backoff), plus `failure_kind`/`terminal_reason`/`error_code`.

Raw and wrapped Go context errors use the same cancellation/deadline codes as
gRPC errors in retry decisions and persisted failure metadata. Explicit workload
reasons, such as a time limit or non-zero exit, retain their `USER` classification
even when the underlying transport code is retryable. Docker image-pull daemon
failures, connection/stream interruptions and overload are infrastructure faults;
invalid image requests, missing images and registry access denials retain the
`IMAGE_PULL_FAILED` user reason.

### Outbox Relay

- `OUTBOX_RELAY_BATCH_SIZE` and `OUTBOX_RELAY_POLL_INTERVAL` control throughput
  and database polling pressure.
- `OUTBOX_RELAY_PROCESSING_LEASE` controls how long a relay owns claimed rows.
- `OUTBOX_RELAY_REPLAY_INTERVAL` and `OUTBOX_RELAY_REPLAY_MAX_INTERVAL` pace
  recurring unfinished cleanup delivery, defaulting to five minutes and one hour.
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

Search indexing splits each consumer batch into JSON requests of at most 4 MiB,
counting escaping, metadata, array brackets and commas. Count/time batch settings
still control Kafka accumulation. If Meilisearch or its proxy rejects a request
with HTTP 413, the processor splits it further and waits for each accepted task
before proceeding. Stable document IDs make replay after partial indexing safe.

Retained-log indexing and cleanup check task status immediately, then every 250 ms
while pending, with a two-minute timeout per task. Cancellation and failed tasks
still stop processing. The shorter interval reduces completion delay but increases
task-status requests under concurrent indexing.

A document larger than the 4 MiB request budget, or rejected with 413 even when
sent alone, returns retryable `ResourceExhausted`. No log is truncated or skipped
and the Kafka batch remains uncommitted. A single-document rejection can still
hold up its partition until an operator raises the server/proxy payload limit;
normal published log lines are capped at 256 KiB before JSON encoding. For a
document above the application budget, inspect its producer and correct the
source of oversized events before retrying.
Rejection errors include document, workflow and job IDs to identify the affected
record. Keep limits large enough for one fully encoded document. Transient search failures
remain retryable; do not force-commit offsets to clear a rejection.


Operational traces omit raw workflow and notification payload attributes.
Consumer diagnostics omit raw broker values and whole analytics events; JSON
parse failures report an error type, while workflow/analytics dispatch failures
report a gRPC error code. The shared Kafka runner also limits processor-error
logs and span exceptions to the code and a generic message; retry and commit
decisions still use the original error. Topic, partition, offset and selected resource
identifiers remain available for investigation. Startup, request and consumer contexts
are passed to the OpenTelemetry bridge for trace correlation without encoding
their values into console logs. These changes do not alter stored workflow
payloads or user-visible job logs. Operators still control collector access,
retention and exports; existing telemetry is not retroactively removed.

### Analytics Cleanup

- `ANALYTICS_PROCESSOR_CLEANUP_ENABLED`
- `ANALYTICS_PROCESSOR_CLEANUP_INTERVAL`
- `ANALYTICS_PROCESSOR_CLEANUP_BATCH_SIZE`
- `ANALYTICS_PROCESSED_EVENTS_RETENTION`

Processed-event retention supports replay dedupe. Avoid setting it too low for
the longest replay window you expect to tolerate.

### Retained cleanup data

Cleanup receipts, creation intents and materializations survive source deletion
and transport pruning. Retained proof and unfinished work can therefore grow even
when the queue is quiet. Use the following on-demand reports to inspect that
storage and decide whether to investigate cleanup or expand deployment capacity.

Save the SQL blocks below as `retention-inventory.sql` and `retention-backlog.sql`.
Use an observer libpq service profile with database CONNECT, public-schema USAGE
and SELECT on the seven reported tables; superuser privileges are not required.
Keep credentials out of command-line URIs and captured samples.

```sh
PGCONNECT_TIMEOUT=5 psql -X -qAt -v ON_ERROR_STOP=1 \
  'service=chronoverse-observer' -f retention-inventory.sql
PGCONNECT_TIMEOUT=5 psql -X -qAt -v ON_ERROR_STOP=1 \
  'service=chronoverse-observer' -f retention-backlog.sql
```

Both reports use read-only transactions, UTC and a one-second lock timeout. The
inventory SELECT has a five-second statement deadline and the exact backlog
SELECT ten seconds. Connection setup has its own timeout; these are not total
wall-time or I/O budgets. Accept a sample only after a zero exit and a complete
JSON result. A timeout, permission error, missing schema or partial result means
unknown, not a healthy zero; retain the previous good sample separately.

#### Catalog and file-size inventory

`table_bytes` includes TOAST and auxiliary table storage; `index_bytes` measures
indexes. `total_bytes` combines both. These physical observations are separate
function calls, so concurrent changes can make the components differ; read-only
MVCC does not freeze files or statistics. Database bytes already include these
relations: do not add database and relation totals together. Neither is free space.

Row/dead-tuple statistics are estimates, not exact counts or reclaimable bytes.
Use the analyze timestamps and modification count for context; do not run ANALYZE
or VACUUM as a side effect of sampling. Record collection time and deployment/
volume identity, since database names and OIDs alone do not distinguish clones or
restores. Measure local cost before choosing a polling frequency.

```sql
-- Catalog/file-size sample; no application-row count or modification.
BEGIN READ ONLY;
SET LOCAL statement_timeout = '5s';
SET LOCAL lock_timeout = '1s';
SET LOCAL timezone = 'UTC';
SET LOCAL search_path = pg_catalog;
WITH relations(oid) AS (
    VALUES
        ('public.container_creation_intents'::regclass),
        ('public.container_materializations'::regclass),
        ('public.container_cleanup_receipts'::regclass),
        ('public.runtime_capacity_holds'::regclass),
        ('public.outbox_events'::regclass),
        ('public.container_intent_redrives'::regclass),
        ('public.container_cleanup_redrives'::regclass)
), sizes AS (
    SELECT c.relname AS relation,
           c.oid,
           pg_table_size(c.oid) AS table_bytes,
           pg_indexes_size(c.oid) AS index_bytes,
           pg_total_relation_size(c.oid) AS total_bytes,
           s.n_live_tup AS estimated_live_rows,
           s.n_dead_tup AS estimated_dead_rows,
           s.n_mod_since_analyze,
           s.last_analyze,
           s.last_autoanalyze
    FROM relations r
    JOIN pg_class c ON c.oid = r.oid
    LEFT JOIN pg_stat_user_tables s ON s.relid = c.oid
)
SELECT jsonb_build_object(
    'sampled_at', clock_timestamp(),
    'database', current_database(),
    'database_oid', (SELECT oid FROM pg_database WHERE datname = current_database()),
    'database_bytes', pg_database_size(current_database()),
    'server_version', current_setting('server_version'),
    'postmaster_started_at', pg_postmaster_start_time(),
    'read_only', current_setting('transaction_read_only'),
    'relations', (SELECT jsonb_agg(to_jsonb(s) ORDER BY relation) FROM sizes s)
);
COMMIT;
```

#### Exact retained-backlog diagnostic

This report scans application data; run it for diagnosis or a planned capacity
review, not on every worker/relay poll. Exact scans can consume CPU, I/O and temp
space and retain an MVCC snapshot. A timeout is a reason to inspect cost, not to
silently remove the deadline. Inventory and backlog are separate samples.

`MATCHED` means an existing receipt satisfies the pruning predicate's job,
container, runtime and intent comparison. Exact proof is checked first; only
unmatched rows are classified `INVALID_IDENTITY` or `MISSING`. Opaque container/
runtime strings remain untrimmed. Intent text follows pruning's canonical text
comparison: a valid but noncanonical payload UUID can be `MISSING`. This does not
normalize it into removal authority or validate a new RPC request.

Counts cover retained cleanup-action outbox rows, not lifetime cleanup or every
remaining intent/name obligation. Pruned transport disappears while proof and
name surveillance remain. A zero cleanup count does not establish that all work
or late-creation risk is gone. Active holds can represent live execution, not a
leak, and must not be added again to `runtime_nodes.running_jobs`. Marker/proof
mismatches call for diagnosis, never fabricated receipts or deletion. Whole-table
bytes cannot be assigned to individual cohorts using these counts.

```sql
-- On-demand full scans; timeout/error means unknown, never a healthy zero.
BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY;
SET LOCAL statement_timeout = '10s';
SET LOCAL lock_timeout = '1s';
SET LOCAL timezone = 'UTC';
SET LOCAL search_path = pg_catalog, public;
WITH cleanup AS MATERIALIZED (
    SELECT e.topic,
           e.status::text AS publication_state,
           e.cleanup_state,
           CASE
               WHEN COALESCE((
                   SELECT r.intent_id::text = e.payload->'ContainerCleanup'->>'IntentID'
                   FROM public.container_cleanup_receipts r
                   WHERE r.job_id = CASE
                       WHEN pg_input_is_valid(e.payload->>'JobID', 'uuid')
                       THEN (e.payload->>'JobID')::uuid
                   END
                     AND r.container_id = e.payload->'ContainerCleanup'->>'ContainerID'
                     AND r.runtime_instance = e.payload->'ContainerCleanup'->>'RuntimeInstance'
               ), FALSE) THEN 'MATCHED'
               WHEN NOT COALESCE(pg_input_is_valid(e.payload->>'JobID', 'uuid'), FALSE)
                 OR NOT COALESCE(pg_input_is_valid(e.payload->'ContainerCleanup'->>'IntentID', 'uuid'), FALSE)
                 OR COALESCE(e.payload->'ContainerCleanup'->>'ContainerID', '') = ''
                 OR COALESCE(e.payload->'ContainerCleanup'->>'RuntimeInstance', '') = ''
               THEN 'INVALID_IDENTITY'
               ELSE 'MISSING'
           END AS receipt,
           e.created_at
    FROM public.outbox_events e
    WHERE e.payload->>'Action' = 'CONTAINER_CLEANUP'
), groups AS (
    SELECT topic, publication_state, cleanup_state, receipt,
           count(*) AS rows,
           min(created_at) AS oldest_created_at
    FROM cleanup
    GROUP BY topic, publication_state, cleanup_state, receipt
)
SELECT jsonb_build_object(
    'sampled_at', statement_timestamp(),
    'database', current_database(),
    'read_only', current_setting('transaction_read_only'),
    'cleanup_groups', COALESCE((SELECT jsonb_agg(to_jsonb(g)
        ORDER BY topic, publication_state, cleanup_state, receipt) FROM groups g), '[]'::jsonb),
    'cleanup_rows', (SELECT count(*) FROM cleanup),
    'cleanup_without_exact_receipt', (SELECT count(*) FROM cleanup WHERE receipt <> 'MATCHED'),
    'completed_without_exact_receipt', (SELECT count(*) FROM cleanup WHERE cleanup_state = 'COMPLETED' AND receipt <> 'MATCHED'),
    'matched_receipt_without_completed_marker', (SELECT count(*) FROM cleanup WHERE receipt = 'MATCHED' AND cleanup_state IS DISTINCT FROM 'COMPLETED'),
    'holds', (SELECT jsonb_build_object(
        'active', count(*) FILTER (WHERE released_at IS NULL),
        'released', count(*) FILTER (WHERE released_at IS NOT NULL)) FROM public.runtime_capacity_holds),
    'ordinary_outbox_rows', (SELECT count(*) FROM public.outbox_events WHERE payload->>'Action' IS DISTINCT FROM 'CONTAINER_CLEANUP'),
    'non_cleanup_rows_with_cleanup_state', (SELECT count(*) FROM public.outbox_events
        WHERE payload->>'Action' IS DISTINCT FROM 'CONTAINER_CLEANUP' AND cleanup_state IS NOT NULL)
);
COMMIT;
```

#### Capacity runway

Use existing filesystem, PVC or provider-quota monitoring for available/used
bytes, timestamps and storage-budget identity. Database size excludes WAL, logs,
backups and other databases/shared consumers. Map tables, indexes, TOAST and WAL
to their actual volumes using deployment/tablespace configuration; these reports
do not discover that mapping. Calculate each relevant volume separately.

For comparable samples over a representative interval:

```text
growth_bytes_per_day = (used_later - used_earlier) / elapsed_days
usable_headroom = max(0, available_now - operator_safety_reserve)
runway_days = usable_headroom / growth_bytes_per_day
```

Only calculate runway for positive representative growth. Missing history,
zero/negative growth, mismatched volumes, restores or resizes do not give a reliable
exhaustion forecast. Available bytes already exclude current usage; do not subtract
database size again. For illustration, 120 GiB available minus a 20 GiB reserve,
at a measured 5 GiB/day, gives 20 days. These are not defaults or a measurement of
your deployment. Choose reserve, review cadence and capacity alerts for the time
needed to expand storage. No storage threshold authorizes proof garbage collection.

See PostgreSQL's [size-function definitions](https://www.postgresql.org/docs/18/functions-admin.html#FUNCTIONS-ADMIN-DBSIZE)
and [statistics semantics](https://www.postgresql.org/docs/18/monitoring-stats.html).

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
scripts/compose/up.sh
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
- Confirm workers can reach runtime node IPs on TCP `2376`. The Docker proxy
  uses `hostNetwork` so CNI host-port DNAT and pod NetworkPolicy do not break
  cross-node access; restrict node-to-node TCP `2376` at the infrastructure
  firewall layer. The proxy still requires
  `docker-proxy-ca`/`server`/`client-*` Secrets plus `docker-proxy-auth`
  `DOCKER_PROXY_TOKEN` (mTLS `verify required` + token second factor — missing
  or mismatched material fails closed and workers cannot ping `127.0.0.1:2376`
  or `$(NODE_IP):2376`).
- Host-network runtime-agent connections to PgBouncer are allowed only from
  the declared source CIDRs. Depending on the CNI, host-network traffic may
  reach pods with a node/WireGuard source or the node's pod CIDR; include both
  stable ranges in production (for example,
  `scripts/k8s/setup.sh --mode production --runtime-node-cidrs '10.250.0.0/24 10.42.0.0/16'`)
  so node replacement and scale-out remain covered. The setup script refuses
  to guess a production range; local mode may snapshot current node addresses,
  but pod CIDRs still need to be supplied when the CNI uses them. Update the
  ranges and rerun setup when the node pool or CNI network changes. Never
  replace this with `0.0.0.0/0`.
- The base policy intentionally uses the reserved `192.0.2.1/32` placeholder
  and is fail-closed. If your platform applies Kustomize directly instead of
  using `setup.sh`, keep the CIDR in a private overlay under source control:
  add a JSON6902 patch targeting
  `NetworkPolicy/chronoverse-runtime-agent-postgres` that replaces
  `/spec/ingress/0/from` with `podSelector: {}` plus one `ipBlock` per stable
  Docker-node CIDR. Apply that same overlay on every run; do not edit the
  generated base or run setup without the matching `--runtime-node-cidrs`, or
  the generated patch will be replaced on the next apply.
- The same declared node CIDRs are applied to
  `NetworkPolicy/chronoverse-runtime-agent-telemetry`, allowing only TCP 4317
  into LGTM for runtime-agent OTLP telemetry. Direct overlays must patch both
  policies; do not broaden LGTM ingress to all sources.
- The same mechanism fills the node placeholders in the default-deny tree:
  `NetworkPolicy/chronoverse-frontend` (hostNetwork controller to nginx),
  `NetworkPolicy/chronoverse-kubelet-probes` (kubelet to gateway and
  dashboard, node addresses only — no pod peer, so pod traffic to the
  upstreams stays restricted to nginx), and `NetworkPolicy/chronoverse-egress` (workers to node
  `2376`, via `/spec/egress/0/to`). Direct overlays must patch all three;
  the namespace stays fail-closed without them.
- A pod-network ingress controller reaches nginx from its own pod address,
  which no node range covers, so `NetworkPolicy/chronoverse-frontend` also
  admits pods labeled `app.kubernetes.io/name: ingress-nginx` and
  `app.kubernetes.io/component: controller` in any namespace. Keep that rule
  in ingress position 1; position 0 stays the node rule the CIDR patch
  replaces. A controller with different pod labels needs its own selector
  there, and the nginx real-IP trust list has to cover its addresses.
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
