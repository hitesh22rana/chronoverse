package scheduler

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

type schedulerServiceFunc func(context.Context) (int, error)

func (f schedulerServiceFunc) Run(ctx context.Context) (int, error) { return f(ctx) }

func TestNewRetainsConfigurationAndService(t *testing.T) {
	t.Parallel()
	cfg := &Config{PollInterval: time.Second, ContextTimeout: time.Minute}
	called := false
	app := New(t.Context(), cfg, schedulerServiceFunc(func(context.Context) (int, error) { called = true; return 0, nil }))
	if app.cfg != cfg || app.logger == nil {
		t.Fatalf("New()=%#v", app)
	}
	if _, err := app.svc.Run(t.Context()); err != nil || !called {
		t.Fatalf("service called=%v error=%v", called, err)
	}
}

func TestRunCanceledBeforeFirstTick(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	calls := 0
	app := New(ctx, &Config{PollInterval: time.Hour, ContextTimeout: time.Second}, schedulerServiceFunc(func(context.Context) (int, error) { calls++; return 0, nil }))
	if err := app.Run(ctx); err != nil || calls != 0 {
		t.Fatalf("Run()=%v calls=%d", err, calls)
	}
}

func TestRunContinuesAfterServiceFailureAndCancelsEachAttempt(t *testing.T) {
	t.Parallel()
	parent, stop := context.WithTimeout(t.Context(), 5*time.Second)
	defer stop()
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	core, logs := observer.New(zap.InfoLevel)
	var attempts []context.Context
	sentinel := errors.New("temporary scheduling failure")
	app := &Scheduler{logger: zap.New(core), cfg: &Config{PollInterval: time.Millisecond, ContextTimeout: time.Second}, svc: schedulerServiceFunc(func(attempt context.Context) (int, error) {
		if len(attempts) > 0 && attempts[len(attempts)-1].Err() != context.Canceled {
			t.Error("previous attempt was not canceled before next poll")
		}
		deadline, ok := attempt.Deadline()
		if !ok || !deadline.After(time.Now()) {
			t.Error("poll did not receive a live timeout context")
		}
		attempts = append(attempts, attempt)
		if len(attempts) == 1 {
			return 0, sentinel
		}
		if len(attempts) >= 3 {
			cancel()
		}
		return 4, nil
	})}
	if err := app.Run(ctx); err != nil {
		t.Fatalf("Run()=%v", err)
	}
	if parent.Err() != nil || len(attempts) < 3 {
		t.Fatalf("parent=%v attempts=%d", parent.Err(), len(attempts))
	}
	assertCompletedAttempts(t, attempts, logs, sentinel)
}

func assertCompletedAttempts(t *testing.T, attempts []context.Context, logs *observer.ObservedLogs, sentinel error) {
	t.Helper()
	for _, attempt := range attempts {
		if attempt.Err() != context.Canceled {
			t.Errorf("attempt context=%v", attempt.Err())
		}
	}
	failures := logs.FilterMessage("error occurred while running the scheduler").All()
	if len(failures) != 1 || failures[0].ContextMap()["error"] != sentinel.Error() {
		t.Fatalf("failure logs=%v", failures)
	}
	successes := logs.FilterMessage("successfully scheduled jobs").All()
	if len(successes) != len(attempts)-1 {
		t.Fatalf("success logs=%d attempts=%d", len(successes), len(attempts))
	}
	for i := range successes {
		entry := &successes[i]
		if entry.ContextMap()["total"] != int64(4) {
			t.Errorf("scheduled total=%v", entry.ContextMap())
		}
	}
	if logs.FilterMessage("stopping the scheduler").Len() != 1 {
		t.Fatal("shutdown was not logged")
	}
}

func TestRunEnforcesAttemptDeadline(t *testing.T) {
	t.Parallel()
	parent, stop := context.WithTimeout(t.Context(), 5*time.Second)
	defer stop()
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	calls := 0
	app := New(ctx, &Config{PollInterval: time.Millisecond, ContextTimeout: 5 * time.Millisecond}, schedulerServiceFunc(func(attempt context.Context) (int, error) {
		calls++
		<-attempt.Done()
		if calls == 1 && !errors.Is(attempt.Err(), context.DeadlineExceeded) {
			t.Errorf("attempt error=%v", attempt.Err())
		}
		cancel()
		return 0, attempt.Err()
	}))
	if err := app.Run(ctx); err != nil {
		t.Fatalf("Run()=%v", err)
	}
	if parent.Err() != nil || calls < 1 {
		t.Fatalf("parent=%v calls=%d", parent.Err(), calls)
	}
}
