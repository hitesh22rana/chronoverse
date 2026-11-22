package jobs

import (
	"context"

	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"
)

// BeginContainerCleanupAttempt grants cleanup execution authority for internal administrators.
func (j *Jobs) BeginContainerCleanupAttempt(
	ctx context.Context,
	req *jobspb.ContainerCleanupAttemptRequest,
) (
	*jobspb.ContainerCleanupAttemptResponse,
	error,
) {
	ctx, cancel := context.WithTimeout(ctx, j.cfg.Deadline)
	defer cancel()
	return j.svc.BeginContainerCleanupAttempt(ctx, req)
}

// FailContainerCleanupAttempt records cleanup failure for internal administrators.
func (j *Jobs) FailContainerCleanupAttempt(
	ctx context.Context,
	req *jobspb.ContainerCleanupAttemptRequest,
) (
	*jobspb.ContainerCleanupAttemptResponse,
	error,
) {
	ctx, cancel := context.WithTimeout(ctx, j.cfg.Deadline)
	defer cancel()
	return j.svc.FailContainerCleanupAttempt(ctx, req)
}

// RedriveContainerCleanup requeues retained cleanup work for internal administrators.
func (j *Jobs) RedriveContainerCleanup(
	ctx context.Context,
	req *jobspb.ContainerCleanupAttemptRequest,
) (
	*jobspb.ContainerCleanupAttemptResponse,
	error,
) {
	ctx, cancel := context.WithTimeout(ctx, j.cfg.Deadline)
	defer cancel()
	return j.svc.RedriveContainerCleanup(ctx, req)
}
