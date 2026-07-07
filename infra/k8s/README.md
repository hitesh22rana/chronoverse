# Chronoverse Kubernetes manifests

Chronoverse Kubernetes support is packaged with Kustomize and an operator-facing
setup script.

## Setup

Use the setup script as the primary entrypoint:

```sh
scripts/k8s/setup.sh --mode local
scripts/k8s/setup.sh --mode production
```

`local` is a single-node, self-contained validation strategy. It includes
in-cluster PostgreSQL, Redis, ClickHouse, Kafka, Meilisearch, LGTM, hostPath
storage, generated local certificate bootstrap jobs, one replica per app, and a
single-node kind example.

`production` is a self-hosted strategy for your Kubernetes infrastructure. It
includes Chronoverse services, workers, PostgreSQL, Redis, Kafka, ClickHouse,
Meilisearch, runtime-agent, Docker proxy, Nginx, migrations, topic
initialization, dynamic PVCs, and HPAs.

The script is interactive by default and also supports repeatable flags:

```sh
scripts/k8s/setup.sh --mode production --namespace chronoverse --dry-run
scripts/k8s/setup.sh --mode production --storage-class fast-ssd
scripts/k8s/setup.sh --mode local --create-kind
```

## Render and Apply Directly

Direct Kustomize usage remains available after prerequisites are prepared:

```sh
kubectl kustomize infra/k8s/overlays/local
kubectl kustomize infra/k8s/overlays/production

kubectl apply -k infra/k8s/overlays/local
kubectl apply -k infra/k8s/overlays/production
```

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

The setup script checks required Secrets before applying manifests. Complete
operator-provided Secrets are preserved and never overwritten. Missing Secrets
are generated and created. Partial Secrets fail with a clear missing-key error.

Production Secrets include:

- `postgres-secret`: `POSTGRES_USER`, `POSTGRES_PASSWORD`, `POSTGRES_DB`
- `clickhouse-secret`: `CLICKHOUSE_PASSWORD`
- `meilisearch-secret`: `MEILISEARCH_MASTER_KEY`, `MEILI_MASTER_KEY`
- `kafka-tls-secret`: `KAFKA_SSL_KEYSTORE_PASSWORD`, `KAFKA_SSL_TRUSTSTORE_PASSWORD`, `KAFKA_SSL_KEY_PASSWORD`
- `chronoverse-auth`: `auth.ed`, `auth.ed.pub`
- `chronoverse-ca`: `ca.crt`
- `chronoverse-client-tls`: `tls.crt`, `tls.key`
- `chronoverse-service-tls`: service certificate/key pairs for users, workflows, jobs, notifications, and analytics services
- `chronoverse-infra-tls`: certificate/key pairs for PostgreSQL, Redis, ClickHouse, Kafka, and Meilisearch
- `chronoverse-kafka-tls`: `kafka.keystore.jks`, `kafka.truststore.jks`, `keystore_creds.txt`, `truststore_creds.txt`, `key_creds.txt`

## Storage

The local overlay uses hostPath PVs and is intended for single-node validation.

The production overlay uses dynamic PVCs by default. Provide a StorageClass with
`scripts/k8s/setup.sh --mode production --storage-class <name>` or rely on the
cluster default StorageClass. HostPath in production is a non-HA fallback only
and should be used only after explicitly accepting the risk.

## Runtime Ownership

Container workflows use Docker through runtime ownership. Each labeled
Docker-capable node runs one `docker-proxy` DaemonSet pod with a `runtime-agent`
sidecar. Workers do not need the Docker node label and can schedule anywhere.

Official overlays register `tcp://$(NODE_IP):2375` through the Docker proxy
`hostPort`, not `tcp://docker-proxy:2375`. This keeps running job cleanup valid
across proxy pod restarts on the same node.

Multi-node kind and similar Docker-container-based Kubernetes emulators may not
route one emulator node's hostPort from pods on another emulator node. If you
choose that topology, use a pod-IP runtime endpoint override as an
emulator-specific workaround. Real single-node and multi-node Kubernetes
clusters should use node-stable runtime endpoints.

Worker pods need egress to TCP `2375` on runtime node IPs. The base
NetworkPolicy allows that port, but production should also restrict access with
private networking, node firewalls or security groups, and the Docker socket
proxy allowlist. Do not expose TCP `2375` publicly.

## Validation

```sh
make k8s/render/local
make k8s/render/production
make k8s/dry-run/local
make k8s/dry-run/production
scripts/k8s/setup.sh --mode local --dry-run
scripts/k8s/setup.sh --mode production --dry-run
```
