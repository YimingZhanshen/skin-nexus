// Package marketfmt is the single canonical encoding for the signed plugin
// registry. The server and skin-plugin must marshal exactly the same bytes, so
// no caller is allowed to hand-roll the payload JSON.
package marketfmt

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"skin-nexus/server/internal/signedpkg"
	"skin-nexus/server/internal/version"
)

type Dist struct {
	URL    string `json:"url"`
	Size   int64  `json:"size,omitempty"`
	SHA256 string `json:"sha256"`
}

type Requires struct {
	Plugins map[string]string `json:"plugins,omitempty"`
	Server  string            `json:"server,omitempty"`
}

type Package struct {
	Name         string            `json:"name"`
	Version      string            `json:"version"`
	Title        map[string]string `json:"title,omitempty"`
	Description  map[string]string `json:"description,omitempty"`
	Author       string            `json:"author,omitempty"`
	Requires     *Requires         `json:"requires,omitempty"`
	Conflicts    []string          `json:"conflicts,omitempty"`
	Capabilities []string          `json:"capabilities,omitempty"`
	MinServer    string            `json:"minServer,omitempty"`
	Changelog    string            `json:"changelog,omitempty"`
	Readme       string            `json:"readme,omitempty"`
	Dist         Dist              `json:"dist"`
}

type Registry struct {
	Schema      int       `json:"schema"`
	GeneratedAt string    `json:"generatedAt"`
	ExpiresAt   string    `json:"expiresAt"`
	KeyID       string    `json:"keyId"`
	Packages    []Package `json:"packages"`
	Signature   string    `json:"signature,omitempty"`
}

var nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{1,63}$`)
var shaRe = regexp.MustCompile(`^[a-f0-9]{64}$`)

// Payload returns the canonical bytes covered by the registry signature.
func Payload(r Registry) ([]byte, error) {
	// The anonymous struct order is load-bearing: it must match the server's
	// historical payload exactly.
	payload := struct {
		Schema      int       `json:"schema"`
		GeneratedAt string    `json:"generatedAt"`
		ExpiresAt   string    `json:"expiresAt"`
		KeyID       string    `json:"keyId"`
		Packages    []Package `json:"packages"`
	}{r.Schema, r.GeneratedAt, r.ExpiresAt, r.KeyID, r.Packages}
	return json.Marshal(payload)
}

func Build(pkgs []Package, keyID string, generatedAt, expiresAt time.Time) Registry {
	sort.Slice(pkgs, func(i, j int) bool { return pkgs[i].Name < pkgs[j].Name })
	return Registry{
		Schema: 1, GeneratedAt: generatedAt.UTC().Format(time.RFC3339),
		ExpiresAt: expiresAt.UTC().Format(time.RFC3339), KeyID: keyID, Packages: pkgs,
	}
}

func Sign(r *Registry, priv ed25519.PrivateKey) error {
	payload, err := Payload(*r)
	if err != nil {
		return err
	}
	r.Signature = base64.StdEncoding.EncodeToString(signedpkg.Sign(priv, payload))
	return nil
}

func VerifyRegistry(raw []byte, ring signedpkg.Keyring, now time.Time) (*Registry, error) {
	if len(raw) > 1<<20 {
		return nil, fmt.Errorf("registry exceeds 1 MiB")
	}
	var r Registry
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, fmt.Errorf("registry JSON: %w", err)
	}
	if r.Schema != 1 {
		return nil, fmt.Errorf("unsupported market schema %d", r.Schema)
	}
	if r.KeyID == "" || r.Signature == "" {
		return nil, fmt.Errorf("registry is not signed")
	}
	sig, err := signedpkg.DecodeSignature(r.Signature)
	if err != nil {
		return nil, err
	}
	payload, err := Payload(r)
	if err != nil {
		return nil, err
	}
	if err := ring.Verify(r.KeyID, payload, sig); err != nil {
		return nil, err
	}
	if r.ExpiresAt == "" {
		return nil, fmt.Errorf("registry has no expiresAt")
	}
	exp, err := time.Parse(time.RFC3339, r.ExpiresAt)
	if err != nil {
		return nil, fmt.Errorf("registry expiresAt: %w", err)
	}
	if !now.Before(exp) {
		return nil, fmt.Errorf("market registry has expired")
	}
	seen := map[string]bool{}
	for i := range r.Packages {
		p := &r.Packages[i]
		if !nameRe.MatchString(p.Name) {
			return nil, fmt.Errorf("invalid package name %q", p.Name)
		}
		if seen[p.Name] {
			return nil, fmt.Errorf("duplicate package %q", p.Name)
		}
		seen[p.Name] = true
		if p.Version == "" {
			return nil, fmt.Errorf("package %s has no version", p.Name)
		}
		u, err := url.Parse(p.Dist.URL)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.RawQuery != "" || u.Fragment != "" {
			return nil, fmt.Errorf("package %s has invalid dist URL", p.Name)
		}
		if p.Dist.Size <= 0 || !shaRe.MatchString(strings.ToLower(p.Dist.SHA256)) {
			return nil, fmt.Errorf("package %s has invalid dist size/sha256", p.Name)
		}
	}
	return &r, nil
}

// CompareVersions orders two package versions with the repository's one
// version predicate (internal/version). A version that cannot be ordered is
// an error, never a zero: the callers below already treat an error as "no
// comparison", which is what a malformed registry entry deserves.
func CompareVersions(a, b string) (int, error) {
	return version.CompareStrings(a, b)
}
