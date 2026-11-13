package oidc

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// readFile is the seam the table-driven keyfile tests use to inject a read
// failure that is NOT "file missing" — the distinction the loader must make
// (see LoadOrCreateKey). Same pattern as logx.exit.
var readFile = os.ReadFile

// LoadOrCreateKey loads the OIDC token signing key (PKCS#1 or PKCS#8 PEM) or
// generates an RSA-4096 key on first boot. This key is intentionally separate
// from the texture signing key (ygg.Signer): tokens and texture signatures
// must rotate independently (contract §3.1 密钥分离).
func LoadOrCreateKey(path string) (*rsa.PrivateKey, error) {
	b, err := readFile(path)
	if err == nil {
		block, _ := pem.Decode(b)
		if block == nil {
			return nil, errors.New("oidc: signing key is not PEM")
		}
		if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
			return k, nil
		}
		if k, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
			if rk, ok := k.(*rsa.PrivateKey); ok {
				return rk, nil
			}
		}
		return nil, errors.New("oidc: unsupported signing key format")
	}
	// Only a genuinely missing file may mint a new key: a transient read
	// failure (permissions, FS error) used to fall through to regeneration,
	// which silently REPLACED the key and invalidated every issued token —
	// and a successful rewrite after a failed read destroys the old key for
	// good. Read errors surface instead of rotating secrets.
	if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("oidc: read signing key: %w", err)
	}
	key, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		return nil, err
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		return nil, err
	}
	return key, nil
}
