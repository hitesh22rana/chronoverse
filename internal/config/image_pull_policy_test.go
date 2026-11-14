package config_test

import (
	"strings"
	"testing"

	"github.com/hitesh22rana/chronoverse/internal/config"
)

func TestWorkersRejectInvalidImagePullTTLAtStartup(t *testing.T) {
	setExecutionWorkerRequiredEnvironment(t)
	t.Setenv("MEILISEARCH_MASTER_KEY", "test-key")
	for _, worker := range []struct {
		prefix string
		init   func() error
	}{
		{"WORKFLOW_WORKER", func() error { _, err := config.InitWorkflowWorkerConfig(); return err }},
		{"EXECUTION_WORKER", func() error { _, err := config.InitExecutionJobConfig(); return err }},
	} {
		t.Run(worker.prefix, func(t *testing.T) {
			for _, ttl := range []string{"-1s", "1ns", "2ms", "150ms", "0s"} {
				t.Run(ttl, func(t *testing.T) {
					t.Setenv(worker.prefix+"_IMAGE_PULL_LOCK_TTL", ttl)
					err := worker.init()
					if ttl == "150ms" || ttl == "0s" {
						if err != nil {
							t.Fatal(err)
						}
						return
					}
					if err == nil || !strings.Contains(err.Error(), worker.prefix+"_IMAGE_PULL_LOCK_TTL") {
						t.Fatalf("invalid startup TTL %s = %v", ttl, err)
					}
				})
			}
		})
	}
}
