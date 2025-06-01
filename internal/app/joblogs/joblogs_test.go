package joblogs_test

import (
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/hitesh22rana/chronoverse/internal/app/joblogs"
	joblogsmock "github.com/hitesh22rana/chronoverse/internal/app/joblogs/mock"
)

func TestMain(t *testing.T) {
	ctrl := gomock.NewController(t)

	svc := joblogsmock.NewMockService(ctrl)
	app := joblogs.New(t.Context(), svc)

	_ = app
}
