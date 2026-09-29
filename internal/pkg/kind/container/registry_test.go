//nolint:testpackage // Tests the unexported registry allowlist alongside the validator.
package container

import (
	"encoding/json"
	"testing"
)

func containerPayload(t *testing.T, image string) string {
	t.Helper()

	payload, err := json.Marshal(map[string]any{"image": image})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return string(payload)
}

func TestExtractAndValidateContainerDetailsRegistryGuard(t *testing.T) {
	t.Parallel()

	allowed := []string{
		"alpine:3.21",
		"docker.io/library/alpine:3.21",
		"docker.io:443/library/alpine:3.21",
		"ghcr.io/owner/image:tag",
		"public.ecr.aws/nginx/nginx:stable",
		"gcr.io/project/image",
		"us.gcr.io/project/image",
		"eu.gcr.io/project/image",
		"asia.gcr.io/project/image",
		"us-docker.pkg.dev/project/image:tag",
		"mcr.microsoft.com/dotnet/runtime:8.0",
		"MyReg.AzureCR.io/image:tag",
		"quay.io/prometheus/prometheus:v3.0.0",
		"registry.k8s.io/pause:3.10",
	}
	for _, image := range allowed {
		t.Run("allowed "+image, func(t *testing.T) {
			t.Parallel()

			details, err := ExtractAndValidateContainerDetails(containerPayload(t, image))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if details.Image != image {
				t.Fatalf("Image = %q, want %q", details.Image, image)
			}
		})
	}

	rejected := []string{
		"evil.com/image:tag",
		"docker.io.evil.com/image",
		"localhost:5000/image",
		"my.azurecr.io.evil.com/image",
		// Per-location and per-registry cloud hosts must be a single label, so a
		// multi-label host on an attacker-controlled domain is not an allowlist hit.
		"a.b-docker.pkg.dev/image",
		"a.b.azurecr.io/image",
		"not a reference %%%",
	}
	for _, image := range rejected {
		t.Run("rejected "+image, func(t *testing.T) {
			t.Parallel()

			if _, err := ExtractAndValidateContainerDetails(containerPayload(t, image)); err == nil {
				t.Fatalf("image %q accepted, want rejection", image)
			}
		})
	}
}
