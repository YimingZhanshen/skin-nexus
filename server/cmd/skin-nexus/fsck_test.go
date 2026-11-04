package main

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"skin-nexus/server/internal/store"
	"skin-nexus/server/internal/texture"
)

func hex64(seed byte) string {
	out := make([]byte, 0, 64)
	for i := 0; i < 64; i++ {
		out = append(out, seed)
	}
	return string(out)
}

// A file nothing can reach is the finding fsck exists for; a file that only a
// tombstone cites is evidence and must not be swept; a row whose bytes are gone is
// the failure the operator most needs to hear about.
func TestClassifyFSCKSeparatesReachableFromOrphanAndProof(t *testing.T) {
	reachable, struck, gone := hex64('a'), hex64('b'), hex64('c')
	usage := map[string]int{reachable: 2, gone: 1}
	tombstoned := map[string]bool{struck: true}
	stored := []string{reachable, struck, "not-a-content-address"}

	got := classifyFSCK(usage, tombstoned, stored, nil)

	if !reflect.DeepEqual(got.OrphanFiles, []string{}) {
		t.Errorf("orphans = %v, want none: every stored content address is either reachable or struck", got.OrphanFiles)
	}
	if !reflect.DeepEqual(got.StruckFiles, []string{struck}) {
		t.Errorf("struck = %v, want the tombstoned address kept out of the sweep", got.StruckFiles)
	}
	if !reflect.DeepEqual(got.MissingFiles, []string{gone}) {
		t.Errorf("missing = %v, want the address a row cites but the disk lacks", got.MissingFiles)
	}
	if !reflect.DeepEqual(got.UnknownNames, []string{"not-a-content-address"}) {
		t.Errorf("unknown names = %v, want the stray file reported", got.UnknownNames)
	}
	if got.StoredFiles != 3 {
		t.Errorf("stored files = %d, want 3", got.StoredFiles)
	}
}

// A file with no row and no tombstone is what --fix is allowed to unlink; the
// classification must not confuse it with either of the two kinds it leaves alone.
func TestClassifyFSCKFindsTrueOrphans(t *testing.T) {
	loose := hex64('d')
	got := classifyFSCK(map[string]int{hex64('a'): 1}, map[string]bool{hex64('b'): true},
		[]string{hex64('a'), loose}, nil)

	if !reflect.DeepEqual(got.OrphanFiles, []string{loose}) {
		t.Errorf("orphans = %v, want only the file nothing cites", got.OrphanFiles)
	}
	if len(got.StruckFiles) != 0 {
		t.Errorf("struck = %v, want empty: nothing was cited as deleted evidence", got.StruckFiles)
	}
}

// The recycled slot is reported, never repaired: the tombstone is the record of a
// verdict, the live row is somebody else's skin, and only a human can say which one
// the site should keep.
func TestClassifyFSCKCarriesRecycledSlots(t *testing.T) {
	slots := []store.RecycledSlot{{TextureID: 7, TombstoneHash: hex64('e'), LiveHash: hex64('f')}}
	got := classifyFSCK(map[string]int{hex64('f'): 1}, map[string]bool{hex64('e'): true}, []string{hex64('f')}, slots)

	if len(got.RecycledSlots) != 1 || got.RecycledSlots[0].TextureID != 7 {
		t.Fatalf("recycled slots = %+v, want the planted one passed through", got.RecycledSlots)
	}
}

// The render ceiling is a property of the decoder, so the judgment "is this file
// one the site can no longer draw" has to use the same arithmetic the decoder
// uses — including the fact that it is the *declared* size that gets refused.
func TestOverRenderCeilingNamesTheOversizedFiles(t *testing.T) {
	ceiling := 100
	dims := map[string][2]uint32{
		hex64('a'): {10, 10}, // exactly at the ceiling → still renders
		hex64('b'): {11, 10}, // 110 → over
		hex64('c'): {2, 2},   // comfortably under
		hex64('d'): {1, 101}, // a tall strip is still over
	}
	got := overRenderCeiling(dims, ceiling)
	want := []string{hex64('b'), hex64('d')}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("over ceiling = %v, want %v (sorted, exclusive of the file exactly at the limit)", got, want)
	}
	if len(overRenderCeiling(map[string][2]uint32{hex64('a'): {10, 10}}, ceiling)) != 0 {
		t.Error("a file exactly at the ceiling must not be reported — the decoder accepts it")
	}
	if got := overRenderCeiling(nil, ceiling); len(got) != 0 {
		t.Errorf("no files checked produced %v, want none", got)
	}
}

// declaredSize must read the header out of a real file and refuse something that
// is not a PNG rather than reporting a nonsense size.
func TestDeclaredSizeReadsOnlyTheHeader(t *testing.T) {
	dir := t.TempDir()
	ts, err := texture.NewStore(dir)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	name := hex64('e')
	if err := os.WriteFile(filepath.Join(dir, name), pngSheet(t, 64, 32), 0o600); err != nil {
		t.Fatalf("plant a sheet: %v", err)
	}
	w, h, err := declaredSize(ts, name)
	if err != nil {
		t.Fatalf("declared size: %v", err)
	}
	if w != 64 || h != 32 {
		t.Errorf("declared %dx%d, want 64x32", w, h)
	}

	junk := hex64('f')
	if err := os.WriteFile(filepath.Join(dir, junk), []byte("not a png at all, but long enough to read"), 0o600); err != nil {
		t.Fatalf("plant junk: %v", err)
	}
	if _, _, err := declaredSize(ts, junk); err == nil {
		t.Error("a non-PNG reported a size instead of an error")
	}
}

// pngSheet encodes a real PNG of the given size, because a header hand-written
// from a spec and a header the encoder emits are not the same evidence.
func pngSheet(t *testing.T, w, h int) []byte {
	t.Helper()
	var b bytes.Buffer
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	if err := png.Encode(&b, img); err != nil {
		t.Fatalf("encode: %v", err)
	}
	return b.Bytes()
}
