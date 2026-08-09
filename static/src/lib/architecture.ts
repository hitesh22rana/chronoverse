export const architectureMapContent = {
  stages: [
    { label: "HTTP gateway", items: ["Sessions", "CSRF", "REST", "SSE"] },
    { label: "gRPC domains", items: ["users", "workflows", "jobs", "notifications", "analytics"] },
    { label: "Kafka workers", items: ["scheduler", "workflow", "execution", "job logs", "analytics", "outbox"] },
    { label: "Runtime data plane", items: ["runtime-agent", "Docker proxy", "runtime_nodes"] },
    { label: "infrastructure", items: ["PostgreSQL", "ClickHouse", "Redis", "Kafka", "Meilisearch", "LGTM"] },
  ],
  caption: "Synchronous domain ownership with asynchronous, replay-safe orchestration.",
} as const;
