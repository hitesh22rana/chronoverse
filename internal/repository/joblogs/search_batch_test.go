//nolint:testpackage // Exercises search submission through the repository boundary.
package joblogs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/meilisearch/meilisearch-go"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	kafkapkg "github.com/hitesh22rana/chronoverse/internal/pkg/kafka"
)

func TestSearchInsertionBoundsEncodedBytes(t *testing.T) {
	const budget = 4 << 20
	for _, tc := range []struct {
		name, message string
		wantCode      codes.Code
		wantRequests  int
	}{
		{name: "exact budget", message: strings.Repeat("x", budget-len(`[{"id":"0","message":"","user_id":"user-a"}]`)), wantRequests: 3},
		{name: "escaped log lines", message: strings.Repeat("\x00", 256<<10), wantRequests: 2},
		{name: "single document above budget", message: strings.Repeat("x", budget), wantCode: codes.ResourceExhausted},
	} {
		t.Run(tc.name, func(t *testing.T) {
			documents := []*map[string]any{
				{"id": "0", "user_id": "user-a", "message": tc.message},
				{"id": "1", "user_id": "user-a", "message": tc.message},
				{"id": "2", "user_id": "user-b", "message": tc.message},
			}
			var accepted []json.RawMessage
			requests := 0
			repo := &Repository{ms: &batchMeiliFake{index: &batchMeiliIndexFake{add: func(_ context.Context, documents any) (*meilisearch.TaskInfo, error) {
				raw, err := json.Marshal(documents)
				require.NoError(t, err)
				require.LessOrEqual(t, len(raw), budget)
				var batch []json.RawMessage
				require.NoError(t, json.Unmarshal(raw, &batch))
				accepted = append(accepted, batch...)
				requests++
				return &meilisearch.TaskInfo{TaskUID: int64(requests)}, nil
			}}, wait: func(context.Context, int64) (*meilisearch.Task, error) {
				return &meilisearch.Task{Status: meilisearch.TaskStatusSucceeded}, nil
			}}}
			err := repo.insertLogsDocumentsToMeiliSearch(t.Context(), documents)
			require.Equal(t, tc.wantCode, status.Code(err))
			require.Equal(t, tc.wantRequests, requests)
			if err != nil {
				require.False(t, kafkapkg.ShouldCommitOnError(err))
				return
			}
			want, err := json.Marshal(documents)
			require.NoError(t, err)
			got, err := json.Marshal(accepted)
			require.NoError(t, err)
			require.JSONEq(t, string(want), string(got))
		})
	}
}

func TestSearchInsertionHandlesPayloadRejectionThroughSDK(t *testing.T) {
	var limit atomic.Int64
	limit.Store(180)
	accepted := make(chan []json.RawMessage, 16)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && r.URL.Path == "/tasks/1" {
			if _, err := io.WriteString(w, `{"uid":1,"status":"succeeded"}`); err != nil {
				t.Error(err)
			}
			return
		}
		if r.Method != http.MethodPost || !strings.HasPrefix(r.URL.Path, "/indexes/job_logs/documents") {
			t.Errorf("unexpected search request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if int64(len(raw)) > limit.Load() {
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			if _, err := io.WriteString(w, `{"message":"request too large","code":"payload_too_large","type":"invalid_request","link":"https://example.test/errors"}`); err != nil {
				t.Error(err)
			}
			return
		}
		var documents []json.RawMessage
		if err := json.Unmarshal(raw, &documents); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		accepted <- documents
		w.WriteHeader(http.StatusAccepted)
		if _, err := io.WriteString(w, `{"taskUid":1}`); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	client := meilisearch.New(server.URL)
	defer client.Close()
	repo := &Repository{ms: client}
	documents := make([]*map[string]any, 0, 3)
	for i := range 3 {
		documents = append(documents, &map[string]any{"id": fmt.Sprint(i), "user_id": fmt.Sprint(i), "message": strings.Repeat("x", 100)})
	}
	require.NoError(t, repo.insertLogsDocumentsToMeiliSearch(t.Context(), documents))
	var indexed []json.RawMessage
	for len(accepted) > 0 {
		indexed = append(indexed, (<-accepted)...)
	}
	want, err := json.Marshal(documents)
	require.NoError(t, err)
	got, err := json.Marshal(indexed)
	require.NoError(t, err)
	require.JSONEq(t, string(want), string(got))
	// Even a single document can exceed an operator's server/proxy limit.
	limit.Store(1)
	err = repo.insertLogsDocumentsToMeiliSearch(t.Context(), documents[:1])
	require.Equal(t, codes.ResourceExhausted, status.Code(err))
	require.False(t, kafkapkg.ShouldCommitOnError(err))
	require.Empty(t, accepted)
	// Raising the limit recovers the exact same retained document.
	limit.Store(180)
	require.NoError(t, repo.insertLogsDocumentsToMeiliSearch(t.Context(), documents[:1]))
	require.Len(t, accepted, 1)
	want, err = json.Marshal(documents[:1])
	require.NoError(t, err)
	got, err = json.Marshal(<-accepted)
	require.NoError(t, err)
	require.JSONEq(t, string(want), string(got))
}

func TestSearchInsertionPreservesBatchOnPartialFailure(t *testing.T) {
	documents := []*map[string]any{{"id": "0"}, {"id": "1"}, {"id": "2"}}
	fail := true
	var accepted []string
	repo := &Repository{ms: &batchMeiliFake{index: &batchMeiliIndexFake{add: func(_ context.Context, documents any) (*meilisearch.TaskInfo, error) {
		raw, err := json.Marshal(documents)
		require.NoError(t, err)
		var batch []map[string]string
		require.NoError(t, json.Unmarshal(raw, &batch))
		if len(batch) > 1 {
			return nil, fmt.Errorf("proxy rejection: %w", &meilisearch.Error{StatusCode: http.StatusRequestEntityTooLarge})
		}
		if batch[0]["id"] == "1" && fail {
			return nil, fmt.Errorf("temporary search outage")
		}
		accepted = append(accepted, batch[0]["id"])
		return &meilisearch.TaskInfo{TaskUID: 1}, nil
	}}, wait: func(context.Context, int64) (*meilisearch.Task, error) {
		return &meilisearch.Task{Status: meilisearch.TaskStatusSucceeded}, nil
	}}}
	err := repo.insertLogsDocumentsToMeiliSearch(t.Context(), documents)
	require.Equal(t, codes.Internal, status.Code(err))
	require.False(t, kafkapkg.ShouldCommitOnError(err))
	require.Equal(t, []string{"0"}, accepted)
	fail = false
	require.NoError(t, repo.insertLogsDocumentsToMeiliSearch(t.Context(), documents))
	require.Equal(t, []string{"0", "0", "1", "2"}, accepted)
}

func TestSearchInsertionKeepsSubmissionFailuresRetryable(t *testing.T) {
	small := []*map[string]any{{"id": "small", "message": "log"}}
	large := []*map[string]any{{"id": "first", "message": strings.Repeat("x", 3<<20)}, {"id": "second", "message": strings.Repeat("x", 3<<20)}}
	for _, tc := range []struct {
		name         string
		documents    []*map[string]any
		failure      error
		wantRequests int
	}{
		{name: "encoding failure", documents: []*map[string]any{{"timestamp": time.Date(10000, time.January, 1, 0, 0, 0, 0, time.UTC)}}},
		{name: "service unavailable", documents: small, failure: &meilisearch.Error{StatusCode: http.StatusServiceUnavailable}, wantRequests: 1},
		{name: "byte budget flush failure", documents: large, failure: errors.New("search unavailable"), wantRequests: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requests := 0
			repo := &Repository{ms: &batchMeiliFake{index: &batchMeiliIndexFake{add: func(context.Context, any) (*meilisearch.TaskInfo, error) { requests++; return nil, tc.failure }}}}
			err := repo.insertLogsDocumentsToMeiliSearch(t.Context(), tc.documents)
			require.Equal(t, codes.Internal, status.Code(err))
			require.False(t, kafkapkg.ShouldCommitOnError(err))
			require.Equal(t, tc.wantRequests, requests)
		})
	}
}
