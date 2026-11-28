package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"
)

type jobLogsDownloadRequest struct {
	WorkflowID  string
	JobID       string
	UserID      string
	Stream      jobspb.LogStream
	SearchQuery string
}

func (s *Server) handleListJobs(w http.ResponseWriter, r *http.Request) {
	workflowID := r.PathValue("workflow_id")
	if workflowID == "" {
		http.Error(w, "job ID not found", http.StatusBadRequest)
		return
	}

	userID, ok := requestUserID(w, r)
	if !ok {
		return
	}

	cursor := r.URL.Query().Get("cursor")

	status := r.URL.Query().Get("status")
	if status != "" {
		if !isValidJobStatus(status) {
			http.Error(w, "invalid status", http.StatusBadRequest)
			return
		}
	}

	trigger := r.URL.Query().Get("trigger")
	if trigger != "" {
		if !isValidJobTrigger(trigger) {
			http.Error(w, "invalid trigger", http.StatusBadRequest)
			return
		}
	}

	res, err := s.jobsClient.ListJobs(r.Context(), &jobspb.ListJobsRequest{
		WorkflowId: workflowID,
		UserId:     userID,
		Cursor:     cursor,
		Filters: &jobspb.ListJobsFilters{
			Status:  status,
			Trigger: trigger,
		},
	})
	if err != nil {
		handleError(w, err, "failed to list jobs")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	//nolint:errcheck // The error is always nil
	json.NewEncoder(w).Encode(res)
}

func (s *Server) handleManualScheduleJob(w http.ResponseWriter, r *http.Request) {
	workflowID := r.PathValue("workflow_id")
	if workflowID == "" {
		http.Error(w, "job ID not found", http.StatusBadRequest)
		return
	}

	userID, ok := requestUserID(w, r)
	if !ok {
		return
	}

	idempotencyKey, ok := idempotencyKeyFromHeader(r)
	if !ok {
		http.Error(w, "idempotency key is required", http.StatusBadRequest)
		return
	}

	// ScheduleJob schedules the job manually.
	res, err := s.jobsClient.ScheduleJob(r.Context(), &jobspb.ScheduleJobRequest{
		WorkflowId:     workflowID,
		UserId:         userID,
		ScheduledAt:    time.Now().Format(time.RFC3339Nano),
		Trigger:        jobsmodel.JobTriggerManual.ToString(),
		IdempotencyKey: idempotencyKey,
	})
	if err != nil {
		handleError(w, err, "failed to schedule job")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	//nolint:errcheck // The error is always nil
	json.NewEncoder(w).Encode(res)
}

func (s *Server) handleGetJob(w http.ResponseWriter, r *http.Request) {
	workflowID := r.PathValue("workflow_id")
	if workflowID == "" {
		http.Error(w, "job ID not found", http.StatusBadRequest)
		return
	}

	jobID := r.PathValue("job_id")
	if jobID == "" {
		http.Error(w, "job ID not found", http.StatusBadRequest)
		return
	}

	userID, ok := requestUserID(w, r)
	if !ok {
		return
	}

	res, err := s.jobsClient.GetJob(r.Context(), &jobspb.GetJobRequest{
		Id:         jobID,
		WorkflowId: workflowID,
		UserId:     userID,
	})
	if err != nil {
		handleError(w, err, "failed to get job")
		return
	}

	w.Header().Set("Content-Type", "application/json")

	w.WriteHeader(http.StatusOK)
	//nolint:errcheck // The error is always nil
	json.NewEncoder(w).Encode(res)
}

func (s *Server) handleGetJobLogs(w http.ResponseWriter, r *http.Request) {
	workflowID := r.PathValue("workflow_id")
	if workflowID == "" {
		http.Error(w, "job ID not found", http.StatusBadRequest)
		return
	}

	jobID := r.PathValue("job_id")
	if jobID == "" {
		http.Error(w, "job ID not found", http.StatusBadRequest)
		return
	}

	userID, ok := requestUserID(w, r)
	if !ok {
		return
	}

	cursor := r.URL.Query().Get("cursor")

	res, err := s.jobsClient.GetJobLogs(r.Context(), &jobspb.GetJobLogsRequest{
		Id:         jobID,
		WorkflowId: workflowID,
		UserId:     userID,
		Cursor:     cursor,
		Filters: &jobspb.GetJobLogsFilters{
			Stream: jobspb.LogStream_LOG_STREAM_ALL,
		},
	})
	if err != nil {
		handleError(w, err, "failed to get job logs")
		return
	}

	w.Header().Set("Content-Type", "application/json")

	w.WriteHeader(http.StatusOK)
	//nolint:errcheck // The error is always nil
	json.NewEncoder(w).Encode(res)
}

func (s *Server) handleSearchJobLogs(w http.ResponseWriter, r *http.Request) {
	workflowID := r.PathValue("workflow_id")
	if workflowID == "" {
		http.Error(w, "job ID not found", http.StatusBadRequest)
		return
	}

	jobID := r.PathValue("job_id")
	if jobID == "" {
		http.Error(w, "job ID not found", http.StatusBadRequest)
		return
	}

	userID, ok := requestUserID(w, r)
	if !ok {
		return
	}

	cursor := r.URL.Query().Get("cursor")

	stream, err := getJobLogsStreamType(r.URL.Query().Get("stream"))
	if err != nil {
		http.Error(w, "invalid log stream type", http.StatusBadRequest)
		return
	}

	message := r.URL.Query().Get("q")

	var res *jobspb.GetJobLogsResponse
	switch message {
	case "":
		res, err = s.jobsClient.GetJobLogs(r.Context(), &jobspb.GetJobLogsRequest{
			Id:         jobID,
			WorkflowId: workflowID,
			UserId:     userID,
			Cursor:     cursor,
			Filters: &jobspb.GetJobLogsFilters{
				Stream: stream,
			},
		})
	default:
		res, err = s.jobsClient.SearchJobLogs(r.Context(), &jobspb.SearchJobLogsRequest{
			Id:         jobID,
			WorkflowId: workflowID,
			UserId:     userID,
			Cursor:     cursor,
			Filters: &jobspb.SearchJobLogsFilters{
				Stream:  stream,
				Message: message,
			},
		})
	}

	if err != nil {
		handleError(w, err, "failed to get job logs")
		return
	}

	w.Header().Set("Content-Type", "application/json")

	w.WriteHeader(http.StatusOK)
	//nolint:errcheck // The error is always nil
	json.NewEncoder(w).Encode(res)
}

// handleDownloadJobLogs authorizes the first page before committing the response.
func (s *Server) handleDownloadJobLogs(w http.ResponseWriter, r *http.Request) {
	downloadReq, ok := parseJobLogsDownloadRequest(w, r)
	if !ok || !s.ensureJobLogsDownloadReady(w, r, &downloadReq) {
		return
	}

	flusher, canFlush := w.(http.Flusher)
	if r.Method != http.MethodHead && !canFlush {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	page, err := s.fetchJobLogsDownloadPage(r, &downloadReq, "")
	if err != nil {
		s.logStreamError(r.Context(), "failed to fetch first log download page", err)
		handleError(w, err, "failed to fetch logs")
		return
	}

	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Content-Disposition", `attachment; filename="`+downloadReq.JobID+`-logs.ndjson"`)
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}

	s.streamJobLogsDownload(w, r, flusher, &downloadReq, page)
}

func parseJobLogsDownloadRequest(w http.ResponseWriter, r *http.Request) (jobLogsDownloadRequest, bool) {
	workflowID := r.PathValue("workflow_id")
	if workflowID == "" {
		http.Error(w, "workflow ID not found", http.StatusBadRequest)
		return jobLogsDownloadRequest{}, false
	}
	jobID := r.PathValue("job_id")
	if jobID == "" {
		http.Error(w, "job ID not found", http.StatusBadRequest)
		return jobLogsDownloadRequest{}, false
	}
	// Gate the Content-Disposition filename value before any use.
	if _, err := uuid.Parse(jobID); err != nil {
		http.Error(w, "invalid job ID", http.StatusBadRequest)
		return jobLogsDownloadRequest{}, false
	}
	value := r.Context().Value(userIDKey{})
	if value == nil {
		http.Error(w, "user ID not found", http.StatusBadRequest)
		return jobLogsDownloadRequest{}, false
	}
	userID, ok := value.(string)
	if !ok || userID == "" {
		http.Error(w, "user ID not found", http.StatusBadRequest)
		return jobLogsDownloadRequest{}, false
	}

	streamParam := r.URL.Query().Get("stream")
	stream, err := getJobLogsStreamType(streamParam)
	if err != nil {
		http.Error(w, "invalid log stream type", http.StatusBadRequest)
		return jobLogsDownloadRequest{}, false
	}
	return jobLogsDownloadRequest{
		WorkflowID:  workflowID,
		JobID:       jobID,
		UserID:      userID,
		Stream:      stream,
		SearchQuery: r.URL.Query().Get("q"),
	}, true
}

func (s *Server) ensureJobLogsDownloadReady(
	w http.ResponseWriter,
	r *http.Request,
	downloadReq *jobLogsDownloadRequest,
) bool {
	res, err := s.jobsClient.GetJob(r.Context(), &jobspb.GetJobRequest{
		Id:         downloadReq.JobID,
		WorkflowId: downloadReq.WorkflowID,
		UserId:     downloadReq.UserID,
	})
	if err != nil {
		handleError(w, err, "failed to get job")
		return false
	}

	if !isTerminalJobStatus(res.GetStatus()) {
		http.Error(w, "job is not yet completed", http.StatusBadRequest)
		return false
	}

	return true
}

func (s *Server) streamJobLogsDownload(
	w http.ResponseWriter,
	r *http.Request,
	flusher http.Flusher,
	request *jobLogsDownloadRequest,
	page *jobspb.GetJobLogsResponse,
) {
	ctx := r.Context()
	encoder := json.NewEncoder(w)
	cursor := ""
	for ctx.Err() == nil {
		for _, log := range page.GetLogs() {
			record := jobLogExportRecord{
				Type: "log",
				jobLogExportEntry: &jobLogExportEntry{
					Timestamp:   log.GetTimestamp(),
					SequenceNum: log.GetSequenceNum(),
					Stream:      log.GetStream(),
					EventID:     log.GetEventId(),
					MessageRaw:  log.GetMessage(),
				},
			}
			if err := encoder.Encode(record); err != nil {
				s.logStreamError(ctx, "failed to write log", err)
				return
			}
		}
		next := page.GetCursor()
		if next == "" {
			if ctx.Err() == nil {
				if err := encoder.Encode(jobLogExportRecord{Type: "complete"}); err != nil {
					s.logStreamError(ctx, "failed to write log completion", err)
				}
			}
			return
		}

		flusher.Flush()
		if next == cursor {
			s.writeJobLogsDownloadError(ctx, encoder, errors.New("log cursor did not advance"))
			return
		}
		cursor = next
		var err error
		page, err = s.fetchJobLogsDownloadPage(r, request, cursor)
		if err != nil {
			s.writeJobLogsDownloadError(ctx, encoder, err)
			return
		}
	}
}

func (s *Server) fetchJobLogsDownloadPage(
	r *http.Request,
	downloadReq *jobLogsDownloadRequest,
	cursor string,
) (*jobspb.GetJobLogsResponse, error) {
	if downloadReq.SearchQuery == "" {
		return s.jobsClient.GetJobLogs(r.Context(), &jobspb.GetJobLogsRequest{
			Id:         downloadReq.JobID,
			WorkflowId: downloadReq.WorkflowID,
			UserId:     downloadReq.UserID,
			Cursor:     cursor,
			SortOrder:  jobspb.LogSortOrder_LOG_SORT_ORDER_ASC,
			Filters: &jobspb.GetJobLogsFilters{
				Stream: downloadReq.Stream,
			},
		})
	}

	return s.jobsClient.SearchJobLogs(r.Context(), &jobspb.SearchJobLogsRequest{
		Id:               downloadReq.JobID,
		WorkflowId:       downloadReq.WorkflowID,
		UserId:           downloadReq.UserID,
		Cursor:           cursor,
		SortOrder:        jobspb.LogSortOrder_LOG_SORT_ORDER_ASC,
		DisableHighlight: true,
		Filters: &jobspb.SearchJobLogsFilters{
			Stream:  downloadReq.Stream,
			Message: downloadReq.SearchQuery,
		},
	})
}

type jobLogExportRecord struct {
	Type string `json:"type"`
	*jobLogExportEntry
	Message string `json:"message,omitempty"`
}

type jobLogExportEntry struct {
	Timestamp   string `json:"timestamp"`
	SequenceNum uint32 `json:"sequence_num"`
	Stream      string `json:"stream"`
	EventID     string `json:"event_id"`
	MessageRaw  string `json:"message_raw"`
}

func (s *Server) writeJobLogsDownloadError(ctx context.Context, encoder *json.Encoder, err error) {
	s.logStreamError(ctx, "failed to fetch logs", err)
	if ctx.Err() != nil {
		return
	}
	if err := encoder.Encode(jobLogExportRecord{Type: "error", Message: "stream failed"}); err != nil {
		s.logStreamError(ctx, "failed to write log download error", err)
	}
}

// logStreamError records stream failures with the trace ID for correlation.
func (s *Server) logStreamError(ctx context.Context, message string, err error) {
	log := s.logger
	if spanCtx := trace.SpanContextFromContext(ctx); spanCtx.IsValid() {
		log = log.With(zap.String("trace_id", spanCtx.TraceID().String()))
	}
	log.Error(message, zap.Error(err))
}

func (s *Server) handleJobEvents(w http.ResponseWriter, r *http.Request) {
	workflowID := r.PathValue("workflow_id")
	if workflowID == "" {
		http.Error(w, "workflow ID not found", http.StatusBadRequest)
		return
	}

	jobID := r.PathValue("job_id")
	if jobID == "" {
		http.Error(w, "job ID not found", http.StatusBadRequest)
		return
	}

	userID, ok := requestUserID(w, r)
	if !ok {
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Connection", "keep-alive")

	// Disable compression for SSE
	w.Header().Del("Content-Encoding")
	w.Header().Del("Transfer-Encoding")

	rc, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	ctx := r.Context()

	// A HEAD probe sees the stream headers and stops there: opening the stream
	// would hold it until the client disconnects, with no body to receive.
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}

	stream, err := s.jobsClient.StreamJobLogs(ctx, &jobspb.StreamJobLogsRequest{
		Id:         jobID,
		WorkflowId: workflowID,
		UserId:     userID,
	})
	if err != nil {
		s.logStreamError(ctx, "failed to stream job logs", err)
		fmt.Fprint(w, "event: error\ndata: {\"message\":\"stream failed\"}\n\n")
		rc.Flush()
		return
	}

	fmt.Fprintf(w, "event: connected\ndata: {\"status\":\"connected\"}\n\n")
	rc.Flush()

	s.forwardJobLogEvents(ctx, w, rc, stream)
}

func (s *Server) forwardJobLogEvents(
	ctx context.Context,
	w http.ResponseWriter,
	rc http.Flusher,
	stream jobspb.JobsService_StreamJobLogsClient,
) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
			msg, err := stream.Recv()
			if err != nil {
				if errors.Is(err, io.EOF) {
					fmt.Fprintf(w, "event: end\ndata: {\"status\":\"stream_ended\"}\n\n")
					rc.Flush()
					return
				}

				s.logStreamError(ctx, "job logs stream failed", err)
				fmt.Fprint(w, "event: error\ndata: {\"message\":\"stream failed\"}\n\n")
				rc.Flush()
				return
			}

			if msg == nil {
				continue
			}

			// Log protobuf fields are strings and integers.
			//nolint:errcheck // This concrete message has no fallible marshaler.
			data, _ := json.Marshal(msg)

			fmt.Fprintf(w, "event: log\ndata: %s\n\n", data)
			rc.Flush()
		}
	}
}
