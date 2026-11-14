//nolint:testpackage // Exercises lifecycle lock ownership with the real Redis store.
package workflow

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	workflowsmodel "github.com/hitesh22rana/chronoverse/internal/model/workflows"
	"github.com/hitesh22rana/chronoverse/internal/pkg/testkit"
	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"
)

func TestIntegrationExpiredWorkflowOwnerCannotReleaseReplacementLock(t *testing.T) {
	store := testkit.Redis(t)
	client := goredis.NewClient(&goredis.Options{Addr: testkit.RedisAddr(t)})
	defer client.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	workflow := lifecycleWorkflow()
	workflow.Id = uuid.NewString()
	event := lifecycleEvent(workflowsmodel.ActionReschedule)
	event.ID = workflow.Id
	key := fmt.Sprintf("%s:%s:%s", lockKeyPrefix, event.Action.ToString(), workflow.Id)

	start := func() (<-chan struct{}, func(), <-chan error) {
		entered, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
		var once sync.Once
		unblock := func() { once.Do(func() { close(release) }) }
		repo, _ := lifecycleRepository(workflow)
		repo.rdb = store
		repo.svc.Jobs = &testJobsClient{scheduleJob: func(ctx context.Context, _ *jobspb.ScheduleJobRequest) (*jobspb.ScheduleJobResponse, error) {
			close(entered)
			select {
			case <-release:
				return &jobspb.ScheduleJobResponse{Id: "job"}, nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}}
		go func() { done <- repo.rescheduleWorkflow(ctx, event) }()
		t.Cleanup(unblock)
		return entered, unblock, done
	}
	await := func(entered <-chan struct{}) {
		t.Helper()
		select {
		case <-entered:
		case <-ctx.Done():
			t.Fatal("workflow did not enter scheduling")
		}
	}
	firstEntered, finishFirst, firstDone := start()
	await(firstEntered)
	// Expire the real lock while the first operation is still in progress.
	if err := client.PExpire(ctx, key, time.Millisecond).Err(); err != nil {
		t.Fatal(err)
	}
	testkit.Eventually(t, 2*time.Second, 10*time.Millisecond, func() bool {
		count, err := client.Exists(ctx, key).Result()
		return err == nil && count == 0
	})
	secondEntered, finishSecond, secondDone := start()
	await(secondEntered)
	finishFirst()
	select {
	case err := <-firstDone:
		require.NoError(t, err)
	case <-ctx.Done():
		t.Fatal("first workflow did not finish")
	}
	if count, err := client.Exists(ctx, key).Result(); err != nil || count != 1 {
		t.Fatalf("expired owner released replacement lock: exists=%d err=%v", count, err)
	}
	finishSecond()
	select {
	case err := <-secondDone:
		require.NoError(t, err)
	case <-ctx.Done():
		t.Fatal("second workflow did not finish")
	}
	if count, err := client.Exists(ctx, key).Result(); err != nil || count != 0 {
		t.Fatalf("replacement owner did not release its lock: exists=%d err=%v", count, err)
	}
}

func TestIntegrationCanceledWorkflowReleasesOwnedLock(t *testing.T) {
	store := testkit.Redis(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	workflow := lifecycleWorkflow()
	workflow.Id = uuid.NewString()
	event := lifecycleEvent(workflowsmodel.ActionReschedule)
	event.ID = workflow.Id
	repo, _ := lifecycleRepository(workflow)
	repo.rdb = store
	repo.svc.Jobs = &testJobsClient{scheduleJob: func(ctx context.Context, _ *jobspb.ScheduleJobRequest) (*jobspb.ScheduleJobResponse, error) {
		cancel()
		return nil, ctx.Err()
	}}
	if err := repo.rescheduleWorkflow(ctx, event); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled reschedule = %v", err)
	}
	key := fmt.Sprintf("%s:%s:%s", lockKeyPrefix, event.Action.ToString(), workflow.Id)
	token, acquired, err := store.AcquireDistributedLockWithToken(t.Context(), key, time.Minute)
	if err != nil || !acquired {
		t.Fatalf("canceled owner retained lock: acquired=%v err=%v", acquired, err)
	}
	if err := store.ReleaseDistributedLockWithToken(t.Context(), key, token); err != nil {
		t.Fatal(err)
	}
}
