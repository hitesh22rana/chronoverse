package workflows

// Action represents the action of the workflow.
type Action string

// Actions for the workflow.
const (
	ActionBuild     Action = "BUILD"
	ActionTerminate Action = "TERMINATE"
	ActionDelete    Action = "DELETE"
)

// ToString converts the Action to its string representation.
func (a Action) ToString() string {
	return string(a)
}

// WorkflowEvent represents the event of the workflow.
type WorkflowEvent struct {
	ID     string
	UserID string
	Action Action
}
