package outboxrelay //nolint:testpackage // Observes private topic-loop logging alongside the public Run lifecycle.

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/hitesh22rana/chronoverse/internal/pkg/kafka"
)

type pollingCall struct {
	topic string
	at    time.Time
	ctx   context.Context //nolint:containedctx // Retained only to assert cancellation after PublishTopic returns.
	err   error
}

type pollingService struct {
	mu    sync.Mutex
	calls []pollingCall
	block bool
}

func (s *pollingService) PublishTopic(ctx context.Context, topic string, _ *zap.Logger) (int, error) {
	s.mu.Lock()
	index := 0
	for _, call := range s.calls {
		if call.topic == topic {
			index++
		}
	}
	s.calls = append(s.calls, pollingCall{topic: topic, at: time.Now(), ctx: ctx})
	position := len(s.calls) - 1
	s.mu.Unlock()
	if s.block {
		<-ctx.Done()
		s.mu.Lock()
		s.calls[position].err = ctx.Err()
		s.mu.Unlock()
		return 0, ctx.Err()
	}
	switch index % 3 {
	case 0:
		return 0, errors.New("publish unavailable")
	case 1:
		return 0, nil
	default:
		return 2, nil
	}
}

func (*pollingService) CleanupPublishedEvents(context.Context, time.Duration, int) (int64, error) {
	panic("topic-only relay must not clean published events")
}

func (*pollingService) CleanupCommandIdempotencyKeys(context.Context, int) (int64, error) {
	panic("topic-only relay must not clean idempotency keys")
}

func (s *pollingService) snapshot() []pollingCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.calls)
}

type pollingContextKey struct{}

func TestRunEnabledTopicPollingContracts(t *testing.T) {
	t.Parallel()
	for mask := 0; mask < 8; mask++ {
		t.Run(fmt.Sprintf("enabled-%03b", mask), func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				const interval = 10 * time.Second
				const timeout = 2 * time.Second
				start := time.Now()
				ctx, cancel := context.WithCancel(context.WithValue(t.Context(), pollingContextKey{}, "request-value"))
				defer cancel()
				core, logs := observer.New(zapcore.InfoLevel)
				svc := &pollingService{}
				cfg := &Config{WorkflowEnabled: mask&1 != 0, JobsEnabled: mask&2 != 0, AnalyticsEnabled: mask&4 != 0, PollInterval: interval, ContextTimeout: timeout}
				app := &OutboxRelay{cfg: cfg, svc: svc, logger: zap.New(core)}
				done := make(chan error, 1)
				go func() { done <- app.Run(ctx) }()
				synctest.Wait()
				expected := []string{}
				for i, topic := range []string{kafka.TopicWorkflows, kafka.TopicJobs, kafka.TopicAnalytics} {
					if mask&(1<<i) != 0 {
						expected = append(expected, topic)
					}
				}
				time.Sleep(interval - time.Nanosecond)
				synctest.Wait()
				if calls := svc.snapshot(); len(calls) != 0 {
					t.Fatalf("published before first tick: %v", calls)
				}
				for tick := 1; tick <= 3; tick++ {
					if tick == 1 {
						time.Sleep(time.Nanosecond)
					} else {
						time.Sleep(interval)
					}
					synctest.Wait()
					assertPollingTick(t, svc.snapshot(), expected, tick, start, interval, timeout)
				}
				assertPollingLogs(t, logs, expected)
				cancel()
				synctest.Wait()
				select {
				case err := <-done:
					if err != nil {
						t.Fatal(err)
					}
				default:
					t.Fatal("Run did not return after cancellation")
				}
				before := len(svc.snapshot())
				time.Sleep(5 * interval)
				synctest.Wait()
				if got := len(svc.snapshot()); got != before {
					t.Fatalf("publishing continued after Run: %d -> %d", before, got)
				}
			})
		})
	}
}

func TestRunTopicDeadlineAndShutdown(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		const interval = 10 * time.Second
		const timeout = 2 * time.Second
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		svc := &pollingService{block: true}
		app := New(ctx, &Config{JobsEnabled: true, PollInterval: interval, ContextTimeout: timeout}, svc)
		done := make(chan error, 1)
		go func() { done <- app.Run(ctx) }()
		synctest.Wait()
		time.Sleep(interval)
		synctest.Wait()
		calls := svc.snapshot()
		if len(calls) != 1 || calls[0].ctx.Err() != nil {
			t.Fatalf("first publish=%v", calls)
		}
		time.Sleep(timeout)
		synctest.Wait()
		calls = svc.snapshot()
		if !errors.Is(calls[0].err, context.DeadlineExceeded) {
			t.Fatalf("publish timeout=%v", calls[0].err)
		}
		time.Sleep(interval - timeout)
		synctest.Wait()
		calls = svc.snapshot()
		if len(calls) != 2 || calls[1].ctx.Err() != nil || calls[1].ctx == calls[0].ctx {
			t.Fatalf("retry contexts=%v", calls)
		}
		cancel()
		synctest.Wait()
		calls = svc.snapshot()
		if !errors.Is(calls[1].err, context.Canceled) {
			t.Fatalf("in-flight shutdown error=%v", calls[1].err)
		}
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		default:
			t.Fatal("Run did not wait for canceled publish")
		}
	})
}

func assertPollingTick(t *testing.T, calls []pollingCall, expected []string, tick int, start time.Time, interval, timeout time.Duration) {
	t.Helper()

	if len(calls) != tick*len(expected) {
		t.Fatalf("tick%d calls=%d want%d", tick, len(calls), tick*len(expected))
	}
	for _, topic := range expected {
		count := 0
		for _, call := range calls {
			if call.topic != topic {
				continue
			}
			count++
			if want := start.Add(time.Duration(count) * interval); call.at != want {
				t.Fatalf("%s call%d at=%v want=%v", topic, count, call.at, want)
			}
			deadline, ok := call.ctx.Deadline()
			if !ok || deadline != call.at.Add(timeout) {
				t.Fatalf("%s deadline=%v exists=%t", topic, deadline, ok)
			}
			if call.ctx.Value(pollingContextKey{}) != "request-value" || !errors.Is(call.ctx.Err(), context.Canceled) {
				t.Fatalf("%s call context value=%v err=%v", topic, call.ctx.Value(pollingContextKey{}), call.ctx.Err())
			}
		}
		if count != tick {
			t.Fatalf("%s calls=%d want%d", topic, count, tick)
		}
	}
}

func assertPollingLogs(t *testing.T, logs *observer.ObservedLogs, expected []string) {
	t.Helper()

	// Error, empty and positive batches each leave the loop alive for the next tick.
	if got := logs.FilterMessage("failed to publish outbox events").Len(); got != len(expected) {
		t.Fatalf("error logs=%d", got)
	}
	published := logs.FilterMessage("published outbox events").All()
	if len(published) != len(expected) {
		t.Fatalf("positive logs=%d", len(published))
	}
	for i := range published {
		fields := published[i].ContextMap()
		topic, ok := fields["topic"].(string)
		if !ok || !slices.Contains(expected, topic) || fields["total"] != int64(2) {
			t.Fatalf("published fields=%v", fields)
		}
	}
}
