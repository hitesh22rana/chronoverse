package testkit

import (
	"context"
	"fmt"

	"github.com/meilisearch/meilisearch-go"
	tcmeili "github.com/testcontainers/testcontainers-go/modules/meilisearch"

	meilisearchpkg "github.com/hitesh22rana/chronoverse/internal/pkg/meilisearch"
)

const (
	meilisearchImage = "getmeili/meilisearch:v1.55.0"
	meilisearchKey   = "chronoverse-test-master-key"
)

// startMeilisearch starts a Meilisearch container and configures every index the
// application uses (searchable, filterable and sortable attributes), reusing the
// production index setup.
func startMeilisearch(ctx context.Context, s *suite) (meilisearch.ServiceManager, error) {
	return startMeilisearchWith(ctx, s, meilisearchStartup{
		startContainer: func(ctx context.Context) (*tcmeili.MeilisearchContainer, error) {
			return tcmeili.Run(ctx, meilisearchImage, tcmeili.WithMasterKey(meilisearchKey))
		},
		setupIndexes: meilisearchpkg.SetupIndexes,
	})
}

type meilisearchStartup struct {
	startContainer func(context.Context) (*tcmeili.MeilisearchContainer, error)
	setupIndexes   func(context.Context, meilisearch.ServiceManager) error
}

func startMeilisearchWith(ctx context.Context, s *suite, startup meilisearchStartup) (meilisearch.ServiceManager, error) {
	ctr, err := startup.startContainer(ctx)
	if ctr != nil {
		s.containers = append(s.containers, ctr)
	}
	if err != nil {
		return nil, fmt.Errorf("start meilisearch container: %w", err)
	}

	addr, err := ctr.Address(ctx)
	if err != nil {
		return nil, fmt.Errorf("meilisearch address: %w", err)
	}

	//nolint:errcheck // Address returns a nonempty HTTP endpoint; the key is fixed and TLS is disabled.
	client, _ := meilisearchpkg.New(ctx, &meilisearchpkg.Config{URI: addr, MasterKey: meilisearchKey})

	if err := startup.setupIndexes(ctx, client); err != nil {
		client.Close()
		return nil, fmt.Errorf("setup meilisearch indexes: %w", err)
	}

	s.meiliURI = addr
	return client, nil
}
