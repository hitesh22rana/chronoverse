package testkit

import (
	"context"
	"errors"
	"testing"

	"github.com/testcontainers/testcontainers-go"
)

func TestTerminateCleanSuite(t *testing.T) {
	s := &suite{}
	s.terminate(t.Context())
	if s.containers != nil {
		t.Fatal("terminate retained containers")
	}
}

type terminationContainer struct {
	testcontainers.Container
	terminate func(context.Context) error
}

func (c terminationContainer) Terminate(ctx context.Context, _ ...testcontainers.TerminateOption) error {
	return c.terminate(ctx)
}

func TestTerminateStopsAllContainersAfterError(t *testing.T) {
	ctx := t.Context()
	s := &suite{}
	calls := [2]int{}
	for i := range calls {
		s.containers = append(s.containers, terminationContainer{terminate: func(got context.Context) error {
			if got != ctx {
				t.Error("container termination lost the teardown context")
			}
			calls[i]++
			if i == 0 {
				return errors.New("container shutdown failed")
			}
			return nil
		}})
	}

	s.terminate(ctx)
	if calls != [2]int{1, 1} {
		t.Fatalf("container termination calls = %v, want [1 1] despite first error", calls)
	}
	if s.containers != nil {
		t.Fatal("terminate retained containers")
	}
	s.terminate(ctx)
	if calls != [2]int{1, 1} {
		t.Fatalf("repeated teardown terminated containers again: %v", calls)
	}
}
