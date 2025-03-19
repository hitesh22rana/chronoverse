package workflow_test

import (
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/hitesh22rana/chronoverse/internal/app/workflow"
	workflowmock "github.com/hitesh22rana/chronoverse/internal/app/workflow/mock"
)

func TestMain(t *testing.T) {
	ctrl := gomock.NewController(t)

	svc := workflowmock.NewMockService(ctrl)
	server := workflow.New(t.Context(), svc)

	_ = server
}
