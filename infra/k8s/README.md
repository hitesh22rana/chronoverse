# Chronoverse Kubernetes manifests

Chronoverse Kubernetes support is packaged with Kustomize and an operator-facing
setup script.

## Prerequisites

- `kubectl` configured for the intended cluster.
- OpenSSL for generated credentials and certificates.
- Java `keytool` when production Kafka TLS material must be generated.
- kind only when using `--create-kind`.
- A default dynamic StorageClass or `--storage-class <name>` for production.
- KEDA installed by the platform team; Chronoverse uses it for Kafka consumer
  lag autoscaling in every overlay.
- metrics-server or an equivalent `autoscaling/v2` resource-metrics provider
  before relying on the production HPAs.
- An nginx-compatible ingress controller providing `IngressClass` `nginx` when
  Kubernetes Ingress access is required. Port-forward access does not need it.

Container execution also requires labeled Docker-capable nodes as described
under [Cluster Prerequisites](#cluster-prerequisites).

Chronoverse deliberately does not install cluster-wide controllers. A typical
KEDA installation is:

```sh
helm repo add kedacore https://kedacore.github.io/charts
helm repo update
helm upgrade --install keda kedacore/keda --version 2.20.2 --namespace keda --create-namespace
kubectl get crd scaledobjects.keda.sh
kubectl get apiservice v1beta1.external.metrics.k8s.io
```

Install metrics-server and an ingress controller using the lifecycle mechanism
recommended by the Kubernetes provider. `setup.sh` validates or warns about
these contracts and prints actionable guidance; it never assumes ownership of
them.

## Setup

Use the setup script as the primary entrypoint:

```sh
scripts/k8s/setup.sh --mode local
scripts/k8s/setup.sh --mode production
```

`local` is a single-node, self-contained validation strategy. It includes
in-cluster PostgreSQL, Redis, ClickHouse, Kafka, Meilisearch, LGTM, hostPath
storage, generated local certificate bootstrap jobs, one replica per app, and a
single-node kind example. Because KEDA cannot read certificates from the local
certificate PVC, a narrowly scoped bootstrap Job mirrors only the local CA and
generic Kafka client identity into `chronoverse-ca` and
`chronoverse-client-tls` Secrets after certificate generation.

`production` is a self-hosted strategy for your Kubernetes infrastructure. It
includes Chronoverse services, workers, PostgreSQL, Redis, Kafka, ClickHouse,
Meilisearch, runtime-agent, Docker proxy, Nginx, migrations, topic
initialization, dynamic PVCs, and HPAs.

The script is interactive by default and also supports repeatable flags:

```sh
scripts/k8s/setup.sh --mode production --dry-run
scripts/k8s/setup.sh --mode production --storage-class fast-ssd
scripts/k8s/setup.sh --mode production --context my-cluster
scripts/k8s/setup.sh --mode production --skip-apply
scripts/k8s/setup.sh --mode production --context my-cluster --rotate-docker-proxy-certs
scripts/k8s/setup.sh --mode local --create-kind
```

`--skip-apply` bootstraps prerequisites without applying manifests. `--context`
is passed to every kubectl operation. Run `scripts/k8s/setup.sh --help` for the
authoritative option list.

## Render and Apply Directly

Direct Kustomize usage remains available after required Secrets, certificates,
node labels, storage, public URLs, and ingress values are prepared:

```sh
kubectl kustomize infra/k8s/overlays/local
kubectl kustomize infra/k8s/overlays/production

kubectl apply -k infra/k8s/overlays/local
kubectl apply -k infra/k8s/overlays/production
```

## gRPC Service Discovery

The users, workflows, jobs, notifications, and analytics Services are headless.
Kubernetes DNS publishes their ready Pod IPs instead of one virtual ClusterIP,
allowing the gRPC clients' `round_robin` policy to balance calls across the
resolved replicas. The public HTTP server and dashboard continue to use normal
ClusterIP Services.

Headless discovery makes the current replicas visible to gRPC. A healthy,
long-lived gRPC-Go channel may not immediately re-resolve DNS solely because an
HPA added a replica, so proactive endpoint refresh remains a separate scaling
improvement.

## Cluster Prerequisites

Kubernetes does not include a generic `kubectl` command to create a cluster.
Use your lifecycle tool, such as kind, minikube, kubeadm, or managed Kubernetes
provisioning, then apply Chronoverse.

Container workflows require Docker-capable runtime nodes. Label every node that
should own Docker containers:

```sh
kubectl label node <node-name> chronoverse.io/docker-workloads=true
```

Those nodes must expose Docker Engine at `/var/run/docker.sock`. Docker
Desktop's built-in `docker-desktop` Kubernetes context is not sufficient for the
Docker-backed worker path because it does not expose a Docker Engine socket to
pods.

For kind validation:

```sh
kind create cluster --name chronoverse --config infra/k8s/overlays/local/kind-cluster.yaml
kubectl config use-context kind-chronoverse
scripts/k8s/setup.sh --mode local
```

## Secrets

The setup script checks required Secrets before applying manifests. Valid, complete
operator-provided Secrets are preserved and never overwritten. Missing Secrets
are generated and created. Partial Secrets fail with a clear missing-key error.
Local generated data-store credentials are deterministic development defaults
so retained hostPath data can survive a resource delete/recreate without
drifting away from regenerated Secrets. Production generated fallback
credentials remain random.

For `chronoverse-server-security`, the setup script additionally rejects the
known development placeholder, rejects reuse of one value for
`CRYPTO_SECRET` and `SERVER_CSRF_HMAC_SECRET`, and requires
`CRYPTO_SECRET` to be exactly 32 bytes. An invalid local Secret is replaced with
the safe local values; an invalid production Secret stops setup so the operator
can correct it.

Production Secrets include:

- `postgres-secret`: `POSTGRES_USER`, `POSTGRES_PASSWORD`, `POSTGRES_DB`
- `postgres-app-secret`: the dedicated, non-superuser application role using
  the same key names; application pods never receive `postgres-secret`
- `clickhouse-secret`: `CLICKHOUSE_PASSWORD`
- `meilisearch-secret`: `MEILISEARCH_MASTER_KEY`, `MEILI_MASTER_KEY`
- `kafka-tls-secret`: `KAFKA_SSL_KEYSTORE_PASSWORD`, `KAFKA_SSL_TRUSTSTORE_PASSWORD`, `KAFKA_SSL_KEY_PASSWORD`
- `chronoverse-server-security`: `CRYPTO_SECRET`, `SERVER_CSRF_HMAC_SECRET`
- `docker-proxy-auth`: `DOCKER_PROXY_TOKEN`
- `docker-proxy-ca`: `ca.crt` (mTLS CA for Docker proxy)
- `docker-proxy-server`: `server.pem` (HAProxy `crt` + `ca-file` for `:2376`)
- `docker-proxy-client-runtime-agent` / `docker-proxy-client-workflow-worker` / `docker-proxy-client-execution-worker`: `tls.crt`, `tls.key` (separate client-auth-only role identities for `tcp://...:2376`, `DOCKER_PROXY_TLS_*`, `ServerName docker-proxy`)
- `chronoverse-auth`: `auth.ed`, `auth.ed.pub`
- `chronoverse-ca`: `ca.crt`
- `chronoverse-ingress-tls`: `tls.crt`, `tls.key`
- `chronoverse-client-tls`: `tls.crt`, `tls.key`
- `chronoverse-service-tls`: service certificate/key pairs for users, workflows, jobs, notifications, and analytics services
- `chronoverse-infra-tls`: certificate/key pairs for PostgreSQL, Redis, ClickHouse, Kafka, and Meilisearch
- `chronoverse-kafka-tls`: `kafka.keystore.jks`, `kafka.truststore.jks`, `keystore_creds.txt`, `truststore_creds.txt`, `key_creds.txt`

Internal production trust-chain Secrets are atomic. If you bring your own
`chronoverse-ca`, also provide `chronoverse-client-tls`,
`chronoverse-service-tls`, `chronoverse-infra-tls`, and
`chronoverse-kafka-tls` from the same trust chain. The setup script rejects
partial internal TLS Secret sets instead of mixing generated and
operator-provided CAs. `chronoverse-ingress-tls` is external edge TLS and can be
managed independently.

## Storage

The local overlay uses hostPath PVs and is intended for single-node validation.
Those PVs use retained node-local paths; delete the kind cluster or clear the
hostPath directories when you intentionally want a completely empty local data
set.

The production overlay uses dynamic PVCs by default. Provide a StorageClass with
`scripts/k8s/setup.sh --mode production --storage-class <name>` or rely on the
cluster default StorageClass. Production requires dynamic storage; use the
`local` strategy for single-node hostPath validation.

## PostgreSQL connection capacity

Applications connect to the `postgres` Service, which is a two-replica
PgBouncer tier using transaction pooling. PgBouncer connects to the database
through the private `postgres-primary` Service. Each pooler is capped at 25
backend connections (20 normal plus 5 reserve), so the pair can consume at most
50 of PostgreSQL's 100 connections and leaves capacity for bootstrap,
migrations, administration, and failure handling.

The `database-migration` Job also connects directly to `postgres-primary`.
`golang-migrate` uses session-level PostgreSQL advisory locks, which are not
compatible with PgBouncer transaction pooling; normal application traffic must
continue to use the pooled `postgres` Service.

The setup script creates `chronoverse_app` as a dedicated non-superuser role.
Only PostgreSQL itself and the role-bootstrap Job receive `postgres-secret`;
application pods receive `postgres-app-secret`. Application pool budgets are
declared per workload ConfigMap. API services use 0 minimum / 4 maximum,
scheduling and analytics use 0 / 2, runtime-agent uses 0 / 2, job-log
processing uses 0 / 4, and outbox uses 1 / 4. The Go client honors a zero
minimum and retries PostgreSQL startup for roughly 30 seconds with bounded
backoff.

PostgreSQL-client Deployments use `maxSurge: 0`, and gRPC services drain for up
to 20 seconds on SIGTERM before their pools and telemetry providers close.
These are capacity guards, not proof of workload capacity: validate PgBouncer
queue time, PostgreSQL memory/CPU, transaction duration, and connection counts
under representative load before changing replica or pool ceilings.

## Runtime Ownership

Container workflows use Docker through runtime ownership. Each labeled
Docker-capable node runs one `docker-proxy` DaemonSet pod with a `runtime-agent`
sidecar. Workers do not need the Docker node label and can schedule anywhere.

Official overlays register an IPv4/IPv6-safe `NODE_IP:2376` endpoint through
the Docker proxy `hostPort`, not a load-balanced ClusterIP. This keeps running job cleanup valid
across proxy pod restarts on the same node. The DaemonSet's HAProxy binds
`:2376 ssl crt /certs/docker-proxy/server.pem ca-file /certs/docker-proxy/ca.crt verify required`
plus the `X-Chronoverse-Docker-Proxy-Token` header and an exact Docker
method/path allowlist; runtime-agent health probes `tcp://127.0.0.1:2376`
(loopback), while the advertised node endpoint uses role-specific mTLS
certificates (`DOCKER_PROXY_TLS_CA_FILE`/`CERT_FILE`/`KEY_FILE`,
`ServerName docker-proxy` via `internal/pkg/kind/container/docker.go`) —
`docker-proxy-client-runtime-agent` (ping/version), `workflow-worker` (image
inspect/pull plus container logs/stop/delete), and `execution-worker` (the
workflow surface plus container inspect/create/start/wait and network
inspect/create). Unknown identities and cross-role calls are denied. The token
remains as a second factor; workload containers receive neither.

The root HAProxy server key and non-root runtime-agent client key use separate
projected volumes. Worker pods mount only their own role Secret and use
`fsGroup: 101` with `0440` files, so replica count and node count do not change
key readability or expose another role's private key. TLS clients reload the CA
bundle and keypair on new handshakes, and endpoint clients are bounded to 256
entries with idle/LRU eviction to avoid unbounded growth as runtime nodes churn.

Multi-node kind and similar Docker-container-based Kubernetes emulators may not
route one emulator node's hostPort from pods on another emulator node. If you
choose that topology, use a pod-IP runtime endpoint override as an
emulator-specific workaround. Real single-node and multi-node Kubernetes
clusters should use node-stable runtime endpoints.

Worker pods need egress to TCP `2376` on runtime node IPs. The base
NetworkPolicy allows that port, but `hostPort` bypasses `NetworkPolicy` across
CNI implementations — production must restrict `2376` at the infrastructure
layer (node firewall / security group / CNI host policy) in addition to the
mTLS + token + allowlist. Do not expose `2376` publicly.

The supported runtime matrix is explicit:

| Topology | Support and requirement |
| --- | --- |
| Compose development/production | Single Docker host and one proxy; workers may replicate in production and share a least-privilege role identity. |
| Kubernetes single-node | Supported when the node exposes `/var/run/docker.sock` and carries the Docker workload label. |
| Kubernetes multi-node | Supported when every labeled runtime node exposes Docker Engine and worker pods can route to every labeled node IP on `2376`. |
| containerd-only Kubernetes or Docker Desktop's built-in Kubernetes | Not supported for Docker-backed workflows because the required Docker Engine socket is absent. |
| Multi-node kind-like emulators | Conditional: some emulator networks cannot route cross-node hostPorts; validate first or use the documented pod-IP override only for that emulator. |

The execution-worker role can create containers and is therefore
node-root-equivalent if both that role key and token are compromised. mTLS
prevents network impersonation and the role ACL contains runtime-agent and
workflow-worker compromise from container creation, but the workflow cleanup
role can still read logs and stop/delete a known container ID. These calls are
not tenant-scoped at the HAProxy layer. The design does not turn Docker Engine
into a tenant security boundary; use a purpose-built execution broker or
isolated/rootless runtime pools where stronger blast-radius control is required.

## Docker Proxy Certificate Rotation

The setup script creates new installations atomically. Rotate an existing
installation during a maintenance window with:

```sh
scripts/k8s/setup.sh --mode production --context <context> \
  --rotate-docker-proxy-certs
```

The rotation script installs an old+new CA bundle, rolls the DaemonSet, rotates
the three client-role Secrets, rotates the server Secret, then removes the old
CA and rolls all proxy clients again. This order works for single-node and
multi-node clusters without a mixed-issuer authentication gap. A single-node
DaemonSet still has a brief proxy interruption while its hostPort pod restarts.
Use `scripts/k8s/rotate-docker-proxy-certs.sh --context <context>` directly when
the manifests do not need to be reapplied. Setup rejects rotation with
`--dry-run`/`--skip-apply` and requires an existing proxy and worker deployment.

Production authentication cookies are scoped from `SERVER_HOST_URL`. A
production overlay configured for `https://chronoverse.example.com` will reject
cookies when accessed through `http://localhost:8080` port-forwarding. Use the
`local` strategy for normal localhost browser testing. For a temporary
production-overlay smoke test in kind, patch `server-config` to
`http://localhost:8080`, restart `deployment/server`, and reapply the production
overlay before treating the cluster as production again.

## Validation

```sh
make k8s/render/local
make k8s/render/production
make k8s/dry-run/local
make k8s/dry-run/production
scripts/k8s/setup.sh --mode local --dry-run
scripts/k8s/setup.sh --mode production --dry-run
```

The render targets work offline. The kubectl and setup-script dry runs still use
API discovery and must point at a reachable cluster. Use a separate validator
such as `kubeconform` when live-cluster OpenAPI validation is not available.
