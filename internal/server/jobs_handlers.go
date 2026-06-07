package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

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

// handleListJobs handles the list jobs by job ID request.
func (s *Server) handleListJobs(w http.ResponseWriter, r *http.Request) {
	// Get the job ID from the path	parameters
	workflowID := r.PathValue("workflow_id")
	if workflowID == "" {
		http.Error(w, "job ID not found", http.StatusBadRequest)
		return
	}

	// Get the user ID from the context
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

	// Get cursor from the query parameters
	cursor := r.URL.Query().Get("cursor")

	// Get status from the query parameters
	status := r.URL.Query().Get("status")
	if status != "" {
		// Validate the job status
		if !isValidJobStatus(status) {
			http.Error(w, "invalid status", http.StatusBadRequest)
			return
		}
	}

	// Get trigger from the query parameters
	trigger := r.URL.Query().Get("trigger")
	if trigger != "" {
		// Validate the job trigger
		if !isValidJobTrigger(trigger) {
			http.Error(w, "invalid trigger", http.StatusBadRequest)
			return
		}
	}

	// ListJobs lists the jobs by job ID.
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

	// Write the response
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	//nolint:errcheck // The error is always nil
	json.NewEncoder(w).Encode(res)
}

// handleManualScheduleJob handles the manual schedule job request.
func (s *Server) handleManualScheduleJob(w http.ResponseWriter, r *http.Request) {
	// Get the job ID from the path	parameters
	workflowID := r.PathValue("workflow_id")
	if workflowID == "" {
		http.Error(w, "job ID not found", http.StatusBadRequest)
		return
	}

	// Get the user ID from the context
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

	// Write the response
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	//nolint:errcheck // The error is always nil
	json.NewEncoder(w).Encode(res)
}

// handleGetJob handles the get job by job ID request.
func (s *Server) handleGetJob(w http.ResponseWriter, r *http.Request) {
	// Get the job ID from the path	parameters
	workflowID := r.PathValue("workflow_id")
	if workflowID == "" {
		http.Error(w, "job ID not found", http.StatusBadRequest)
		return
	}

	// Get the job ID from the path parameters
	jobID := r.PathValue("job_id")
	if jobID == "" {
		http.Error(w, "job ID not found", http.StatusBadRequest)
		return
	}

	// Get the user ID from the context
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

	// GetJob gets the job by job ID.
	res, err := s.jobsClient.GetJob(r.Context(), &jobspb.GetJobRequest{
		Id:         jobID,
		WorkflowId: workflowID,
		UserId:     userID,
	})
	if err != nil {
		handleError(w, err, "failed to get job")
		return
	}

	// Write the response
	w.Header().Set("Content-Type", "application/json")
	if isTerminalJobStatus(res.GetStatus()) {
		w.Header().Set("Cache-Control", "public, max-age=7200") // Cache for 2 hrs
	}

	w.WriteHeader(http.StatusOK)
	//nolint:errcheck // The error is always nil
	json.NewEncoder(w).Encode(res)
}

// handleGetJobLogs handles the get job logs by job ID request.
func (s *Server) handleGetJobLogs(w http.ResponseWriter, r *http.Request) {
	// Get the job ID from the path	parameters
	workflowID := r.PathValue("workflow_id")
	if workflowID == "" {
		http.Error(w, "job ID not found", http.StatusBadRequest)
		return
	}

	// Get the job ID from the path parameters
	jobID := r.PathValue("job_id")
	if jobID == "" {
		http.Error(w, "job ID not found", http.StatusBadRequest)
		return
	}

	// Get the user ID from the context
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

	// Get cursor from the query parameters
	cursor := r.URL.Query().Get("cursor")

	// GetJobLogs gets the job logs by job ID.
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

	// Write the response
	w.Header().Set("Content-Type", "application/json")
	setJobLogsCacheControl(w, cursor, res.GetCursor())

	w.WriteHeader(http.StatusOK)
	//nolint:errcheck // The error is always nil
	json.NewEncoder(w).Encode(res)
}

// handleSearchJobLogs handles the search job logs by job ID and filters request.
func (s *Server) handleSearchJobLogs(w http.ResponseWriter, r *http.Request) {
	// Get the job ID from the path	parameters
	workflowID := r.PathValue("workflow_id")
	if workflowID == "" {
		http.Error(w, "job ID not found", http.StatusBadRequest)
		return
	}

	// Get the job ID from the path parameters
	jobID := r.PathValue("job_id")
	if jobID == "" {
		http.Error(w, "job ID not found", http.StatusBadRequest)
		return
	}

	// Get the user ID from the context
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

	// Get cursor from the query parameters
	cursor := r.URL.Query().Get("cursor")

	// Get status from the query parameters
	stream, err := getJobLogsStreamType(r.URL.Query().Get("stream"))
	if err != nil {
		http.Error(w, "invalid log stream type", http.StatusBadRequest)
		return
	}

	// Get the search query from the query parameters
	message := r.URL.Query().Get("q")

	var res *jobspb.GetJobLogsResponse
	switch message {
	case "":
		// GetJobLogs gets the filtered logs for a job with only stream specified.
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
		// SearchJobLogs gets the filtered logs of a jobs with both message and stream present
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

	// Write the response
	w.Header().Set("Content-Type", "application/json")
	setJobLogsCacheControl(w, cursor, res.GetCursor())

	w.WriteHeader(http.StatusOK)
	//nolint:errcheck // The error is always nil
	json.NewEncoder(w).Encode(res)
}

func setJobLogsCacheControl(w http.ResponseWriter, requestCursor, responseCursor string) {
	if requestCursor != "" && responseCursor != "" {
		w.Header().Set("Cache-Control", "public, max-age=7200") // Cache for 2 hrs
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
			writeJobLogsDownloadError(w, downloadReq.Format, "failed to fetch logs", err)
			rc.Flush()
			return true
		}

		for _, log := range res.GetLogs() {
			writeErr := writeJobLogsDownloadLog(w, downloadReq.Format, log, &isFirstJSONLog)
			if writeErr != nil {
				writeJobLogsDownloadError(w, downloadReq.Format, "failed to write log", writeErr)
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

func writeJobLogsDownloadError(w io.Writer, format, message string, err error) {
	switch format {
	case jobLogsDownloadFormatJSON:
		encoded, jsonErr := json.Marshal(map[string]string{
			"message": message,
			"error":   err.Error(),
		})
		if jsonErr != nil {
			return
		}
		fmt.Fprintf(w, `],"error":%s}`, encoded)
	case jobLogsDownloadFormatJSONL:
		encodeErr := json.NewEncoder(w).Encode(map[string]string{
			"message": message,
			"error":   err.Error(),
		})
		if encodeErr != nil {
			return
		}
	default:
		fmt.Fprintf(w, "\n--- ERROR: %s ---\n%s\n", message, err.Error())
	}
}

// handleJobEvents handles the job events by job ID request.
func (s *Server) handleJobEvents(w http.ResponseWriter, r *http.Request) {
	// Get the workflow ID from the path parameters
	workflowID := r.PathValue("workflow_id")
	if workflowID == "" {
		http.Error(w, "workflow ID not found", http.StatusBadRequest)
		return
	}

	// Get the job ID from the path parameters
	jobID := r.PathValue("job_id")
	if jobID == "" {
		http.Error(w, "job ID not found", http.StatusBadRequest)
		return
	}

	// Get the user ID from the context
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

	// Response controller for streaming
	rc, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	// Create a context that can be canceled when the client disconnects
	ctx := r.Context()

	// Start the gRPC stream
	stream, err := s.jobsClient.StreamJobLogs(ctx, &jobspb.StreamJobLogsRequest{
		Id:         jobID,
		WorkflowId: workflowID,
		UserId:     userID,
	})
	if err != nil {
		// Send an error event to the client
		fmt.Fprintf(w, "event: error\ndata: %s\n\n", err.Error())
		rc.Flush()
		return
	}

	// Send initial connection event
	fmt.Fprintf(w, "event: connected\ndata: {\"status\":\"connected\"}\n\n")
	rc.Flush()

	// Stream the logs
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

				// Send error event and close
				fmt.Fprintf(w, "event: error\ndata: %s\n\n", err.Error())
				rc.Flush()
				return
			}

			if msg == nil {
				continue
			}

			// Marshal the log message
			data, err := json.Marshal(msg)
			if err != nil {
				fmt.Fprintf(w, "event: error\ndata: failed to marshal log message\n\n")
				rc.Flush()
				continue
			}

			// Send the log event
			fmt.Fprintf(w, "event: log\ndata: %s\n\n", data)
			rc.Flush()
		}
	}
}
