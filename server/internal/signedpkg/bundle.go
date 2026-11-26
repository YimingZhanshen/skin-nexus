package signedpkg

import (
	"archive/zip"
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"time"
)

type BundleMeta struct {
	Name      string `json:"name"`
	Version   string `json:"version"`
	KeyID     string `json:"keyId"`
	Signature string `json:"signature"`
}

type Entry struct {
	Name string
	Data []byte
}

func ReadBundleMeta(zipPath string) (BundleMeta, error) {
	raw, missing, err := readEntry(zipPath, "bundle.json", MaxMetadataBytes)
	if missing {
		return BundleMeta{}, fmt.Errorf("bundle.json missing (signed bundles only)")
	}
	if err != nil {
		return BundleMeta{}, err
	}
	var meta BundleMeta
	if err := json.NewDecoder(bytes.NewReader(raw)).Decode(&meta); err != nil {
		return BundleMeta{}, fmt.Errorf("bundle.json: %w", err)
	}
	if meta.Name == "" || meta.Version == "" || meta.KeyID == "" || meta.Signature == "" {
		return BundleMeta{}, fmt.Errorf("bundle.json is missing name/version/keyId/signature")
	}
	return meta, nil
}

func VerifyBundle(zipPath string, ring Keyring) (BundleMeta, error) {
	meta, err := ReadBundleMeta(zipPath)
	if err != nil {
		return BundleMeta{}, err
	}
	digest, err := ContentDigest(zipPath, "bundle.json")
	if err != nil {
		return BundleMeta{}, err
	}
	sig, err := DecodeSignature(meta.Signature)
	if err != nil {
		return BundleMeta{}, err
	}
	if err := ring.Verify(meta.KeyID, BundlePayload(meta.Name, meta.Version, digest), sig); err != nil {
		return BundleMeta{}, err
	}
	return meta, nil
}

func ReadEntry(zipPath, name string, max int64) ([]byte, error) {
	b, missing, err := readEntry(zipPath, name, max)
	if missing {
		return nil, os.ErrNotExist
	}
	return b, err
}

// readEntry distinguishes an absent entry from failures opening or reading the
// archive so callers do not mislabel a broken archive as an unsigned bundle.
func readEntry(zipPath, name string, max int64) ([]byte, bool, error) {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return nil, false, err
	}
	defer zr.Close()
	for _, f := range zr.File {
		if f.Name != name {
			continue
		}
		if max > 0 && int64(f.UncompressedSize64) > max {
			return nil, false, fmt.Errorf("%s exceeds %d bytes", name, max)
		}
		rc, err := f.Open()
		if err != nil {
			return nil, false, err
		}
		defer rc.Close()
		limit := max + 1
		if limit <= 0 {
			limit = 1 << 20
		}
		b, err := io.ReadAll(io.LimitReader(rc, limit))
		if err != nil {
			return nil, false, err
		}
		if max > 0 && int64(len(b)) > max {
			return nil, false, fmt.Errorf("%s exceeds %d bytes", name, max)
		}
		return b, false, nil
	}
	return nil, true, nil
}

// WriteNamedBundle writes a deterministic zip plus a signed root bundle.json.
func WriteNamedBundle(path, name, version string, entries []Entry, privKey []byte, keyID string) (digest, sha string, err error) {
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	for _, e := range entries {
		if e.Name == "bundle.json" {
			return "", "", fmt.Errorf("bundle.json is reserved")
		}
		if err := ValidateEntryName(e.Name); err != nil {
			return "", "", err
		}
	}
	digest, err = digestEntries(entries)
	if err != nil {
		return "", "", err
	}
	sig := Sign(ed25519.PrivateKey(privKey), BundlePayload(name, version, digest))
	meta, _ := json.Marshal(BundleMeta{
		Name: name, Version: version, KeyID: keyID,
		Signature: base64.StdEncoding.EncodeToString(sig),
	})
	entries = append(entries, Entry{Name: "bundle.json", Data: meta})
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })

	f, err := os.Create(path)
	if err != nil {
		return "", "", err
	}
	zw := zip.NewWriter(f)
	for _, e := range entries {
		h := &zip.FileHeader{Name: e.Name, Method: zip.Deflate, Modified: time.Unix(0, 0).UTC()}
		h.SetMode(0o644)
		w, err := zw.CreateHeader(h)
		if err != nil {
			_ = zw.Close()
			_ = f.Close()
			_ = os.Remove(path)
			return "", "", err
		}
		if _, err := w.Write(e.Data); err != nil {
			_ = zw.Close()
			_ = f.Close()
			_ = os.Remove(path)
			return "", "", err
		}
	}
	if err := zw.Close(); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return "", "", err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return "", "", err
	}
	sha, err = SHA256File(path)
	if err != nil {
		_ = os.Remove(path)
		return "", "", err
	}
	priv := ed25519.PrivateKey(privKey)
	ring := Keyring{keyID: priv.Public().(ed25519.PublicKey)}
	if _, err := VerifyBundle(path, ring); err != nil {
		_ = os.Remove(path)
		return "", "", fmt.Errorf("self-verify bundle: %w", err)
	}
	return digest, sha, nil
}

func digestEntries(entries []Entry) (string, error) {
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	h := sha256.New()
	for _, e := range entries {
		sum := sha256.Sum256(e.Data)
		fmt.Fprintf(h, "%s\x00%s\n", e.Name, hex.EncodeToString(sum[:]))
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func SHA256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
