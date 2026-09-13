package container

import (
	"strings"

	"github.com/distribution/reference"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// allowedContainerRegistries are the exact registry hosts workflows may pull from. Pulls supply no
// credentials, so the allowlist also pins where public images may come from. Matching is exact, never substring.
var allowedContainerRegistries = map[string]struct{}{
	// Docker Hub / GitHub.
	"docker.io": {}, "ghcr.io": {},
	// Amazon ECR Public.
	"public.ecr.aws": {},
	// Google Container Registry (regional hosts).
	"gcr.io": {}, "us.gcr.io": {}, "eu.gcr.io": {}, "asia.gcr.io": {},
	// Microsoft (MCR + Azure; per-registry hosts match below).
	"mcr.microsoft.com": {},
	// Other public sources.
	"quay.io": {}, "registry.k8s.io": {},
}

// validateContainerImage rejects malformed references and off-allowlist registries.
func validateContainerImage(image string) error {
	named, err := reference.ParseNormalizedNamed(image)
	if err != nil {
		return status.Errorf(codes.InvalidArgument, "image reference is invalid: %v", err)
	}
	if !isAllowedContainerRegistry(reference.Domain(named)) {
		return status.Errorf(codes.InvalidArgument, "image registry %q is not allowed", reference.Domain(named))
	}
	return nil
}

// isAllowedContainerRegistry reports whether a registry host is allowed. Per-location Artifact Registry
// (<location>-docker.pkg.dev) and per-registry Azure (<registry>.azurecr.io) hosts match on a single non-empty label.
func isAllowedContainerRegistry(host string) bool {
	host = strings.ToLower(host)
	if _, ok := allowedContainerRegistries[host]; ok {
		return true
	}
	if prefix, ok := strings.CutSuffix(host, "-docker.pkg.dev"); ok {
		return prefix != "" && !strings.Contains(prefix, ".")
	}
	if prefix, ok := strings.CutSuffix(host, ".azurecr.io"); ok {
		return prefix != "" && !strings.Contains(prefix, ".")
	}
	return false
}
