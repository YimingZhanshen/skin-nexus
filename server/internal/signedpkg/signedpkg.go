// Package signedpkg implements the minimal signed-plugin trust primitives:
// pinned Ed25519 keys, detached signatures, SHA-256, and a deterministic
// content digest for a zip bundle. No network or filesystem policy lives here.
package signedpkg

import (
	"archive/zip"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"sort"
	"strings"
)

// Keyring maps a key id to a pinned Ed25519 public key.
type Keyring map[string]ed25519.PublicKey

// ParseKeys parses "id:base64pub,id2:base64pub".
func ParseKeys(raw string) (Keyring, error) {
	kr := Keyring{}
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		id, pub, ok := strings.Cut(part, ":")
		if !ok || id == "" {
			return nil, fmt.Errorf("invalid trust key %q (want id:base64pub)", part)
		}
		b, err := base64.StdEncoding.DecodeString(pub)
		if err != nil || len(b) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("trust key %q: invalid ed25519 public key", id)
		}
		kr[id] = ed25519.PublicKey(b)
	}
	if len(kr) == 0 {
		return nil, fmt.Errorf("no trust keys configured")
	}
	return kr, nil
}

func (k Keyring) Verify(keyID string, data, sig []byte) error {
	pub, ok := k[keyID]
	if !ok {
		return fmt.Errorf("unknown signing key %q", keyID)
	}
	if len(sig) != ed25519.SignatureSize || !ed25519.Verify(pub, data, sig) {
		return fmt.Errorf("signature verification failed for key %q", keyID)
	}
	return nil
}

// Sign is used by tests and the packaging tool; production only verifies.
func Sign(priv ed25519.PrivateKey, data []byte) []byte {
	return ed25519.Sign(priv, data)
}

func DecodeSignature(s string) ([]byte, error) {
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil || len(b) != ed25519.SignatureSize {
		return nil, fmt.Errorf("invalid signature encoding")
	}
	return b, nil
}

func SHA256(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// ContentDigest hashes every zip entry except the named metadata file, in
// sorted order, as path + entry SHA-256. The signature covers this digest, so
// adding/replacing bundle.json does not invalidate it (bundle.json is the
// signature carrier, not part of the payload).
func ContentDigest(zipPath, exclude string) (string, error) {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return "", err
	}
	defer zr.Close()
	if len(zr.File) > MaxEntries {
		return "", fmt.Errorf("zip has too many entries (%d)", len(zr.File))
	}
	type entry struct {
		name string
		sum  string
	}
	var entries []entry
	var total int64
	for _, f := range zr.File {
		if f.FileInfo().IsDir() || f.Name == exclude {
			continue
		}
		if f.UncompressedSize64 > uint64(MaxEntryBytes) {
			return "", fmt.Errorf("zip entry %q exceeds %d bytes", f.Name, MaxEntryBytes)
		}
		if f.CompressedSize64 > 0 {
			ratio := f.UncompressedSize64 / f.CompressedSize64
			if ratio > uint64(MaxZipRatio) ||
				(ratio == uint64(MaxZipRatio) && f.UncompressedSize64%f.CompressedSize64 != 0) {
				return "", fmt.Errorf("zip entry %q has suspicious compression ratio", f.Name)
			}
		}
		rc, err := f.Open()
		if err != nil {
			return "", err
		}
		h := sha256.New()
		n, copyErr := io.Copy(h, io.LimitReader(rc, int64(MaxEntryBytes)+1))
		rc.Close()
		if copyErr != nil {
			return "", copyErr
		}
		if n > int64(MaxEntryBytes) {
			return "", fmt.Errorf("zip entry %q exceeds %d bytes", f.Name, MaxEntryBytes)
		}
		if n > int64(MaxTotalBytes)-total {
			return "", fmt.Errorf("zip exceeds %d uncompressed bytes", MaxTotalBytes)
		}
		total += n
		entries = append(entries, entry{name: f.Name, sum: hex.EncodeToString(h.Sum(nil))})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].name < entries[j].name })
	h := sha256.New()
	for _, e := range entries {
		fmt.Fprintf(h, "%s\x00%s\n", e.name, e.sum)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// BundlePayload is the canonical signed payload for a bundle.
func BundlePayload(name, version, contentDigest string) []byte {
	return []byte(name + "\n" + version + "\n" + contentDigest)
}
