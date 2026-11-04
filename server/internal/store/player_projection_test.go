package store

// Two write/read shapes that were paid for one row at a time. The projection
// test is deliberately a cross-check against the per-row query it replaces: the
// claim under test is not "the list is faster" but "the list says the same thing".

import (
	"context"
	"testing"
)

func TestSQLiteEnginePlayersCarryTheirTextureIDs(t *testing.T) {
	st := openSQLiteStore(t)
	ctx := context.Background()
	uid := newMeasureUser(t, st, "proj@example.com", "projuser")

	skin, err := st.AddTexture(ctx, uid, "skin", hashOf("proj-skin"), "slim", TextureMeta{Description: "s"})
	if err != nil {
		t.Fatalf("add skin: %v", err)
	}
	cape, err := st.AddTexture(ctx, uid, "cape", hashOf("proj-cape"), "", TextureMeta{Description: "c"})
	if err != nil {
		t.Fatalf("add cape: %v", err)
	}

	pid, _, err := st.CreatePlayer(ctx, uid, "ProjSteve")
	if err != nil {
		t.Fatalf("create player: %v", err)
	}
	if err := st.SetPlayerTextures(ctx, pid, skin, cape); err != nil {
		t.Fatalf("wear: %v", err)
	}
	bare, _, err := st.CreatePlayer(ctx, uid, "ProjBare")
	if err != nil {
		t.Fatalf("create second player: %v", err)
	}

	players, err := st.PlayersByUID(ctx, uid)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(players) != 2 {
		t.Fatalf("players = %d, want 2", len(players))
	}

	for _, p := range players {
		// The single-row query is the independent source: if the join projection
		// disagrees with it, the list lies about what a player is wearing.
		wantSkin, wantCape, err := st.PlayerTextureIDs(ctx, p.ID)
		if err != nil {
			t.Fatalf("PlayerTextureIDs(%d): %v", p.ID, err)
		}
		if p.SkinTID != wantSkin || p.CapeTID != wantCape {
			t.Errorf("player %d projected skin=%d cape=%d, per-row query says %d/%d",
				p.ID, p.SkinTID, p.CapeTID, wantSkin, wantCape)
		}
	}

	worn := players[0]
	if worn.ID != pid || worn.SkinTID != skin || worn.CapeTID != cape {
		t.Errorf("wearing player projected %+v, want skin %d cape %d", worn, skin, cape)
	}
	if bareRow := players[1]; bareRow.ID != bare || bareRow.SkinTID != 0 || bareRow.CapeTID != 0 {
		t.Errorf("bare player projected %+v, want 0/0 — a NULL column must not read as someone else's texture", bareRow)
	}

	// The single-player path must agree with the list, since both go through the
	// same projection now.
	one, err := st.PlayerByID(ctx, pid)
	if err != nil {
		t.Fatalf("PlayerByID: %v", err)
	}
	if one.SkinTID != skin || one.CapeTID != cape {
		t.Errorf("PlayerByID projected skin=%d cape=%d, want %d/%d", one.SkinTID, one.CapeTID, skin, cape)
	}
	// Hashes still come from the same join (plugin URL filters key on them).
	if one.SkinHash != hashOf("proj-skin") || one.CapeHash != hashOf("proj-cape") {
		t.Errorf("hashes lost from the projection: %+v", one)
	}
}

func TestSQLiteEngineInvitationBatch(t *testing.T) {
	st := openSQLiteStore(t)
	ctx := context.Background()

	codes, err := st.CreateInvitationCodes(ctx, 3, "batch")
	if err != nil {
		t.Fatalf("create batch: %v", err)
	}
	if len(codes) != 3 {
		t.Fatalf("returned %d codes, want 3", len(codes))
	}
	seenID := map[int64]bool{}
	seenCode := map[string]bool{}
	for _, c := range codes {
		if c.ID == 0 || seenID[c.ID] {
			t.Errorf("code %+v has a missing or duplicate id", c)
		}
		if seenCode[c.Code] {
			t.Errorf("duplicate generated code %q", c.Code)
		}
		seenID[c.ID], seenCode[c.Code] = true, true
		if c.Description != "batch" {
			t.Errorf("description = %q, want the batch note", c.Description)
		}
	}

	// What came back must be what is stored — the panel shows the returned list
	// and the redemption path only ever sees the rows.
	var stored int
	if err := st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM invitation_codes`).Scan(&stored); err != nil {
		t.Fatalf("count: %v", err)
	}
	if stored != 3 {
		t.Errorf("rows=%d, want 3", stored)
	}
	for _, c := range codes {
		if _, ok, err := st.FindInvitationCode(ctx, c.Code); err != nil || !ok {
			t.Errorf("returned code %q is not in the table (%v)", c.Code, err)
		}
	}

	// n is clamped, not trusted: the admin form takes a count from a text input.
	if got, err := st.CreateInvitationCodes(ctx, 0, "clamped"); err != nil || len(got) != 1 {
		t.Errorf("n=0 produced %d codes (%v), want the floor of 1", len(got), err)
	}

	// The contract change that matters for the operator: a failed batch returns
	// nothing rather than the rows it managed before failing, so an error screen
	// can never mean "some codes exist that you have not been shown".
	dead, cancel := context.WithCancel(context.Background())
	cancel()
	got, err := st.CreateInvitationCodes(dead, 20, "doomed")
	if err == nil {
		t.Fatal("a cancelled batch reported success")
	}
	if len(got) != 0 {
		t.Errorf("failed batch returned %d codes, want none", len(got))
	}
	var after int
	if err := st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM invitation_codes WHERE description = 'doomed'`).Scan(&after); err != nil {
		t.Fatalf("count doomed: %v", err)
	}
	if after != 0 {
		t.Errorf("%d rows were written by a batch that failed", after)
	}
}
