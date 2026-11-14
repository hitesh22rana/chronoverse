package config_test

import (
	"strings"
	"testing"

	"github.com/hitesh22rana/chronoverse/internal/config"
)

func TestWorkersValidateImagePullDurationsAtStartup(t *testing.T) {
	clearExecutionWorkerEnvironment(t)
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
			for _, field := range []string{"TTL", "WAIT_TIMEOUT", "RETRY_INTERVAL"} {
				t.Setenv(worker.prefix+"_IMAGE_PULL_LOCK_"+field, "0s")
			}
			for _, tc := range []struct {
				field, value string
				valid        bool
			}{
				{"TTL", "-1s", false},
				{"TTL", "1ns", false},
				{"TTL", "2ms", false},
				{"TTL", "150ms", true},
				{"TTL", "0s", true},
				{"WAIT_TIMEOUT", "-1s", false},
				{"WAIT_TIMEOUT", "0s", true},
				{"WAIT_TIMEOUT", "2s", true},
				{"RETRY_INTERVAL", "-1s", false},
				{"RETRY_INTERVAL", "0s", true},
				{"RETRY_INTERVAL", "1ms", true},
			} {
				t.Run(tc.field+"/"+tc.value, func(t *testing.T) {
					key := worker.prefix + "_IMAGE_PULL_LOCK_" + tc.field
					t.Setenv(key, tc.value)
					err := worker.init()
					if tc.valid {
						if err != nil {
							t.Fatal(err)
						}
						return
					}
					if err == nil || !strings.Contains(err.Error(), key) || strings.Contains(err.Error(), "rpc error") {
						t.Fatalf("invalid startup setting %s = %v", key, err)
					}
				})
			}
		})
	}
}
