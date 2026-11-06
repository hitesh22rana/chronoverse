package jobs_test

import (
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/hitesh22rana/chronoverse/internal/model/jobs"
	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"
)

func TestOptionalCleanupResponsesHaveEmptyProtocolValues(t *testing.T) {
	t.Parallel()
	var snapshot *jobs.CancelJobSnapshot
	require.True(t, proto.Equal(&jobspb.CancelJobResponse{}, snapshot.ToProto()))
	var node *jobs.RuntimeNode
	require.True(t, proto.Equal(&jobspb.GetReadyRuntimeNodeResponse{}, node.ToProto()))
	snapshot = &jobs.CancelJobSnapshot{
		ID:             "job",
		PreviousStatus: "RUNNING",
		ContainerID: sql.NullString{
			String: "container",
			Valid:  true,
		},
		RuntimeNodeID: sql.NullString{
			String: "node",
			Valid:  true,
		},
		RuntimeEndpoint: sql.NullString{
			String: "https://runtime:2376",
			Valid:  true,
		},
		Attempt: 3,
	}
	require.True(t,
		proto.Equal(&jobspb.CancelJobResponse{
			Id:              "job",
			PreviousStatus:  "RUNNING",
			ContainerId:     "container",
			RuntimeNodeId:   "node",
			RuntimeEndpoint: "https://runtime:2376",
			Attempt:         3,
		},
			snapshot.ToProto()))
	require.True(t,
		proto.Equal(&jobspb.GetReadyRuntimeNodeResponse{
			RuntimeNodeId:   "node",
			RuntimeEndpoint: "https://runtime:2376",
		},
			(&jobs.RuntimeNode{
				RuntimeNodeID:   "node",
				RuntimeEndpoint: "https://runtime:2376",
			}).ToProto()))
}

func TestListProtocolSeparatesInternalPlacementFromPublicStatus(t *testing.T) {
	t.Parallel()
	timestamp := time.Date(2026, time.November, 6, 12, 30, 0, 123, time.UTC)
	response := &jobs.ListJobsResponse{
		Cursor: "next-page",
		Jobs: []*jobs.JobByWorkflowIDResponse{{
			ID:          "job",
			WorkflowID:  "workflow",
			JobStatus:   "COMPLETED",
			JobTrigger:  "MANUAL",
			Attempts:    2,
			ScheduledAt: timestamp,
			StartedAt: sql.NullTime{
				Time:  timestamp,
				Valid: true,
			},
			CompletedAt: sql.NullTime{
				Time:  timestamp,
				Valid: true,
			},
			CreatedAt: timestamp,
			UpdatedAt: timestamp,
			ContainerID: sql.NullString{
				String: "container",
				Valid:  true,
			},
			RuntimeNodeID: sql.NullString{
				String: "node",
				Valid:  true,
			},
			RuntimeEndpoint: sql.NullString{
				String: "https://runtime:2376",
				Valid:  true,
			},
		}},
	}
	want := &jobspb.ListJobsResponse{
		Cursor: "next-page",
		Jobs: []*jobspb.JobsResponse{{
			Id:          "job",
			WorkflowId:  "workflow",
			Status:      "COMPLETED",
			Trigger:     "MANUAL",
			Attempts:    2,
			ScheduledAt: "2026-11-06T12:30:00.000000123Z",
			StartedAt:   "2026-11-06T12:30:00.000000123Z",
			CompletedAt: "2026-11-06T12:30:00.000000123Z",
			CreatedAt:   "2026-11-06T12:30:00.000000123Z",
			UpdatedAt:   "2026-11-06T12:30:00.000000123Z",
		}},
	}
	require.True(t, proto.Equal(want, response.ToProto(false)))
	want.Jobs[0].ContainerId = "container"
	want.Jobs[0].RuntimeNodeId = "node"
	want.Jobs[0].RuntimeEndpoint = "https://runtime:2376"
	require.True(t, proto.Equal(want, response.ToProto(true)))
	response.Jobs[0].JobStatus = "PENDING"
	response.Jobs[0].StartedAt = sql.NullTime{}
	response.Jobs[0].CompletedAt = sql.NullTime{}
	public := response.ToProto(false)
	require.Empty(t, public.Jobs[0].StartedAt)
	require.Empty(t, public.Jobs[0].CompletedAt)
	require.Empty(t, public.Jobs[0].ContainerId)
	require.Empty(t, public.Jobs[0].RuntimeNodeId)
	require.Empty(t, public.Jobs[0].RuntimeEndpoint)
}
