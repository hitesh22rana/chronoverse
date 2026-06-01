package joblogevents

import (
	"context"
	"time"

	"go.uber.org/zap"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
	"github.com/hitesh22rana/chronoverse/internal/pkg/redis"
)

const (
	// DefaultLiveBufferSize is the default number of best-effort live log events
	// allowed to queue locally before new live events are dropped.
	DefaultLiveBufferSize = 4096

	// DefaultLivePublishTimeout bounds each Redis live publish attempt.
	DefaultLivePublishTimeout = 100 * time.Millisecond
)

// LivePublisherConfig configures a bounded best-effort live log publisher.
type LivePublisherConfig struct {
	BufferSize     int
	PublishTimeout time.Duration
}

// LivePublisher publishes job log events to Redis without blocking the durable Kafka path.
type LivePublisher struct {
	rdb            *redis.Store
	events         chan *jobsmodel.JobLogEvent
	publishTimeout time.Duration
}

// NewLivePublisher creates a bounded live log publisher.
func NewLivePublisher(rdb *redis.Store, cfg LivePublisherConfig) *LivePublisher {
	if rdb == nil {
		return nil
	}

	if cfg.BufferSize <= 0 {
		cfg.BufferSize = DefaultLiveBufferSize
	}
	if cfg.PublishTimeout <= 0 {
		cfg.PublishTimeout = DefaultLivePublishTimeout
	}

	return &LivePublisher{
		rdb:            rdb,
		events:         make(chan *jobsmodel.JobLogEvent, cfg.BufferSize),
		publishTimeout: cfg.PublishTimeout,
	}
}

// TryPublish queues a retained log event for best-effort Redis live publishing.
func (p *LivePublisher) TryPublish(event *jobsmodel.JobLogEvent) bool {
	if p == nil || event == nil || !event.Retention {
		return false
	}

	liveEvent := *event
	liveEvent.LivePublished = true

	select {
	case p.events <- &liveEvent:
		return true
	default:
		return false
	}
}

// Run publishes queued live events until the context is canceled.
func (p *LivePublisher) Run(ctx context.Context, logger *zap.Logger) {
	if p == nil {
		return
	}
	if logger == nil {
		logger = zap.NewNop()
	}

	for {
		select {
		case <-ctx.Done():
			return
		case event := <-p.events:
			p.publish(ctx, logger, event)
		}
	}
}

func (p *LivePublisher) publish(parentCtx context.Context, logger *zap.Logger, event *jobsmodel.JobLogEvent) {
	if event == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.WithoutCancel(parentCtx), p.publishTimeout)
	defer cancel()

	if _, err := PublishLive(ctx, p.rdb, event); err != nil {
		logger.Warn("failed to publish live job log",
			zap.String("job_id", event.JobID),
			zap.String("workflow_id", event.WorkflowID),
			zap.String("event_key", event.EventKey),
			zap.String("stream", event.Stream),
			zap.Uint32("sequence_num", event.SequenceNum),
			zap.Error(err),
		)
	}
}
