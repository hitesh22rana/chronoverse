package runtime

import "time"

const (
	// NodeStatusReady means a runtime node can accept new work.
	NodeStatusReady = "READY"
	// NodeStatusDraining means a runtime node should not receive new work.
	NodeStatusDraining = "DRAINING"
	// NodeStatusUnhealthy means a runtime node is not available.
	NodeStatusUnhealthy = "UNHEALTHY"
)

// Node represents a Docker-capable runtime node.
type Node struct {
	ID              string    `db:"id"`
	NodeName        string    `db:"node_name"`
	DockerEndpoint  string    `db:"docker_endpoint"`
	Status          string    `db:"status"`
	LastHeartbeatAt time.Time `db:"last_heartbeat_at"`
	MaxConcurrency  int32     `db:"max_concurrency"`
	RunningJobs     int32     `db:"running_jobs"`
}
