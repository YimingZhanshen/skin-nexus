package store

// The three read-only measurements fsck gained for the 2026-11-05 round. Each
// exists to size a decision this package must not make on its own: doubled
// likes, a sitemap that stopped reaching the newest skins, and wardrobe rows
// outliving their texture.

import (
	"context"
	"testing"
)

func newMeasureUser(t *testing.T, st *Mysql, email, nick string) int64 {
	t.Helper()
	uid, err := st.CreateUser(context.Background(), email, nick, "x", 3, "")
	if err != nil {
		t.Fatalf("create user %s: %v", nick, err)
	}
	return uid
}

func TestSQLiteEngineFSCKDuplicateCloset(t *testing.T) {
	st := openSQLiteStore(t)
	ctx := context.Background()
	uid := newMeasureUser(t, st, "dup@example.com", "dupuser")

	a, err := st.AddTexture(ctx, uid, "skin", hashOf("dup-a"), "default", TextureMeta{Description: "a"})
	if err != nil {
		t.Fatalf("add a: %v", err)
	}
	b, err := st.AddTexture(ctx, uid, "skin", hashOf("dup-b"), "default", TextureMeta{Description: "b"})
	if err != nil {
		t.Fatalf("add b: %v", err)
	}
	if _, err := st.AddClosetEntry(ctx, uid, a, ""); err != nil {
		t.Fatalf("like a: %v", err)
	}

	pairs, extra, err := st.FSCKDuplicateCloset(ctx)
	if err != nil {
		t.Fatalf("duplicate closet: %v", err)
	}
	if pairs != 0 || extra != 0 {
		t.Fatalf("clean pivot reported pairs=%d extra=%d, want 0/0 — a report that cries wolf never gets read", pairs, extra)
	}

	// The race AddClosetEntry cannot prevent: the same (account, texture) stored
	// more than once, which is exactly what inflates the ♥ the library shows.
	for i := 0; i < 3; i++ {
		if _, err := st.db.ExecContext(ctx,
			`INSERT INTO user_closet (user_uid, texture_tid, item_name) VALUES (?, ?, NULL)`, uid, b); err != nil {
			t.Fatalf("plant duplicate: %v", err)
		}
	}
	pairs, extra, err = st.FSCKDuplicateCloset(ctx)
	if err != nil {
		t.Fatalf("duplicate closet: %v", err)
	}
	if pairs != 1 {
		t.Errorf("duplicate pairs = %d, want 1", pairs)
	}
	// Three rows for one (account, texture) pair means two that never happened.
	if extra != 2 {
		t.Errorf("extra rows = %d, want 2 (three rows for a pair that means one)", extra)
	}

	// A different account liking the same texture is not a duplicate.
	other := newMeasureUser(t, st, "dup2@example.com", "dupuser2")
	if _, err := st.AddClosetEntry(ctx, other, b, ""); err != nil {
		t.Fatalf("second account likes b: %v", err)
	}
	if p, e, err := st.FSCKDuplicateCloset(ctx); err != nil || p != 1 || e != 2 {
		t.Errorf("after a second account liked the same texture: pairs=%d extra=%d err=%v, want 1/2", p, e, err)
	}
}

func TestSQLiteEngineFSCKClosetRowsWithoutTexture(t *testing.T) {
	st := openSQLiteStore(t)
	ctx := context.Background()
	uid := newMeasureUser(t, st, "gone@example.com", "goneuser")
	tid, err := st.AddTexture(ctx, uid, "skin", hashOf("alive"), "default", TextureMeta{Description: "alive"})
	if err != nil {
		t.Fatalf("add texture: %v", err)
	}
	if _, err := st.AddClosetEntry(ctx, uid, tid, ""); err != nil {
		t.Fatalf("like: %v", err)
	}
	n, err := st.FSCKClosetOnMissingTextures(ctx)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Fatalf("dangling rows = %d, want 0 for a consistent library", n)
	}

	// A wardrobe row for a texture id that is not in the table: the state left
	// behind when a whole-delete removed the row and the pivot kept citing it.
	if _, err := st.db.ExecContext(ctx,
		`INSERT INTO user_closet (user_uid, texture_tid, item_name) VALUES (?, ?, NULL)`, uid, tid+999); err != nil {
		t.Fatalf("plant dangling row: %v", err)
	}
	if n, err := st.FSCKClosetOnMissingTextures(ctx); err != nil || n != 1 {
		t.Errorf("dangling rows = %d %v, want 1", n, err)
	}
}

func TestSQLiteEngineFSCKSitemapCoverage(t *testing.T) {
	st := openSQLiteStore(t)
	ctx := context.Background()
	uid := newMeasureUser(t, st, "map@example.com", "mapuser")
	for i := 0; i < 3; i++ {
		if _, err := st.AddTexture(ctx, uid, "skin", hashOf(string(rune('m'+i))), "default",
			TextureMeta{Description: "public"}); err != nil {
			t.Fatalf("add %d: %v", i, err)
		}
	}
	if _, err := st.AddTexturePrivate(ctx, uid, "skin", hashOf("private-one"), "slim",
		TextureMeta{Description: "private"}); err != nil {
		t.Fatalf("add private: %v", err)
	}

	public, listed, err := st.FSCKSitemapCoverage(ctx)
	if err != nil {
		t.Fatalf("coverage: %v", err)
	}
	// Private textures must never count as sitemap material.
	if public != 3 || listed != 3 {
		t.Fatalf("public=%d listed=%d, want 3/3 (the private row is not the crawler's)", public, listed)
	}

	// What the coverage reports has to equal what the route can hand out — the
	// number is only useful if the two never drift apart.
	rows, err := st.PublicTextureIDs(ctx, SitemapIDLimit)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != listed {
		t.Errorf("route listed %d ids but coverage reported %d", len(rows), listed)
	}
	if SitemapIDLimit != 5000 {
		t.Errorf("SitemapIDLimit = %d; changing it changes which urls a crawler is told about, so it is an operator decision, not a constant to tune here", SitemapIDLimit)
	}
}
