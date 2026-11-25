//nolint:testpackage // Exercises search orchestration with a real job scope check.
package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"
	"github.com/meilisearch/meilisearch-go"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	"github.com/hitesh22rana/chronoverse/internal/pkg/testkit"
	jobssvc "github.com/hitesh22rana/chronoverse/internal/service/jobs"
	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"
)

//nolint:gocyclo // Keeps request, pagination and failure assertions beside the scenario.
func TestIntegrationSearchJobLogsPagingAndFailures(t *testing.T) {
	ctx := context.Background()
	pg := testkit.Postgres(t)
	repo := newTestRepository(t)
	userID, workflowID := seedUserWorkflow(ctx, t, pg)
	jobID, err := repo.ScheduleJob(ctx, workflowID, userID, time.Now().UTC().Format(time.RFC3339Nano), "MANUAL", "search-coverage-"+uuid.NewString(), 1)
	if err != nil {
		t.Fatalf("schedule job: %v", err)
	}
	timestamp := time.Now().UTC().Format(time.RFC3339Nano)
	cases := []struct {
		name                 string
		stream               int
		ascending, highlight bool
		cursor               string
		response             string
		httpStatus           int
		missingJob           bool
		code                 codes.Code
		wantMore             bool
	}{
		{
			name: "descending stdout page", stream: 1, wantMore: true,
			response: fmt.Sprintf(`{"hits":[
                {"id":"a","event_id":"event-a","sequence_num":3,"stream":"stdout","message":"three","timestamp":%q},
                {"id":"b","event_id":"event-b","sequence_num":2,"stream":"stdout","message":"two"},
                {"id":"c","event_id":"event-c","sequence_num":1,"stream":"stdout","message":"one"}
            ]}`, timestamp),
		},
		{
			name: "ascending stderr cursor highlighted", stream: 2, ascending: true, highlight: true,
			cursor: encodeJobLogsCursor(jobLogsCursor{SequenceNum: 5, Stream: "stderr", EventID: "cursor-doc"}),
			response: `{"hits":[
                {"id":"f","event_id":"event-f","sequence_num":6,"stream":"stderr","message":"raw","_formatted":{"message":"marked"}}
            ]}`,
		},
		{name: "search failure", response: `{"message":"search failed","code":"internal","type":"internal","link":""}`, httpStatus: http.StatusBadRequest, code: codes.Internal},
		{name: "malformed hit", response: `{"hits":[{"id":"bad","timestamp":"invalid"}]}`, code: codes.Internal},
		{name: "missing owned job", response: `{"hits":[]}`, missingJob: true, code: codes.NotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			requests := make(chan map[string]any, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/indexes/job_logs/search" {
					t.Errorf("search path=%q", r.URL.Path)
				}
				var request map[string]any
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Errorf("decode request: %v", err)
				}
				select {
				case requests <- request:
				default:
					t.Errorf("unexpected duplicate search request")
				}
				w.Header().Set("Content-Type", "application/json")
				if tc.httpStatus != 0 {
					w.WriteHeader(tc.httpStatus)
				}
				if _, err := w.Write([]byte(tc.response)); err != nil {
					t.Errorf("write search response: %v", err)
				}
			}))
			defer server.Close()
			repo.ms = meilisearch.New(server.URL)
			searchID := jobID
			if tc.missingJob {
				searchID = uuid.NewString()
			}
			sortOrder := jobsmodel.JobLogsSortOrderDesc
			if tc.ascending {
				sortOrder = jobsmodel.JobLogsSortOrderAsc
			}
			result, err := repo.SearchJobLogs(
				ctx, searchID, workflowID, userID, tc.cursor,
				&jobsmodel.SearchJobLogsFilters{Stream: tc.stream, Message: "needle"},
				jobsmodel.SearchJobLogsOptions{SortOrder: sortOrder, DisableHighlight: !tc.highlight},
			)
			if status.Code(err) != tc.code {
				t.Fatalf("error=%v, want %v", err, tc.code)
			}
			if tc.code != codes.OK {
				if result != nil {
					t.Fatalf("failure exposed result=%v", result)
				}
				return
			}
			if (result.Cursor != "") != tc.wantMore {
				t.Fatalf("cursor=%q, want more=%v", result.Cursor, tc.wantMore)
			}
			if tc.wantMore {
				if len(result.JobLogs) != 2 || result.JobLogs[0].EventID != "event-a" || result.JobLogs[1].EventID != "event-b" {
					t.Fatalf("page dropped or reordered logs: %+v", result.JobLogs)
				}
				cursor, err := extractDataFromGetJobLogsCursor(result.Cursor)
				if err != nil || cursor.EventID != "c" || cursor.SequenceNum != 1 || len(result.JobLogs) != 2 {
					t.Fatalf("page=%+v cursor=%+v err=%v", result.JobLogs, cursor, err)
				}
			}
			if tc.highlight {
				if len(result.JobLogs) != 1 || result.HighlightToken == "" || result.JobLogs[0].Message != "marked" {
					t.Fatalf("highlight result=%+v", result)
				}
			} else if result.HighlightToken != "" {
				t.Fatalf("unexpected token %q", result.HighlightToken)
			}
			var request map[string]any
			select {
			case request = <-requests:
			default:
				t.Fatal("search completed without a request")
			}
			filter, ok := request["filter"].(string)
			if !ok {
				t.Fatalf("filter is not a string: %v", request)
			}
			for _, part := range []string{`user_id = "` + userID + `"`, `workflow_id = "` + workflowID + `"`, `job_id = "` + jobID + `"`} {
				if !strings.Contains(filter, part) {
					t.Errorf("filter %q missing %q", filter, part)
				}
			}
			if request["q"] != "needle" || request["limit"] != float64(3) {
				t.Errorf("request=%v", request)
			}
			if tc.stream != 0 {
				stream := "stdout"
				if tc.stream == 2 {
					stream = "stderr"
				}
				if !strings.Contains(filter, `stream = "`+stream+`"`) {
					t.Errorf("stream filter=%q", filter)
				}
			}
			if tc.ascending && !strings.Contains(filter, "sequence_num > 5") {
				t.Errorf("ascending cursor filter=%q", filter)
			}
			if tc.highlight {
				if request["highlightPreTag"] != jobLogsHighlightStart+result.HighlightToken+jobLogsHighlightSuffix {
					t.Errorf("highlight tags=%v", request)
				}
			}
		})
	}
}

func TestIntegrationRetainedLogSearchPreservesRequestDeadline(t *testing.T) {
	repo := newTestRepository(t)
	userID, workflowID := seedUserWorkflow(t.Context(), t, testkit.Postgres(t))
	jobID, err := repo.ScheduleJob(t.Context(), workflowID, userID, time.Now().UTC().Format(time.RFC3339Nano), "MANUAL", "search-timeout-"+uuid.NewString(), 1)
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		if _, readErr := io.Copy(io.Discard, r.Body); readErr != nil {
			t.Errorf("read search request: %v", readErr)
			return
		}
		started <- struct{}{}
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer server.Close()
	defer close(release)
	repo.ms = meilisearch.New(server.URL)

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	result, err := jobssvc.New(validator.New(), repo).SearchJobLogs(ctx, &jobspb.SearchJobLogsRequest{
		Id: jobID, WorkflowId: workflowID, UserId: userID,
		Filters: &jobspb.SearchJobLogsFilters{Stream: jobspb.LogStream_LOG_STREAM_ALL, Message: "needle"},
	})
	select {
	case <-started:
	default:
		t.Fatalf("search did not reach the backend: %v", err)
	}
	if result != nil || status.Code(err) != codes.DeadlineExceeded {
		t.Fatalf("search result=%v error=%v, want DeadlineExceeded", result, err)
	}
}
