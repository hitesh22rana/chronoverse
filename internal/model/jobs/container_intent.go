package jobs

// ContainerIntentCommand carries an internal, lease- or discovery-fenced operation.
type ContainerIntentCommand struct {
	WorkflowGeneration                                                     int64
	Stage                                                                  string
	JobID, LeaseToken, IntentID, RuntimeInstance, RequestHash, ContainerID string
	Outcome, ErrorCode, ErrorMessage, Token, Reason, Actor                 string
	Attempt, BatchSize                                                     int32
	Generation                                                             int64
}

// ContainerIntent survives deletion of its source job and owner.
type ContainerIntent struct {
	ImageReference                                             string
	WorkflowGeneration                                         int64
	Stage, InspectionErrorCode, InspectionErrorMessage         string
	Quarantined, HeldCapacity                                  bool
	InspectionErrors                                           int32
	ID, JobID, WorkflowID, UserID, Name                        string
	RuntimeNodeID, RuntimeEndpoint, RuntimeInstance            string
	RequestHash, ContainerID, Outcome, ErrorCode, ErrorMessage string
	Token, NextInspectionAt, LastInspectionAt                  string
	Attempt                                                    int32
	Generation                                                 int64
	CreateReserved, StartReserved, Closed, LogRetention        bool
}

// ContainerIntentResult never replays a previously returned mutation grant.
type ContainerIntentResult struct {
	Intent  *ContainerIntent
	Granted bool
}

// CleanupIdentity names one verified physical container and its owning intent.
type CleanupIdentity struct {
	JobID           string
	ContainerID     string
	RuntimeInstance string
	IntentID        string
}
