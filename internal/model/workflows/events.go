package workflows

// Action represents the action of the workflow.
type Action string

// Actions for the workflow.
const (
	ActionContainerCleanup Action = "CONTAINER_CLEANUP"
	ActionBuild            Action = "BUILD"
	ActionReschedule       Action = "RESCHEDULE"
	ActionTerminate        Action = "TERMINATE"
	ActionDelete           Action = "DELETE"
	ActionJobCompleted     Action = "JOB_COMPLETED"
	ActionJobFailed        Action = "JOB_FAILED"
)

// ToString converts the Action to its string representation.
func (a Action) ToString() string {
	return string(a)
}

// WorkflowEvent represents the event of the workflow.
type WorkflowEvent struct {
	EventKey          string
	ID                string
	UserID            string
	Action            Action
	Generation        int64
	CleanupGeneration int64 `json:",omitempty"`
	JobID             string
	FailureKind       string
	ErrorCode         string
	ErrorMessage      string
	ContainerCleanup  *ContainerCleanup `json:",omitempty"`
}

// ContainerCleanup retains runtime routing after a job or workflow is deleted.
type ContainerCleanup struct {
	ContainerID     string
	RuntimeNodeID   string
	RuntimeEndpoint string
	Attempts        int32
	LogRetention    bool
}
