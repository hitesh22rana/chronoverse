package redis

import "fmt"

const (
	// ChannelJobLogsPrefix is the prefix for job-specific log channels.
	ChannelJobLogsPrefix = "job_logs:"
)

// GetJobLogsChannel returns the job-specific channel name for streaming logs.
func GetJobLogsChannel(jobID string) string {
	return fmt.Sprintf("%s%s", ChannelJobLogsPrefix, jobID)
}
