package outboxrelay

import (
	"context"

	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// claimWorkflow serializes allocation only; the turn is released before broker I/O.
func (r *Repository) claimWorkflow(ctx context.Context, tx pgx.Tx, topic, token string) ([]*Event, error) {
	var firstReplay bool
	if err := tx.QueryRow(ctx, `SELECT next_replay FROM outbox_relay_turns WHERE topic=$1 FOR UPDATE`, topic).Scan(&firstReplay); err != nil {
		return nil, status.Errorf(codes.Internal, "read workflow publication turn: %v", err)
	}
	excluded := make([]string, 0, r.cfg.BatchSize)
	first, err := r.claimBatch(ctx, tx, topic, token, r.cfg.BatchSize/2+r.cfg.BatchSize%2, &firstReplay, excluded)
	if err != nil {
		return nil, err
	}
	for _, event := range first {
		excluded = append(excluded, event.ID)
	}
	secondReplay := !firstReplay
	second, err := r.claimBatch(ctx, tx, topic, token, r.cfg.BatchSize-len(first), &secondReplay, excluded)
	if err != nil {
		return nil, err
	}
	for _, event := range second {
		excluded = append(excluded, event.ID)
	}
	if remaining := r.cfg.BatchSize - len(excluded); remaining > 0 {
		borrowed, borrowErr := r.claimBatch(ctx, tx, topic, token, remaining, &firstReplay, excluded)
		if borrowErr != nil {
			return nil, borrowErr
		}
		first = append(first, borrowed...)
	}
	events := make([]*Event, 0, len(first)+len(second))
	// UPDATE RETURNING has no ordering contract. Alternate the explicitly sorted lanes.
	for i := 0; i < len(first) || i < len(second); i++ {
		if i < len(first) {
			events = append(events, first[i])
		}
		if i < len(second) {
			events = append(events, second[i])
		}
	}
	if len(events) > 0 {
		actualFirstReplay := events[0].ReplayRound > 0
		if _, err := tx.Exec(ctx, `UPDATE outbox_relay_turns SET next_replay=$2 WHERE topic=$1`, topic, !actualFirstReplay); err != nil {
			return nil, status.Errorf(codes.Internal, "advance workflow publication turn: %v", err)
		}
	}
	return events, nil
}
