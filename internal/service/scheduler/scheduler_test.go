package scheduler_test

import (
	"errors"
	"testing"

	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/service/scheduler"
	schedulermock "github.com/hitesh22rana/chronoverse/internal/service/scheduler/mock"
)

func TestRun(t *testing.T) {
	ctrl := gomock.NewController(t)

	// Create a mock repository
	mockRepo := schedulermock.NewMockRepository(ctrl)

	// Create a new service
	s := scheduler.New(mockRepo)

	type want struct {
		total int
		err   error
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
				).Return(100, nil)
			},
			want: want{
				total: 100,
				err:   nil,
			},
		},
		{
			name: "error",
			mock: func() {
				mockRepo.EXPECT().Run(
					gomock.Any(),
				).Return(0, status.Error(codes.Internal, "internal error"))
			},
			want: want{
				total: 0,
				err:   status.Error(codes.Internal, "internal error"),
			},
		},
	}

	defer ctrl.Finish()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.mock()
			total, err := s.Run(t.Context())
			if err != nil && !errors.Is(err, tt.want.err) {
				t.Errorf("expected error %v, got %v", tt.want.err, err)
			}

			if total != tt.want.total {
				t.Errorf("expected total %d, got %d", tt.want.total, total)
			}
		})
	}
}
