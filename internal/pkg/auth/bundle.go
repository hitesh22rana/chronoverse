package auth

import (
	"crypto"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/golang-jwt/jwt/v5"
)

const (
	issuersDir            = "certs/issuers"
	trustedBundleFilename = "trusted.json"
)

// bundleEntry is a trusted kid binding.
type bundleEntry struct {
	Iss       string
	PublicKey crypto.PublicKey
	PubPath   string
}

// bundleFileEntry is the JSON shape of trusted.json.
type bundleFileEntry struct {
	Iss string `json:"iss"`
	Pub string `json:"pub"`
}

func kidForKey(issuer string, pub ed25519.PublicKey) string {
	if len(pub) < 4 {
		return issuer + ":unknown"
	}
	return fmt.Sprintf("%s:%s", issuer, hex.EncodeToString(pub[:4]))
}

func bundlePathsForPublicKey(publicKeyPath string) []string {
	candidates := []string{}
	// Derived from publicKeyPath when it lives under issuers/<issuer>/auth.ed.pub
	if strings.Contains(publicKeyPath, issuersDir) {
		dir := filepath.Dir(publicKeyPath) // certs/issuers/<issuer>
		for dir != "." && dir != "/" {
			if filepath.Base(dir) == "issuers" {
				candidates = append(candidates, filepath.Join(filepath.Dir(dir), issuersDir, trustedBundleFilename))
				candidates = append(candidates, filepath.Join(dir, trustedBundleFilename))
				break
			}
			next := filepath.Dir(dir)
			if next == dir {
				break
			}
			dir = next
		}
	}
	candidates = append(candidates, filepath.Join(issuersDir, trustedBundleFilename))
	candidates = append(candidates, filepath.Join("/certs", "issuers", trustedBundleFilename))
	candidates = append(candidates, filepath.Join("/certs", trustedBundleFilename))
	// De-duplicate
	seen := map[string]struct{}{}
	uniq := []string{}
	for _, p := range candidates {
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		uniq = append(uniq, p)
	}
	return uniq
}

func loadPublicKeyFromPEM(path string) (ed25519.PublicKey, []byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	pub, err := jwt.ParseEdPublicKeyFromPEM(data)
	if err != nil {
		return nil, nil, err
	}
	edPub, ok := pub.(ed25519.PublicKey)
	if !ok {
		return nil, nil, fmt.Errorf("not ed25519 public key: %T", pub)
	}
	return edPub, data, nil
}

func resolveBundlePubPath(bundlePath, pub string) []string {
	if strings.Contains(pub, "-----BEGIN") {
		return nil
	}
	candidates := []string{}
	if filepath.IsAbs(pub) {
		candidates = append(candidates, pub)
	} else {
		candidates = append(candidates, pub)
		candidates = append(candidates, filepath.Join(filepath.Dir(bundlePath), pub))
		candidates = append(candidates, filepath.Join(issuersDir, pub))
		candidates = append(candidates, filepath.Join("certs", pub))
		candidates = append(candidates, filepath.Join("/certs", pub))
		candidates = append(candidates, filepath.Join(filepath.Dir(bundlePath), filepath.Base(pub)))
		// If pub is like "issuers/server/auth.ed.pub", the file as mounted is at /certs/issuers/server/auth.ed.pub
		// which is filepath.Join("/certs", pub) => /certs/issuers/server/auth.ed.pub -> works.
		// If pub is "server/auth.ed.pub" (relative to issuersDir), join with bundle dir.
	}
	// de-duplicate
	seen := map[string]struct{}{}
	uniq := []string{}
	for _, c := range candidates {
		if _, ok := seen[c]; ok {
			continue
		}
		seen[c] = struct{}{}
		uniq = append(uniq, c)
	}
	return uniq
}

func loadBundle(bundlePath string) (map[string]*bundleEntry, error) {
	raw, err := os.ReadFile(bundlePath)
	if err != nil {
		return nil, err
	}
	var fileMap map[string]bundleFileEntry
	if err := json.Unmarshal(raw, &fileMap); err != nil {
		return nil, fmt.Errorf("parse trusted bundle %s: %w", bundlePath, err)
	}
	out := map[string]*bundleEntry{}
	for kid, entry := range fileMap {
		if kid == "" || entry.Iss == "" || entry.Pub == "" {
			continue
		}
		// Inline PEM?
		if strings.Contains(entry.Pub, "-----BEGIN") {
			pub, err := jwt.ParseEdPublicKeyFromPEM([]byte(entry.Pub))
			if err != nil {
				continue
			}
			out[kid] = &bundleEntry{Iss: entry.Iss, PublicKey: pub, PubPath: bundlePath + ":" + kid}
			continue
		}
		candidates := resolveBundlePubPath(bundlePath, entry.Pub)
		var pubKey crypto.PublicKey
		var loaded bool
		for _, cand := range candidates {
			if pk, _, err := loadPublicKeyFromPEM(cand); err == nil {
				pubKey = pk
				loaded = true
				break
			}
		}
		if !loaded {
			continue
		}
		out[kid] = &bundleEntry{Iss: entry.Iss, PublicKey: pubKey, PubPath: entry.Pub}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("trusted bundle %s contains no valid entries", bundlePath)
	}
	return out, nil
}

func findAndLoadBundle(publicKeyPath string) (map[string]*bundleEntry, string) {
	for _, cand := range bundlePathsForPublicKey(publicKeyPath) {
		if m, err := loadBundle(cand); err == nil {
			return m, cand
		}
	}
	// Direct issuersDir fallback
	if m, err := loadBundle(filepath.Join(issuersDir, trustedBundleFilename)); err == nil {
		return m, filepath.Join(issuersDir, trustedBundleFilename)
	}
	if m, err := loadBundle(filepath.Join("/certs", "issuers", trustedBundleFilename)); err == nil {
		return m, filepath.Join("/certs", "issuers", trustedBundleFilename)
	}
	return nil, ""
}
