package analyticsprocessor

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

type lifecycleService struct {
	run     func(context.Context) error
	cleanup func(context.Context, time.Duration, int) (int64, error)
}

func (s lifecycleService) Run(ctx context.Context) error { return s.run(ctx) }
func (s lifecycleService) CleanupProcessedEvents(ctx context.Context, retention time.Duration, batch int) (int64, error) {
	return s.cleanup(ctx, retention, batch)
}

func TestNewDefaults(t *testing.T) {
	t.Parallel()
	svc := lifecycleService{}
	app := New(t.Context(), nil, svc)
	if app.cfg == nil || *app.cfg != (Config{}) || app.logger == nil {
		t.Fatalf("invalid defaults: %#v", app)
	}
	cfg := &Config{CleanupBatchSize: 17}
	if New(t.Context(), cfg, svc).cfg != cfg {
		t.Fatal("configuration was not retained")
	}
}

func TestRunWithoutMaintenance(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		cfg    Config
		runErr error
	}{
		{name: "disabled", cfg: Config{CleanupInterval: time.Millisecond, CleanupBatchSize: 1}},
		{name: "zero interval", cfg: Config{CleanupEnabled: true, CleanupBatchSize: 1}},
		{name: "negative interval", cfg: Config{CleanupEnabled: true, CleanupInterval: -time.Second, CleanupBatchSize: 1}},
		{name: "zero batch", cfg: Config{CleanupEnabled: true, CleanupInterval: time.Millisecond}},
		{name: "negative batch", cfg: Config{CleanupEnabled: true, CleanupInterval: time.Millisecond, CleanupBatchSize: -1}},
		{name: "service error", runErr: errors.New("service failed")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			core, logs := observer.New(zap.InfoLevel)
			var serviceCtx context.Context
			calls := 0
			app := &AnalyticsProcessor{logger: zap.New(core), cfg: &tc.cfg, svc: lifecycleService{
				run: func(ctx context.Context) error { calls++; serviceCtx = ctx; return tc.runErr },
				cleanup: func(context.Context, time.Duration, int) (int64, error) {
					t.Error("disabled maintenance ran")
					return 0, nil
				},
			}}
			if err := app.Run(t.Context()); err != nil {
				t.Fatalf("Run()=%v", err)
			}
			if calls != 1 || serviceCtx == nil {
				t.Fatalf("calls=%d context=%v", calls, serviceCtx)
			}
			if serviceCtx.Err() != context.Canceled {
				t.Fatalf("service context=%v", serviceCtx.Err())
			}
			if tc.runErr != nil {
				if logs.FilterMessage("error occurred while running the analytics processor").Len() != 1 {
					t.Fatal("service failure not logged")
				}
			} else if logs.FilterMessage("successfully exited the analytics processor").Len() != 1 {
				t.Fatal("successful exit not logged")
			}
		})
	}
}

func TestRunWaitsForCanceledMaintenance(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		started := make(chan struct{})
		canceled := make(chan struct{})
		release := make(chan struct{})
		finished := make(chan struct{})
		returned := make(chan struct{})
		app := New(t.Context(), &Config{CleanupEnabled: true, CleanupInterval: time.Hour, CleanupBatchSize: 13, ProcessedEventsRetention: 24 * time.Hour}, lifecycleService{
			run: func(context.Context) error { <-started; return nil },
			cleanup: func(ctx context.Context, retention time.Duration, batch int) (int64, error) {
				if retention != 24*time.Hour || batch != 13 {
					t.Errorf("retention=%v batch=%d", retention, batch)
				}
				close(started)
				<-ctx.Done()
				if ctx.Err() != context.Canceled {
					t.Errorf("cleanup context=%v", ctx.Err())
				}
				close(canceled)
				<-release
				close(finished)
				return 0, ctx.Err()
			},
		})
		go func() {
			if err := app.Run(t.Context()); err != nil {
				t.Errorf("Run()=%v", err)
			}
			close(returned)
		}()
		synctest.Wait()
		select {
		case <-canceled:
		default:
			t.Error("maintenance was not canceled")
		}
		select {
		case <-returned:
			t.Error("Run returned before maintenance was released")
		default:
		}
		close(release)
		synctest.Wait()
		select {
		case <-finished:
		default:
			t.Error("maintenance did not finish")
		}
		select {
		case <-returned:
		default:
			t.Error("Run did not return after maintenance finished")
		}
	})
}

func TestRunMaintenanceContinuesAfterFailure(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	core, logs := observer.New(zap.InfoLevel)
	ready := make(chan struct{})
	calls := 0
	app := &AnalyticsProcessor{
		logger: zap.New(core),
		cfg:    &Config{CleanupEnabled: true, CleanupInterval: time.Millisecond, CleanupBatchSize: 7, ProcessedEventsRetention: time.Hour},
		svc: lifecycleService{
			run: func(ctx context.Context) error {
				select {
				case <-ready:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			},
			cleanup: func(_ context.Context, retention time.Duration, batch int) (int64, error) {
				if retention != time.Hour || batch != 7 {
					t.Errorf("retention=%v batch=%d", retention, batch)
				}
				calls++
				switch calls {
				case 1:
					return 0, errors.New("temporary cleanup failure")
				case 2:
					return 3, nil
				default:
					if calls == 3 {
						close(ready)
					}
					return 0, nil
				}
			},
		},
	}
	if err := app.Run(ctx); err != nil {
		t.Fatalf("Run()=%v", err)
	}
	if ctx.Err() != nil || calls < 3 {
		t.Fatalf("calls=%d context=%v", calls, ctx.Err())
	}
	if logs.FilterMessage("failed to cleanup processed events").Len() != 1 {
		t.Fatal("cleanup failure not logged once")
	}
	entries := logs.FilterMessage("cleaned up processed events").All()
	if len(entries) != 1 || entries[0].ContextMap()["total"] != int64(3) {
		t.Fatalf("cleanup logs=%v", entries)
	}
}
