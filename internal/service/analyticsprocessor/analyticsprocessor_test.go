package analyticsprocessor_test

import (
	"testing"
	"time"

	"go.uber.org/mock/gomock"

	"github.com/hitesh22rana/chronoverse/internal/service/analyticsprocessor"
	analyticsprocessormock "github.com/hitesh22rana/chronoverse/internal/service/analyticsprocessor/mock"
)

func TestCleanupProcessedEvents(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockRepo := analyticsprocessormock.NewMockRepository(ctrl)
	s := analyticsprocessor.New(mockRepo)

	retention := 336 * time.Hour
	mockRepo.EXPECT().CleanupProcessedEvents(gomock.Any(), retention, 1000).Return(int64(3), nil)

	total, err := s.CleanupProcessedEvents(t.Context(), retention, 1000)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if total != 3 {
		t.Fatalf("expected 3 rows cleaned up, got %d", total)
	}
}
