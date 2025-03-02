package scheduler_test

import (
	"testing"
	"time"

	"go.uber.org/mock/gomock"

	"github.com/hitesh22rana/chronoverse/internal/app/scheduler"
	schedulermock "github.com/hitesh22rana/chronoverse/internal/app/scheduler/mock"
)

func TestMain(t *testing.T) {
	ctrl := gomock.NewController(t)

	svc := schedulermock.NewMockService(ctrl)
	server := scheduler.New(t.Context(), &scheduler.Config{
		PollInterval: time.Second * 10,
	}, svc)

	_ = server
}
