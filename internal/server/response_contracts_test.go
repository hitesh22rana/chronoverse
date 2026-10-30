package server

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestCompressionSetsHeadersForImplicitSuccess(t *testing.T) {
	s := &Server{logger: zap.NewNop()}
	handler := s.withCompressionMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "7")
		_, err := io.WriteString(w, "payload")
		require.NoError(t, err)
	}))
	request := httptest.NewRequest(http.MethodGet, "/jobs", http.NoBody)
	request.Header.Set("Accept-Encoding", "gzip")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, "gzip", response.Result().Header.Get("Content-Encoding"))
	require.Empty(t, response.Result().Header.Get("Content-Length"))
	require.Contains(t, response.Result().Header.Get("Vary"), "Accept-Encoding")
	reader, err := gzip.NewReader(response.Body)
	require.NoError(t, err)
	defer reader.Close()
	body, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.Equal(t, "payload", string(body))
}

func TestCompressionKeepsStreamingAndPlainResponsesUnchanged(t *testing.T) {
	for _, test := range []struct{ path, encoding string }{
		{"/jobs", "identity"},
		{"/workflows/a/jobs/b/logs/raw", "gzip"},
		{"/workflows/a/jobs/b/events", "gzip"},
	} {
		t.Run(test.path, func(t *testing.T) {
			s := &Server{logger: zap.NewNop()}
			handler := s.withCompressionMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusAccepted)
				_, _ = io.WriteString(w, "plain")
			}))
			request := httptest.NewRequest(http.MethodGet, test.path, http.NoBody)
			request.Header.Set("Accept-Encoding", test.encoding)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			require.Equal(t, http.StatusAccepted, response.Code)
			require.Empty(t, response.Header().Get("Content-Encoding"))
			require.Equal(t, "plain", response.Body.String())
		})
	}
}

func TestRequestLoggingUsesMatchedRoutePattern(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/jobs/sensitive-id?token=secret", strings.NewReader(""))
	request.Pattern = "GET /jobs/{id}"
	require.Equal(t, "GET /jobs/{id}", requestLogPath(request))
	request.Pattern = ""
	require.Equal(t, "/jobs/sensitive-id", requestLogPath(request))
}

func TestCompressionPreservesBodylessResponses(t *testing.T) {
	for _, statusCode := range []int{0, http.StatusSwitchingProtocols, http.StatusNoContent, http.StatusNotModified} {
		t.Run(http.StatusText(statusCode), func(t *testing.T) {
			s := &Server{logger: zap.NewNop()}
			handler := s.withCompressionMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if statusCode != 0 {
					w.WriteHeader(statusCode)
				}
			}))
			request := httptest.NewRequest(http.MethodGet, "/jobs", http.NoBody)
			request.Header.Set("Accept-Encoding", "gzip")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			wantStatus := statusCode
			if wantStatus == 0 {
				wantStatus = http.StatusOK
			}
			require.Equal(t, wantStatus, response.Code)
			require.Empty(t, response.Body.Bytes())
			require.Empty(t, response.Result().Header.Get("Content-Encoding"))
		})
	}
}

func TestCompressionLeavesHeadRepresentationHeadersIntact(t *testing.T) {
	s := &Server{logger: zap.NewNop()}
	handler := s.withCompressionMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "7")
		w.WriteHeader(http.StatusOK)
	}))
	request := httptest.NewRequest(http.MethodHead, "/jobs", http.NoBody)
	request.Header.Set("Accept-Encoding", "gzip")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	require.Equal(t, "7", response.Result().Header.Get("Content-Length"))
	require.Empty(t, response.Result().Header.Get("Content-Encoding"))
	require.Empty(t, response.Body.Bytes())
}

func TestCompressionRejectsBodyWritesForBodylessStatus(t *testing.T) {
	for _, statusCode := range []int{http.StatusNoContent, http.StatusNotModified} {
		t.Run(http.StatusText(statusCode), func(t *testing.T) {
			writeError := make(chan error, 1)
			s := &Server{logger: zap.NewNop()}
			handler := s.withCompressionMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(statusCode)
				_, err := w.Write([]byte("forbidden body"))
				writeError <- err
			}))
			server := httptest.NewServer(handler)
			t.Cleanup(server.Close)
			request, err := http.NewRequest(http.MethodGet, server.URL, http.NoBody)
			require.NoError(t, err)
			request.Header.Set("Accept-Encoding", "gzip")
			response, err := server.Client().Do(request)
			require.NoError(t, err)
			defer response.Body.Close()
			body, err := io.ReadAll(response.Body)
			require.NoError(t, err)
			require.Equal(t, statusCode, response.StatusCode)
			require.Empty(t, response.Header.Get("Content-Encoding"))
			require.Empty(t, body)
			require.ErrorIs(t, awaitLifecycleError(t, writeError), http.ErrBodyNotAllowed)
		})
	}
}

func TestCompressionKeepsFirstFinalStatusAfterInformationalHeader(t *testing.T) {
	s := &Server{logger: zap.NewNop()}
	handler := s.withCompressionMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusEarlyHints)
		w.WriteHeader(http.StatusAccepted)
		w.WriteHeader(http.StatusEarlyHints)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, "accepted")
	}))
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	request, err := http.NewRequest(http.MethodGet, server.URL, http.NoBody)
	require.NoError(t, err)
	request.Header.Set("Accept-Encoding", "gzip")
	response, err := server.Client().Do(request)
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, http.StatusAccepted, response.StatusCode)
	require.Equal(t, "gzip", response.Header.Get("Content-Encoding"))
	reader, err := gzip.NewReader(response.Body)
	require.NoError(t, err)
	defer reader.Close()
	body, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.Equal(t, "accepted", string(body))
}
