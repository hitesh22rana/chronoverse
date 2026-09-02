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

func bundlePathsForPublicKey(_ string) []string {
	// ponytail: two canonical locations cover local (relative) and container (/certs) mounts.
	return []string{
		filepath.Join(issuersDir, trustedBundleFilename),
		"/certs/issuers/" + trustedBundleFilename,
	}
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
		if strings.Contains(entry.Pub, "-----BEGIN") {
			pub, err := jwt.ParseEdPublicKeyFromPEM([]byte(entry.Pub))
			if err != nil {
				continue
			}
			out[kid] = &bundleEntry{Iss: entry.Iss, PublicKey: pub, PubPath: bundlePath + ":" + kid}
			continue
		}
		// Single canonical resolution: bundle dir + pub (pub is "$iss/auth.ed.pub").
		pubPath := entry.Pub
		if !filepath.IsAbs(pubPath) {
			pubPath = filepath.Join(filepath.Dir(bundlePath), pubPath)
		}
		pk, _, err := loadPublicKeyFromPEM(pubPath)
		if err != nil {
			continue
		}
		out[kid] = &bundleEntry{Iss: entry.Iss, PublicKey: pk, PubPath: entry.Pub}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("trusted bundle %s contains no valid entries", bundlePath)
	}
	return out, nil
}

func findAndLoadBundle(publicKeyPath string) (bundle map[string]*bundleEntry, bundlePath string) {
	for _, cand := range bundlePathsForPublicKey(publicKeyPath) {
		if m, err := loadBundle(cand); err == nil {
			return m, cand
		}
	}
	return nil, ""
}
