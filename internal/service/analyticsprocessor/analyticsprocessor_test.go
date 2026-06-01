package analyticsprocessor_test

import (
	"errors"
	"testing"
	"time"

	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/service/analyticsprocessor"
	analyticsprocessormock "github.com/hitesh22rana/chronoverse/internal/service/analyticsprocessor/mock"
)

func TestRun(t *testing.T) {
	ctrl := gomock.NewController(t)

	// Create a mock repository
	mockRepo := analyticsprocessormock.NewMockRepository(ctrl)

	// Create a new service
	s := analyticsprocessor.New(mockRepo)

	type want struct {
		err error
	}

	tests := []struct {
		name string
		mock func()
		want want
	}{
		{
			name: "success",
			mock: func() {
				mockRepo.EXPECT().Run(
					gomock.Any(),
				).Return(nil)
			},
			want: want{
				err: nil,
			},
		},
		{
			name: "error",
			mock: func() {
				mockRepo.EXPECT().Run(
					gomock.Any(),
				).Return(status.Error(codes.Internal, "internal error"))
			},
			want: want{
				err: status.Error(codes.Internal, "internal error"),
			},
		},
	}

	defer ctrl.Finish()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.mock()
			err := s.Run(t.Context())
			if err != nil && !errors.Is(err, tt.want.err) {
				t.Errorf("expected error %v, got %v", tt.want.err, err)
			}
		})
	}
}

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
