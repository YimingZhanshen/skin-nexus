package store

// CHARACTERIZATION TESTS — ticket G10 / E2 (stable UUID identity).
//
// These tests PIN TODAY'S behaviour of the chain
//   删除角色 → 该名字被释放 → 用同名重建 → 新角色的 UUID 与材质挂载状态
// as the code actually works now. They are not a specification of what the
// chain SHOULD do. If a future round intentionally changes any step, this
// file must go red and be updated DELIBERATELY — never softened to get green.
//
// Runs on the SQLite engine helper from sqlite_engine_test.go (no build tag,
// no server), so `go test ./...` really executes it — unlike the MySQL
// integration suite, which is gated behind -tags integration + SKIN_TEST_DSN
// and therefore is NOT part of the default green gate.
//
// Source chain being pinned:
//   account.Service.DeletePlayer (../account/players.go:220)
//     -> Mysql.AdminDeletePlayer  (player_admin.go:177-206)
//        DELETE uuid_map WHERE player_id   (player_admin.go:193)
//        UPDATE ygg_tokens SET selected_pid = NULL (player_admin.go:194)
//        DELETE FROM players               (player_admin.go:195)
//        texture rows are NOT touched      (player_admin.go:171-176 comment)
//   account.Service.CreatePlayer (../account/players.go:145)
//     -> Mysql.CreatePlayer (web.go:816-833): uuid = OfflineUUID(name)
//        (web.go:817) plus a brand new uuid_map row (web.go:829)
//   OfflineUUID (web.go:887-893) is a PURE function of the name only.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"skin-nexus/server/internal/ygg"
)

// g10TokenRow is a local copy of what the integration suite's tokenRowFor
// builds (integration_test.go:1166-1170) — that helper is behind
// //go:build integration, so the default run cannot reach it.
func g10TokenRow(uid, pid int64, access string) ygg.Token {
	now := time.Now()
	return ygg.Token{UserID: uid, ClientToken: "g10-ct", AccessToken: access,
		SelectedPID: pid, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
}

// TestG10RecreatedPlayerGetsTheSameOfflineUUID pins the answer to E2: a
// recreated character with a reused name gets the SAME offline UUID, because
// OfflineUUID is a pure function of the name (web.go:887) and CreatePlayer
// recomputes it from the name on every insert (web.go:817). The identity is
// re-DERIVED, not restored — the uuid_map row that recorded it was deleted
// with the player (player_admin.go:193).
func TestG10RecreatedPlayerGetsTheSameOfflineUUID(t *testing.T) {
	st := openSQLiteStore(t)
	ctx := context.Background()
	uid, err := st.CreateUser(ctx, "g10-uuid@example.com", "g10uuid", "x", 3, "")
	if err != nil {
		t.Fatal(err)
	}

	const name = "G10Reborn"
	pid1, uuid1, err := st.CreatePlayer(ctx, uid, name)
	if err != nil {
		t.Fatal(err)
	}
	if uuid1 != OfflineUUID(name) {
		t.Fatalf("uuid1 must equal OfflineUUID(name): %s vs %s", uuid1, OfflineUUID(name))
	}

	if _, _, err := st.AdminDeletePlayer(ctx, pid1); err != nil {
		t.Fatal(err)
	}
	pid2, uuid2, err := st.CreatePlayer(ctx, uid, name)
	if err != nil {
		t.Fatalf("recreating a released name must succeed: %v", err)
	}
	if uuid2 != uuid1 {
		t.Fatalf("UUID DRIFT on delete+recreate: %s -> %s (铁律 2 territory)", uuid1, uuid2)
	}
	// OBSERVED: the row id is NOT stable — AUTO_INCREMENT hands out a new one.
	if pid2 == pid1 {
		t.Fatal("players.id is expected to change on recreate; if it stopped changing, update this test deliberately")
	}
	// The new row resolves to the same UUID through the fresh uuid_map entry.
	p, err := st.PlayerByUUID(ctx, uuid1)
	if err != nil {
		t.Fatalf("PlayerByUUID after recreate: %v", err)
	}
	if p.ID != pid2 || p.Name != name {
		t.Fatalf("uuid now resolves to the NEW row: %+v (want pid %d)", p, pid2)
	}
	if _, err := st.PlayerByID(ctx, pid1); err != ErrNotFound {
		t.Fatalf("old row must be gone, got %v", err)
	}
}

// TestG10RecreatedPlayerLosesTheTextureMount pins the second half of E2: the
// deleted player's skin/cape mount is gone, while the texture rows themselves
// survive (content-addressed library semantics). So a same-name recreation
// keeps the UUID but answers `textures:{}` until something is worn again.
func TestG10RecreatedPlayerLosesTheTextureMount(t *testing.T) {
	st := openSQLiteStore(t)
	ctx := context.Background()
	uid, err := st.CreateUser(ctx, "g10-mount@example.com", "g10mount", "x", 3, "")
	if err != nil {
		t.Fatal(err)
	}
	pid, uuid, err := st.CreatePlayer(ctx, uid, "G10Mounted")
	if err != nil {
		t.Fatal(err)
	}
	skinTID, err := st.AddTexture(ctx, uid, "skin", hashOf("g10skin"), "slim", TextureMeta{Description: "worn"})
	if err != nil {
		t.Fatal(err)
	}
	capeTID, err := st.AddTexture(ctx, uid, "cape", hashOf("g10cape"), "", TextureMeta{Description: "worn cape"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetPlayerTextures(ctx, pid, skinTID, capeTID); err != nil {
		t.Fatal(err)
	}
	// Pre-condition: the mount really is visible through the ygg projection
	// (the SQL that feeds the signed textures property).
	before, err := st.PlayerByUUID(ctx, uuid)
	if err != nil || before.SkinTID != skinTID || before.CapeTID != capeTID {
		t.Fatalf("mount not visible before delete: %+v err=%v", before, err)
	}

	if _, _, err := st.AdminDeletePlayer(ctx, pid); err != nil {
		t.Fatal(err)
	}
	// OBSERVED: texture rows survive the player delete.
	var nTex int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM textures WHERE id IN (?,?)`, skinTID, capeTID).Scan(&nTex); err != nil {
		t.Fatal(err)
	}
	if nTex != 2 {
		t.Fatalf("texture rows must be kept (player_admin.go:171-176), got %d", nTex)
	}

	_, uuid2, err := st.CreatePlayer(ctx, uid, "G10Mounted")
	if err != nil {
		t.Fatal(err)
	}
	if uuid2 != uuid {
		t.Fatalf("uuid must be stable across the recreate: %s -> %s", uuid, uuid2)
	}
	after, err := st.PlayerByUUID(ctx, uuid)
	if err != nil {
		t.Fatal(err)
	}
	// OBSERVED: tid_skin/tid_cape are NULL on the fresh row (web.go:824 inserts
	// only uid+name), so the hashes are gone from the projection too — this is
	// what makes the recreated profile's textures property collapse to {}.
	if after.SkinTID != 0 || after.CapeTID != 0 {
		t.Fatalf("recreated player must start UNMOUNTED, got skin=%d cape=%d", after.SkinTID, after.CapeTID)
	}
	if after.SkinURL != "" || after.CapeURL != "" || after.SkinHash != "" || after.CapeHash != "" {
		t.Fatalf("recreated projection must be empty, got %+v", after)
	}
	if after.SkinModel != "default" {
		t.Fatalf("playerSelect COALESCEs model to default (mysql.go:173), got %q", after.SkinModel)
	}
}

// TestG10DeleteDetachesTokenSelectionButKeepsTheToken pins the token half of
// the delete: the session token survives with its profile selection set to
// NULL (player_admin.go:194), which is what makes the client fall back to
// profile selection rather than being logged out. The uuid_map row, by
// contrast, is deleted — that is the record of "this name's uuid".
func TestG10DeleteDetachesTokenSelectionButKeepsTheToken(t *testing.T) {
	st := openSQLiteStore(t)
	ctx := context.Background()
	uid, err := st.CreateUser(ctx, "g10-token@example.com", "g10token", "x", 3, "")
	if err != nil {
		t.Fatal(err)
	}
	pid, uuid, err := st.CreatePlayer(ctx, uid, "G10TokenSteve")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SaveToken(ctx, g10TokenRow(uid, pid, "g10-at-1")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.AdminDeletePlayer(ctx, pid); err != nil {
		t.Fatal(err)
	}
	var nMap int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM uuid_map WHERE name = ?`, "G10TokenSteve").Scan(&nMap); err != nil {
		t.Fatal(err)
	}
	if nMap != 0 {
		t.Fatalf("uuid_map row must be deleted with the player, got %d", nMap)
	}
	// The access_token COLUMN holds a digest (mysql.go:229), so the row is read
	// back through TokenByAccess, which is what the protocol layer uses.
	tok, err := st.TokenByAccess(ctx, "g10-at-1")
	if err != nil {
		t.Fatalf("the session token itself must SURVIVE the player delete: %v", err)
	}
	// OBSERVED: selected_pid was squeezed to NULL, which the scan reports as 0.
	if tok.SelectedPID != 0 {
		t.Fatalf("token selection must be detached to NULL, got selected_pid=%d", tok.SelectedPID)
	}
	if tok.UserID != uid {
		t.Fatalf("token owner changed by the delete: %d vs %d", tok.UserID, uid)
	}
	// Re-creating the name re-derives the identical uuid.
	if _, uuid2, err := st.CreatePlayer(ctx, uid, "G10TokenSteve"); err != nil || uuid2 != uuid {
		t.Fatalf("recreate = %q/%v, want the same uuid %s", uuid2, err, uuid)
	}
}

// TestG10RenameDoesNotRecomputeTheUUID pins 铁律 2 on an engine the default
// `go test ./...` run actually reaches: AdminRenamePlayer moves only the name
// columns, never uuid_map.uuid (player_admin.go:101-115). The MySQL
// equivalent lives behind -tags integration and is not part of the gate.
//
// It also pins the OBSERVED consequence of a purely name-derived identity:
// the OLD name is released by the rename and re-creating it re-derives the
// SAME uuid the renamed row still owns — which uuid_map's UNIQUE(uuid) index
// (migrations/mysql/0001_init.sql:55) forbids. The expected value below is
// whatever the code does today: a duplicate-uuid insert must be rejected.
func TestG10RenameDoesNotRecomputeTheUUID(t *testing.T) {
	st := openSQLiteStore(t)
	ctx := context.Background()
	uid, err := st.CreateUser(ctx, "g10-rename@example.com", "g10rename", "x", 3, "")
	if err != nil {
		t.Fatal(err)
	}
	pid, uuid, err := st.CreatePlayer(ctx, uid, "G10OldName")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AdminRenamePlayer(ctx, pid, "G10NewName"); err != nil {
		t.Fatal(err)
	}
	p, err := st.PlayerByID(ctx, pid)
	if err != nil || p.Name != "G10NewName" {
		t.Fatalf("after rename: %+v %v", p, err)
	}
	if p.UUID != uuid {
		t.Fatalf("rename must not move the UUID: %s -> %s", uuid, p.UUID)
	}
	if _, err := st.PlayerByName(ctx, "G10OldName"); err == nil {
		t.Fatal("old name must be released by the rename")
	}
	// OBSERVED: recreating the released old name re-derives the uuid the
	// renamed row still holds, so the UNIQUE(uuid) index rejects the insert.
	_, uuidAgain, err := st.CreatePlayer(ctx, uid, "G10OldName")
	t.Logf("observed recreate-after-rename error: %v (uuid=%q)", err, uuidAgain)
	if err == nil {
		t.Fatalf("recreating a renamed-away name produced uuid %s with no error — the uuid_map UNIQUE(uuid) collision assertion below must be revisited", uuidAgain)
	}
	if !strings.Contains(err.Error(), "UNIQUE") && !strings.Contains(err.Error(), "constraint") {
		t.Fatalf("expected a uuid_map uniqueness failure, got %v", err)
	}
	// This half was updated DELIBERATELY in slice 3 (P2), exactly as this file's
	// own instruction anticipated ("if it starts being wrapped, update
	// deliberately and fix the caller's message"). CreatePlayer now routes a
	// players/uuid_map unique-key collision through isDupKey and wraps it as
	// ErrDuplicate (web.go wrapPlayerCollision), so account.CreatePlayer's
	// duplicate branch (players.go:151-153) answers CodeNameTaken — a 409-shaped
	// "the name is not available" — instead of the CodeInternal 500-shaped answer
	// this test used to pin. The raw constraint text is preserved in the chain
	// for diagnostics (asserted just above).
	if !errors.Is(err, ErrDuplicate) {
		t.Fatalf("a reused (renamed-away) name must surface as ErrDuplicate so the caller reports CodeNameTaken, got %v", err)
	}
}

// TestG10OfflineUUIDIsPureAndCaseSensitive pins the two properties every
// answer above rests on (web.go:887-893): the formula is a pure function of
// the exact name bytes and it is case-SENSITIVE, while the name LOOKUP is
// case-insensitive (mysql.go:156-161). That combination is a latent drift
// surface and is recorded, not fixed.
func TestG10OfflineUUIDIsPureAndCaseSensitive(t *testing.T) {
	if a, b := OfflineUUID("Notch"), OfflineUUID("Notch"); a != b {
		t.Fatalf("OfflineUUID is not pure: %s vs %s", a, b)
	}
	if OfflineUUID("Notch") == OfflineUUID("notch") {
		t.Fatal("OfflineUUID is case-SENSITIVE today; if it ever folds case, update deliberately")
	}
	// 32 lowercase hex, no dashes (models.go:7 contract note).
	u := OfflineUUID("G10Shape")
	if len(u) != 32 || u != strings.ToLower(u) {
		t.Fatalf("uuid shape = %q", u)
	}
	for i, c := range u {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Fatalf("non-hex at %d: %q", i, u)
		}
	}
	// Version nibble forced to 3 and variant to RFC4122 (web.go:890-891).
	if u[12] != '3' {
		t.Fatalf("version nibble must be 3, got %q in %s", u[12], u)
	}
	if !strings.ContainsRune("89ab", rune(u[16])) {
		t.Fatalf("variant nibble must be 8|9|a|b, got %q in %s", u[16], u)
	}
	// The store hands the same value out through CreatePlayer.
	st := openSQLiteStore(t)
	ctx := context.Background()
	uid, err := st.CreateUser(ctx, "g10-pure@example.com", "g10pure", "x", 3, "")
	if err != nil {
		t.Fatal(err)
	}
	_, uuid, err := st.CreatePlayer(ctx, uid, "G10Shape")
	if err != nil {
		t.Fatal(err)
	}
	if uuid != u {
		t.Fatalf("CreatePlayer uuid = %s, OfflineUUID = %s", uuid, u)
	}
	// Case-insensitive lookup vs case-sensitive uuid: "g10shape" resolves to
	// the row created as "G10Shape".
	if p, err := st.PlayerByName(ctx, "g10shape"); err != nil || p.UUID != uuid {
		t.Fatalf("case-folded name lookup: %+v %v", p, err)
	}
}
