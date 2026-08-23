package postgres

const (
	// TableUsers is the name of the users table.
	TableUsers = "users"
	// TableWorkflows is the name of the workflows table.
	TableWorkflows = "workflows"
	// TableJobs is the name of the jobs table.
	TableJobs = "jobs"
	// TableNotifications is the name of the notifications table.
	TableNotifications = "notifications"
	// TableAnalytics is the name of the analytics table.
	TableAnalytics = "analytics"
	// TableCommandIdempotencyKeys is the name of the shared command idempotency ledger.
	TableCommandIdempotencyKeys = "command_idempotency_keys"
	// TableCommandIdempotencyLegacyIdentities stores rollback-compatible workflow command identities.
	TableCommandIdempotencyLegacyIdentities = "command_idempotency_legacy_identities"
	// TableOutboxEvents is the name of the outbox events table.
	TableOutboxEvents = "outbox_events"
	// TableProcessedEvents is the name of the processed events table.
	TableProcessedEvents = "processed_events"
	// TableWorkflowTerminalEffects is the name of the workflow terminal effects table.
	TableWorkflowTerminalEffects = "workflow_terminal_effects"
	// TableRuntimeNodes is the name of the runtime nodes table.
	TableRuntimeNodes = "runtime_nodes"
)
