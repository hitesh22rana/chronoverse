package svc

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestServiceShutdownReversesAcquisitionAndContinuesAfterErrors(t *testing.T) {
	var order []string
	var shutdownContext context.Context
	s := serviceShutdown{stopSignals: func() { order = append(order, "signals") }}
	for _, name := range []string{"traces", "metrics", "logs", "logger"} {
		s.funcs = append(s.funcs, func(ctx context.Context) error {
			if ctx.Err() != nil {
				t.Fatalf("shutdown received canceled context: %v", ctx.Err())
			}
			deadline, ok := ctx.Deadline()
			if !ok || time.Until(deadline) <= 0 || time.Until(deadline) > 10*time.Second {
				t.Fatalf("shutdown must have a bounded fresh deadline: %v, %v", deadline, ok)
			}
			shutdownContext = ctx
			order = append(order, name)
			return errors.New("close failed")
		})
	}

	s.close()
	s.close()
	if want := []string{"signals", "logger", "logs", "metrics", "traces"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("cleanup order/repetition = %v, want %v", order, want)
	}
	if !errors.Is(shutdownContext.Err(), context.Canceled) {
		t.Fatalf("shutdown deadline context was not released: %v", shutdownContext.Err())
	}
}

func TestServiceShutdownIsConcurrentAndHandlesPartialInitialization(t *testing.T) {
	var stops, closes atomic.Int32
	s := serviceShutdown{
		stopSignals: func() { stops.Add(1) },
		funcs: []func(context.Context) error{func(context.Context) error {
			closes.Add(1)
			return nil
		}},
	}
	var callers sync.WaitGroup
	for range 20 {
		callers.Go(s.close)
	}
	callers.Wait()
	if stops.Load() != 1 || closes.Load() != 1 {
		t.Fatalf("cleanup ran repeatedly: signals=%d provider=%d", stops.Load(), closes.Load())
	}

	var emptyStops int
	empty := serviceShutdown{stopSignals: func() { emptyStops++ }}
	empty.close()
	if emptyStops != 1 {
		t.Fatalf("no-provider failure did not unregister signals: %d", emptyStops)
	}
}
