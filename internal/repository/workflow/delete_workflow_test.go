//nolint:testpackage // Exercises private delete helpers with scripted SDK fakes.
package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/meilisearch/meilisearch-go"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	meilisearchpkg "github.com/hitesh22rana/chronoverse/internal/pkg/meilisearch"
)

// deleteWorkflowMeiliEnqueueCall records one DeleteDocumentsByFilterWithContext
// call so a test can pin the index and the exact tenant-scoped filter the
// helper sent to the SDK.
type deleteWorkflowMeiliEnqueueCall struct {
	index  string
	filter any
}

// deleteWorkflowMeiliFake is a scripted meilisearch.ServiceManager for the
// delete-log helpers. The embedded interfaces stay nil so any SDK method the
// helpers are not expected to call panics loudly instead of silently passing.
type deleteWorkflowMeiliFake struct {
	meilisearch.ServiceManager

	enqueue func(ctx context.Context, filter any) (*meilisearch.TaskInfo, error)
	wait    func(ctx context.Context, taskUID int64) (*meilisearch.Task, error)

	enqueueCalls []deleteWorkflowMeiliEnqueueCall
	waitCalls    []int64
}

func (f *deleteWorkflowMeiliFake) Index(uid string) meilisearch.IndexManager {
	return &deleteWorkflowMeiliIndexFake{parent: f, uid: uid}
}

func (f *deleteWorkflowMeiliFake) WaitForTaskWithContext(ctx context.Context, taskUID int64, _ time.Duration) (*meilisearch.Task, error) {
	f.waitCalls = append(f.waitCalls, taskUID)
	return f.wait(ctx, taskUID)
}

// deleteWorkflowMeiliIndexFake scripted IndexManager for one index name.
type deleteWorkflowMeiliIndexFake struct {
	meilisearch.IndexManager

	parent *deleteWorkflowMeiliFake
	uid    string
}

func (f *deleteWorkflowMeiliIndexFake) DeleteDocumentsByFilterWithContext(
	ctx context.Context,
	filter any,
	_ *meilisearch.DocumentOptions,
) (*meilisearch.TaskInfo, error) {
	f.parent.enqueueCalls = append(f.parent.enqueueCalls, deleteWorkflowMeiliEnqueueCall{index: f.uid, filter: filter})
	return f.parent.enqueue(ctx, filter)
}

func TestDeleteWorkflowLogsFromMeiliSearchSDKContracts(t *testing.T) {
	t.Parallel()

	const (
		workflowID = "workflow-1"
		userID     = "user-1"
		taskUID    = int64(42)
	)
	wantFilter := `workflow_id = "workflow-1" AND user_id = "user-1"`

	// The SDK task type carries an unexported error struct, so a failed task
	// fixture is built through its JSON shape.
	failedTask := meilisearch.Task{}
	if err := json.Unmarshal(
		[]byte(`{"status":"failed","error":{"message":"could not delete documents","code":"internal","type":"internal"}}`),
		&failedTask,
	); err != nil {
		t.Fatalf("unmarshal failed task fixture: %v", err)
	}

	tests := []struct {
		name        string
		enqueue     func(ctx context.Context, filter any) (*meilisearch.TaskInfo, error)
		wait        func(ctx context.Context, taskUID int64) (*meilisearch.Task, error)
		wantCode    codes.Code
		wantMessage []string
		wantEnqueue int
		wantWait    int
	}{
		{
			name: "successful enqueue then wait",
			enqueue: func(context.Context, any) (*meilisearch.TaskInfo, error) {
				return &meilisearch.TaskInfo{TaskUID: taskUID}, nil
			},
			wait: func(context.Context, int64) (*meilisearch.Task, error) {
				return &meilisearch.Task{Status: meilisearch.TaskStatusSucceeded}, nil
			},
			wantCode:    codes.OK,
			wantEnqueue: 1,
			wantWait:    1,
		},
		{
			name: "enqueue error is wrapped and retried",
			enqueue: func(context.Context, any) (*meilisearch.TaskInfo, error) {
				return nil, errors.New("meilisearch connection refused")
			},
			wantCode:    codes.Internal,
			wantMessage: []string{"failed to enqueue Meilisearch logs deletion for workflow workflow-1", "meilisearch connection refused"},
			wantEnqueue: 2,
		},
		{
			name: "nil task response is rejected and retried",
			enqueue: func(context.Context, any) (*meilisearch.TaskInfo, error) {
				//nolint:nilnil // Scripted SDK response used to exercise the empty task guard.
				return nil, nil
			},
			wantCode:    codes.Internal,
			wantMessage: []string{"failed to enqueue Meilisearch logs deletion for workflow workflow-1", "empty task response"},
			wantEnqueue: 2,
		},
		{
			name: "enqueue deadline is mapped without wrapping",
			enqueue: func(context.Context, any) (*meilisearch.TaskInfo, error) {
				return nil, context.DeadlineExceeded
			},
			wantCode:    codes.DeadlineExceeded,
			wantMessage: []string{"context deadline exceeded"},
			wantEnqueue: 2,
		},
		{
			name: "enqueue cancellation is mapped without wrapping",
			enqueue: func(context.Context, any) (*meilisearch.TaskInfo, error) {
				return nil, context.Canceled
			},
			wantCode:    codes.Canceled,
			wantMessage: []string{"context canceled"},
			wantEnqueue: 2,
		},
		{
			name: "wait error is wrapped and retried",
			enqueue: func(context.Context, any) (*meilisearch.TaskInfo, error) {
				return &meilisearch.TaskInfo{TaskUID: taskUID}, nil
			},
			wait: func(context.Context, int64) (*meilisearch.Task, error) {
				return nil, errors.New("meilisearch unavailable")
			},
			wantCode:    codes.Internal,
			wantMessage: []string{"failed waiting for Meilisearch logs deletion task 42 for workflow workflow-1", "meilisearch unavailable"},
			wantEnqueue: 1,
			wantWait:    2,
		},
		{
			name: "unsuccessful task status surfaces the task error",
			enqueue: func(context.Context, any) (*meilisearch.TaskInfo, error) {
				return &meilisearch.TaskInfo{TaskUID: taskUID}, nil
			},
			wait: func(context.Context, int64) (*meilisearch.Task, error) {
				return &failedTask, nil
			},
			wantCode:    codes.Internal,
			wantMessage: []string{"finished with status failed", "could not delete documents"},
			wantEnqueue: 1,
			wantWait:    2,
		},
		{
			name: "wait deadline is mapped without wrapping",
			enqueue: func(context.Context, any) (*meilisearch.TaskInfo, error) {
				return &meilisearch.TaskInfo{TaskUID: taskUID}, nil
			},
			wait: func(context.Context, int64) (*meilisearch.Task, error) {
				return nil, context.DeadlineExceeded
			},
			wantCode:    codes.DeadlineExceeded,
			wantMessage: []string{"context deadline exceeded"},
			wantEnqueue: 1,
			wantWait:    2,
		},
		{
			name: "wait cancellation is mapped without wrapping",
			enqueue: func(context.Context, any) (*meilisearch.TaskInfo, error) {
				return &meilisearch.TaskInfo{TaskUID: taskUID}, nil
			},
			wait: func(context.Context, int64) (*meilisearch.Task, error) {
				return nil, context.Canceled
			},
			wantCode:    codes.Canceled,
			wantMessage: []string{"context canceled"},
			wantEnqueue: 1,
			wantWait:    2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fake := &deleteWorkflowMeiliFake{enqueue: tt.enqueue, wait: tt.wait}
			repo := &Repository{ms: fake}

			err := repo.deleteWorkflowLogsFromMeiliSearch(t.Context(), workflowID, userID)

			if code := status.Code(err); code != tt.wantCode {
				t.Fatalf("deleteWorkflowLogsFromMeiliSearch() code = %s, want %s: %v", code, tt.wantCode, err)
			}
			for _, want := range tt.wantMessage {
				if err == nil || !strings.Contains(err.Error(), want) {
					t.Fatalf("deleteWorkflowLogsFromMeiliSearch() error = %v, want message containing %q", err, want)
				}
			}
			if len(fake.enqueueCalls) != tt.wantEnqueue {
				t.Fatalf("enqueue calls = %d, want %d", len(fake.enqueueCalls), tt.wantEnqueue)
			}
			if len(fake.waitCalls) != tt.wantWait {
				t.Fatalf("wait calls = %d, want %d", len(fake.waitCalls), tt.wantWait)
			}
			for _, call := range fake.enqueueCalls {
				if call.index != meilisearchpkg.IndexJobLogs {
					t.Fatalf("enqueue index = %q, want %q", call.index, meilisearchpkg.IndexJobLogs)
				}
				if call.filter != wantFilter {
					t.Fatalf("enqueue filter = %v, want %q", call.filter, wantFilter)
				}
			}
			for _, gotUID := range fake.waitCalls {
				if gotUID != taskUID {
					t.Fatalf("wait taskUID = %d, want %d", gotUID, taskUID)
				}
			}
		})
	}
}

func TestDeleteWorkflowLogsFromMeiliSearchRequiresClient(t *testing.T) {
	t.Parallel()

	err := (&Repository{}).deleteWorkflowLogsFromMeiliSearch(t.Context(), "workflow-1", "user-1")
	if code := status.Code(err); code != codes.Internal {
		t.Fatalf("deleteWorkflowLogsFromMeiliSearch() code = %s, want %s: %v", code, codes.Internal, err)
	}
	if !strings.Contains(err.Error(), "client is not configured") || !strings.Contains(err.Error(), "workflow-1") {
		t.Fatalf("deleteWorkflowLogsFromMeiliSearch() error = %v, want workflow id and configuration message", err)
	}
}

func TestDeleteWorkflowStatusErrorMapping(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		err      error
		wantCode codes.Code
		want     string
	}{
		{
			name:     "deadline exceeded keeps its own code and message",
			err:      context.DeadlineExceeded,
			wantCode: codes.DeadlineExceeded,
			want:     context.DeadlineExceeded.Error(),
		},
		{
			name:     "wrapped deadline exceeded is recognized",
			err:      fmt.Errorf("exec: %w", context.DeadlineExceeded),
			wantCode: codes.DeadlineExceeded,
			want:     "exec: context deadline exceeded",
		},
		{
			name:     "cancellation keeps its own code and message",
			err:      context.Canceled,
			wantCode: codes.Canceled,
			want:     context.Canceled.Error(),
		},
		{
			name:     "wrapped cancellation is recognized",
			err:      fmt.Errorf("exec: %w", context.Canceled),
			wantCode: codes.Canceled,
			want:     "exec: context canceled",
		},
		{
			name:     "other errors are internal with the caller message",
			err:      errors.New("storage failure"),
			wantCode: codes.Internal,
			want:     "delete logs: storage failure",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := deleteWorkflowStatusError("delete logs", tt.err)
			if code := status.Code(got); code != tt.wantCode {
				t.Fatalf("deleteWorkflowStatusError() code = %s, want %s: %v", code, tt.wantCode, got)
			}
			if wantErr := status.Error(tt.wantCode, tt.want); got.Error() != wantErr.Error() {
				t.Fatalf("deleteWorkflowStatusError() = %q, want %q", got.Error(), wantErr.Error())
			}
		})
	}
}

func TestDeleteWorkflowLockAcquisitionGuards(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		acquired bool
		err      error
	}{
		{
			name: "lock service error",
			err:  errors.New("redis unavailable"),
		},
		{
			name:     "lock already held",
			acquired: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			lock := &lifecycleLock{acquired: tt.acquired, err: tt.err}
			repo := &Repository{rdb: lock}

			err := repo.deleteWorkflow(t.Context(), "workflow-1", "user-1")
			if code := status.Code(err); code != codes.Aborted {
				t.Fatalf("deleteWorkflow() code = %s, want %s: %v", code, codes.Aborted, err)
			}
			if !strings.Contains(err.Error(), "failed to acquire distributed lock") {
				t.Fatalf("deleteWorkflow() error = %v, want distributed lock message", err)
			}
			if len(lock.keys) != 1 || lock.keys[0] != "workflow:DELETE:workflow-1" {
				t.Fatalf("deleteWorkflow() lock keys = %v, want [workflow:DELETE:workflow-1]", lock.keys)
			}
			assertLockTTLs(t, lock, deleteWorkflowExpirationTimeout)
			if len(lock.released) != 0 {
				t.Fatalf("deleteWorkflow() released a lock it never owned: %v", lock.released)
			}
		})
	}
}
