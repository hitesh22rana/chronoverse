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

func TestIsAllowedContainerRegistry(t *testing.T) {
	t.Parallel()

	allowed := []string{
		"docker.io", "ghcr.io", "public.ecr.aws",
		"gcr.io", "us.gcr.io", "eu.gcr.io", "asia.gcr.io",
		"mcr.microsoft.com", "quay.io", "registry.k8s.io",
		"us-docker.pkg.dev", "europe-west1-docker.pkg.dev",
		"myregistry.azurecr.io",
		"docker.io:443", "ghcr.io:443", "myregistry.azurecr.io:443",
		"us-docker.pkg.dev:443",
	}
	for _, host := range allowed {
		if !isAllowedContainerRegistry(host) {
			t.Errorf("isAllowedContainerRegistry(%q) = false, want true", host)
		}
	}

	// Host matching is exact: subdomains, suffix tricks, and multi-label
	// cloud patterns must not pass.
	rejected := []string{
		"", "evil.com", "docker.io.evil.com", "evil-docker.io",
		"localhost:5000", "my.azurecr.io.evil.com",
		"evil-docker.pkg.dev.evil.com", "a.b-docker.pkg.dev",
		"a.b.azurecr.io", ".azurecr.io",
		"evil.com:443", "docker.io:443.evil.com", "docker.io:http",
	}
	for _, host := range rejected {
		if isAllowedContainerRegistry(host) {
			t.Errorf("isAllowedContainerRegistry(%q) = true, want false", host)
		}
	}
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
