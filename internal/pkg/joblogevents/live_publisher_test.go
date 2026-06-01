//nolint:testpackage // Tests bounded queue internals without widening the public API.
package joblogevents

import (
	"testing"

	jobsmodel "github.com/hitesh22rana/chronoverse/internal/model/jobs"
)

func TestLivePublisherTryPublishIsBounded(t *testing.T) {
	t.Parallel()

	publisher := &LivePublisher{
		events: make(chan *jobsmodel.JobLogEvent, 1),
	}
	event := &jobsmodel.JobLogEvent{
		JobID:     "job-1",
		Retention: true,
	}

	if !publisher.TryPublish(event) {
		t.Fatal("TryPublish() first event = false, want true")
	}
	if event.LivePublished {
		t.Fatal("TryPublish() mutated original event LivePublished = true, want false")
	}
	queued := <-publisher.events
	if !queued.LivePublished {
		t.Fatal("queued event LivePublished = false, want true")
	}
	publisher.events <- queued

	if publisher.TryPublish(event) {
		t.Fatal("TryPublish() with full queue = true, want false")
	}
}

func TestLivePublisherTryPublishSkipsUnretainedLogs(t *testing.T) {
	t.Parallel()

	publisher := &LivePublisher{
		events: make(chan *jobsmodel.JobLogEvent, 1),
	}

	if publisher.TryPublish(&jobsmodel.JobLogEvent{JobID: "job-1"}) {
		t.Fatal("TryPublish() unretained event = true, want false")
	}
}
