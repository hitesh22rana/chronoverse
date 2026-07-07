# Chronoverse Kubernetes manifests

Chronoverse Kubernetes support is packaged with Kustomize, which is available through `kubectl`.

## Render and apply

```sh
kubectl kustomize infra/k8s/overlays/local
kubectl kustomize infra/k8s/overlays/production

kubectl apply -k infra/k8s/overlays/local
kubectl apply -k infra/k8s/overlays/production
```

Kubernetes does not include a generic `kubectl` command to create a cluster.
Use your cluster lifecycle tool, such as kind, minikube, kubeadm, or managed
Kubernetes provisioning, then apply the Kustomize overlay with `kubectl`.

Container workflows require Docker-capable runtime nodes. Before applying the
overlay, make sure every node that should own Docker containers exposes Docker
Engine at `/var/run/docker.sock` and has this label:

```sh
kubectl label node <node-name> chronoverse.io/docker-workloads=true
```

For kind, the Docker socket mount, shared certificate mount, and node label must
be configured when the cluster is created. The repository includes a two-node
local example where both nodes are Docker-capable and mount the same host
certificate directory into `/var/lib/chronoverse-data/certs`:

```sh
kind create cluster --name chronoverse --config infra/k8s/overlays/local/kind-cluster.yaml
kubectl config use-context kind-chronoverse
kubectl apply -k infra/k8s/overlays/local
```

Do not use Docker Desktop's built-in `docker-desktop` Kubernetes context for
the Docker-backed worker path. That cluster runs containerd and does not expose
Docker Engine at `/var/run/docker.sock` inside the node, so `docker-proxy` fails
with `hostPath type check failed: /var/run/docker.sock is not a socket file`.

## Layout

- `base/`: application services, workers, dashboard, Nginx, Docker proxy, RBAC, network policy, PDBs, Kafka topic initialization, and shared configuration.
- `overlays/local/`: multi-node-capable kind profile with one replica per app deployment, in-cluster PostgreSQL, Redis, ClickHouse, Kafka, Meilisearch, LGTM, hostPath storage, and certificate bootstrap jobs. The stateful dependencies and Chronoverse app pods use generated local TLS material from the shared cert volume, which is mounted by every Docker-capable kind node.
- `overlays/production/`: external-ready profile that expects managed data stores and pre-created Secrets. It includes HorizontalPodAutoscalers for stateless APIs and workers.

## Required production Secrets

Create these before applying `overlays/production`:

- `postgres-secret`: `POSTGRES_USER`, `POSTGRES_PASSWORD`, `POSTGRES_DB`
- `clickhouse-secret`: `CLICKHOUSE_PASSWORD`
- `meilisearch-secret`: `MEILISEARCH_MASTER_KEY`
- `chronoverse-auth`: `auth.ed`, `auth.ed.pub`
- `chronoverse-ca`: `ca.crt`
- `chronoverse-client-tls`: `tls.crt`, `tls.key`
- `chronoverse-service-tls`: `users-service.crt`, `users-service.key`, `workflows-service.crt`, `workflows-service.key`, `jobs-service.crt`, `jobs-service.key`, `notifications-service.crt`, `notifications-service.key`, `analytics-service.crt`, `analytics-service.key`
- `chronoverse-kafka-tls`: `kafka.keystore.jks`, `kafka.truststore.jks`, `keystore_creds.txt`, `truststore_creds.txt`, `key_creds.txt`

## Production notes

- Patch the production ConfigMaps for real PostgreSQL, Redis, Kafka, ClickHouse, Meilisearch, public URL, and allowed origins.
- `init-kafka-topics` creates or expands `workflows`, `jobs`, `job_logs`, and `analytics` topics.
- Production HPAs require metrics-server or another provider for `autoscaling/v2` resource metrics. CPU/memory HPAs are included for app services and workers; Kafka-lag-based worker scaling needs KEDA or custom metrics.
- Container workflows use the Docker socket proxy through runtime ownership. Each labeled Docker-capable node runs one `docker-proxy` DaemonSet pod with a `runtime-agent` sidecar. Workers do not need the Docker node label and can schedule anywhere.
- Runtime-agent registers `tcp://$(NODE_IP):2375` through the Docker proxy `hostPort`, not pod IP or `tcp://docker-proxy:2375`. This keeps running job cleanup valid across proxy pod restarts on the same node.
- Worker pods need egress to TCP `2375` on runtime node IPs. The base NetworkPolicy allows that port, but production should also restrict access with private networking, node firewalls or security groups, and the Docker socket proxy allowlist. Do not expose TCP `2375` publicly.
- If a host already runs a Docker TCP listener on `2375`, the DaemonSet cannot bind the same host port. Move the host listener or choose a different restricted port consistently in `containerPort`, `hostPort`, `DOCKER_HOST`, and `RUNTIME_AGENT_DOCKER_ENDPOINT`.
- The local overlay uses hostPath storage and generated cert material. Do not use it as the production security or persistence model.
