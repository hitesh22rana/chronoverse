package jobs

import (
	"context"

	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"
)

// EnsureContainerIntent applies the service deadline to the lease-fenced intent ensure.
func (j *Jobs) EnsureContainerIntent(ctx context.Context, req *jobspb.ContainerIntentCommand) (*jobspb.ContainerIntentResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, j.cfg.Deadline)
	defer cancel()
	return j.svc.EnsureContainerIntent(ctx, req)
}

// GetContainerIntent applies the service deadline to an internal intent lookup.
func (j *Jobs) GetContainerIntent(ctx context.Context, req *jobspb.ContainerIntentCommand) (*jobspb.ContainerIntentResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, j.cfg.Deadline)
	defer cancel()
	return j.svc.GetContainerIntent(ctx, req)
}

// ReserveContainerCreate applies the service deadline to the create reservation.
func (j *Jobs) ReserveContainerCreate(ctx context.Context, req *jobspb.ContainerIntentCommand) (*jobspb.ContainerIntentResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, j.cfg.Deadline)
	defer cancel()
	return j.svc.ReserveContainerCreate(ctx, req)
}

// ObserveContainer applies the service deadline to the verified materialization observation.
func (j *Jobs) ObserveContainer(ctx context.Context, req *jobspb.ContainerIntentCommand) (*jobspb.ContainerIntentResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, j.cfg.Deadline)
	defer cancel()
	return j.svc.ObserveContainer(ctx, req)
}

// ReserveContainerStart applies the service deadline to the start reservation.
func (j *Jobs) ReserveContainerStart(ctx context.Context, req *jobspb.ContainerIntentCommand) (*jobspb.ContainerIntentResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, j.cfg.Deadline)
	defer cancel()
	return j.svc.ReserveContainerStart(ctx, req)
}

// CloseContainerIntent applies the service deadline to the intent closure.
func (j *Jobs) CloseContainerIntent(ctx context.Context, req *jobspb.ContainerIntentCommand) (*jobspb.ContainerIntentResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, j.cfg.Deadline)
	defer cancel()
	return j.svc.CloseContainerIntent(ctx, req)
}

// ReportContainerInspection applies the service deadline to the inspection report.
func (j *Jobs) ReportContainerInspection(ctx context.Context, req *jobspb.ContainerIntentCommand) (*jobspb.ContainerIntentResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, j.cfg.Deadline)
	defer cancel()
	return j.svc.ReportContainerInspection(ctx, req)
}

// RedriveContainerIntent applies the service deadline to the administrative redrive.
func (j *Jobs) RedriveContainerIntent(ctx context.Context, req *jobspb.ContainerIntentCommand) (*jobspb.ContainerIntentResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, j.cfg.Deadline)
	defer cancel()
	return j.svc.RedriveContainerIntent(ctx, req)
}

// ClaimContainerIntents applies the service deadline to the discovery claim.
func (j *Jobs) ClaimContainerIntents(ctx context.Context, req *jobspb.ContainerIntentCommand) (*jobspb.ListContainerIntentsResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, j.cfg.Deadline)
	defer cancel()
	return j.svc.ClaimContainerIntents(ctx, req)
}

// ListContainerIntents applies the service deadline to an internal intent listing.
func (j *Jobs) ListContainerIntents(ctx context.Context, req *jobspb.ContainerIntentCommand) (*jobspb.ListContainerIntentsResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, j.cfg.Deadline)
	defer cancel()
	return j.svc.ListContainerIntents(ctx, req)
}
