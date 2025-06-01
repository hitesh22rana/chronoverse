package databasemigration_test

import (
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/hitesh22rana/chronoverse/internal/app/databasemigration"
	databasemigrationmock "github.com/hitesh22rana/chronoverse/internal/app/databasemigration/mock"
)

func TestMain(t *testing.T) {
	ctrl := gomock.NewController(t)

	svc := databasemigrationmock.NewMockService(ctrl)
	app := databasemigration.New(t.Context(), svc)

	_ = app
}
