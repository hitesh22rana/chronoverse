package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestFirewallReadyChecker(t *testing.T) {
	t.Parallel()

	now := time.Now()
	fresh := filepath.Join(t.TempDir(), "fresh")
	if err := os.WriteFile(fresh, []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(fresh, now, now); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(t.TempDir(), "stale")
	if err := os.WriteFile(stale, []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(stale, now, now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}

	checker := func(path string) firewallReadyChecker {
		return firewallReadyChecker{path: path, maxAge: time.Minute, now: func() time.Time { return now }}
	}

	if err := checker("").Healthy(t.Context()); err != nil {
		t.Fatalf("empty path Healthy() error = %v (want disabled)", err)
	}
	if err := checker(fresh).Healthy(t.Context()); err != nil {
		t.Fatalf("fresh marker Healthy() error = %v", err)
	}
	if err := checker(stale).Healthy(t.Context()); status.Code(err) != codes.Unavailable {
		t.Fatalf("stale marker Healthy() code = %s, want %s", status.Code(err), codes.Unavailable)
	}
	if err := checker(filepath.Join(t.TempDir(), "missing")).Healthy(t.Context()); status.Code(err) != codes.Unavailable {
		t.Fatalf("missing marker Healthy() code = %s, want %s", status.Code(err), codes.Unavailable)
	}
}
