package signedpkg

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestKeyringSignVerify(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	raw := "test:" + base64.StdEncoding.EncodeToString(pub)
	kr, err := ParseKeys(raw)
	if err != nil {
		t.Fatal(err)
	}
	data := BundlePayload("demo", "1.0.0", "abc")
	sig := Sign(priv, data)
	if err := kr.Verify("test", data, sig); err != nil {
		t.Fatal(err)
	}
	if err := kr.Verify("test", []byte("tampered"), sig); err == nil {
		t.Fatal("tampered payload accepted")
	}
	if err := kr.Verify("other", data, sig); err == nil {
		t.Fatal("unknown key accepted")
	}
	if _, err := ParseKeys("bad"); err == nil {
		t.Fatal("malformed keyring accepted")
	}
}

func TestContentDigest(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "b.zip")
	makeZip(t, path, map[string]string{"plugin.json": "{}", "index.js": "1", "bundle.json": "sig"})
	d1, err := ContentDigest(path, "bundle.json")
	if err != nil {
		t.Fatal(err)
	}
	const want = "3c5ed545f83f1cb7366422b95b79809a15cc492363dfc0edbb5db52434e73d3c"
	if d1 != want {
		t.Fatalf("canonical digest = %q, want %q", d1, want)
	}
	// bundle.json is excluded, so changing it keeps the digest.
	makeZip(t, path, map[string]string{"plugin.json": "{}", "index.js": "1", "bundle.json": "other"})
	d2, err := ContentDigest(path, "bundle.json")
	if err != nil {
		t.Fatal(err)
	}
	if d1 != d2 {
		t.Fatal("bundle.json affected content digest")
	}
	makeZip(t, path, map[string]string{"plugin.json": "{}", "index.js": "2", "bundle.json": "sig"})
	d3, err := ContentDigest(path, "bundle.json")
	if err != nil {
		t.Fatal(err)
	}
	if d1 == d3 {
		t.Fatal("payload change did not affect digest")
	}
}

func TestBundlePayloadCanonicalFormat(t *testing.T) {
	got := BundlePayload("demo", "1.0.0", "abc")
	if want := []byte("demo\n1.0.0\nabc"); !bytes.Equal(got, want) {
		t.Fatalf("payload = %q, want %q", got, want)
	}
}

func TestContentDigestEntryCountLimit(t *testing.T) {
	for _, tc := range []struct {
		name    string
		entries int
		wantErr bool
	}{
		{name: "at_limit", entries: MaxEntries},
		{name: "over_limit", entries: MaxEntries + 1, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "count.zip")
			writeTestZip(t, path, emptyEntries(tc.entries))
			_, err := ContentDigest(path, "bundle.json")
			if tc.wantErr {
				if err == nil || !strings.Contains(err.Error(), "too many entries") {
					t.Fatalf("error = %v, want entry count rejection", err)
				}
			} else if err != nil {
				t.Fatalf("archive at entry limit rejected: %v", err)
			}
		})
	}
}

func TestContentDigestDeclaredEntrySizeLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "declared.zip")
	writeDeflatedRawEntry(t, path, "payload.bin", MaxEntryBytes+1, []byte("small"))
	_, err := ContentDigest(path, "bundle.json")
	if err == nil || !strings.Contains(err.Error(), "exceeds 33554432 bytes") {
		t.Fatalf("error = %v, want declared size rejection", err)
	}
}

func TestContentDigestRejectsForgedActualEntrySize(t *testing.T) {
	path := filepath.Join(t.TempDir(), "forged.zip")
	data := lowRatioFixture(MaxEntryBytes + 1)
	// The central directory claims the per-entry limit, but the deflate stream
	// expands beyond it. The digest must reject rather than trust the header.
	writeDeflatedRawEntry(t, path, "payload.bin", MaxEntryBytes, data)
	if _, err := ContentDigest(path, "bundle.json"); err == nil {
		t.Fatal("forged oversized entry accepted")
	}
}

func TestContentDigestAggregateActualSizeLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "aggregate.zip")
	// Three 24 MiB entries expand to 72 MiB in aggregate while staying below
	// the compression ratio cap, so only the aggregate limit can reject it.
	data := lowRatioFixture(24 << 20)
	writeTestZip(t, path, []Entry{
		{Name: "a.bin", Data: data},
		{Name: "b.bin", Data: data},
		{Name: "c.bin", Data: data},
	})
	_, err := ContentDigest(path, "bundle.json")
	if err == nil || !strings.Contains(err.Error(), "exceeds 67108864 uncompressed bytes") {
		t.Fatalf("error = %v, want aggregate size rejection", err)
	}
}

func TestContentDigestCompressionRatioLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ratio.zip")
	data := make([]byte, 2<<20)
	writeDeflatedRawEntry(t, path, "payload.bin", uint64(len(data)), data)
	_, err := ContentDigest(path, "bundle.json")
	if err == nil || !strings.Contains(err.Error(), "suspicious compression ratio") {
		t.Fatalf("error = %v, want compression ratio rejection", err)
	}
}

func lowRatioFixture(size int) []byte {
	const blockSize = 1 << 20
	pattern := make([]byte, blockSize)
	var counter [8]byte
	for offset := 0; offset < 64<<10; offset += sha256.Size {
		binary.LittleEndian.PutUint64(counter[:], uint64(offset/sha256.Size))
		sum := sha256.Sum256(counter[:])
		copy(pattern[offset:], sum[:])
	}
	text := []byte("signedpkg-bounded-read-fixture")
	for offset := 64 << 10; offset < len(pattern); offset++ {
		pattern[offset] = text[offset%len(text)]
	}
	data := make([]byte, size)
	for offset := 0; offset < len(data); offset += len(pattern) {
		copy(data[offset:], pattern)
	}
	return data
}

func emptyEntries(count int) []Entry {
	entries := make([]Entry, count)
	for i := range entries {
		entries[i].Name = fmt.Sprintf("%04d.txt", i)
	}
	return entries
}

func writeTestZip(t *testing.T, path string, entries []Entry) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for _, entry := range entries {
		w, err := zw.Create(entry.Name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(entry.Data); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func writeDeflatedRawEntry(t *testing.T, path, name string, declared uint64, data []byte) {
	t.Helper()
	var compressed bytes.Buffer
	w, err := flate.NewWriter(&compressed, flate.BestCompression)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	h := &zip.FileHeader{
		Name:               name,
		Method:             zip.Deflate,
		CRC32:              crc32.ChecksumIEEE(data),
		CompressedSize64:   uint64(compressed.Len()),
		UncompressedSize64: declared,
	}
	raw, err := zw.CreateRaw(h)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Write(compressed.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func makeZip(t *testing.T, path string, files map[string]string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(body))
	}
	_ = zw.Close()
	_ = f.Close()
}
