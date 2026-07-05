# Multi-Node Runtime Locality Issue

## Summary

This document describes a runtime locality problem that appears when a
container-execution platform scales from a single Kubernetes node to multiple
runtime-capable nodes.

The problem is not Kubernetes itself. The problem is that container images,
running containers, container IDs, and Docker logs are node-local state, while
the application workers that make lifecycle decisions are scheduled
independently across the cluster.

In a single-node setup, this issue usually does not exist because every worker
talks to the same Docker daemon. In a multi-node setup, each node has its own
Docker daemon, image cache, running containers, and log streams. Without
explicit node ownership and routing, a worker can receive a request for an image
or container that exists only on a different node.

## Current Generic Setup

The current architecture can be described generically as:

- A Kubernetes cluster runs application workers as regular Deployments.
- A `workflow-worker` or equivalent worker handles workflow lifecycle events,
  such as build, validate, terminate, delete, reschedule, and cleanup.
- An `execution-worker` or equivalent worker handles job execution events, such
  as claim job, start container, stream logs, renew leases, complete job, fail
  job, and recover expired leases.
- A Docker socket proxy runs on Docker-capable nodes, commonly as a DaemonSet.
- Workers access Docker through the proxy instead of mounting the Docker socket
  directly.
- Docker-capable nodes are labeled, for example:

  ```text
  runtime.example.com/docker-workloads=true
  ```

- Workers are scheduled onto labeled nodes using node affinity or node
  selectors.
- Events are delivered through Kafka or a similar message broker.
- Events are keyed by a stable workload identifier, such as `workflow_id`, so
  events for the same workflow are ordered within a topic partition.

This setup is workable on a single node. It becomes unsafe on multiple nodes if
workers rely on node-local Docker state without recording which node owns that
state.

## Why Single-Node Works

In a single-node setup, all runtime actors share one node and one Docker daemon:

```text
workflow-worker -> docker-proxy -> Docker daemon on node-a
execution-worker -> docker-proxy -> Docker daemon on node-a
runtime containers -> Docker daemon on node-a
image cache -> Docker daemon on node-a
```

If the workflow worker pulls or validates an image, the execution worker can use
that same image because both talk to the same daemon.

If the execution worker creates a container, the workflow worker can terminate,
inspect, read logs, or remove that container because the container ID belongs to
the same daemon.

The specific node-locality problem does not exist because there is only one
runtime node.

Single-node still has normal operational limitations:

- The node is a single point of failure.
- Runtime capacity is limited by one node's CPU, memory, disk, and Docker daemon.
- Worker replicas increase concurrency only within that one node's capacity.
- If the node dies, running containers and local image cache state are lost.

Those are capacity and availability limits, not cross-node locality bugs.

## Why Multi-Node Breaks

In a multi-node setup, Docker state is isolated per node:

```text
node-a Docker daemon != node-b Docker daemon
node-a image cache != node-b image cache
node-a containers != node-b containers
node-a container logs != node-b container logs
```

For example:

```text
workflow-worker runs on node-a
execution-worker runs on node-b
```

The workflow worker may pull or validate an image on `node-a`. Later, the
execution worker may receive a job for the same workflow on `node-b`. The image
may not exist on `node-b`, even though the workflow was previously built or
validated successfully.

Another example:

```text
execution-worker starts container abc123 on node-b
workflow-worker later handles terminate/delete on node-a
```

The workflow worker cannot reliably terminate, inspect, stream logs, or remove
container `abc123` through `node-a` because that container exists only in the
Docker daemon on `node-b`.

This is the core issue:

```text
Workload lifecycle state is cluster-level.
Docker runtime state is node-local.
The system does not persist or route by node-local ownership.
```

## Kafka Partitioning Helps Ordering, Not Node Locality

Message keys such as `workflow_id` are useful and should be kept. They provide
ordering within a topic partition:

```text
same workflow_id -> same partition in workflows topic
same workflow_id -> same partition in jobs topic
```

However, this does not guarantee that different consumer groups or different
topics are processed on the same Kubernetes node.

For example:

```text
workflows topic partition 2 -> workflow-worker pod on node-a
jobs topic partition 2      -> execution-worker pod on node-b
```

Even if both topics use `workflow_id` as the key, Kafka does not coordinate
Kubernetes pod placement across consumer groups. Partition ownership belongs to
Kafka consumers, not to Kubernetes nodes.

Kafka partitioning provides:

- Ordered processing within a partition.
- Stable routing within a topic for the same key.
- Better concurrency across partitions.

Kafka partitioning does not provide:

- Same-node placement across different worker types.
- Same-node placement across different Kafka topics.
- Awareness of Docker image cache state.
- Awareness of where a container ID was created.
- Automatic routing of lifecycle operations to the node that owns a container.

## Concrete Failure Modes

### Image Exists On The Wrong Node

The workflow worker pulls or validates an image on one node. The execution
worker later runs on another node and tries to start the container.

Possible outcomes:

- Container create fails because the image is missing locally.
- The second node pulls the image again, causing cold-start latency.
- Different nodes may pull different image content if mutable tags are used.
- Registry rate limits may be hit because every node pulls independently.
- Build status can say "ready" even though not every runtime node can execute.

### Mutable Tags Can Drift Across Nodes

If a workflow uses a mutable tag such as:

```text
example/image:latest
```

then different nodes can pull different image digests at different times.

Possible outcomes:

- Build validation happens against one image digest.
- Execution happens against another image digest.
- Results become non-reproducible.
- Debugging becomes difficult because the tag no longer identifies exact code.

### Container ID Exists Only On One Node

Docker container IDs are local to the daemon that created them.

If a worker on another node tries to operate on the container ID, it may see:

- Not found errors.
- Missing logs.
- Failed terminate requests.
- Failed remove requests.
- Incorrect recovery behavior.

### Terminate And Delete May Not Clean Up Running Containers

Workflow termination or deletion often needs to:

- Mark jobs canceled.
- Stop running containers.
- Replay final logs.
- Remove containers.
- Delete retained logs from external stores.

If the cleanup worker is not routed to the node where the container runs, it
cannot reliably stop or remove that container.

Possible outcomes:

- The workflow appears terminated at the application level.
- The container continues running on the original node.
- Resources leak.
- Logs continue to be produced after cancellation.
- Future cleanup has no reliable way to find the container.

### Log Streaming Is Node-Local

Docker log streams are exposed by the daemon that owns the container.

If the component responsible for reading logs runs on the wrong node, it cannot
replay, stream, or recover logs for that container.

Possible outcomes:

- Live logs disappear.
- Final logs are missing.
- Canceled job logs are not replayed.
- Expired lease recovery loses logs.
- Retained log stores become incomplete.

### Expired Lease Recovery Can Run On The Wrong Node

Distributed execution systems often recover jobs whose worker died or failed to
renew a lease.

Recovery may need to:

- Inspect the container.
- Determine whether it is still running.
- Terminate the container.
- Replay logs.
- Remove the container.
- Mark the job completed or failed.

If recovery is performed by a worker on a different node than the original
container, recovery cannot inspect or clean up the correct container.

Possible outcomes:

- The job is marked failed even though the container completed.
- The job is retried while the old container is still running.
- Duplicate execution happens.
- Containers leak.
- Logs are incomplete or duplicated.

### Pod Rescheduling Breaks Implicit Locality

Even if pod affinity initially places related workers on the same node,
Kubernetes can reschedule pods because of:

- Node drains.
- Node failures.
- Rolling deployments.
- Autoscaler decisions.
- Resource pressure.
- Pod eviction.

If the system depends on implicit same-node placement, rescheduling can break
lifecycle access after the container has already been created.

### Scaling Workers Does Not Scale Runtime Ownership

Scaling worker Deployments increases the number of consumers, but it does not
automatically coordinate image or container ownership.

Possible outcomes:

- New workers receive events for containers created elsewhere.
- More workers increase the probability of cross-node mismatch.
- Kafka partitions and Kubernetes nodes become unrelated scheduling domains.
- Scaling beyond one node creates correctness issues rather than only capacity
  improvements.

### Generic Cluster Service Hides Node Identity

A normal Kubernetes Service over all Docker proxy pods hides which node is being
targeted.

For runtime lifecycle operations, this is dangerous. A request such as
"remove container abc123" must go to the node where `abc123` exists. A generic
load-balanced Service can send it to the wrong proxy.

Node-local traffic policies reduce accidental cross-node routing for pods on the
same node, but they do not solve cases where the worker itself is on the wrong
node.

### Security Risk Increases With Cross-Node Docker Access

If workers need to call Docker proxies on arbitrary nodes, the Docker API becomes
a cluster-level data plane.

Risks include:

- Exposing privileged Docker operations across the cluster network.
- Overly broad NetworkPolicy permissions.
- Difficulty enforcing per-node or per-job authorization.
- Accidental access to containers owned by other workloads.
- Larger blast radius if a worker is compromised.

The Docker socket, even through a proxy, should be treated as highly privileged.

## Root Cause

The root cause is missing runtime ownership metadata and routing.

The system currently has durable identifiers such as:

```text
workflow_id
job_id
container_id
```

But a `container_id` is not enough to identify node-local runtime state in a
multi-node runtime. The missing association is the node or runtime endpoint that
owns that state:

```text
workflow_id
job_id
runtime_node_id
runtime_endpoint_id
container_id
image_ref_or_digest
```

Without this metadata, the system cannot reliably answer:

- Which node owns this container?
- Which Docker daemon should receive terminate/remove/log requests?
- Which node has already pulled the image?
- Which node is healthy enough to accept a new job?
- What should happen if the owning node disappears?

## Why Node Affinity Alone Is Not Enough

Node affinity can restrict workers to Docker-capable nodes:

```text
runtime.example.com/docker-workloads=true
```

This is necessary, but not sufficient.

It guarantees:

- Workers run only on eligible runtime nodes.
- Docker proxy pods run only on eligible runtime nodes.

It does not guarantee:

- Workflow worker and execution worker for the same workflow run on the same
  node.
- A future terminate/delete event runs on the same node that created the
  container.
- A recovered job is inspected on the correct node.
- A newly scaled worker sees images pulled by another node.

Pod affinity can improve placement, but it is still an implicit scheduling hint.
It does not make node-local Docker state globally visible.

## Problem Statement

The platform currently assumes that Docker image cache and container lifecycle
state are globally available to workers. That assumption is true enough on a
single-node Kubernetes cluster because all workers share one Docker daemon. It is
false on a multi-node cluster because each node has a separate Docker daemon,
image cache, running containers, and log streams.

Kafka partitioning by workload ID preserves event ordering, but it does not
guarantee that workflow lifecycle workers and execution workers for the same
workload run on the same Kubernetes node. Therefore, image pulls, container
starts, log streaming, termination, deletion, and lease recovery can target the
wrong node in a multi-node deployment.

## Proposed Solution: Runtime Ownership With Direct Docker Data Plane

The preferred solution is a hybrid runtime-control model:

```text
runtime-agent    -> node-local control plane and heartbeat
docker-proxy     -> node-local Docker API proxy
jobs-service     -> runtime assignment and durable ownership
execution-worker -> Docker data-plane client for execution and log streaming
workflow-worker  -> workflow lifecycle orchestrator and owner-aware cleanup client
```

The important correction is not that every Docker operation must be proxied
through a new service. The important correction is that every Docker operation
must include the runtime node that owns the target Docker state.

This means the unsafe API shape:

```text
Terminate(container_id)
Logs(container_id)
Remove(container_id)
Inspect(container_id)
```

must become:

```text
Terminate(runtime_node_id, container_id)
Logs(runtime_node_id, container_id)
Remove(runtime_node_id, container_id)
Inspect(runtime_node_id, container_id)
```

In this model, the high-volume data path stays in the `execution-worker`.
Container logs do not need to flow through `runtime-agent`.

## Runtime Node Pod

Run a runtime node pod as a DaemonSet on every Docker-capable Kubernetes node.
The pod should contain two containers:

```text
runtime-node DaemonSet pod
|-- docker-proxy
`-- runtime-agent
```

The `docker-proxy` container exposes the local Docker daemon. The
`runtime-agent` container registers and heartbeats the Docker endpoint for that
specific pod and Kubernetes node.

The `runtime-agent` can discover its own identity using the Kubernetes downward
API:

```yaml
env:
- name: NODE_NAME
  valueFrom:
    fieldRef:
      fieldPath: spec.nodeName
- name: POD_IP
  valueFrom:
    fieldRef:
      fieldPath: status.podIP
```

The registered endpoint would be:

```text
runtime_node_id = <node name or stable node uid>
node_name = <spec.nodeName>
docker_endpoint = tcp://<runtime-node-pod-ip>:2375
status = READY
last_heartbeat_at = now()
```

Because `runtime-agent` and `docker-proxy` are sidecars in the same pod, the
agent does not need to query EndpointSlices to discover which proxy belongs to
which node. The pod itself knows its node name and pod IP.

## Runtime Registry

Add a small runtime registry to PostgreSQL. A minimal version is:

```sql
CREATE TABLE runtime_nodes (
    id TEXT PRIMARY KEY,
    node_name TEXT UNIQUE NOT NULL,
    docker_endpoint TEXT NOT NULL,
    status TEXT NOT NULL,
    last_heartbeat_at TIMESTAMP WITHOUT TIME ZONE NOT NULL,
    max_concurrency INT NOT NULL,
    running_jobs INT NOT NULL DEFAULT 0,
    labels JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMP WITHOUT TIME ZONE NOT NULL,
    updated_at TIMESTAMP WITHOUT TIME ZONE NOT NULL
);
```

Extend jobs with runtime ownership:

```sql
ALTER TABLE jobs
    ADD COLUMN runtime_node_id TEXT NULL REFERENCES runtime_nodes(id),
    ADD COLUMN runtime_endpoint TEXT NULL;
```

`runtime_endpoint` can be denormalized onto the job as a historical snapshot.
That makes debugging easier and protects terminal job history from later endpoint
changes. Live operations should still resolve the latest endpoint from
`runtime_nodes` using `runtime_node_id`.

Resolved image metadata belongs to the workflow row, not the job row:

```sql
ALTER TABLE workflows
    ADD COLUMN resolved_image_ref TEXT NULL,
    ADD COLUMN resolved_image_digest TEXT NULL;
```

These fields are only meaningful for `CONTAINER` workflows. `payload` remains
user-authored configuration, and `build_hash` remains based on build-relevant
user-authored inputs instead of derived digest output.

## Endpoint Lifecycle And Node Restarts

If a runtime-node pod restarts and gets a new pod IP, the PostgreSQL row must be
updated. The update is handled automatically by `runtime-agent` during startup
and heartbeat.

The lifecycle should be:

1. Runtime-node pod starts on a Kubernetes node.
2. `runtime-agent` reads `NODE_NAME` and `POD_IP`.
3. `runtime-agent` pings the local Docker proxy.
4. `runtime-agent` upserts `runtime_nodes`.
5. `runtime-agent` heartbeats every few seconds.
6. `jobs-service` only assigns jobs to rows whose heartbeat is fresh and whose
   status is `READY`.

For example:

```sql
INSERT INTO runtime_nodes (
    id,
    node_name,
    docker_endpoint,
    status,
    last_heartbeat_at,
    max_concurrency,
    running_jobs,
    created_at,
    updated_at
) VALUES (
    $1,
    $2,
    $3,
    'READY',
    now() AT TIME ZONE 'utc',
    $4,
    0,
    now() AT TIME ZONE 'utc',
    now() AT TIME ZONE 'utc'
)
ON CONFLICT (id) DO UPDATE
SET node_name = EXCLUDED.node_name,
    docker_endpoint = EXCLUDED.docker_endpoint,
    status = EXCLUDED.status,
    last_heartbeat_at = EXCLUDED.last_heartbeat_at,
    max_concurrency = EXCLUDED.max_concurrency,
    updated_at = EXCLUDED.updated_at;
```

If the node or pod disappears, no one needs to eagerly update the row
immediately. The heartbeat becomes stale. `jobs-service` should treat stale
runtime nodes as unavailable:

```sql
WHERE status = 'READY'
  AND last_heartbeat_at > (now() AT TIME ZONE 'utc') - interval '30 seconds'
```

This is enough for scheduling safety. A separate reconciliation loop can mark
stale rows as `UNHEALTHY`, but job assignment should not depend on that loop
running perfectly.

For running jobs on a restarted node:

- If the Docker daemon survived and containers still exist, recovery can use the
  latest endpoint from `runtime_nodes` and inspect the existing container.
- If the node reboot removed runtime containers, recovery should mark the job as
  lost or system-failed and retry according to the normal retry policy.
- If the runtime node heartbeat is stale, recovery should not blindly retry on a
  different node until the old owner is considered dead, otherwise duplicate
  execution is possible.

## Job Claim And Assignment

`jobs-service` already owns the atomic transition from queued to running. Runtime
assignment should happen in the same transaction.

Conceptually, `ClaimJob` should:

1. Select one fresh `READY` runtime node with available capacity.
2. Atomically move the job from `QUEUED` to `RUNNING`.
3. Persist `runtime_node_id`.
4. Set lease fields.
5. Increment the runtime node's `running_jobs`.
6. Return the runtime node and endpoint to `execution-worker`.

The claim response should include:

```proto
string runtime_node_id = 11;
string runtime_endpoint = 12;
```

The `execution-worker` then creates a Docker client for that endpoint and uses
that same client for image pull, container create/start, wait, and log
streaming. The image digest is read from the workflow response; jobs never store
image metadata.

When the container is created, `AttachJobContainer` should persist both the
container and the runtime owner:

```proto
message AttachJobContainerRequest {
  string id = 1;
  string lease_token = 2;
  string container_id = 3;
  string runtime_node_id = 4;
}
```

The database update should validate the owner:

```sql
UPDATE jobs
SET container_id = $3
WHERE id = $1
  AND lease_token = $2
  AND runtime_node_id = $4
  AND status = 'RUNNING';
```

## Workflow Build Semantics

In a multi-node runtime, workflow build should not mean "pull this image into
the Docker daemon visible to this `workflow-worker`."

The safer meaning is:

```text
workflow build = validate payload and resolve image reference to immutable digest
job execution = ensure that digest exists on the assigned runtime node
```

The `workflow-worker` may remain a Docker data-plane client for low-volume,
owner-aware cleanup or explicit prewarming, but it should not rely on its local
Docker daemon as evidence that every runtime node can execute the workflow.

## Terminate And Delete

Workflow termination and deletion should still be orchestrated by
`workflow-worker`, but Docker cleanup must be owner-aware.

For each running or canceled container job, `workflow-worker` should fetch:

```text
job_id
runtime_node_id
container_id
```

Then it should resolve the latest endpoint for `runtime_node_id` and call:

```text
Terminate(runtime_endpoint, container_id)
Logs(runtime_endpoint, container_id)
Remove(runtime_endpoint, container_id)
```

If the runtime node is unavailable, the workflow can still be marked terminated
at the application level, but the container cleanup should be recorded as
pending or best-effort. A reconciliation loop can finish cleanup when the node
returns or mark it abandoned after a configured timeout.

## Lease Recovery

Lease recovery should preserve runtime ownership.

In the hybrid model, recovery does not require the recovering
`execution-worker` pod to be scheduled on the same Kubernetes node as the
container. Any execution worker can recover the job if it uses the persisted
`runtime_node_id` and the latest endpoint for that runtime node.

`RecoverExpiredJobLeases` should return:

```text
job_id
workflow_id
container_id
runtime_node_id
runtime_endpoint
lease_token
```

Recovery then inspects, stops, replays logs, removes, completes, or fails the
container through the owner runtime endpoint.

If the runtime node heartbeat is stale, recovery should avoid Docker operations
until the owner is considered dead. After that grace period, the job should be
marked lost or released for retry according to the retry policy.

## Local Docker Compose

The same architecture works in Docker Compose as a single-runtime-node
deployment.

Compose can run:

```text
docker-proxy
runtime-agent
execution-worker
workflow-worker
jobs-service
```

The compose `runtime-agent` registers a static local runtime:

```text
runtime_node_id = local-docker
node_name = compose-local
docker_endpoint = tcp://docker-proxy:2375
status = READY
```

All container jobs get `runtime_node_id = local-docker`. The workers still use
the same runtime-aware code path as Kubernetes, but there is only one possible
runtime node. If the local runtime is not registered or its heartbeat is stale,
container claims pause with a retryable unavailable error instead of falling
back to an unowned Docker host.

## Single-Node Kubernetes

Single-node Kubernetes is also the one-runtime-node case.

The runtime-node DaemonSet creates one runtime-node pod. The agent registers one
row. Every job is assigned to that row. Docker operations still use
`runtime_node_id + container_id`, but all operations resolve to the same Docker
daemon.

The behavior is equivalent to the current deployment, except the ownership is
now explicit and durable.

## Gossip Is Not Needed Initially

A gossip protocol between `runtime-agent` pods is not necessary for the first
correct multi-node design.

The system already needs a strongly consistent place for job ownership and lease
state: PostgreSQL. Runtime liveness can use the same source of truth:

```text
runtime-agent -> heartbeat -> PostgreSQL
jobs-service -> reads fresh runtime rows during ClaimJob
workers -> resolve runtime_node_id through PostgreSQL-backed services
```

This is simpler and safer than gossip because job assignment, leases, and
runtime availability are evaluated from the same durable control plane.

Gossip would add operational complexity:

- membership convergence delay
- split-brain behavior during network partitions
- another protocol to secure and observe
- disagreement between agent membership and PostgreSQL job ownership
- harder local development and test setup

The recommended initial health model is:

```text
Kubernetes liveness/readiness probes
+ runtime-agent Docker ping
+ runtime-agent heartbeat freshness
+ jobs-service capacity checks
```

Use gossip only if runtime scheduling later needs very low-latency decentralized
placement decisions. Chronoverse does not need that to solve the locality bug.

## Security Requirements

Node-specific Docker endpoints create a cluster-internal Docker data plane. That
must be treated as privileged.

Minimum requirements:

- Only `execution-worker`, `workflow-worker`, and `runtime-agent` should reach
  the Docker proxy port.
- NetworkPolicy should block all other pods from `tcp/2375`.
- The proxy should expose only the Docker API methods Chronoverse needs.
- Prefer mTLS or an authenticated proxy before exposing node-specific Docker
  endpoints across namespaces.
- Never expose Docker proxy endpoints outside the cluster.

## Target Invariant

After this change, the platform should maintain this invariant:

```text
Every container_id used by Chronoverse is always paired with the runtime_node_id
that created it, and every Docker operation resolves that runtime_node_id before
touching Docker.
```

That invariant is what makes the same code path correct in Docker Compose,
single-node Kubernetes, and multi-node Kubernetes.
