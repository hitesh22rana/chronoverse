package executor_test

import (
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/hitesh22rana/chronoverse/internal/app/executor"
	executormock "github.com/hitesh22rana/chronoverse/internal/app/executor/mock"
)

func TestMain(t *testing.T) {
	ctrl := gomock.NewController(t)

	svc := executormock.NewMockService(ctrl)
	server := executor.New(t.Context(), svc)

	_ = server
}
