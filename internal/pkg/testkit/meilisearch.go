package testkit

import (
	"context"
	"fmt"

	"github.com/meilisearch/meilisearch-go"
	tcmeili "github.com/testcontainers/testcontainers-go/modules/meilisearch"

	meilisearchpkg "github.com/hitesh22rana/chronoverse/internal/pkg/meilisearch"
)

const (
	meilisearchImage = "getmeili/meilisearch:v1.45.2"
	meilisearchKey   = "chronoverse-test-master-key"
)

// startMeilisearch starts a Meilisearch container and configures every index the
// application uses (searchable, filterable and sortable attributes), reusing the
// production index setup.
func startMeilisearch(ctx context.Context, s *suite) (meilisearch.ServiceManager, error) {
	ctr, err := tcmeili.Run(ctx, meilisearchImage, tcmeili.WithMasterKey(meilisearchKey))
	if err != nil {
		return nil, fmt.Errorf("start meilisearch container: %w", err)
	}
	s.containers = append(s.containers, ctr)

	addr, err := ctr.Address(ctx)
	if err != nil {
		return nil, fmt.Errorf("meilisearch address: %w", err)
	}

	client, err := meilisearchpkg.New(ctx,
		meilisearchpkg.WithURI(addr),
		meilisearchpkg.WithMasterKey(meilisearchKey),
	)
	if err != nil {
		return nil, fmt.Errorf("create meilisearch client: %w", err)
	}

	if err := meilisearchpkg.SetupIndexes(ctx, client); err != nil {
		return nil, fmt.Errorf("setup meilisearch indexes: %w", err)
	}

	return client, nil
}
