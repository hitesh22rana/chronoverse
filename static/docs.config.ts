export type DocPage = {
  slug: string;
  title: string;
  description: string;
  source: string;
  sourceRefs: string[];
};

export type DocGroup = {
  title: string;
  pages: DocPage[];
};

export const docsConfig: DocGroup[] = [
  {
    title: "Getting Started",
    pages: [
      { slug: "introduction", title: "Introduction", description: "What Chronoverse is and where it fits.", source: "introduction", sourceRefs: ["README.md"] },
      { slug: "quickstart", title: "Quickstart", description: "Run the development stack with Docker Compose.", source: "quickstart", sourceRefs: ["compose.dev.yaml"] },
      { slug: "installation", title: "Installation", description: "Prerequisites and development setup.", source: "installation", sourceRefs: ["README.md", "Makefile"] },
      { slug: "getting-started/first-workflow", title: "Your first workflow", description: "Create and run a container workflow.", source: "getting-started/first-workflow", sourceRefs: ["internal/server/workflows_handlers.go", "internal/server/jobs_handlers.go"] },
    ],
  },
  {
    title: "Core Concepts",
    pages: [
      { slug: "concepts/workflows", title: "Workflows", description: "Definitions, generations, builds, and lifecycle rules.", source: "concepts/workflows", sourceRefs: ["proto/workflows/workflows.proto"] },
      { slug: "concepts/jobs", title: "Jobs", description: "Scheduled execution attempts and state transitions.", source: "concepts/jobs", sourceRefs: ["proto/jobs/jobs.proto"] },
      { slug: "concepts/scheduling", title: "Scheduling", description: "Automatic polling and manual dispatch.", source: "concepts/scheduling", sourceRefs: ["internal/repository/scheduler/scheduler.go"] },
      { slug: "concepts/workers", title: "Workers", description: "Background consumers and their ownership boundaries.", source: "concepts/workers", sourceRefs: ["cmd/scheduling-worker/main.go", "cmd/execution-worker/main.go"] },
      { slug: "concepts/lifecycle-states", title: "Lifecycle states", description: "Workflow build and job execution state machines.", source: "concepts/lifecycle-states", sourceRefs: ["internal/model/jobs/jobs.go", "internal/model/workflows/workflows.go"] },
      { slug: "concepts/log-retention", title: "Log retention", description: "Live output, retained logs, and disabled-retention behavior.", source: "concepts/log-retention", sourceRefs: ["internal/repository/joblogs/joblogs.go"] },
    ],
  },
  {
    title: "Engineering",
    pages: [
      { slug: "engineering/architecture", title: "Architecture", description: "The complete runtime topology and communication model.", source: "engineering/architecture", sourceRefs: ["docs/architecture.md", "compose.prod.yaml"] },
      { slug: "engineering/service-boundaries", title: "Service boundaries", description: "HTTP gateway and gRPC domain ownership.", source: "engineering/service-boundaries", sourceRefs: ["internal/server/server.go", "proto"] },
      { slug: "engineering/event-flows", title: "Event flows", description: "Workflow creation through container completion.", source: "engineering/event-flows", sourceRefs: ["docs/architecture.md"] },
      { slug: "engineering/replay-safety", title: "Replay safety", description: "How duplicate delivery and partial failure are handled.", source: "engineering/replay-safety", sourceRefs: ["internal/pkg/idempotency/idempotency.go", "internal/pkg/kafka/commit_policy.go"] },
      { slug: "engineering/transactional-outbox", title: "Transactional outbox", description: "Atomic state changes and durable event publication.", source: "engineering/transactional-outbox", sourceRefs: ["internal/pkg/outbox/outbox.go", "internal/repository/outboxrelay/outboxrelay.go"] },
      { slug: "engineering/job-leases", title: "Durable job leases", description: "Execution ownership, renewal, and recovery.", source: "engineering/job-leases", sourceRefs: ["internal/repository/jobs/lease.go", "internal/repository/executor/recovery.go"] },
      { slug: "engineering/kafka-processing", title: "Kafka processing", description: "Partition ordering, retries, and commit policy.", source: "engineering/kafka-processing", sourceRefs: ["internal/pkg/kafka/partition_runner.go"] },
      { slug: "engineering/image-pull-coordination", title: "Image pull coordination", description: "Redis locks that prevent shared-host pull storms.", source: "engineering/image-pull-coordination", sourceRefs: ["internal/repository/workflow/image_pull_lock.go"] },
      { slug: "engineering/logging-search-pipeline", title: "Logging and search pipeline", description: "Live logs, Kafka, ClickHouse, and Meilisearch.", source: "engineering/logging-search-pipeline", sourceRefs: ["internal/repository/joblogs/process_logs_batch.go", "internal/pkg/joblogevents/live_publisher.go"] },
      { slug: "engineering/trace-propagation", title: "Trace propagation", description: "Distributed context across HTTP, gRPC, and Kafka.", source: "engineering/trace-propagation", sourceRefs: ["internal/pkg/otel/otel.go", "internal/pkg/kafka/kafka.go"] },
    ],
  },
  {
    title: "Features",
    pages: [
      { slug: "features/workflow-types", title: "Workflow types", description: "Heartbeat and container execution models.", source: "features/workflow-types", sourceRefs: ["internal/model/workflows/workflows.go"] },
      { slug: "features/job-scheduling", title: "Job scheduling", description: "Interval scheduling and immediate manual runs.", source: "features/job-scheduling", sourceRefs: ["internal/repository/scheduler/scheduler.go"] },
      { slug: "features/log-streaming", title: "Log streaming and search", description: "SSE, retained logs, highlights, filters, and downloads.", source: "features/log-streaming", sourceRefs: ["internal/server/jobs_handlers.go"] },
      { slug: "features/notifications", title: "Notifications", description: "Workflow and job status notifications.", source: "features/notifications", sourceRefs: ["proto/notifications/notifications.proto"] },
      { slug: "features/analytics", title: "Analytics", description: "User and workflow execution aggregates.", source: "features/analytics", sourceRefs: ["proto/analytics/analytics.proto"] },
    ],
  },
  {
    title: "HTTP API",
    pages: [
      { slug: "api/overview", title: "API overview", description: "Base paths, session requirements, and response behavior.", source: "api/overview", sourceRefs: ["docs/api.md", "internal/server/server.go"] },
      { slug: "api/authentication", title: "Authentication", description: "Registration, login, session cookies, and logout.", source: "api/authentication", sourceRefs: ["internal/server/users_handlers.go", "internal/server/csrf.go"] },
      { slug: "api/request-safety", title: "CSRF and idempotency", description: "Mutation safety for browser and retrying clients.", source: "api/request-safety", sourceRefs: ["internal/server/middlewares.go", "internal/pkg/idempotency/idempotency.go"] },
      { slug: "api/pagination-errors", title: "Pagination and errors", description: "Cursor pagination and HTTP status mapping.", source: "api/pagination-errors", sourceRefs: ["internal/server/helpers.go"] },
      { slug: "api/server-sent-events", title: "Server-Sent Events", description: "Live job log stream behavior and error frames.", source: "api/server-sent-events", sourceRefs: ["internal/server/jobs_handlers.go"] },
      { slug: "api/reference", title: "Endpoint reference", description: "OpenAPI-generated public HTTP endpoint reference.", source: "api/reference", sourceRefs: ["internal/server/server.go"] },
    ],
  },
  {
    title: "Internal Contracts",
    pages: [
      { slug: "internal/grpc-services", title: "gRPC services", description: "Internal service RPC inventory and authorization.", source: "internal/grpc-services", sourceRefs: ["proto"] },
      { slug: "internal/kafka-events", title: "Kafka events", description: "Topics, keys, envelopes, and delivery expectations.", source: "internal/kafka-events", sourceRefs: ["internal/model/jobs/events.go", "internal/model/workflows/events.go"] },
      { slug: "internal/data-stores", title: "Data stores", description: "Persistence ownership across five infrastructure systems.", source: "internal/data-stores", sourceRefs: ["internal/pkg/postgres/tables.go", "internal/pkg/clickhouse/tables.go"] },
    ],
  },
  {
    title: "Deployment",
    pages: [
      { slug: "deployment/overview", title: "Deployment overview", description: "Development and production topology.", source: "deployment/overview", sourceRefs: ["compose.dev.yaml", "compose.prod.yaml"] },
      { slug: "deployment/development", title: "Development environment", description: "Local stack, ports, and debugging workflow.", source: "deployment/development", sourceRefs: ["compose.dev.yaml"] },
      { slug: "deployment/production", title: "Production deployment", description: "Images, Nginx, replicas, and hardening.", source: "deployment/production", sourceRefs: ["compose.prod.yaml"] },
      { slug: "deployment/configuration", title: "Configuration reference", description: "Environment variables by service and worker.", source: "deployment/configuration", sourceRefs: ["docs/configuration.md", "internal/config"] },
      { slug: "deployment/security", title: "Certificates and security", description: "TLS, mTLS, JWTs, cookies, and secrets.", source: "deployment/security", sourceRefs: ["certs", "internal/pkg/auth/auth.go"] },
    ],
  },
  {
    title: "Operations",
    pages: [
      { slug: "operations/monitoring", title: "Monitoring", description: "Health checks and runtime signals.", source: "operations/monitoring", sourceRefs: ["docs/operations.md", "compose.prod.yaml"] },
      { slug: "operations/observability", title: "Observability", description: "OpenTelemetry signals and LGTM dashboards.", source: "operations/observability", sourceRefs: ["internal/pkg/otel/otel.go"] },
      { slug: "operations/scaling", title: "Scaling", description: "Partitions, replicas, leases, and capacity boundaries.", source: "operations/scaling", sourceRefs: ["docs/operations.md"] },
      { slug: "operations/recovery", title: "Retries and recovery", description: "Outbox retries, lease recovery, and stale event handling.", source: "operations/recovery", sourceRefs: ["internal/repository/executor/recovery.go", "internal/repository/outboxrelay/outboxrelay.go"] },
      { slug: "operations/troubleshooting", title: "Troubleshooting", description: "Diagnose TLS, readiness, logs, SSE, and API issues.", source: "operations/troubleshooting", sourceRefs: ["docs/operations.md"] },
    ],
  },
  {
    title: "Contributing",
    pages: [
      { slug: "contributing/repository-layout", title: "Repository layout", description: "Where services, workers, contracts, and frontends live.", source: "contributing/repository-layout", sourceRefs: ["README.md"] },
      { slug: "contributing/development", title: "Development workflow", description: "Generate, lint, test, build, and release changes.", source: "contributing/development", sourceRefs: ["Makefile", ".github/workflows/ci.yaml"] },
    ],
  },
];

export const docPages = docsConfig.flatMap((group) => group.pages);

export function getDocPage(slug: string) {
  return docPages.find((page) => page.slug === slug);
}

export function getDocHref(slug: string) {
  return `/docs/${slug}`;
}
