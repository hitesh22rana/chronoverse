package workflows_test

import (
	"testing"

	"github.com/go-playground/validator/v10"
	"go.uber.org/mock/gomock"

	"github.com/hitesh22rana/chronoverse/internal/service/workflows"
	workflowsmock "github.com/hitesh22rana/chronoverse/internal/service/workflows/mock"
)

func TestCleanupWorkflowIdempotencyKeys(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	repo := workflowsmock.NewMockRepository(ctrl)
	cache := workflowsmock.NewMockCache(ctrl)
	s := workflows.New(validator.New(), repo, cache)

	repo.EXPECT().CleanupWorkflowIdempotencyKeys(gomock.Any(), 1000).Return(int64(2), nil)

	total, err := s.CleanupWorkflowIdempotencyKeys(t.Context(), 1000)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if total != 2 {
		t.Fatalf("expected 2 rows cleaned up, got %d", total)
	}
}
