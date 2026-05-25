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
	// TableWorkflowIdempotencyKeys is the name of the workflow idempotency keys table.
	TableWorkflowIdempotencyKeys = "workflow_idempotency_keys"
	// TableOutboxEvents is the name of the outbox events table.
	TableOutboxEvents = "outbox_events"
	// TableProcessedEvents is the name of the processed events table.
	TableProcessedEvents = "processed_events"
	// TableWorkflowFailureEvents is the name of the workflow failure events table.
	TableWorkflowFailureEvents = "workflow_failure_events"
	// TableLogAnalyticsOffsets is the name of the log analytics offsets table.
	TableLogAnalyticsOffsets = "log_analytics_offsets"
)
