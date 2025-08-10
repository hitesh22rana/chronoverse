package analyticsprocessor_test

import (
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/hitesh22rana/chronoverse/internal/app/analyticsprocessor"
	analyticsprocessormock "github.com/hitesh22rana/chronoverse/internal/app/analyticsprocessor/mock"
)

func TestMain(t *testing.T) {
	ctrl := gomock.NewController(t)

	svc := analyticsprocessormock.NewMockService(ctrl)
	app := analyticsprocessor.New(t.Context(), svc)

	_ = app
}
