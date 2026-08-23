package executor

import (
	"context"
	"sync"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"
)

type handoffState uint8

const (
	handoffClaiming handoffState = iota
	handoffActive
	handoffAwaitingReconciliation
)

// handoffEntry owns exactly one registry permit. Waiters only observe it and
// never release resources owned by its placeholder caller.
type handoffEntry struct {
	id      string
	state   handoffState
	request *jobspb.ClaimJobRequest
	claim   *jobspb.ClaimJobResponse
	done    chan struct{}
	result  error
}

type handoffRegistry struct {
	mu      sync.Mutex
	limit   int
	entries map[string]*handoffEntry
}

func newHandoffRegistry(limit int) *handoffRegistry {
	return &handoffRegistry{limit: limit, entries: make(map[string]*handoffEntry, limit)}
}

// getOrReserve atomically checks identity, capacity, and installs the claiming
// placeholder. There is no separate capacity acquisition race.
func (g *handoffRegistry) getOrReserve(id string, request *jobspb.ClaimJobRequest) (*handoffEntry, bool, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if entry, ok := g.entries[id]; ok {
		return entry, false, nil
	}
	if len(g.entries) >= g.limit {
		return nil, false, status.Error(codes.ResourceExhausted, "execution handoff reconciliation capacity is full")
	}

	clonedRequest := &jobspb.ClaimJobRequest{}
	proto.Merge(clonedRequest, request)
	entry := &handoffEntry{
		id:      id,
		state:   handoffClaiming,
		request: clonedRequest,
		done:    make(chan struct{}),
	}
	g.entries[id] = entry
	return entry, true, nil
}

func (g *handoffRegistry) resolveRemoved(entry *handoffEntry, result error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	current, ok := g.entries[entry.id]
	if !ok || current != entry || current.state != handoffClaiming {
		return
	}
	entry.result = result
	delete(g.entries, entry.id)
	close(entry.done)
}

func (g *handoffRegistry) activate(entry *handoffEntry, claim *jobspb.ClaimJobResponse) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	current, ok := g.entries[entry.id]
	if !ok || current != entry || current.state != handoffClaiming {
		return false
	}
	entry.claim = &jobspb.ClaimJobResponse{}
	proto.Merge(entry.claim, claim)
	entry.state = handoffActive
	close(entry.done)
	return true
}

func (g *handoffRegistry) wait(ctx context.Context, entry *handoffEntry) error {
	select {
	case <-entry.done:
		return entry.result
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (g *handoffRegistry) markAwaiting(id string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if entry := g.entries[id]; entry != nil && entry.state == handoffActive {
		entry.state = handoffAwaitingReconciliation
	}
}

func (g *handoffRegistry) consume(jobID, leaseToken string) {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	for id, entry := range g.entries {
		if entry.claim != nil && entry.claim.GetId() == jobID && entry.claim.GetLeaseToken() == leaseToken {
			delete(g.entries, id)
			return
		}
	}
}

func (g *handoffRegistry) awaiting() []*handoffEntry {
	g.mu.Lock()
	defer g.mu.Unlock()
	entries := make([]*handoffEntry, 0, len(g.entries))
	for _, entry := range g.entries {
		if entry.state == handoffAwaitingReconciliation {
			entries = append(entries, entry)
		}
	}
	return entries
}

func (g *handoffRegistry) removeAwaiting(entry *handoffEntry) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if current := g.entries[entry.id]; current == entry && current.state == handoffAwaitingReconciliation {
		delete(g.entries, entry.id)
	}
}

func (g *handoffRegistry) size() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.entries)
}
