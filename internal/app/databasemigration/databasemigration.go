//go:generate mockgen -source=$GOFILE -package=$GOPACKAGE -destination=./mock/$GOFILE

package databasemigration

import (
	"context"

	"go.uber.org/zap"

	loggerpkg "github.com/hitesh22rana/chronoverse/internal/pkg/logger"
)

// Service provides data migration related operations.
type Service interface {
	Run(ctx context.Context) error
}

// DataMigration represents the data migration.
type DataMigration struct {
	logger *zap.Logger
	svc    Service
}

// New creates a new data migration.
func New(ctx context.Context, svc Service) *DataMigration {
	return &DataMigration{
		logger: loggerpkg.FromContext(ctx),
		svc:    svc,
	}
}

// Run starts the data migration.
func (dm *DataMigration) Run(ctx context.Context) error {
	err := dm.svc.Run(ctx)
	if err != nil {
		dm.logger.Error("error occurred while running the data migration job", zap.Error(err))
	} else {
		dm.logger.Info("successfully exited the data migration job")
	}

	return err
}
