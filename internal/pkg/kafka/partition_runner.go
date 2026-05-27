package kafka

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	otelcodes "go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	defaultPartitionRunnerBackoff       = time.Second
	defaultPartitionRunerMaxBackoff     = 30 * time.Second
	defaultPartitionRunnerChannelBuffer = 1024
)

// RecordProcessor processes one Kafka record. Retruning a retryable gRPC status
// keeps the record uncommitted and retries it in the same partition lane.
type RecordProcessor func(context.Context, *kgo.Record) error

// RecordBatchProcessor processes a contiguous batch from one Kafka partition.
// Returning a retryable gRPC status keeps the batch uncommitted and retries it in the same partition lane.
type RecordBatchProcessor func(context.Context, []*kgo.Record) error

type partitionClient interface {
	PollFetches(context.Context) kgo.Fetches
	CommitRecords(context.Context, ...*kgo.Record) error
	PauseFetchPartitions(map[string][]int32) map[string][]int32
	ResumeFetchPartitions(map[string][]int32)
}

// PartitionLifecycle forwards Kafka group partition lifecycle callbacks to the active partition runner for a client.
type PartitionLifecycle struct {
	mu     sync.RWMutex
	runner *PartitionRunner
}

// NewPartitionLifecycle creates a new PartitionLifecycle.
func NewPartitionLifecycle() *PartitionLifecycle {
	return &PartitionLifecycle{}
}

// Register attaches a runner to the lifecycle callbacks.
func (p *PartitionLifecycle) Register(runner *PartitionRunner) {
	if p == nil {
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	p.runner = runner
}

// OnAssigned records partitions assigned to this consumer group member.
func (p *PartitionLifecycle) OnAssigned(_ context.Context, _ *kgo.Client, partitions map[string][]int32) {
	if runner := p.activeRunner(); runner != nil {
		runner.assign(partitions)
	}
}

// OnRevoked stops lanes for partitions revoked from this consumer group member.
func (p *PartitionLifecycle) OnRevoked(_ context.Context, _ *kgo.Client, partitions map[string][]int32) {
	if runner := p.activeRunner(); runner != nil {
		runner.revoke(partitions)
	}
}

// OnLost stops lanes for partitions lost by this consumer group member.
func (p *PartitionLifecycle) OnLost(_ context.Context, _ *kgo.Client, partitions map[string][]int32) {
	if runner := p.activeRunner(); runner != nil {
		runner.revoke(partitions)
	}
}

func (p *PartitionLifecycle) activeRunner() *PartitionRunner {
	if p == nil {
		return nil
	}

	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.runner
}

// PartitionRunnerConfig configures a partition-aware Kafka processor.
type PartitionRunnerConfig struct {
	Name          string
	RetryBackoff  time.Duration
	MaxBackoff    time.Duration
	ChannelBuffer int
	BatchSize     int
	BatchInterval time.Duration
	Logger        *zap.Logger
	Tracer        trace.Tracer
}

type partitionKey struct {
	topic     string
	partition int32
}

func (k partitionKey) String() string {
	return fmt.Sprintf("%s:%d", k.topic, k.partition)
}

type partitionLane struct {
	key           partitionKey
	done          <-chan struct{}
	cancel        context.CancelFunc
	notify        chan struct{}
	mu            sync.Mutex
	queue         []*kgo.Record
	backlogPaused bool
	retryPaused   bool
}

// PartitionRunner processes records sequentially per topic partition and concurrently across partitions.
type PartitionRunner struct {
	client       partitionClient
	process      RecordProcessor
	processBatch RecordBatchProcessor

	name          string
	retryBackoff  time.Duration
	maxBackoff    time.Duration
	channelBuffer int
	batchSize     int
	batchInterval time.Duration
	logger        *zap.Logger
	tp            trace.Tracer

	mu            sync.RWMutex
	assigned      map[partitionKey]struct{}
	assignedKnown bool
	lanes         map[partitionKey]*partitionLane
	wg            sync.WaitGroup
}

// NewPartitionRunner creates a new partition-aware runner.
func NewPartitionRunner(
	client partitionClient,
	process RecordProcessor,
	cfg *PartitionRunnerConfig,
	lifecycle *PartitionLifecycle,
) *PartitionRunner {
	if cfg == nil {
		cfg = &PartitionRunnerConfig{}
	}

	name := cfg.Name
	if name == "" {
		name = "kafka.partition-runner"
	}
	backoff := cfg.RetryBackoff
	if backoff <= 0 {
		backoff = defaultPartitionRunnerBackoff
	}
	maxBackoff := cfg.MaxBackoff
	if maxBackoff <= 0 {
		maxBackoff = defaultPartitionRunerMaxBackoff
	}
	channelBuffer := cfg.ChannelBuffer
	if channelBuffer <= 0 {
		channelBuffer = defaultPartitionRunnerChannelBuffer
	}
	logger := cfg.Logger
	if logger == nil {
		logger = zap.NewNop()
	}
	tp := cfg.Tracer
	if tp == nil {
		tp = otel.Tracer(name)
	}
	batchSize := cfg.BatchSize
	if batchSize <= 0 {
		batchSize = 1
	}

	runner := &PartitionRunner{
		client:        client,
		process:       process,
		name:          name,
		retryBackoff:  backoff,
		maxBackoff:    maxBackoff,
		channelBuffer: channelBuffer,
		batchSize:     batchSize,
		batchInterval: cfg.BatchInterval,
		logger:        logger,
		tp:            tp,
		assigned:      make(map[partitionKey]struct{}),
		lanes:         make(map[partitionKey]*partitionLane),
	}
	lifecycle.Register(runner)

	return runner
}

// NewPartitionBatchRunner creates a partition-aware runner for partition-local bacthes.
func NewPartitionBatchRunner(
	client partitionClient,
	processBatch RecordBatchProcessor,
	cfg *PartitionRunnerConfig,
	lifecycle *PartitionLifecycle,
) *PartitionRunner {
	runner := NewPartitionRunner(client, nil, cfg, lifecycle)
	runner.processBatch = processBatch

	return runner
}

// SetLogger updates the logger used by the runner.
func (r *PartitionRunner) SetLogger(logger *zap.Logger) {
	if logger == nil {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.logger = logger
}

// Run polls Kafka and dispatches records to partition lanes.
func (r *PartitionRunner) Run(ctx context.Context) error {
	defer r.stopAll()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		fetches := r.client.PollFetches(ctx)
		if fetches.IsClientClosed() {
			r.logger.Warn(
				"kafka client closed, shutting down partition runner",
				zap.String("runner", r.name),
			)
			return nil
		}

		for _, fetchErr := range fetches.Errors() {
			r.logger.Error(
				"error while fetching records",
				zap.String("runner", r.name),
				zap.String("topic", fetchErr.Topic),
				zap.Int32("partition", fetchErr.Partition),
				zap.Error(fetchErr.Err),
			)
		}

		if fetches.Empty() {
			continue
		}

		var dispatchErr error
		fetches.EachPartition(func(partition kgo.FetchTopicPartition) {
			if dispatchErr != nil {
				return
			}

			if len(partition.Records) == 0 {
				return
			}

			if err := r.dispatchPartition(
				ctx,
				partition.Topic,
				partition.Partition,
				partition.Records,
			); err != nil {
				dispatchErr = err
				return
			}
		})

		if dispatchErr != nil {
			return dispatchErr
		}
	}
}

func (r *PartitionRunner) dispatchPartition(
	ctx context.Context,
	topic string,
	partition int32,
	records []*kgo.Record,
) error {
	key := partitionKey{
		topic:     topic,
		partition: partition,
	}

	lane := r.laneFor(ctx, key)
	if lane == nil {
		r.logger.Warn(
			"skipping records from unassigned partition",
			zap.String("runner", r.name),
			zap.String("topic", topic),
			zap.Int32("partition", partition),
			zap.Int("records", len(records)))
		return nil
	}

	queued := lane.enqueue(records)
	if queued >= r.channelBuffer {
		r.setPaused(lane, pauseReasonBacklog, true)
	}

	select {
	case <-lane.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

func (r *PartitionRunner) laneFor(ctx context.Context, key partitionKey) *partitionLane {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.assigned[key]; r.assignedKnown && !ok {
		return nil
	}

	if lane, ok := r.lanes[key]; ok {
		return lane
	}

	laneCtx, cancel := context.WithCancel(ctx)
	lane := &partitionLane{
		key:    key,
		done:   laneCtx.Done(),
		cancel: cancel,
		notify: make(chan struct{}, 1),
		queue:  make([]*kgo.Record, 0),
	}
	r.lanes[key] = lane

	r.wg.Go(func() {
		r.runLane(laneCtx, lane)
	})

	return lane
}

func (l *partitionLane) enqueue(records []*kgo.Record) int {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.queue = append(l.queue, records...)
	select {
	case l.notify <- struct{}{}:
	default:
	}

	return len(l.queue)
}

func (l *partitionLane) pop(ctx context.Context) (*kgo.Record, error) {
	for {
		l.mu.Lock()
		if len(l.queue) > 0 {
			record := l.queue[0]
			l.queue[0] = nil
			l.queue = l.queue[1:]
			l.mu.Unlock()
			return record, nil
		}
		notify := l.notify
		l.mu.Unlock()

		select {
		case <-notify:
		case <-ctx.Done():
			return nil, context.Canceled
		}
	}
}

func (l *partitionLane) popBatch(
	ctx context.Context,
	maxRecords int,
	maxWait time.Duration,
) ([]*kgo.Record, error) {
	record, err := l.pop(ctx)
	if err != nil {
		return nil, err
	}

	records := []*kgo.Record{record}
	if maxRecords <= 1 {
		return records, nil
	}

	if maxWait <= 0 {
		records = append(records, l.drain(maxRecords-len(records))...)
		return records, nil
	}

	timer := time.NewTimer(maxWait)
	defer timer.Stop()

	for len(records) < maxRecords {
		if drained := l.drain(maxRecords - len(records)); len(drained) > 0 {
			records = append(records, drained...)
			continue
		}

		select {
		case <-l.notify:
		case <-timer.C:
			return records, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	return records, nil
}

func (l *partitionLane) drain(limit int) []*kgo.Record {
	if limit <= 0 {
		return nil
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	if len(l.queue) == 0 {
		return nil
	}
	if len(l.queue) < limit {
		limit = len(l.queue)
	}

	records := make([]*kgo.Record, limit)
	copy(records, l.queue[:limit])
	clear(l.queue[:limit])
	l.queue = l.queue[limit:]

	return records
}

func (l *partitionLane) queueLen() int {
	l.mu.Lock()
	defer l.mu.Unlock()

	return len(l.queue)
}

func (r *PartitionRunner) runLane(ctx context.Context, lane *partitionLane) {
	for {
		if r.processBatch != nil {
			records, err := lane.popBatch(ctx, r.batchSize, r.batchInterval)
			if err != nil {
				return
			}
			r.resumeBacklogIfReady(lane)
			if len(records) == 0 {
				continue
			}
			if err := r.processAndCommitBatch(ctx, lane, records); err != nil {
				r.logger.Warn(
					"partition lane stopped",
					zap.String("runner", r.name),
					zap.String("partition", lane.key.String()),
					zap.Error(err),
				)
				return
			}
			continue
		}

		record, err := lane.pop(ctx)
		if err != nil {
			return
		}
		r.resumeBacklogIfReady(lane)
		if record == nil {
			return
		}
		if err := r.processAndCommit(ctx, lane, record); err != nil {
			r.logger.Warn(
				"partition lane stopped",
				zap.String("runner", r.name),
				zap.String("partition", lane.key.String()),
				zap.Error(err),
			)
			return
		}
	}
}

func (r *PartitionRunner) processAndCommit(
	ctx context.Context,
	lane *partitionLane,
	record *kgo.Record,
) error {
	ctxWithTrace, span := r.tp.Start(
		ctx,
		r.name+".record",
		trace.WithAttributes(
			attribute.String("messaging.system", "kafka"),
			attribute.String("runner", r.name),
			attribute.String("partition_lane", lane.key.String()),
			attribute.String("topic", record.Topic),
			attribute.Int64("offset", record.Offset),
			attribute.Int64("partition", int64(record.Partition)),
			attribute.String("key", string(record.Key)),
		),
	)
	defer span.End()

	backoff := r.retryBackoff
	paused := false
	attempts := 0
	defer func() {
		if paused {
			r.setPaused(lane, pauseReasonRetry, false)
		}
	}()

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		attempts++
		span.SetAttributes(attribute.Int("attempts", attempts))

		processErr := r.process(ctxWithTrace, record)
		if processErr != nil && !ShouldCommitOnError(processErr) {
			span.RecordError(processErr)
			span.SetAttributes(
				attribute.Bool("retryable_error", true),
				attribute.String("error_code", status.Code(processErr).String()),
			)
			if !paused {
				r.setPaused(lane, pauseReasonRetry, true)
				paused = true
			}
			r.logger.Warn(
				"retryable record processing error, retrying in partition lane",
				zap.String("runner", r.name),
				zap.String("topic", record.Topic),
				zap.Int32("partition", record.Partition),
				zap.Int64("offset", record.Offset),
				zap.Error(processErr),
			)
			if waitErr := wait(ctxWithTrace, backoff); waitErr != nil {
				return waitErr
			}
			backoff = nextBackoff(backoff, r.maxBackoff)
			continue
		}
		if processErr != nil {
			span.RecordError(processErr)
			span.SetAttributes(
				attribute.Bool("retryable_error", false),
				attribute.String("error_code", status.Code(processErr).String()),
			)
		}

		if err := r.commit(ctxWithTrace, lane, []*kgo.Record{record}); err != nil {
			span.RecordError(err)
			span.SetStatus(otelcodes.Error, err.Error())
			return err
		}

		if paused {
			r.setPaused(lane, pauseReasonRetry, false)
			paused = false
		}
		if processErr == nil {
			span.SetStatus(otelcodes.Ok, "record processed and committed")
		}
		return nil
	}
}

func (r *PartitionRunner) processAndCommitBatch(
	ctx context.Context,
	lane *partitionLane,
	records []*kgo.Record,
) error {
	if len(records) == 0 {
		return nil
	}

	first := records[0]
	last := records[len(records)-1]
	ctxWithTrace, span := r.tp.Start(
		ctx,
		r.name+".batch",
		trace.WithAttributes(
			attribute.String("messaging.system", "kafka"),
			attribute.String("runner", r.name),
			attribute.String("partition_lane", lane.key.String()),
			attribute.String("topic", first.Topic),
			attribute.Int64("first_offset", first.Offset),
			attribute.Int64("last_offset", last.Offset),
			attribute.Int64("partition", int64(first.Partition)),
			attribute.Int("record_count", len(records)),
		),
	)
	defer span.End()

	backoff := r.retryBackoff
	paused := false
	attempts := 0
	defer func() {
		if paused {
			r.setPaused(lane, pauseReasonRetry, false)
		}
	}()

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		attempts++
		span.SetAttributes(attribute.Int("attempts", attempts))

		processErr := r.processBatch(ctxWithTrace, records)
		if processErr != nil && !ShouldCommitOnError(processErr) {
			span.RecordError(processErr)
			span.SetAttributes(
				attribute.Bool("retryable_error", true),
				attribute.String("error_code", status.Code(processErr).String()),
			)
			if !paused {
				r.setPaused(lane, pauseReasonRetry, true)
				paused = true
			}
			r.logger.Warn(
				"retryable batch processing error, retrying in partition lane",
				zap.String("runner", r.name),
				zap.String("topic", first.Topic),
				zap.Int32("partition", first.Partition),
				zap.Int64("first_offset", first.Offset),
				zap.Int64("last_offset", last.Offset),
				zap.Int("records", len(records)),
				zap.Error(processErr),
			)
			if waitErr := wait(ctxWithTrace, backoff); waitErr != nil {
				return waitErr
			}
			backoff = nextBackoff(backoff, r.maxBackoff)
			continue
		}
		if processErr != nil {
			span.RecordError(processErr)
			span.SetAttributes(
				attribute.Bool("retryable_error", false),
				attribute.String("error_code", status.Code(processErr).String()),
			)
		}

		if err := r.commit(ctxWithTrace, lane, records); err != nil {
			span.RecordError(err)
			span.SetStatus(otelcodes.Error, err.Error())
			return err
		}

		if paused {
			r.setPaused(lane, pauseReasonRetry, false)
			paused = false
		}
		if processErr == nil {
			span.SetStatus(otelcodes.Ok, "batch processed and committed")
		}
		return nil
	}
}

func (r *PartitionRunner) commit(
	ctx context.Context,
	lane *partitionLane,
	records []*kgo.Record,
) error {
	backoff := r.retryBackoff
	paused := false
	attempts := 0
	defer func() {
		if paused {
			r.setPaused(lane, pauseReasonRetry, false)
		}
	}()

	first := records[0]
	last := records[len(records)-1]
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !r.ownsLane(lane) {
			return status.Error(codes.Canceled, "partition lane no longer owns partition")
		}

		attempts++
		if err := r.client.CommitRecords(ctx, records...); err != nil {
			span := trace.SpanFromContext(ctx)
			span.RecordError(err)
			span.SetAttributes(attribute.Int("commit_attempt", attempts))
			if !paused {
				r.setPaused(lane, pauseReasonRetry, true)
				paused = true
			}
			r.logger.Warn(
				"failed to commit record, retrying commit",
				zap.String("runner", r.name),
				zap.String("topic", first.Topic),
				zap.Int32("partition", first.Partition),
				zap.Int64("first_offset", first.Offset),
				zap.Int64("last_offset", last.Offset),
				zap.Int("records", len(records)),
				zap.Error(err),
			)
			if waitErr := wait(ctx, backoff); waitErr != nil {
				return waitErr
			}
			backoff = nextBackoff(backoff, r.maxBackoff)
			continue
		}

		trace.SpanFromContext(ctx).SetAttributes(attribute.Int("commit_attempt", attempts))
		if paused {
			r.setPaused(lane, pauseReasonRetry, false)
			paused = false
		}
		return nil
	}
}

func (r *PartitionRunner) assign(partitions map[string][]int32) {
	resumePartitions := make(map[string][]int32)

	r.mu.Lock()
	for topic, topicPartitions := range partitions {
		for _, partition := range topicPartitions {
			key := partitionKey{topic: topic, partition: partition}
			if _, ok := r.lanes[key]; !ok {
				resumePartitions[topic] = append(resumePartitions[topic], partition)
			}
			r.assigned[key] = struct{}{}
		}
	}
	r.assignedKnown = true
	r.mu.Unlock()

	if len(resumePartitions) > 0 {
		r.client.ResumeFetchPartitions(resumePartitions)
	}
}

func (r *PartitionRunner) revoke(partitions map[string][]int32) {
	resumePartitions := make(map[string][]int32)

	r.mu.Lock()
	for topic, topicPartitions := range partitions {
		for _, partition := range topicPartitions {
			key := partitionKey{topic: topic, partition: partition}
			delete(r.assigned, key)
			if lane, ok := r.lanes[key]; ok {
				lane.cancel()
				delete(r.lanes, key)
			}
			resumePartitions[topic] = append(resumePartitions[topic], partition)
		}
	}
	r.mu.Unlock()

	if len(resumePartitions) > 0 {
		r.client.ResumeFetchPartitions(resumePartitions)
	}
}

func (r *PartitionRunner) ownsLane(lane *partitionLane) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if _, ok := r.assigned[lane.key]; r.assignedKnown && !ok {
		return false
	}

	return r.lanes[lane.key] == lane
}

type pauseReason uint8

const (
	pauseReasonBacklog pauseReason = iota
	pauseReasonRetry
)

func (r *PartitionRunner) resumeBacklogIfReady(lane *partitionLane) {
	if lane.queueLen() <= r.channelBuffer/2 {
		r.setPaused(lane, pauseReasonBacklog, false)
	}
}

func (r *PartitionRunner) setPaused(lane *partitionLane, reason pauseReason, paused bool) {
	lane.mu.Lock()
	wasPaused := lane.backlogPaused || lane.retryPaused
	switch reason {
	case pauseReasonBacklog:
		lane.backlogPaused = paused
	case pauseReasonRetry:
		lane.retryPaused = paused
	}
	isPaused := lane.backlogPaused || lane.retryPaused
	lane.mu.Unlock()

	if wasPaused == isPaused || !r.ownsLane(lane) {
		return
	}
	if isPaused {
		r.pause(lane.key)
		return
	}
	r.resume(lane.key)
}

func (r *PartitionRunner) pause(key partitionKey) {
	r.client.PauseFetchPartitions(map[string][]int32{key.topic: {key.partition}})
}

func (r *PartitionRunner) resume(key partitionKey) {
	r.client.ResumeFetchPartitions(map[string][]int32{key.topic: {key.partition}})
}

func (r *PartitionRunner) stopAll() {
	r.mu.Lock()
	for key, lane := range r.lanes {
		lane.cancel()
		delete(r.lanes, key)
	}
	r.mu.Unlock()

	r.wg.Wait()
}

func wait(ctx context.Context, duration time.Duration) error {
	if duration <= 0 {
		return nil
	}

	timer := time.NewTimer(duration)
	defer timer.Stop()

	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func nextBackoff(current, maxDuration time.Duration) time.Duration {
	if current <= 0 {
		return defaultPartitionRunnerBackoff
	}
	next := current * 2
	if maxDuration > 0 && next > maxDuration {
		return maxDuration
	}
	return next
}
