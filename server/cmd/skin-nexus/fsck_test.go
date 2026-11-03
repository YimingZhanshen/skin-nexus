package main

import (
	"reflect"
	"testing"

	"skin-nexus/server/internal/store"
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
