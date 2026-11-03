package account

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"skin-nexus/server/internal/migrate"
	"skin-nexus/server/internal/siteurl"
	"skin-nexus/server/internal/store"
	"skin-nexus/server/internal/texture"
)

// A real database, because the promise under test is about three layers agreeing:
// the tombstone the delete writes, the lookup the upload makes, and the file the
// upload is refused before it may touch the disk.
func newUploadService(t *testing.T) (*Service, string) {
	t.Helper()
	siteurl.Set("https://skin.test")

	dir := t.TempDir()
	m, err := store.Open("sqlite", "sqlite:"+filepath.Join(dir, "site.db"), nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = m.Close() })
	if err := migrate.ApplySQLDirTracked(m.DB(), filepath.Join("..", "..", "migrations"), m.Engine()); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	ts, err := texture.NewStore(filepath.Join(dir, "textures"))
	if err != nil {
		t.Fatalf("texture store: %v", err)
	}
	return &Service{Store: m, Textures: ts}, filepath.Join(dir, "textures")
}

func uploadPNG(t *testing.T, seed byte) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 64, 64))
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: seed, G: seed / 2, B: seed / 3, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

func contentAddress(png []byte) string {
	sum := sha256.Sum256(png)
	return hex.EncodeToString(sum[:])
}

func TestUploadRefusesContentThatWasStruckAndDeleted(t *testing.T) {
	svc, dir := newUploadService(t)
	ctx := context.Background()

	uid, err := svc.Store.CreateUser(ctx, "uploader@example.com", "uploader", "x", 3, "")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	handler, err := svc.Store.CreateUser(ctx, "handler@example.com", "handler", "x", 3, "")
	if err != nil {
		t.Fatalf("CreateUser handler: %v", err)
	}

	pngBytes := uploadPNG(t, 200)
	view, created, aerr := svc.UploadTexture(ctx, uid, "skin", "", "offending", pngBytes, CallMeta{IP: "127.0.0.1"})
	if aerr != nil || !created || view.ID == 0 {
		t.Fatalf("first upload = %+v created=%v err=%v", view, created, aerr)
	}
	reportID, err := svc.Store.CreateReport(ctx, view.ID, handler, "offensive", "please take it down")
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if err := svc.Store.StrikeTexture(ctx, view.ID, reportID, handler); err != nil {
		t.Fatalf("strike: %v", err)
	}
	if aerr := svc.DeleteTexture(ctx, handler, view.ID, 0, true, "moderation verdict", CallMeta{IP: "127.0.0.1"}); aerr != nil {
		t.Fatalf("forced delete: %v", aerr)
	}

	// The bytes are gone from the library, so re-uploading them must be refused —
	// publicly and privately both, because a private copy is still wearable.
	for _, attempt := range []struct {
		name string
		call func() (TextureView, bool, *Error)
	}{
		{"public", func() (TextureView, bool, *Error) {
			return svc.UploadTexture(ctx, uid, "skin", "", "again", pngBytes, CallMeta{IP: "127.0.0.1"})
		}},
		{"private", func() (TextureView, bool, *Error) {
			return svc.ImportPrivateTexture(ctx, uid, "skin", "", "again", pngBytes, CallMeta{IP: "127.0.0.1"})
		}},
	} {
		_, _, aerr := attempt.call()
		if aerr == nil {
			t.Fatalf("%s re-upload of struck content succeeded", attempt.name)
		}
		if aerr.Code != CodeViolationReupload {
			t.Errorf("%s re-upload code = %q, want %q", attempt.name, aerr.Code, CodeViolationReupload)
		}
		if aerr.Status() != 403 {
			t.Errorf("%s re-upload status = %d, want 403", attempt.name, aerr.Status())
		}
		// The refusal happens before the file is written, so it leaves nothing behind
		// for the orphan sweep to find.
		if _, err := os.Stat(filepath.Join(dir, contentAddress(pngBytes))); err == nil {
			t.Errorf("%s re-upload wrote the rejected file to disk", attempt.name)
		}
	}
}

func TestUploadAllowsContentDeletedWithoutAVerdict(t *testing.T) {
	svc, _ := newUploadService(t)
	ctx := context.Background()

	uid, err := svc.Store.CreateUser(ctx, "owner@example.com", "owner", "x", 3, "")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	pngBytes := uploadPNG(t, 90)

	view, _, aerr := svc.UploadTexture(ctx, uid, "skin", "", "mine", pngBytes, CallMeta{IP: "127.0.0.1"})
	if aerr != nil {
		t.Fatalf("upload: %v", aerr)
	}
	if aerr := svc.DeleteTexture(ctx, uid, view.ID, uid, true, "", CallMeta{IP: "127.0.0.1"}); aerr != nil {
		t.Fatalf("owner delete: %v", aerr)
	}
	// A plain deletion is not a ban: the same player must get the same skin back.
	if _, created, aerr := svc.UploadTexture(ctx, uid, "skin", "", "mine again", pngBytes, CallMeta{IP: "127.0.0.1"}); aerr != nil {
		t.Fatalf("re-upload after a clean delete = %v, want it allowed", aerr)
	} else if !created {
		t.Error("re-upload after a clean delete did not create a row")
	}
}
