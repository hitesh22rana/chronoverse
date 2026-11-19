package jobs

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
)

const intentColumns = `
	id::text,
	job_id::text,
	workflow_id::text,
	user_id::text,
	name,
	runtime_node_id,
	runtime_endpoint,
	runtime_instance,
	request_hash,
	COALESCE(container_id,''),
	outcome,
	error_code,
	error_message,
	COALESCE(inspection_token::text,''),
	next_inspection_at::text,
	COALESCE(last_inspection_at::text,''),
	attempt,
	inspection_generation,
	create_reserved_at IS NOT NULL,
	start_reserved_at IS NOT NULL,
	closed_at IS NOT NULL,
	log_retention,
	image_reference,
	workflow_generation,
	outcome_stage,
	inspection_error_code,
	inspection_error_message,
	quarantined,
	inspection_errors,
	EXISTS(
		SELECT 1
		FROM runtime_capacity_holds h
		WHERE h.intent_id = container_creation_intents.id
			AND h.released_at IS NULL
	)`

const containerIntentByID = "SELECT " + intentColumns + " FROM container_creation_intents WHERE id=$1"

func scanContainerIntent(row pgx.Row) (*jobsmodel.ContainerIntent, error) {
	i := &jobsmodel.ContainerIntent{}
	err := row.Scan(
		&i.ID,
		&i.JobID,
		&i.WorkflowID,
		&i.UserID,
		&i.Name,
		&i.RuntimeNodeID,
		&i.RuntimeEndpoint,
		&i.RuntimeInstance,
		&i.RequestHash,
		&i.ContainerID,
		&i.Outcome,
		&i.ErrorCode,
		&i.ErrorMessage,
		&i.Token,
		&i.NextInspectionAt,
		&i.LastInspectionAt,
		&i.Attempt,
		&i.Generation,
		&i.CreateReserved,
		&i.StartReserved,
		&i.Closed,
		&i.LogRetention,
		&i.ImageReference,
		&i.WorkflowGeneration,
		&i.Stage,
		&i.InspectionErrorCode,
		&i.InspectionErrorMessage,
		&i.Quarantined,
		&i.InspectionErrors,
		&i.HeldCapacity,
	)
	return i, err
}

// UpdateContainerIntent keeps observation distinct from execution authority.
func (r *Repository) UpdateContainerIntent(
	ctx context.Context,
	operation string,
	req *jobsmodel.ContainerIntentCommand,
) (
	out *jobsmodel.ContainerIntentResult,
	resultErr error,
) {
	defer func() {
		if resultErr == nil {
			return
		}
		if _, ok := status.FromError(resultErr); !ok {
			resultErr = r.mapJobLeaseWriteError(resultErr, "container intent operation")
		}
	}()
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "container intent command is required")
	}
	command := *req
	tx, err := r.pg.BeginTx(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // Rollback is a no-op after commit.
	result, err := r.applyContainerIntent(ctx, tx, operation, &command)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, r.mapJobLeaseWriteError(err, "commit container intent")
	}
	return result, nil
}

func (r *Repository) applyContainerIntent(
	ctx context.Context,
	tx pgx.Tx,
	operation string,
	req *jobsmodel.ContainerIntentCommand,
) (*jobsmodel.ContainerIntentResult, error) {
	if err := resolveContainerIntentID(ctx, tx, operation, req); err != nil {
		return nil, err
	}
	processID, workflowGeneration, err := r.lockContainerIntentAuthority(ctx, tx, operation, req)
	if err != nil {
		return nil, err
	}
	if operation == jobsmodel.ContainerIntentOperationEnsure {
		if req.WorkflowGeneration != workflowGeneration {
			return nil, status.Error(codes.Aborted, "workflow generation changed before intent creation")
		}
		if createErr := r.createContainerIntent(ctx, tx, req); createErr != nil {
			return nil, createErr
		}
	}
	intent, err := loadContainerIntent(ctx, tx, req.IntentID)
	if err != nil {
		return nil, err
	}
	if identityErr := validateContainerIntentIdentity(intent, req); identityErr != nil {
		return nil, identityErr
	}
	result := &jobsmodel.ContainerIntentResult{Intent: intent}
	if authorityErr := validateOriginalAuthority(ctx, tx, operation, intent, req, processID, workflowGeneration); authorityErr != nil {
		return nil, authorityErr
	}
	if mutateErr := r.mutateContainerIntent(ctx, tx, operation, req, intent, result); mutateErr != nil {
		return nil, mutateErr
	}
	if operation != jobsmodel.ContainerIntentOperationGet {
		result.Intent, err = refreshContainerIntent(ctx, tx, intent.ID)
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}

func requiresJobAuthority(operation string) bool {
	switch operation {
	case jobsmodel.ContainerIntentOperationEnsure, jobsmodel.ContainerIntentOperationReserveCreate, jobsmodel.ContainerIntentOperationReserveStart, jobsmodel.ContainerIntentOperationClose:
		return true
	default:
		return false
	}
}

func resolveContainerIntentID(
	ctx context.Context,
	tx pgx.Tx,
	operation string,
	req *jobsmodel.ContainerIntentCommand,
) error {
	if req.IntentID != "" || operation == jobsmodel.ContainerIntentOperationEnsure {
		return nil
	}
	err := tx.QueryRow(ctx, `
		SELECT
			i.id::text
		FROM
			container_creation_intents i
		WHERE
			i.job_id = $1
			AND (
				(
					$2 <> ''
					AND EXISTS (
						SELECT
							1
						FROM
							container_materializations m
						WHERE
							m.intent_id = i.id
							AND m.container_id = $2
					)
				)
				OR (
					$2 = ''
					AND i.attempt = $3
				)
			)
	`, req.JobID, req.ContainerID, req.Attempt).Scan(&req.IntentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return status.Error(codes.NotFound, "container intent not found")
	}
	return err
}

func (r *Repository) lockContainerIntentAuthority(
	ctx context.Context,
	tx pgx.Tx,
	operation string,
	req *jobsmodel.ContainerIntentCommand,
) (processID string, workflowGeneration int64, resultErr error) {
	if !requiresJobAuthority(operation) {
		return "", 0, nil
	}
	var workflowID string
	err := tx.QueryRow(ctx, `SELECT workflow_id::text FROM jobs WHERE id=$1`, req.JobID).Scan(&workflowID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", 0, status.Error(codes.Aborted, "container job no longer exists")
	}
	if err != nil {
		return "", 0, err
	}
	var terminated bool
	err = tx.QueryRow(ctx, `
		SELECT
			terminated_at IS NOT NULL,
			generation
		FROM
			workflows
		WHERE
			id = $1
		FOR UPDATE
	`, workflowID).Scan(&terminated, &workflowGeneration)
	if err != nil {
		return "", 0, r.mapJobLeaseReadError(err, "lock intent workflow")
	}
	var valid bool
	err = tx.QueryRow(ctx, `
		SELECT
			status = 'RUNNING'
			AND lease_token = $2
			AND lease_expires_at > (clock_timestamp() AT TIME ZONE 'utc')
			AND attempts = $3,
			COALESCE(lease_process_instance_id::text, '')
		FROM
			jobs
		WHERE
			id = $1
		FOR UPDATE
	`, req.JobID, req.LeaseToken, req.Attempt).Scan(&valid, &processID)
	if err != nil {
		return "", 0, err
	}
	if !valid || (terminated && operation != jobsmodel.ContainerIntentOperationClose) {
		return "", 0, status.Error(codes.Aborted, "container execution authority is no longer held")
	}
	return processID, workflowGeneration, nil
}

func (r *Repository) createContainerIntent(
	ctx context.Context,
	tx pgx.Tx,
	req *jobsmodel.ContainerIntentCommand,
) error {
	id := uuid.NewString()
	_, err := tx.Exec(ctx, `
		INSERT INTO
			container_creation_intents (
				id,
				job_id,
				workflow_id,
				user_id,
				attempt,
				workflow_generation,
				process_instance_id,
				original_lease_hash,
				runtime_node_id,
				runtime_endpoint,
				runtime_instance,
				request_hash,
				name,
				log_retention,
				image_reference
			)
		SELECT
			$1,
			j.id,
			j.workflow_id,
			j.user_id,
			j.attempts,
			w.generation,
			j.lease_process_instance_id,
			encode(sha256(convert_to(j.lease_token, 'UTF8')), 'hex'),
			j.runtime_node_id,
			j.runtime_endpoint,
			$3,
			$4,
			$5,
			w.log_retention,
			COALESCE(
				NULLIF(w.resolved_image_digest, ''),
				w.payload ->> 'image',
				''
			)
		FROM
			jobs j
			JOIN workflows w ON w.id = j.workflow_id
		WHERE
			j.id = $2
		ON CONFLICT (job_id, attempt) DO NOTHING
	`, id, req.JobID, req.RuntimeInstance, req.RequestHash, "cv-"+id)
	if err != nil {
		return r.mapJobLeaseWriteError(err, "ensure container intent")
	}
	return tx.QueryRow(ctx, `
SELECT id::text
FROM container_creation_intents
WHERE job_id=$1 AND attempt=$2`, req.JobID, req.Attempt).Scan(&req.IntentID)
}

func loadContainerIntent(ctx context.Context, tx pgx.Tx, intentID string) (*jobsmodel.ContainerIntent, error) {
	intent, err := scanContainerIntent(tx.QueryRow(ctx, containerIntentByID+" FOR UPDATE", intentID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, status.Error(codes.NotFound, "container intent not found")
	}
	if err != nil {
		return nil, err
	}
	return intent, nil
}

func refreshContainerIntent(ctx context.Context, tx pgx.Tx, intentID string) (*jobsmodel.ContainerIntent, error) {
	return scanContainerIntent(tx.QueryRow(ctx, containerIntentByID, intentID))
}

func validateContainerIntentIdentity(intent *jobsmodel.ContainerIntent, req *jobsmodel.ContainerIntentCommand) error {
	if req.JobID != "" && intent.JobID != req.JobID {
		return status.Error(codes.InvalidArgument, "intent/job mismatch")
	}
	if req.RuntimeInstance != "" && intent.RuntimeInstance != req.RuntimeInstance {
		return status.Error(codes.FailedPrecondition, "intent runtime instance mismatch")
	}
	if req.RequestHash != "" && intent.RequestHash != req.RequestHash {
		return status.Error(codes.AlreadyExists, "intent request is immutable")
	}
	return nil
}

func validateOriginalAuthority(
	ctx context.Context,
	tx pgx.Tx,
	operation string,
	intent *jobsmodel.ContainerIntent,
	req *jobsmodel.ContainerIntentCommand,
	processID string,
	workflowGeneration int64,
) error {
	if !requiresJobAuthority(operation) {
		return nil
	}
	if processID == "" {
		return status.Error(codes.Aborted, "container execution process identity is missing")
	}
	var sameProcess bool
	err := tx.QueryRow(ctx, `
		SELECT
			process_instance_id::text = $2
			AND original_lease_hash = encode(sha256(convert_to($3, 'UTF8')), 'hex')
			AND workflow_generation = $4
		FROM
			container_creation_intents
		WHERE
			id = $1
	`, intent.ID, processID, req.LeaseToken, workflowGeneration).Scan(&sameProcess)
	if err != nil {
		return err
	}
	if !sameProcess && operation != jobsmodel.ContainerIntentOperationClose {
		return status.Error(codes.Aborted, "container authority changed since intent creation")
	}
	return nil
}

func (r *Repository) mutateContainerIntent(
	ctx context.Context,
	tx pgx.Tx,
	operation string,
	req *jobsmodel.ContainerIntentCommand,
	intent *jobsmodel.ContainerIntent,
	result *jobsmodel.ContainerIntentResult,
) error {
	var err error
	switch operation {
	case jobsmodel.ContainerIntentOperationEnsure, jobsmodel.ContainerIntentOperationGet:
		err = observeIntentContainer(ctx, tx, intent, req)
	case jobsmodel.ContainerIntentOperationReserveCreate, jobsmodel.ContainerIntentOperationReserveStart:
		result.Granted, err = reserveContainerIntent(ctx, tx, operation, intent, req)
	case jobsmodel.ContainerIntentOperationObserve:
		err = recordContainerObservation(ctx, tx, intent, req)
	case jobsmodel.ContainerIntentOperationClose:
		err = closeContainerIntent(ctx, tx, intent, req)
	case jobsmodel.ContainerIntentOperationReport:
		err = reportContainerInspection(ctx, tx, intent, req)
	case jobsmodel.ContainerIntentOperationRedrive:
		err = r.redriveContainerIntent(ctx, tx, intent, req)
	default:
		return status.Error(codes.InvalidArgument, "unknown container intent operation")
	}
	if err == nil {
		return nil
	}
	if _, ok := status.FromError(err); ok {
		return err
	}
	return r.mapJobLeaseWriteError(err, "update container intent")
}

func observeIntentContainer(
	ctx context.Context,
	tx pgx.Tx,
	intent *jobsmodel.ContainerIntent,
	req *jobsmodel.ContainerIntentCommand,
) error {
	if req.ContainerID == "" {
		return nil
	}
	var observed bool
	err := tx.QueryRow(ctx, `
		SELECT
			EXISTS (
				SELECT
					1
				FROM
					container_materializations
				WHERE
					intent_id = $1
					AND container_id = $2
					AND runtime_instance = $3
			)
	`, intent.ID, req.ContainerID, intent.RuntimeInstance).Scan(&observed)
	if err != nil {
		return err
	}
	if !observed {
		return status.Error(codes.NotFound, "container materialization is not recorded")
	}
	intent.ContainerID = req.ContainerID
	return nil
}

func reserveContainerIntent(
	ctx context.Context,
	tx pgx.Tx,
	operation string,
	intent *jobsmodel.ContainerIntent,
	req *jobsmodel.ContainerIntentCommand,
) (bool, error) {
	if intent.Closed {
		return false, nil
	}
	column := "create_reserved_at"
	if operation == jobsmodel.ContainerIntentOperationReserveStart {
		column = "start_reserved_at"
		if err := validateStartReservation(ctx, tx, intent, req); err != nil {
			return false, err
		}
	}
	ct, err := tx.Exec(ctx, `
UPDATE container_creation_intents
SET `+column+`=clock_timestamp() AT TIME ZONE 'utc'
WHERE id=$1 AND `+column+` IS NULL AND closed_at IS NULL`, intent.ID)
	if err != nil {
		return false, err
	}
	granted := ct.RowsAffected() == 1
	if operation == jobsmodel.ContainerIntentOperationReserveStart && granted {
		_, err = tx.Exec(ctx, `
INSERT INTO runtime_capacity_holds(
  intent_id,job_id,attempt,container_id,runtime_node_id,runtime_instance
)
VALUES($1,$2,$3,$4,$5,$6)
ON CONFLICT DO NOTHING`, intent.ID, intent.JobID, intent.Attempt, intent.ContainerID, intent.RuntimeNodeID, intent.RuntimeInstance)
		if err != nil {
			return false, err
		}
	}
	return granted, nil
}

func validateStartReservation(
	ctx context.Context,
	tx pgx.Tx,
	intent *jobsmodel.ContainerIntent,
	req *jobsmodel.ContainerIntentCommand,
) error {
	if !intent.CreateReserved || intent.ContainerID == "" || intent.ContainerID != req.ContainerID {
		return status.Error(codes.FailedPrecondition, "start requires the observed immutable container")
	}
	var attached bool
	err := tx.QueryRow(ctx, `SELECT container_id=$2 FROM jobs WHERE id=$1`, intent.JobID, intent.ContainerID).Scan(&attached)
	if err != nil {
		return err
	}
	if !attached {
		return status.Error(codes.FailedPrecondition, "start requires attachment")
	}
	var removed bool
	err = tx.QueryRow(ctx, `
		SELECT
			EXISTS (
				SELECT
					1
				FROM
					container_cleanup_receipts
				WHERE
					intent_id = $1
					AND container_id = $2
					AND runtime_instance = $3
			)
	`, intent.ID, intent.ContainerID, intent.RuntimeInstance).Scan(&removed)
	if err != nil {
		return err
	}
	if removed {
		return status.Error(codes.Aborted, "container was removed before start reservation")
	}
	var blocked bool
	err = tx.QueryRow(ctx, `
		SELECT
			EXISTS (
				SELECT
					1
				FROM
					container_creation_intents i
				WHERE
					i.workflow_id = $1
					AND i.id <> $2
					AND i.start_reserved_at IS NOT NULL
					AND NOT EXISTS (
						SELECT
							1
						FROM
							container_cleanup_receipts r
						WHERE
							r.intent_id = i.id
							AND r.container_id = i.container_id
							AND r.runtime_instance = i.runtime_instance
					)
			)
	`, intent.WorkflowID, intent.ID).Scan(&blocked)
	if err != nil {
		return err
	}
	if blocked {
		return status.Error(codes.Aborted, "prior workflow container remains unresolved")
	}
	return nil
}

func recordContainerObservation(
	ctx context.Context,
	tx pgx.Tx,
	intent *jobsmodel.ContainerIntent,
	req *jobsmodel.ContainerIntentCommand,
) error {
	if req.RuntimeInstance != intent.RuntimeInstance || req.ContainerID == "" {
		return status.Error(codes.InvalidArgument, "verified instance/container observation is required")
	}
	if req.Token != "" {
		if err := validateInspectionLease(ctx, tx, intent, req); err != nil {
			return err
		}
	}
	tag, err := tx.Exec(ctx, `
INSERT INTO container_materializations(intent_id,container_id,runtime_instance)
VALUES($1,$2,$3)
ON CONFLICT (intent_id,container_id,runtime_instance) DO NOTHING`, intent.ID, req.ContainerID, req.RuntimeInstance)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 1 {
		if _, err := tx.Exec(ctx, `
			UPDATE container_creation_intents
			SET
				inspection_urgent = TRUE,
				next_inspection_at = clock_timestamp() AT TIME ZONE 'utc'
			WHERE
				id = $1
		`, intent.ID); err != nil {
			return err
		}
	}
	if intent.ContainerID == "" && !intent.Closed {
		if _, err := tx.Exec(ctx, `
			UPDATE container_creation_intents
			SET
				container_id = $2
			WHERE
				id = $1
				AND container_id IS NULL
				AND closed_at IS NULL
		`, intent.ID, req.ContainerID); err != nil {
			return err
		}
		intent.ContainerID = req.ContainerID
	}
	if intent.Closed || intent.ContainerID != req.ContainerID {
		if _, err := tx.Exec(ctx, `SELECT enqueue_intent_container_cleanup($1,$2)`, intent.ID, req.ContainerID); err != nil {
			return err
		}
	}
	return nil
}

func closeContainerIntent(
	ctx context.Context,
	tx pgx.Tx,
	intent *jobsmodel.ContainerIntent,
	req *jobsmodel.ContainerIntentCommand,
) error {
	_, err := tx.Exec(ctx, `
		UPDATE container_creation_intents
		SET
			closed_at = COALESCE(closed_at, clock_timestamp() AT TIME ZONE 'utc'),
			outcome = $2,
			error_code = $3,
			error_message = $4,
			outcome_stage = $5,
			next_inspection_at = CASE
				WHEN closed_at IS NULL THEN clock_timestamp() AT TIME ZONE 'utc'
				ELSE next_inspection_at
			END,
			inspection_urgent = CASE
				WHEN closed_at IS NULL THEN TRUE
				ELSE inspection_urgent
			END
		WHERE
			id = $1
	`, intent.ID, req.Outcome, req.ErrorCode, truncateDiagnostic(req.ErrorMessage), req.Stage)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `SELECT enqueue_intent_materializations($1)`, intent.ID)
	return err
}

func reportContainerInspection(
	ctx context.Context,
	tx pgx.Tx,
	intent *jobsmodel.ContainerIntent,
	req *jobsmodel.ContainerIntentCommand,
) error {
	if err := validateInspectionLease(ctx, tx, intent, req); err != nil {
		return err
	}
	// The receipt PK (job_id, container_id, runtime_instance) limits the scalar lookup to at most one row.
	// Keep this form: measured NOT EXISTS plans repeatedly scanned a job's receipts.
	_, err := tx.Exec(ctx, `
		UPDATE container_creation_intents
		SET
			inspection_urgent = CASE
				WHEN $2 IN ($5::text, $6::text) THEN inspection_urgent
				ELSE EXISTS (
					SELECT
						1
					FROM
						runtime_capacity_holds h
					WHERE
						h.intent_id = container_creation_intents.id
						AND h.released_at IS NULL
				)
				OR EXISTS (
					SELECT
						1
					FROM
						container_materializations m
					WHERE
						m.intent_id = container_creation_intents.id
						AND NOT COALESCE(
							(
								SELECT
									r.intent_id = m.intent_id
								FROM
									container_cleanup_receipts r
								WHERE
									r.job_id = container_creation_intents.job_id
									AND r.container_id = m.container_id
									AND r.runtime_instance = m.runtime_instance
							),
							FALSE
						)
				)
			END,
			inspection_token = NULL,
			inspection_until = NULL,
			last_inspection_at = clock_timestamp() AT TIME ZONE 'utc',
			inspection_errors = CASE
				WHEN $2 = $5::text THEN LEAST(3, inspection_errors + 1)
				WHEN $2 = $6::text THEN inspection_errors
				ELSE 0
			END,
			quarantined = (
				$2 = $5::text
				AND inspection_errors + 1 >= 3
			),
			inspection_error_code = CASE
				WHEN $2 = $5::text THEN $3
				WHEN $2 = $6::text THEN inspection_error_code
				ELSE ''
			END,
			inspection_error_message = CASE
				WHEN $2 = $5::text THEN $4
				WHEN $2 = $6::text THEN inspection_error_message
				ELSE ''
			END,
			next_inspection_at = (clock_timestamp() AT TIME ZONE 'utc') + (
				CASE
					WHEN $2 = $5::text THEN LEAST(300, 15 * power(2, inspection_errors))
					WHEN $2 = $6::text THEN 15
					ELSE 300
				END + random() * 5
			) * interval '1 second'
		WHERE
			id = $1
	`, intent.ID, req.Outcome, req.ErrorCode, truncateDiagnostic(req.ErrorMessage), jobsmodel.ContainerInspectionError, jobsmodel.ContainerInspectionInterrupted)
	return err
}

func (r *Repository) redriveContainerIntent(
	ctx context.Context,
	tx pgx.Tx,
	intent *jobsmodel.ContainerIntent,
	req *jobsmodel.ContainerIntentCommand,
) error {
	if req.Generation != intent.Generation {
		return status.Error(codes.Aborted, "container intent generation changed")
	}
	if strings.TrimSpace(req.Actor) == "" || strings.TrimSpace(req.Reason) == "" {
		return status.Error(codes.InvalidArgument, "authenticated redrive actor and reason required")
	}
	var busy bool
	err := tx.QueryRow(ctx, `
		SELECT
			inspection_until > (clock_timestamp() AT TIME ZONE 'utc')
		FROM
			container_creation_intents
		WHERE
			id = $1
			AND inspection_until IS NOT NULL
	`, intent.ID).Scan(&busy)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if busy {
		return status.Error(codes.Aborted, "container inspection is active")
	}
	_, err = tx.Exec(ctx, `
INSERT INTO container_intent_redrives(intent_id,expected_generation,generation,actor,reason)
VALUES($1,$2::bigint,$2::bigint+1,$3,$4)`, intent.ID, intent.Generation, req.Actor, req.Reason)
	if err != nil {
		return r.mapJobLeaseWriteError(err, "audit container intent redrive")
	}
	_, err = tx.Exec(ctx, `
		UPDATE container_creation_intents
		SET
			inspection_urgent = TRUE,
			inspection_generation = inspection_generation + 1,
			inspection_errors = 0,
			quarantined = false,
			inspection_token = NULL,
			inspection_until = NULL,
			next_inspection_at = clock_timestamp() AT TIME ZONE 'utc',
			redriven_by = $2,
			redrive_reason = $3,
			redriven_at = clock_timestamp() AT TIME ZONE 'utc'
		WHERE
			id = $1
	`, intent.ID, req.Actor, req.Reason)
	return err
}

func truncateDiagnostic(s string) string {
	s = strings.ToValidUTF8(s, "")
	r := []rune(s)
	if len(r) > 2048 {
		return string(r[:2048])
	}
	return s
}

func validateInspectionLease(
	ctx context.Context,
	tx pgx.Tx,
	intent *jobsmodel.ContainerIntent,
	req *jobsmodel.ContainerIntentCommand,
) error {
	var valid bool
	err := tx.QueryRow(ctx, `
		SELECT
			COALESCE(
				inspection_token::text = $2
				AND inspection_generation = $3
				AND inspection_until > (clock_timestamp() AT TIME ZONE 'utc'),
				false
			)
		FROM
			container_creation_intents
		WHERE
			id = $1
	`, intent.ID, req.Token, req.Generation).Scan(&valid)
	if err != nil {
		return err
	}
	if !valid {
		return status.Error(codes.Aborted, "container inspection ownership expired")
	}
	return nil
}

// ListContainerIntents lists due intents and optionally claims inspection leases.
func (r *Repository) ListContainerIntents(
	ctx context.Context,
	req *jobsmodel.ContainerIntentCommand,
	claim bool,
) (
	out []*jobsmodel.ContainerIntent,
	resultErr error,
) {
	defer func() {
		if resultErr == nil {
			return
		}
		if _, ok := status.FromError(resultErr); !ok {
			resultErr = r.mapJobLeaseReadError(resultErr, "container intent listing")
		}
	}()
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "container intent command is required")
	}
	limit := req.BatchSize
	if limit <= 0 {
		limit = 100
	}
	limit = min(limit, 500)
	query := `
		SELECT ` + intentColumns + `
		FROM container_creation_intents
		ORDER BY
			quarantined DESC,
			next_inspection_at,
			id
		LIMIT $1`
	if claim {
		return r.claimContainerIntents(ctx, int(limit))
	}
	rows, err := r.pg.Query(ctx, query, limit)
	if err != nil {
		return nil, r.mapJobLeaseReadError(err, "list container intents")
	}
	defer rows.Close()
	result := []*jobsmodel.ContainerIntent{}
	for rows.Next() {
		i, scanErr := scanContainerIntent(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, i)
	}
	return result, rows.Err()
}

// Claims serialize only allocation; the turn and executable tokens share one commit.
//
//nolint:gocyclo // Keep the three allocations and their single atomic commit together.
func (r *Repository) claimContainerIntents(ctx context.Context, limit int) ([]*jobsmodel.ContainerIntent, error) {
	tx, err := r.pg.BeginTx(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // Commit owns the successful path.
	var urgent bool
	if err = tx.QueryRow(ctx, `SELECT next_urgent FROM container_discovery_turn WHERE id=TRUE FOR UPDATE`).Scan(&urgent); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, status.Error(codes.Internal, "container discovery turn is missing; initialize from current migrations")
		}
		return nil, err
	}
	first, err := claimContainerIntentLane(ctx, tx, urgent, (limit+1)/2, nil)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, limit)
	for _, intent := range first {
		ids = append(ids, intent.ID)
	}
	second, err := claimContainerIntentLane(ctx, tx, !urgent, limit-len(first), ids)
	if err != nil {
		return nil, err
	}
	for _, intent := range second {
		ids = append(ids, intent.ID)
	}
	if remaining := limit - len(ids); remaining > 0 {
		borrowed, err := claimContainerIntentLane(ctx, tx, urgent, remaining, ids)
		if err != nil {
			return nil, err
		}
		first = append(first, borrowed...)
	}
	result := make([]*jobsmodel.ContainerIntent, 0, len(first)+len(second))
	for i := 0; i < max(len(first), len(second)); i++ {
		if i < len(first) {
			result = append(result, first[i])
		}
		if i < len(second) {
			result = append(result, second[i])
		}
	}
	if len(result) > 0 {
		servedUrgent := urgent
		if len(first) == 0 {
			servedUrgent = !urgent
		}
		if _, err := tx.Exec(ctx, `UPDATE container_discovery_turn SET next_urgent=$1 WHERE id=TRUE`, !servedUrgent); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return result, nil
}

func claimContainerIntentLane(
	ctx context.Context,
	tx pgx.Tx,
	urgent bool,
	limit int,
	excluded []string,
) (
	[]*jobsmodel.ContainerIntent,
	error,
) {
	rows, err := tx.Query(ctx, `
		WITH
			due AS MATERIALIZED (
				SELECT
					id
				FROM
					container_creation_intents
				WHERE
					closed_at IS NOT NULL
					AND NOT quarantined
					AND inspection_urgent = $1
					AND next_inspection_at <= (statement_timestamp() AT TIME ZONE 'utc')
					AND (
						inspection_until IS NULL
						OR inspection_until <= (statement_timestamp() AT TIME ZONE 'utc')
					)
					AND NOT (id = ANY (COALESCE($3::uuid[], ARRAY[]::uuid[])))
				ORDER BY
					next_inspection_at,
					id
				LIMIT $2
				FOR UPDATE
					SKIP LOCKED
			),
			claimed AS (
				UPDATE container_creation_intents
				SET
					inspection_token = uuidv7(),
					inspection_until = (clock_timestamp() AT TIME ZONE 'utc') + interval '45 seconds'
				WHERE
					id = ANY (ARRAY(SELECT id FROM due))
				RETURNING `+intentColumns+`
			)
		SELECT
			*
		FROM
			claimed
		ORDER BY
			next_inspection_at,
			id
	`, urgent, limit, excluded)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []*jobsmodel.ContainerIntent{}
	for rows.Next() {
		intent, err := scanContainerIntent(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, intent)
	}
	return result, rows.Err()
}
