//nolint:testpackage // Verifies task polling through the real client's HTTP boundary.
package meilisearch

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	sdk "github.com/meilisearch/meilisearch-go"
	"github.com/stretchr/testify/require"
)

type boundaryTransport func(*http.Request) (*http.Response, error)

func (f boundaryTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func taskResponse(code int, body string) *http.Response {
	return &http.Response{StatusCode: code, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}

func TestTaskPollingTerminalStatesAndBoundedWait(t *testing.T) {
	for _, state := range []string{"succeeded", "existing", "failed", "canceled", "queued", "processing", "request failure", "missing"} {
		t.Run(state, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				calls := 0
				client := sdk.New("http://search.test", sdk.WithCustomClient(&http.Client{Transport: boundaryTransport(func(r *http.Request) (*http.Response, error) {
					calls++
					require.Equal(t, "/tasks", r.URL.Path)
					require.Equal(t, "7", r.URL.Query().Get("uids"))
					require.Equal(t, "1", r.URL.Query().Get("limit"))
					if state == "request failure" {
						return taskResponse(400, `{"message":"request rejected","code":"invalid_task_uids","type":"invalid_request","link":"https://example.test"}`), nil
					}
					if state == "missing" {
						return taskResponse(200, `{"results":[]}`), nil
					}
					terminal := state
					detail := ""
					if state == "existing" {
						terminal = "failed"
						detail = `,"error":{"code":"index_already_exists","message":"already exists"}`
					}
					if state == "failed" {
						detail = `,"error":{"code":"index_not_found","message":"missing index"}`
					}
					return taskResponse(200, fmt.Sprintf(`{"results":[{"uid":7,"status":%q%s}]}`, terminal, detail)), nil
				})}))
				start := time.Now()
				err := waitForTasks(t.Context(), client, []int64{7})
				switch state {
				case "succeeded", "existing":
					require.NoError(t, err)
					require.Equal(t, 1, calls)
				case "queued", "processing", "missing":
					require.ErrorContains(t, err, "timeout waiting")
					require.Equal(t, time.Minute, time.Since(start))
					require.Positive(t, calls)
				case "request failure":
					require.ErrorContains(t, err, "get Meilisearch task info")
					require.Equal(t, 1, calls)
				case "canceled":
					require.ErrorContains(t, err, "task 7 was canceled")
				case "failed":
					require.ErrorContains(t, err, "task 7 failed")
				}
			})
		})
	}
	require.NoError(t, waitForTasks(t.Context(), nil, nil))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err := waitForTasks(ctx, nil, []int64{7})
	require.ErrorIs(t, err, context.Canceled)
}

func TestSetupFailureStagesStopLaterSettings(t *testing.T) {
	for _, stage := range []string{"create", "create task", "searchable", "filterable", "sortable", "update task", "success"} {
		t.Run(stage, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				var paths []string
				polls := 0
				client := sdk.New("http://search.test", sdk.WithCustomClient(&http.Client{Transport: boundaryTransport(func(r *http.Request) (*http.Response, error) {
					path := r.URL.Path
					paths = append(paths, path)
					rejected := map[string]string{
						"create": "/indexes", "searchable": "/indexes/job_logs/settings/searchable-attributes",
						"filterable": "/indexes/job_logs/settings/filterable-attributes", "sortable": "/indexes/job_logs/settings/sortable-attributes",
					}[stage]
					match := path == rejected
					if match {
						return taskResponse(400, `{"message":"settings rejected","code":"invalid_request","type":"invalid_request"}`), nil
					}
					if path == "/tasks" {
						polls++
						if polls == map[string]int{"create task": 1, "update task": 2}[stage] {
							return taskResponse(200, `{"results":[{"uid":1,"status":"failed","error":{"code":"bad_task","message":"failed"}}]}`), nil
						}
						if polls == 1 {
							return taskResponse(200, `{"results":[{"uid":1,"status":"succeeded"}]}`), nil
						}
						return taskResponse(200, `{"results":[{"uid":2,"status":"succeeded"},{"uid":3,"status":"succeeded"},{"uid":4,"status":"succeeded"}]}`), nil
					}
					uid := map[string]int{
						"/indexes": 1, "/indexes/job_logs/settings/searchable-attributes": 2,
						"/indexes/job_logs/settings/filterable-attributes": 3, "/indexes/job_logs/settings/sortable-attributes": 4,
					}[path]
					return taskResponse(202, fmt.Sprintf(`{"taskUid":%d}`, uid)), nil
				})}))
				err := SetupIndexes(t.Context(), client)
				if stage == "success" {
					require.NoError(t, err)
					require.Equal(t, []string{
						"/indexes", "/tasks", "/indexes/job_logs/settings/searchable-attributes",
						"/indexes/job_logs/settings/filterable-attributes", "/indexes/job_logs/settings/sortable-attributes", "/tasks",
					}, paths)
				} else {
					require.Error(t, err)
					require.Contains(t, err.Error(), map[string]string{
						"create": "create index", "create task": "task 1 failed", "searchable": "update searchable",
						"filterable": "update filterable", "sortable": "update sortable", "update task": "task 1 failed",
					}[stage])
				}
				size := map[string]int{"create": 1, "searchable": 3, "filterable": 4, "sortable": 5}[stage]
				if size > 0 {
					require.Len(t, paths, size)
				}
			})
		})
	}
}
