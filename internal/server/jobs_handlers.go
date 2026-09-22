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

	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
)

const (
	jobLogsDownloadFormatText  = "txt"
	jobLogsDownloadFormatJSON  = "json"
	jobLogsDownloadFormatJSONL = "jsonl"
	jobLogsDownloadStreamAll   = "all"
)

type jobLogsDownloadRequest struct {
	WorkflowID  string
	JobID       string
	UserID      string
	Format      string
	Stream      jobspb.LogStream
	StreamName  string
	SearchQuery string
}

func (s *Server) handleListJobs(w http.ResponseWriter, r *http.Request) {
	workflowID := r.PathValue("workflow_id")
	if workflowID == "" {
		http.Error(w, "job ID not found", http.StatusBadRequest)
		return
	}

	value := r.Context().Value(userIDKey{})
	if value == nil {
		http.Error(w, "user ID not found", http.StatusBadRequest)
		return
	}

	userID, ok := value.(string)
	if !ok || userID == "" {
		http.Error(w, "user ID not found", http.StatusBadRequest)
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

	value := r.Context().Value(userIDKey{})
	if value == nil {
		http.Error(w, "user ID not found", http.StatusBadRequest)
		return
	}

	userID, ok := value.(string)
	if !ok || userID == "" {
		http.Error(w, "user ID not found", http.StatusBadRequest)
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

	value := r.Context().Value(userIDKey{})
	if value == nil {
		http.Error(w, "user ID not found", http.StatusBadRequest)
		return
	}

	userID, ok := value.(string)
	if !ok || userID == "" {
		http.Error(w, "user ID not found", http.StatusBadRequest)
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
	if isTerminalJobStatus(res.GetStatus()) {
		w.Header().Set("Cache-Control", "private, max-age=7200") // Cache for 2 hrs
		w.Header().Add("Vary", "Cookie")
	}

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

	value := r.Context().Value(userIDKey{})
	if value == nil {
		http.Error(w, "user ID not found", http.StatusBadRequest)
		return
	}

	userID, ok := value.(string)
	if !ok || userID == "" {
		http.Error(w, "user ID not found", http.StatusBadRequest)
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
	setJobLogsCacheControl(w, cursor, res.GetCursor())

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

	value := r.Context().Value(userIDKey{})
	if value == nil {
		http.Error(w, "user ID not found", http.StatusBadRequest)
		return
	}

	userID, ok := value.(string)
	if !ok || userID == "" {
		http.Error(w, "user ID not found", http.StatusBadRequest)
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
	setJobLogsCacheControl(w, cursor, res.GetCursor())

	w.WriteHeader(http.StatusOK)
	//nolint:errcheck // The error is always nil
	json.NewEncoder(w).Encode(res)
}

func setJobLogsCacheControl(w http.ResponseWriter, requestCursor, responseCursor string) {
	if requestCursor != "" && responseCursor != "" {
		w.Header().Set("Cache-Control", "private, max-age=7200") // Cache for 2 hrs
		w.Header().Add("Vary", "Cookie")
		return
	}

	w.Header().Set("Cache-Control", "no-store")
}

// handleDownloadJobLogs streams job logs to the client for download.
func (s *Server) handleDownloadJobLogs(w http.ResponseWriter, r *http.Request) {
	downloadReq, ok := parseJobLogsDownloadRequest(w, r)
	if !ok {
		return
	}

	if !s.ensureJobLogsDownloadReady(w, r, &downloadReq) {
		return
	}

	setJobLogsDownloadHeaders(w, &downloadReq)

	rc, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	if downloadReq.Format == jobLogsDownloadFormatJSON {
		headerErr := writeJobLogsDownloadJSONHeader(w, &downloadReq)
		if headerErr != nil {
			http.Error(w, "failed to write logs", http.StatusInternalServerError)
			return
		}
	}

	downloadFailed := s.streamJobLogsDownload(w, r, rc, &downloadReq)
	if downloadReq.Format == jobLogsDownloadFormatJSON && !downloadFailed {
		fmt.Fprint(w, "]}")
		rc.Flush()
	}
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

	format := r.URL.Query().Get("format")
	if format == "" {
		format = jobLogsDownloadFormatText
	}
	if !isValidJobLogsDownloadFormat(format) {
		http.Error(w, "invalid download format", http.StatusBadRequest)
		return jobLogsDownloadRequest{}, false
	}

	streamParam := r.URL.Query().Get("stream")
	stream, err := getJobLogsStreamType(streamParam)
	if err != nil {
		http.Error(w, "invalid log stream type", http.StatusBadRequest)
		return jobLogsDownloadRequest{}, false
	}
	streamName := streamParam
	if streamName == "" {
		streamName = jobLogsDownloadStreamAll
	}

	return jobLogsDownloadRequest{
		WorkflowID:  workflowID,
		JobID:       jobID,
		UserID:      userID,
		Format:      format,
		Stream:      stream,
		StreamName:  streamName,
		SearchQuery: r.URL.Query().Get("q"),
	}, true
}

func isValidJobLogsDownloadFormat(format string) bool {
	return format == jobLogsDownloadFormatText ||
		format == jobLogsDownloadFormatJSON ||
		format == jobLogsDownloadFormatJSONL
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

func setJobLogsDownloadHeaders(w http.ResponseWriter, downloadReq *jobLogsDownloadRequest) {
	switch downloadReq.Format {
	case jobLogsDownloadFormatJSON:
		w.Header().Set("Content-Type", "application/json")
	case jobLogsDownloadFormatJSONL:
		w.Header().Set("Content-Type", "application/x-ndjson")
	default:
		w.Header().Set("Content-Type", "text/plain")
	}
	w.Header().Set("Content-Disposition", `attachment; filename="`+downloadReq.JobID+`-logs.`+downloadReq.Format+`"`)
}

func (s *Server) streamJobLogsDownload(
	w http.ResponseWriter,
	r *http.Request,
	rc http.Flusher,
	downloadReq *jobLogsDownloadRequest,
) (downloadFailed bool) {
	cursor := ""
	isFirstJSONLog := true
	for {
		res, err := s.fetchJobLogsDownloadPage(r, downloadReq, cursor)
		if err != nil {
			s.writeJobLogsDownloadError(r.Context(), w, downloadReq.Format, "failed to fetch logs", err)
			rc.Flush()
			return true
		}

		for _, log := range res.GetLogs() {
			writeErr := writeJobLogsDownloadLog(w, downloadReq.Format, log, &isFirstJSONLog)
			if writeErr != nil {
				s.writeJobLogsDownloadError(r.Context(), w, downloadReq.Format, "failed to write log", writeErr)
				rc.Flush()
				return true
			}
		}
		rc.Flush()

		cursor = res.GetCursor()
		if cursor == "" {
			break
		}
	}

	return false
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

type jobLogsDownloadJSONHeader struct {
	ID         string                    `json:"id"`
	WorkflowID string                    `json:"workflow_id"`
	Filters    jobLogsDownloadJSONFilter `json:"filters"`
}

type jobLogsDownloadJSONFilter struct {
	Query  string `json:"q"`
	Stream string `json:"stream"`
}

type jobLogsDownloadLog struct {
	Timestamp   string           `json:"timestamp"`
	SequenceNum uint32           `json:"sequence_num"`
	Stream      string           `json:"stream"`
	EventID     string           `json:"event_id"`
	MessageRaw  string           `json:"message_raw"`
	MessageJSON *json.RawMessage `json:"message_json,omitempty"`
}

func writeJobLogsDownloadJSONHeader(w io.Writer, downloadReq *jobLogsDownloadRequest) error {
	header, err := json.Marshal(jobLogsDownloadJSONHeader{
		ID:         downloadReq.JobID,
		WorkflowID: downloadReq.WorkflowID,
		Filters: jobLogsDownloadJSONFilter{
			Query:  downloadReq.SearchQuery,
			Stream: downloadReq.StreamName,
		},
	})
	if err != nil {
		return err
	}

	if _, writeErr := w.Write(header[:len(header)-1]); writeErr != nil {
		return writeErr
	}
	_, writeErr := w.Write([]byte(`,"logs":[`))
	return writeErr
}

func newJobLogsDownloadLog(log *jobspb.Log) jobLogsDownloadLog {
	entry := jobLogsDownloadLog{
		Timestamp:   log.GetTimestamp(),
		SequenceNum: log.GetSequenceNum(),
		Stream:      log.GetStream(),
		EventID:     log.GetEventId(),
		MessageRaw:  log.GetMessage(),
	}

	if json.Valid([]byte(log.GetMessage())) {
		raw := json.RawMessage(log.GetMessage())
		entry.MessageJSON = &raw
	}

	return entry
}

func writeJobLogsDownloadLog(w io.Writer, format string, log *jobspb.Log, isFirstJSONLog *bool) error {
	switch format {
	case jobLogsDownloadFormatJSON:
		if !*isFirstJSONLog {
			if _, err := w.Write([]byte(",")); err != nil {
				return err
			}
		}
		*isFirstJSONLog = false
		encoded, err := json.Marshal(newJobLogsDownloadLog(log))
		if err != nil {
			return err
		}
		_, err = w.Write(encoded)
		return err
	case jobLogsDownloadFormatJSONL:
		return json.NewEncoder(w).Encode(newJobLogsDownloadLog(log))
	default:
		_, err := w.Write([]byte(log.GetMessage() + "\n"))
		return err
	}
}

// writeJobLogsDownloadError logs backend detail server-side and sends only a
// generic message so infra strings never reach the client.
func (s *Server) writeJobLogsDownloadError(ctx context.Context, w io.Writer, format, message string, err error) {
	s.logStreamError(ctx, message, err)
	switch format {
	case jobLogsDownloadFormatJSON:
		fmt.Fprint(w, `],"error":{"message":"stream failed"}}`)
	case jobLogsDownloadFormatJSONL:
		fmt.Fprint(w, "{\"message\":\"stream failed\"}\n")
	default:
		fmt.Fprint(w, "\n--- ERROR: stream failed ---\n")
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

	value := r.Context().Value(userIDKey{})
	if value == nil {
		http.Error(w, "user ID not found", http.StatusBadRequest)
		return
	}

	userID, ok := value.(string)
	if !ok || userID == "" {
		http.Error(w, "user ID not found", http.StatusBadRequest)
		return
	}

	// Set SSE headers before writing anything
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	// Disable compression for SSE
	w.Header().Del("Content-Encoding")
	w.Header().Del("Transfer-Encoding") // Ensure no transfer encoding

	rc, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	// Create a context that can be canceled when the client disconnects
	ctx := r.Context()

	stream, err := s.jobsClient.StreamJobLogs(ctx, &jobspb.StreamJobLogsRequest{
		Id:         jobID,
		WorkflowId: workflowID,
		UserId:     userID,
	})
	if err != nil {
		// Send a generic error event to the client
		s.logStreamError(ctx, "failed to stream job logs", err)
		fmt.Fprint(w, "event: error\ndata: {\"message\":\"stream failed\"}\n\n")
		rc.Flush()
		return
	}

	fmt.Fprintf(w, "event: connected\ndata: {\"status\":\"connected\"}\n\n")
	rc.Flush()

	for {
		select {
		case <-ctx.Done():
			// Client disconnected
			return
		default:
			msg, err := stream.Recv()
			if err != nil {
				if errors.Is(err, io.EOF) {
					// Stream ended normally
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

			data, err := json.Marshal(msg)
			if err != nil {
				fmt.Fprintf(w, "event: error\ndata: failed to marshal log message\n\n")
				rc.Flush()
				continue
			}

			fmt.Fprintf(w, "event: log\ndata: %s\n\n", data)
			rc.Flush()
		}
	}
}
