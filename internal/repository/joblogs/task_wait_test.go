//nolint:testpackage // Verifies task-wait latency and lifecycle through the pinned SDK.
package joblogs

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/meilisearch/meilisearch-go"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type taskWaitTransport func(*http.Request) (*http.Response, error)

func (transport taskWaitTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func TestTaskWaitObservesCompletionPromptlyAndPreservesBounds(t *testing.T) {
	for _, test := range []struct {
		name, operation, terminal string
		cancelAfter, deadline     time.Duration
		wantCode                  codes.Code
		wantElapsed               time.Duration
	}{
		{name: "index completed", operation: "add-documents", terminal: "succeeded"},
		{name: "cleanup failed", operation: "delete-documents", terminal: "failed", wantCode: codes.Internal},
		{name: "caller cancellation", operation: "add-documents", cancelAfter: 100 * time.Millisecond, wantCode: codes.Canceled, wantElapsed: 100 * time.Millisecond},
		{name: "caller deadline", operation: "delete-documents", deadline: 100 * time.Millisecond, wantCode: codes.DeadlineExceeded, wantElapsed: 100 * time.Millisecond},
		{name: "operation timeout", operation: "add-documents", wantCode: codes.DeadlineExceeded, wantElapsed: 2 * time.Minute},
	} {
		t.Run(test.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				start := time.Now()
				client := meilisearch.New("http://search.test", meilisearch.WithCustomClient(&http.Client{Transport: taskWaitTransport(func(request *http.Request) (*http.Response, error) {
					if err := request.Context().Err(); err != nil {
						return nil, err
					}
					require.Equal(t, "/tasks/7", request.URL.Path)
					state := "processing"
					if test.terminal != "" && time.Since(start) >= 100*time.Millisecond {
						state = test.terminal
					}
					return &http.Response{
						StatusCode: http.StatusOK,
						Header:     http.Header{"Content-Type": []string{"application/json"}},
						Body:       io.NopCloser(strings.NewReader(fmt.Sprintf(`{"uid":7,"status":%q}`, state))),
					}, nil
				})}))
				defer client.Close()
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				if test.cancelAfter > 0 {
					time.AfterFunc(test.cancelAfter, cancel)
				}
				if test.deadline > 0 {
					var deadlineCancel context.CancelFunc
					ctx, deadlineCancel = context.WithTimeout(ctx, test.deadline)
					defer deadlineCancel()
				}
				err := (&Repository{ms: client}).waitForMeiliSearchTask(ctx, 7, test.operation)
				require.Equal(t, test.wantCode, status.Code(err))
				if test.wantElapsed > 0 {
					require.Equal(t, test.wantElapsed, time.Since(start))
				} else {
					require.LessOrEqual(t, time.Since(start), 500*time.Millisecond, "completed task should not wait for a multi-second poll")
				}
				if test.terminal == "failed" {
					require.Contains(t, err.Error(), "delete-documents task 7 finished with status failed")
				}
			})
		})
	}
}
