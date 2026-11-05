package jobs_test

import (
	"database/sql"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"
)

const (
	leaseContractJobID      = "00000000-0000-4000-8000-000000000001"
	leaseContractWorkflowID = "00000000-0000-4000-8000-000000000002"
	leaseContractUserID     = "00000000-0000-4000-8000-000000000003"
)

func TestExpiredLeaseProtoPreservesRecoveryIdentity(t *testing.T) {
	scheduled := time.Date(2026, 5, 4, 12, 13, 14, 123456789, time.FixedZone("offset", 3600))
	lease := &jobsmodel.ExpiredJobLease{
		ID: leaseContractJobID, WorkflowID: leaseContractWorkflowID, UserID: leaseContractUserID, LeaseToken: "lease", Trigger: "AUTOMATIC",
		ScheduledAt: scheduled, Attempts: 4, LogRetention: true,
		ContainerID: sql.NullString{String: "container", Valid: true}, LeasedBy: sql.NullString{String: "worker", Valid: true},
		RuntimeNodeID: sql.NullString{String: "runtime", Valid: true}, RuntimeEndpoint: sql.NullString{String: "tcp://runtime:2376", Valid: true},
		RuntimeUnavailable: true,
	}
	want := &jobspb.ExpiredJobLease{
		Id: leaseContractJobID, WorkflowId: leaseContractWorkflowID, UserId: leaseContractUserID, LeaseToken: "lease", Trigger: "AUTOMATIC",
		ScheduledAt: "2026-05-04T12:13:14.123456789+01:00", Attempts: 4, LogRetention: true,
		ContainerId: "container", LeasedBy: "worker", RuntimeNodeId: "runtime", RuntimeEndpoint: "tcp://runtime:2376", RuntimeUnavailable: true,
	}
	if got := lease.ToProto(); !proto.Equal(got, want) {
		t.Fatalf("recovery response=%v, want %v", got, want)
	}
	lease.ContainerID = sql.NullString{}
	lease.LeasedBy = sql.NullString{}
	lease.RuntimeNodeID = sql.NullString{}
	lease.RuntimeEndpoint = sql.NullString{}
	lease.RuntimeUnavailable = false
	lease.LogRetention = false
	got := lease.ToProto()
	if got.GetContainerId() != "" || got.GetLeasedBy() != "" || got.GetRuntimeNodeId() != "" || got.GetRuntimeEndpoint() != "" || got.GetRuntimeUnavailable() || got.GetLogRetention() {
		t.Fatalf("unassigned lease invented runtime/container state: %v", got)
	}
	if got.GetId() != leaseContractJobID || got.GetLeaseToken() != "lease" || got.GetAttempts() != 4 {
		t.Fatalf("recovery identity changed: %v", got)
	}
}
