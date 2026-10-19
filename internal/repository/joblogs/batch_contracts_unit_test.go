//nolint:testpackage // Exercises unexported batch and repository helpers.
package joblogs

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/meilisearch/meilisearch-go"
	"github.com/twmb/franz-go/pkg/kgo"
	"go.opentelemetry.io/otel"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	"github.com/hitesh22rana/chronoverse/internal/pkg/joblogevents"
	meilisearchpkg "github.com/hitesh22rana/chronoverse/internal/pkg/meilisearch"
)

func TestFilterActiveWorkflowLogsWithoutValidPairs(t *testing.T) {
	repo := &Repository{tp: otel.Tracer("joblogs-test")}
	active, skipped, err := repo.filterActiveWorkflowLogs(t.Context(), []*queueData{
		nil,
		{},
		{logEntry: &jobsmodel.JobLogEvent{WorkflowID: "not-a-workflow", UserID: "not-a-user"}},
	})
	if err != nil || active != nil || skipped != nil {
		t.Fatalf("invalid-only batch = %v, %v, %v; want nil, nil, nil", active, skipped, err)
	}
}

func TestRunLiveJobLogPublisherWithoutPublisher(t *testing.T) {
	if done := (&Repository{}).runLiveJobLogPublisher(t.Context(), nil); done != nil {
		t.Fatal("publisher without a live client returned a shutdown channel")
	}
}

func TestBatchSizeAndIntervalDefaults(t *testing.T) {
	repo := &Repository{cfg: &Config{}}
	if got := repo.batchSize(); got != 1 {
		t.Fatalf("default batch size = %d, want 1", got)
	}
	if got := repo.batchInterval(); got != 2*time.Second {
		t.Fatalf("default batch interval = %s, want 2s", got)
	}

	repo.cfg = &Config{BatchJobLogsSizeLimit: 7, BatchJobLogsTimeInterval: time.Second}
	if got := repo.batchSize(); got != 7 {
		t.Fatalf("configured batch size = %d, want 7", got)
	}
	if got := repo.batchInterval(); got != time.Second {
		t.Fatalf("configured batch interval = %s, want 1s", got)
	}
}

func TestPublishLogGuardBranches(t *testing.T) {
	ctx := t.Context()
	repo := &Repository{}
	repo.publishLog(ctx, nil)
	repo.publishLog(ctx, &jobsmodel.JobLogEvent{Retention: true})

	repo.livePublisher = &joblogevents.LivePublisher{}
	repo.publishLog(ctx, &jobsmodel.JobLogEvent{EventKey: "log-1", Retention: false})
	repo.publishLog(ctx, &jobsmodel.JobLogEvent{EventKey: "log-2", Retention: true, LivePublished: true})
	repo.publishLog(ctx, &jobsmodel.JobLogEvent{EventKey: "log-3", Retention: true})
}

func TestProcessRecordsBatchSkipsUnparseableRecord(t *testing.T) {
	repo := &Repository{tp: otel.Tracer("joblogs-test")}
	if err := repo.processRecordsBatch(t.Context(), []*kgo.Record{{Value: []byte("{")}}); err != nil {
		t.Fatalf("unparseable record = %v, want nil", err)
	}
}

func TestDeleteLogsFromClickHouseByEventIDWithoutEvents(t *testing.T) {
	if err := (&Repository{}).deleteLogsFromClickHouseByEventID(t.Context(), nil); err != nil {
		t.Fatalf("empty event IDs = %v, want nil", err)
	}
}

func TestJobLogsStatusError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		code codes.Code
		want string
	}{
		{name: "deadline", err: context.DeadlineExceeded, code: codes.DeadlineExceeded, want: context.DeadlineExceeded.Error()},
		{name: "canceled", err: context.Canceled, code: codes.Canceled, want: context.Canceled.Error()},
		{name: "internal", err: errors.New("boom"), code: codes.Internal, want: "failed: boom"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := jobLogsStatusError("failed", tt.err)
			if status.Code(err) != tt.code || !strings.Contains(status.Convert(err).Message(), tt.want) {
				t.Fatalf("status error = %v, want %v containing %q", err, tt.code, tt.want)
			}
		})
	}
}

// batchMeiliFake scripts the SDK calls made by the batch insert and cleanup helpers.
type batchMeiliFake struct {
	meilisearch.ServiceManager
	index meilisearch.IndexManager
	wait  func(context.Context, int64) (*meilisearch.Task, error)
}

func (client *batchMeiliFake) Index(string) meilisearch.IndexManager { return client.index }

func (client *batchMeiliFake) WaitForTaskWithContext(ctx context.Context, taskUID int64, _ time.Duration) (*meilisearch.Task, error) {
	return client.wait(ctx, taskUID)
}

type batchMeiliIndexFake struct {
	meilisearch.IndexManager
	add func(context.Context, any) (*meilisearch.TaskInfo, error)
	del func(context.Context, []string) (*meilisearch.TaskInfo, error)
}

func (index *batchMeiliIndexFake) AddDocumentsWithContext(
	ctx context.Context,
	documents any,
	_ *meilisearch.DocumentOptions,
) (*meilisearch.TaskInfo, error) {
	return index.add(ctx, documents)
}

func (index *batchMeiliIndexFake) DeleteDocumentsWithContext(
	ctx context.Context,
	identifiers []string,
	_ *meilisearch.DocumentOptions,
) (*meilisearch.TaskInfo, error) {
	return index.del(ctx, identifiers)
}

func withoutJobLogsIndex(t *testing.T) {
	t.Helper()
	previous := meilisearchpkg.Indexes[meilisearchpkg.IndexJobLogs]
	meilisearchpkg.Indexes[meilisearchpkg.IndexJobLogs] = nil
	t.Cleanup(func() {
		meilisearchpkg.Indexes[meilisearchpkg.IndexJobLogs] = previous
	})
}

func TestInsertLogsDocumentsToMeiliSearchGuards(t *testing.T) {
	documents := []*map[string]any{{"id": "log_1"}}
	tests := []struct {
		name  string
		ms    meilisearch.ServiceManager
		index func() meilisearch.IndexManager
		want  string
	}{
		{
			name: "missing client",
			want: "client is not configured",
		},
		{
			name:  "missing index",
			ms:    &batchMeiliFake{},
			index: func() meilisearch.IndexManager { return nil },
			want:  "job logs index is not configured",
		},
		{
			name: "empty task response",
			ms: &batchMeiliFake{index: &batchMeiliIndexFake{
				add: func(context.Context, any) (*meilisearch.TaskInfo, error) {
					//nolint:nilnil // Scripted SDK response used to exercise the empty task guard.
					return nil, nil
				},
			}},
			want: "empty task response",
		},
		{
			name: "wait failure",
			ms: &batchMeiliFake{
				index: &batchMeiliIndexFake{
					add: func(context.Context, any) (*meilisearch.TaskInfo, error) {
						return &meilisearch.TaskInfo{TaskUID: 1}, nil
					},
				},
				wait: func(context.Context, int64) (*meilisearch.Task, error) {
					return nil, errors.New("wait failed")
				},
			},
			want: "failed waiting for Meilisearch add-documents task 1",
		},
		{
			name: "failed task",
			ms: &batchMeiliFake{
				index: &batchMeiliIndexFake{
					add: func(context.Context, any) (*meilisearch.TaskInfo, error) {
						return &meilisearch.TaskInfo{TaskUID: 1}, nil
					},
				},
				wait: func(context.Context, int64) (*meilisearch.Task, error) {
					return &meilisearch.Task{Status: meilisearch.TaskStatusFailed}, nil
				},
			},
			want: "finished with status failed",
		},
		{
			name: "success",
			ms: &batchMeiliFake{
				index: &batchMeiliIndexFake{
					add: func(context.Context, any) (*meilisearch.TaskInfo, error) {
						return &meilisearch.TaskInfo{TaskUID: 1}, nil
					},
				},
				wait: func(context.Context, int64) (*meilisearch.Task, error) {
					return &meilisearch.Task{Status: meilisearch.TaskStatusSucceeded}, nil
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.index != nil {
				withoutJobLogsIndex(t)
			}
			repo := &Repository{ms: tt.ms}
			err := repo.insertLogsDocumentsToMeiliSearch(t.Context(), documents)
			if tt.want == "" {
				if err != nil {
					t.Fatalf("insert = %v, want nil", err)
				}
				return
			}
			if status.Code(err) != codes.Internal || !strings.Contains(status.Convert(err).Message(), tt.want) {
				t.Fatalf("insert = %v, want Internal containing %q", err, tt.want)
			}
		})
	}
}

func TestDeleteLogsFromMeiliSearchByIDGuards(t *testing.T) {
	eventIDs := []string{"log:1"}
	successTask := func() (*meilisearch.TaskInfo, error) {
		return &meilisearch.TaskInfo{TaskUID: 1}, nil
	}
	tests := []struct {
		name     string
		eventIDs []string
		ms       meilisearch.ServiceManager
		index    func() meilisearch.IndexManager
		want     string
	}{
		{name: "empty event IDs", eventIDs: nil},
		{name: "missing client", eventIDs: eventIDs, want: "client is not configured"},
		{
			name:     "missing index",
			eventIDs: eventIDs,
			ms:       &batchMeiliFake{},
			index:    func() meilisearch.IndexManager { return nil },
			want:     "job logs index is not configured",
		},
		{
			name:     "delete failure",
			eventIDs: eventIDs,
			ms: &batchMeiliFake{index: &batchMeiliIndexFake{
				del: func(context.Context, []string) (*meilisearch.TaskInfo, error) {
					return nil, errors.New("delete failed")
				},
			}},
			want: "failed to delete skipped Meilisearch logs",
		},
		{
			name:     "empty task response",
			eventIDs: eventIDs,
			ms: &batchMeiliFake{index: &batchMeiliIndexFake{
				del: func(context.Context, []string) (*meilisearch.TaskInfo, error) {
					//nolint:nilnil // Scripted SDK response used to exercise the empty task guard.
					return nil, nil
				},
			}},
			want: "empty task response",
		},
		{
			name:     "wait failure",
			eventIDs: eventIDs,
			ms: &batchMeiliFake{
				index: &batchMeiliIndexFake{del: func(context.Context, []string) (*meilisearch.TaskInfo, error) { return successTask() }},
				wait: func(context.Context, int64) (*meilisearch.Task, error) {
					return nil, errors.New("wait failed")
				},
			},
			want: "failed waiting for Meilisearch delete-documents task 1",
		},
		{
			name:     "failed task",
			eventIDs: eventIDs,
			ms: &batchMeiliFake{
				index: &batchMeiliIndexFake{del: func(context.Context, []string) (*meilisearch.TaskInfo, error) { return successTask() }},
				wait: func(context.Context, int64) (*meilisearch.Task, error) {
					return &meilisearch.Task{Status: meilisearch.TaskStatusFailed}, nil
				},
			},
			want: "finished with status failed",
		},
		{
			name:     "success",
			eventIDs: eventIDs,
			ms: &batchMeiliFake{
				index: &batchMeiliIndexFake{del: func(context.Context, []string) (*meilisearch.TaskInfo, error) { return successTask() }},
				wait: func(context.Context, int64) (*meilisearch.Task, error) {
					return &meilisearch.Task{Status: meilisearch.TaskStatusSucceeded}, nil
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.index != nil {
				withoutJobLogsIndex(t)
			}
			repo := &Repository{ms: tt.ms}
			err := repo.deleteLogsFromMeiliSearchByID(t.Context(), tt.eventIDs)
			if tt.want == "" {
				if err != nil {
					t.Fatalf("delete = %v, want nil", err)
				}
				return
			}
			if status.Code(err) != codes.Internal || !strings.Contains(status.Convert(err).Message(), tt.want) {
				t.Fatalf("delete = %v, want Internal containing %q", err, tt.want)
			}
		})
	}
}
