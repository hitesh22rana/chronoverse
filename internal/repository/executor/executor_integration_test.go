//nolint:testpackage // Integration tests share package-internal helpers and constructors.
package executor

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	"github.com/hitesh22rana/chronoverse/internal/pkg/joblogevents"
	kafkapkg "github.com/hitesh22rana/chronoverse/internal/pkg/kafka"
	containerpkg "github.com/hitesh22rana/chronoverse/internal/pkg/kind/container"
	"github.com/hitesh22rana/chronoverse/internal/pkg/testkit"
)

func TestMain(m *testing.M) {
	testkit.Run(m, testkit.WithRedis(), testkit.WithKafka())
}

// fakeContainerSvc tracks concurrent builds so lock serialization can be
// asserted against real Redis.
type fakeContainerSvc struct {
	mu            sync.Mutex
	builds        []string
	inBuild       map[string]int
	maxConcurrent map[string]int
	buildDelay    time.Duration
}

func (f *fakeContainerSvc) Build(ctx context.Context, imageName string) error {
	f.mu.Lock()
	f.inBuild[imageName]++
	if f.inBuild[imageName] > f.maxConcurrent[imageName] {
		f.maxConcurrent[imageName] = f.inBuild[imageName]
	}
	f.mu.Unlock()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(f.buildDelay):
	}

	f.mu.Lock()
	f.builds = append(f.builds, imageName)
	f.inBuild[imageName]--
	f.mu.Unlock()
	return nil
}

func (f *fakeContainerSvc) ImageExists(context.Context, string) (bool, error) { return false, nil }
func (f *fakeContainerSvc) DockerHost() string                                { return "tcp://fake:2375" }

func (f *fakeContainerSvc) Execute(context.Context, time.Duration, string, []string, []string) (output string, logs <-chan *jobsmodel.JobLog, errCh <-chan error, err error) {
	return "", nil, nil, status.Error(codes.Unimplemented, "not implemented")
}

func (f *fakeContainerSvc) Logs(context.Context, string) (logs <-chan *jobsmodel.JobLog, errCh <-chan error, err error) {
	return nil, nil, status.Error(codes.Unimplemented, "not implemented")
}

func (f *fakeContainerSvc) Inspect(context.Context, string) (*containerpkg.State, error) {
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (f *fakeContainerSvc) Remove(context.Context, string) error {
	return status.Error(codes.Unimplemented, "not implemented")
}

func (f *fakeContainerSvc) Terminate(context.Context, string) error {
	return status.Error(codes.Unimplemented, "not implemented")
}

func TestIntegrationImagePullLockSerializesBuilds(t *testing.T) {
	ctx := context.Background()
	inner := &fakeContainerSvc{
		inBuild:       make(map[string]int),
		maxConcurrent: make(map[string]int),
		buildDelay:    200 * time.Millisecond,
	}

	svc := NewImagePullLockedContainerSvc(inner, testkit.Redis(t), ImagePullLockConfig{
		TTL:           5 * time.Second,
		WaitTimeout:   10 * time.Second,
		RetryInterval: 10 * time.Millisecond,
		LockScope:     "integration-test",
	})

	// Three concurrent pulls of the same image must be serialized by the
	// Redis-backed lock.
	var wg sync.WaitGroup
	for range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := svc.Build(ctx, "alpine:3.22.2"); err != nil {
				t.Errorf("Build: %v", err)
			}
		}()
	}
	wg.Wait()

	if got := inner.maxConcurrent["alpine:3.22.2"]; got != 1 {
		t.Fatalf("max concurrent builds = %d, want 1 (lock did not serialize)", got)
	}
	if got := len(inner.builds); got != 3 {
		t.Fatalf("builds = %d, want 3", got)
	}

	// The lock is released after the build; the next pull proceeds immediately.
	before := time.Now()
	if err := svc.Build(ctx, "alpine:3.22.2"); err != nil {
		t.Fatalf("Build after release: %v", err)
	}
	if elapsed := time.Since(before); elapsed > 2*time.Second {
		t.Fatalf("Build after release took %v, want < 2s (lock not released)", elapsed)
	}
}

func TestIntegrationPublishJobLogBatchToKafka(t *testing.T) {
	ctx := context.Background()
	cfg, err := normalizeConfig(&Config{
		JobLogPublishTimeout: 5 * time.Second,
		JobLogPublishRetries: 2,
		JobLogPublishBackoff: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("normalizeConfig: %v", err)
	}
	repo := &Repository{
		cfg: cfg,
		kfk: testkit.Kafka(t),
	}

	jobID := "00000000-0000-0000-0000-0000000000a1"
	payload, err := joblogevents.Marshal(&jobsmodel.JobLogEvent{
		EventKey:    "log:" + jobID + ":stdout:1",
		JobID:       jobID,
		WorkflowID:  "00000000-0000-0000-0000-0000000000a2",
		UserID:      "00000000-0000-0000-0000-0000000000a3",
		Message:     "executor log line",
		TimeStamp:   time.Now().UTC(),
		SequenceNum: 1,
		Stream:      "stdout",
		Retention:   true,
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	repo.publishJobLogBatch(ctx, []*kgo.Record{{
		Topic: kafkapkg.TopicJobLogs,
		Key:   []byte(jobID),
		Value: payload,
	}})

	consumer := testkit.KafkaConsumer(t, "executor-logs-"+t.Name(), kafkapkg.TopicJobLogs)
	record := testkit.WaitForRecord(t, consumer, 15*time.Second, func(rec *kgo.Record) bool {
		return rec.Topic == kafkapkg.TopicJobLogs
	})
	if got := string(record.Key); got != jobID {
		t.Fatalf("record key = %q, want %q", got, jobID)
	}
	if got := string(record.Value); got == "" {
		t.Fatal("record value is empty, want the marshaled log event")
	}
}
