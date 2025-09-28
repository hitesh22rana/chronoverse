package databasemigration_test

import (
	"errors"
	"testing"

	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/service/databasemigration"
	databasemigrationmock "github.com/hitesh22rana/chronoverse/internal/service/databasemigration/mock"
)

func TestRun(t *testing.T) {
	ctrl := gomock.NewController(t)

	// Create a mock repository
	mockRepo := databasemigrationmock.NewMockRepository(ctrl)

	// Create a new service
	s := databasemigration.New(mockRepo)

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
				mockRepo.EXPECT().MigratePostgres(
					gomock.Any(),
				).Return(nil)
				mockRepo.EXPECT().MigrateClickHouse(
					gomock.Any(),
				).Return(nil)
				mockRepo.EXPECT().SetupMeiliSearch(
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
				mockRepo.EXPECT().MigratePostgres(
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
			if !errors.Is(err, tt.want.err) {
				t.Errorf("Run() error = %v, want %v", err, tt.want.err)
			}
		})
	}
}
