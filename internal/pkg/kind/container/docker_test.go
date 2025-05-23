package container_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/pkg/kind/container"
)

const (
	collectionTimeout = 10 * time.Second
)

func TestDockerWorkflow_Execute(t *testing.T) {
	t.Parallel()

	// Skip if not running in CI environment
	if testing.Short() {
		t.Skip("Skipping long-running tests in short mode")
	}

	workflow, err := container.NewDockerWorkflow()
	require.NoError(t, err)
	require.NotNil(t, workflow)

	t.Cleanup(func() {
		workflow.Close()
	})

	tests := []struct {
		name           string
		image          string
		cmd            []string
		env            []string
		timeout        time.Duration
		executionError error
		timeoutError   error
		errs           []error
		logs           bool
	}{
		{
			name:           "successful execution",
			image:          "alpine:latest",
			cmd:            []string{"/bin/sh", "-c", "echo 'Hello from Docker!' && sleep 5 && echo 'Goodbye from Docker!'"},
			env:            nil,
			timeout:        10 * time.Second,
			executionError: nil,
			errs:           nil,
			logs:           true,
		},
		{
			name:           "successful execution with environment variables",
			image:          "alpine:latest",
			cmd:            []string{"/bin/sh", "-c", "echo $MY_ENV_VAR1 && echo $MY_ENV_VAR2"},
			env:            []string{"MY_ENV_VAR1=Hello from Docker!", "MY_ENV_VAR2=Goodbye from Docker!"},
			timeout:        10 * time.Second,
			executionError: nil,
			errs:           nil,
			logs:           true,
		},
		{
			name:           "error during execution (nonexistent image)",
			image:          "nonexistent:latest",
			cmd:            []string{"/bin/sh", "-c", "echo 'Hello from Docker!'"},
			env:            nil,
			timeout:        5 * time.Second,
			executionError: status.Error(codes.FailedPrecondition, "failed to create container: "),
			errs:           nil,
			logs:           false,
		},
		{
			name:           "error during execution (nonexistent command)",
			image:          "alpine:latest",
			cmd:            []string{"/bin/nonexistent"},
			env:            nil,
			timeout:        5 * time.Second,
			executionError: status.Error(codes.FailedPrecondition, "failed to start container: "),
			errs:           nil,
			logs:           false,
		},
		{
			name:           "error workflow failure during runtime",
			image:          "alpine:latest",
			cmd:            []string{"/bin/sh", "-c", "echo 'About to fail...' && sleep 2 && exit 1"},
			env:            nil,
			timeout:        5 * time.Second,
			executionError: nil,
			errs:           []error{status.Error(codes.Aborted, "container exited with non-zero code: ")},
			logs:           true,
		},
		{
			name:           "error workflow timeout",
			image:          "alpine:latest",
			cmd:            []string{"/bin/sh", "-c", "sleep 5"},
			env:            nil,
			timeout:        2 * time.Second,
			executionError: nil,
			timeoutError:   nil,
			errs:           []error{status.Errorf(codes.DeadlineExceeded, "container execution timed out:")},
			logs:           false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// In the test body
			logs, errs, err := workflow.Execute(t.Context(), tt.timeout, tt.image, tt.cmd, tt.env)
			if tt.executionError != nil {
				require.Error(t, err)
				assert.Equal(t, status.Code(tt.executionError), status.Code(err))
				assert.Contains(t, err.Error(), status.Convert(tt.executionError).Message())
				return
			}

			require.NoError(t, err)

			wg := sync.WaitGroup{}
			wg.Add(1)
			go func() {
				logsCollected, logsCollectedErr := collect(t, logs)
				if tt.timeoutError != nil {
					require.Error(t, logsCollectedErr)
					assert.Equal(t, status.Code(tt.timeoutError), status.Code(logsCollectedErr))
					return
				}

				require.NoError(t, logsCollectedErr)

				if tt.logs {
					assert.NotEmpty(t, logsCollected)
				}

				wg.Done()
			}()

			errsCollected, errsCollectedErr := collect(t, errs)

			if tt.timeoutError != nil {
				require.Error(t, errsCollectedErr)
				assert.Equal(t, status.Code(tt.timeoutError), status.Code(errsCollectedErr))
				return
			}

			if tt.errs != nil {
				require.NotEmpty(t, errsCollected)

				assert.Len(t, errsCollected, len(tt.errs))
				for i, err := range tt.errs {
					assert.Equal(t, status.Code(errsCollected[i]), status.Code(err))
					assert.Contains(t, errsCollected[i].Error(), err.Error())
				}
				return
			}

			wg.Wait()

			assert.Empty(t, errsCollected)
		})
	}
}

func collect[T any](_ *testing.T, ch <-chan T) ([]T, error) {
	var collected []T
	for {
		select {
		case v, ok := <-ch:
			if !ok {
				return collected, nil
			}
			collected = append(collected, v)
		case <-time.After(collectionTimeout):
			return collected, context.DeadlineExceeded
		}
	}
}

func TestDockerWorkflow_Build(t *testing.T) {
	t.Parallel()

	// Skip if not running in CI environment
	if testing.Short() {
		t.Skip("Skipping long-running tests in short mode")
	}

	workflow, err := container.NewDockerWorkflow()
	require.NoError(t, err)
	require.NotNil(t, workflow)

	t.Cleanup(func() {
		workflow.Close()
	})

	tests := []struct {
		name  string
		image string
		err   error
	}{
		{
			name:  "successful pull",
			image: "ghcr.io/hitesh22rana/mq:latest",
			err:   nil,
		},
		{
			name:  "error pull (nonexistent image)",
			image: "nonexistent:latest",
			err:   status.Error(codes.NotFound, "failed to pull image: "),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := workflow.Build(t.Context(), tt.image)
			if tt.err != nil {
				require.Error(t, err)
				assert.Equal(t, status.Code(tt.err), status.Code(err))
				assert.Contains(t, err.Error(), status.Convert(tt.err).Message())
				return
			}

			require.NoError(t, err)
		})
	}
}
