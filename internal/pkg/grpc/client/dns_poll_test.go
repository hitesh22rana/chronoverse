package client //nolint:testpackage // Exercises the polling loop with a fake clock.

import (
	"context"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"google.golang.org/grpc/resolver"
)

type pollingResolver struct{ calls atomic.Int64 }

func (r *pollingResolver) ResolveNow(resolver.ResolveNowOptions) { r.calls.Add(1) }
func (*pollingResolver) Close()                                  {}

func TestPollResolveNowCadenceAndCancellation(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		r := &pollingResolver{}
		done := make(chan struct{})
		go func() {
			pollResolveNow(ctx, r)
			close(done)
		}()
		synctest.Wait()
		time.Sleep(dnsPollInterval - time.Nanosecond)
		synctest.Wait()
		if got := r.calls.Load(); got != 0 {
			t.Fatalf("early resolutions = %d; want 0", got)
		}
		for want := 1; want <= 2; want++ {
			advance := dnsPollInterval
			if want == 1 {
				advance = time.Nanosecond
			}
			time.Sleep(advance)
			synctest.Wait()
			if got := r.calls.Load(); got != int64(want) {
				t.Fatalf("resolutions = %d; want %d", got, want)
			}
		}
		cancel()
		synctest.Wait()
		select {
		case <-done:
		default:
			t.Fatal("polling did not stop after cancellation")
		}
		time.Sleep(2 * dnsPollInterval)
		if got := r.calls.Load(); got != 2 {
			t.Fatalf("resolutions after cancellation = %d; want 2", got)
		}
	})
}
