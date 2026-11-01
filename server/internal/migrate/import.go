// import.go: the APPLY path of the legacy -> SkinNexus data migration
// (design: docs/migration-import-design.md, recon 2026-10-23).
//
// Key decisions:
//   - IDs preserved 1:1 (uid/pid/tid copied verbatim) — zero mapping tables,
//     zero drift, per-row reconcilable;
//   - source tables are addressed via a schema/table prefix
//     (`legacy.` on the same MySQL instance, `legacy_` in tests);
//   - idempotent: INSERT IGNORE on preserved PKs, NOT EXISTS for the
//     keyless user_closet, ON DUPLICATE KEY for options;
//   - password rows carry as-is with password_algo='legacy:bcrypt'
//     (auth verifies $2y$ and lazily rehashes);
//   - uuid rows go through CleanUuidTable (the five yggc:fix-uuid-table
//     steps) before entering uuid_map.
package migrate

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"skin-nexus/server/internal/texture"
)

// ImportOptions configures one import run.
type ImportOptions struct {
	Source       *sql.DB        // legacy database connection
	Target       *sql.DB        // skin_nexus database connection
	SourcePrefix string         // e.g. "legacy." or "legacy_"
	TexturesSrc  string         // legacy storage/textures dir ("" = skip files)
	TexturesDst  *texture.Store // where files land (nil = skip files)
	DryRun       bool           // read-only report, no writes
}

// TableStat counts one imported table.
type TableStat struct {
	Selected int64 `json:"selected"`
	Inserted int64 `json:"inserted"`
	Skipped  int64 `json:"skipped"`
}

// ImportReport is the evidence artifact of one run.
type ImportReport struct {
	Tables      map[string]TableStat `json:"tables"`
	UUID        CleanReport          `json:"uuid"`
	Textures    TextureScan          `json:"texture_scan"`
	FilesCopied int                  `json:"files_copied"`
	Warnings    []string             `json:"warnings"`
}

// RunImport executes the full import (or dry-run report).
func RunImport(ctx context.Context, opt ImportOptions) (ImportReport, error) {
	rep := ImportReport{Tables: map[string]TableStat{}}
	p := opt.SourcePrefix

	// Target schema assertion (review A2): the import column lists require
	// 0005-0008. Without them the first INSERT dies with a generic
	// "Unknown column" and looks like data loss. Dry-run needs no target.
	if !opt.DryRun {
		var n int
		err := opt.Target.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM schema_migrations WHERE file = '0008_legacy_preserve.sql'`).Scan(&n)
		if err != nil || n == 0 {
			return rep, fmt.Errorf("target schema not ready (0008 marker missing; run one SKIN_AUTO_MIGRATE boot first): %v", err)
		}
	}

	// ---- users ----
	// password_algo classification (credential compatibility, user priority
	// 2026-10-23): PHC bcrypt $2y$/$2a$/$2b$ rows -> "legacy:bcrypt",
	// argon2 PHC -> "legacy:argon2", anything else -> "legacy" (salt-chain
	// try-all; needs SKIN_LEGACY_ENV mounted at boot). All 66 production rows
	// are $2y$ (recon) and verify without the chain (PHC short-circuit).
	sel := fmt.Sprintf(`
		SELECT u.uid, u.email, u.nickname, u.password, u.ip,
		       u.avatar, u.register_at, u.verified, u.score, u.locale,
		       u.is_dark_mode, u.permission, u.last_sign_at, u.verification_token,
		       IFNULL(pc.n, 0)
		FROM %susers u
		LEFT JOIN (SELECT uid, COUNT(*) n FROM %splayers GROUP BY uid) pc ON pc.uid = u.uid`, p, p)
	rows, err := opt.Source.QueryContext(ctx, sel)
	if err != nil {
		return rep, fmt.Errorf("users select: %w", err)
	}
	type userRow struct {
		id                  int64
		email, nick, pw, ip string
		avatar              int64
		registerAt          sql.NullTime
		verified            int64
		score               int64
		locale              sql.NullString
		dark                int64
		permission          int64
		lastSign            sql.NullTime
		verifToken          string
		playerCount         int64
	}
	var users []userRow
	for rows.Next() {
		var u userRow
		if err := rows.Scan(&u.id, &u.email, &u.nick, &u.pw, &u.ip, &u.avatar,
			&u.registerAt, &u.verified, &u.score, &u.locale, &u.dark,
			&u.permission, &u.lastSign, &u.verifToken, &u.playerCount); err != nil {
			rows.Close()
			return rep, fmt.Errorf("users scan: %w", err)
		}
		users = append(users, u)
	}
	rows.Close()
	rep.Tables["users"] = TableStat{Selected: int64(len(users))}
	// Preflight (review A1/A2): target uniques uq_users_email/uq_users_nickname
	// are ci and do NOT exist in legacy — INSERT IGNORE would silently drop a
	// conflicting user while their players/closet rows still import. Loud
	// warnings here; the cutover runbook asserts users.skipped == 0.
	seenEmail, seenNick := map[string]int64{}, map[string]int64{}
	for _, u := range users {
		if len(u.email) > 100 {
			rep.Warnings = append(rep.Warnings, fmt.Sprintf("users uid=%d email longer than 100", u.id))
		}
		if ke := foldName(u.email); seenEmail[ke] != 0 {
			rep.Warnings = append(rep.Warnings, fmt.Sprintf("users uid=%d email collides with uid=%d (ci) — will be SKIPPED", u.id, seenEmail[ke]))
		} else {
			seenEmail[ke] = u.id
		}
		if kn := foldName(u.nick); seenNick[kn] != 0 {
			rep.Warnings = append(rep.Warnings, fmt.Sprintf("users uid=%d nickname collides with uid=%d (ci) — will be SKIPPED", u.id, seenNick[kn]))
		} else {
			seenNick[kn] = u.id
		}
	}
	for _, u := range users {
		limit := u.playerCount
		if limit < 3 {
			limit = 3
		}
		algo := "legacy" // salt-chain try-all
		switch {
		case strings.HasPrefix(u.pw, "$2"): // $2a$/$2b$/$2x$/$2y$ bcrypt family
			algo = "legacy:bcrypt"
		case strings.HasPrefix(u.pw, "$argon2"):
			algo = "legacy:argon2"
		}
		avatar := sql.NullInt64{Int64: u.avatar, Valid: u.avatar != 0}
		created := safeTime(u.registerAt, fmt.Sprintf("users uid=%d register_at", u.id), true, &rep)
		canJoin := int64(1)
		if u.permission < 0 {
			canJoin = 0 // legacy permission=-1 = BANNED (review A1)
		}
		args := []any{u.id, u.email, u.nick, u.pw, algo, limit, u.ip,
			avatar, created, created, u.verified, canJoin, u.score, u.locale, u.dark,
			u.permission, safeTime(u.lastSign, fmt.Sprintf("users uid=%d last_sign_at", u.id), false, &rep), u.verifToken}
		if opt.DryRun {
			continue
		}
		res, err := opt.Target.ExecContext(ctx, `
			INSERT IGNORE INTO users
			  (id, email, nickname, password, password_algo, player_limit, ip, avatar_tid,
			   created_at, updated_at, email_verified, can_join_server,
			   score, locale, is_dark_mode, permission, last_sign_at, verification_token)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, args...)
		if err != nil {
			return rep, fmt.Errorf("users insert uid=%d: %w", u.id, err)
		}
		n, _ := res.RowsAffected()
		st := rep.Tables["users"]
		st.Inserted += n
		st.Skipped += 1 - n
		rep.Tables["users"] = st
	}

	// ---- players ----
	sel = fmt.Sprintf(`
		SELECT pid, uid, name, tid_skin, tid_cape, last_modified
		FROM %splayers ORDER BY pid`, p)
	rows, err = opt.Source.QueryContext(ctx, sel)
	if err != nil {
		return rep, fmt.Errorf("players select: %w", err)
	}
	type playerRow struct {
		id, uid      int64
		name         string
		skin, cape   int64 // legacy raw: -1/0 = unset
		lastModified sql.NullTime
	}
	var players []playerRow
	for rows.Next() {
		var pl playerRow
		if err := rows.Scan(&pl.id, &pl.uid, &pl.name, &pl.skin, &pl.cape, &pl.lastModified); err != nil {
			rows.Close()
			return rep, fmt.Errorf("players scan: %w", err)
		}
		players = append(players, pl)
	}
	rows.Close()
	rep.Tables["players"] = TableStat{Selected: int64(len(players))}
	seenPName := map[string]int64{}
	for _, pl := range players {
		if len(pl.name) > 16 {
			// target players.name is VARCHAR(16): strict INSERT IGNORE truncates
			// to a prefix and can collide with a sibling row (review A1 P0)
			rep.Warnings = append(rep.Warnings, fmt.Sprintf("players pid=%d name %q longer than 16 chars — needs manual handling", pl.id, pl.name))
		}
		if kn := foldName(pl.name); seenPName[kn] != 0 {
			rep.Warnings = append(rep.Warnings, fmt.Sprintf("players pid=%d name collides with pid=%d (ci) — will be SKIPPED", pl.id, seenPName[kn]))
		} else {
			seenPName[kn] = pl.id
		}
	}
	for _, pl := range players {
		var skin any
		if pl.skin > 0 {
			skin = pl.skin
		}
		var cape any
		if pl.cape > 0 {
			cape = pl.cape
		}
		if opt.DryRun {
			continue
		}
		created := safeTime(pl.lastModified, fmt.Sprintf("players pid=%d last_modified", pl.id), true, &rep)
		res, err := opt.Target.ExecContext(ctx, `
			INSERT IGNORE INTO players (id, uid, name, tid_skin, tid_cape, created_at, updated_at)
			VALUES (?,?,?,?,?,?,?)`,
			pl.id, pl.uid, pl.name, skin, cape, created, created)
		if err != nil {
			return rep, fmt.Errorf("players insert pid=%d: %w", pl.id, err)
		}
		n, _ := res.RowsAffected()
		st := rep.Tables["players"]
		st.Inserted += n
		st.Skipped += 1 - n
		rep.Tables["players"] = st
	}

	// ---- textures ----
	sel = fmt.Sprintf(`
		SELECT tid, uploader, type, hash, name, upload_at, public
		FROM %stextures ORDER BY tid`, p)
	rows, err = opt.Source.QueryContext(ctx, sel)
	if err != nil {
		return rep, fmt.Errorf("textures select: %w", err)
	}
	type texRow struct {
		id, uid    int64
		legacyType string
		hash, name string
		uploadAt   sql.NullTime
		public     int64
	}
	var texs []texRow
	for rows.Next() {
		var t texRow
		if err := rows.Scan(&t.id, &t.uid, &t.legacyType, &t.hash, &t.name, &t.uploadAt, &t.public); err != nil {
			rows.Close()
			return rep, fmt.Errorf("textures scan: %w", err)
		}
		texs = append(texs, t)
	}
	rows.Close()
	rep.Tables["textures"] = TableStat{Selected: int64(len(texs))}
	for _, t := range texs {
		typ, model := "skin", sql.NullString{}
		switch t.legacyType {
		case "steve":
			model = sql.NullString{String: "default", Valid: true}
		case "alex":
			model = sql.NullString{String: "slim", Valid: true}
		case "cape":
			typ = "cape"
		default:
			rep.Warnings = append(rep.Warnings, fmt.Sprintf("textures tid=%d unknown legacy type %q (imported as skin)", t.id, t.legacyType))
		}
		if opt.DryRun {
			continue
		}
		res, err := opt.Target.ExecContext(ctx, `
			INSERT IGNORE INTO textures (id, uid, type, hash, model, description, upload_at, public)
			VALUES (?,?,?,?,?,?,?,?)`,
			t.id, t.uid, typ, t.hash, model, t.name,
			safeTime(t.uploadAt, fmt.Sprintf("textures tid=%d upload_at", t.id), true, &rep), t.public)
		if err != nil {
			return rep, fmt.Errorf("textures insert tid=%d: %w", t.id, err)
		}
		n, _ := res.RowsAffected()
		st := rep.Tables["textures"]
		st.Inserted += n
		st.Skipped += 1 - n
		rep.Tables["textures"] = st
	}

	// ---- uuid -> uuid_map (five-step clean) ----
	sel = fmt.Sprintf(`SELECT id, name, uuid, pid FROM %suuid ORDER BY id`, p)
	rows, err = opt.Source.QueryContext(ctx, sel)
	if err != nil {
		return rep, fmt.Errorf("uuid select: %w", err)
	}
	var uuidRows []UuidRow
	for rows.Next() {
		var r UuidRow
		var pid sql.NullInt64
		if err := rows.Scan(&r.ID, &r.Name, &r.UUID, &pid); err != nil {
			rows.Close()
			return rep, fmt.Errorf("uuid scan: %w", err)
		}
		r.PID = pid.Int64
		uuidRows = append(uuidRows, r)
	}
	rows.Close()
	nameToPID := map[string]int64{}
	for _, pl := range players {
		nameToPID[foldName(pl.name)] = pl.id // folded: ci-faithful matching (review A1)
	}
	cleaned, uuidRep := CleanUuidTable(uuidRows, nameToPID)
	rep.UUID = uuidRep
	rep.Tables["uuid_map"] = TableStat{Selected: int64(len(uuidRows))}
	if !opt.DryRun {
		for _, r := range cleaned {
			if !isHexOfLen(r.UUID, 32) {
				rep.Warnings = append(rep.Warnings, fmt.Sprintf("uuid_map %q has malformed uuid — skipped", r.Name))
				continue
			}
			res, err := opt.Target.ExecContext(ctx,
				`INSERT IGNORE INTO uuid_map (name, uuid, player_id) VALUES (?,?,?)`,
				r.Name, r.UUID, r.PID) // r.UUID already normalized by the clean
			if err != nil {
				return rep, fmt.Errorf("uuid_map insert %s: %w", r.Name, err)
			}
			n, _ := res.RowsAffected()
			st := rep.Tables["uuid_map"]
			st.Inserted += n
			st.Skipped += 1 - n
			rep.Tables["uuid_map"] = st
		}
	}

	// ---- options (INSERT IGNORE: reruns never clobber operator changes;
	// duplicates resolved first-wins = lowest id, matching legacy runtime
	// reads (review A1: ON DUPLICATE kept the LAST id instead)) ----
	sel = fmt.Sprintf(`SELECT option_name, option_value FROM %soptions ORDER BY id`, p)
	rows, err = opt.Source.QueryContext(ctx, sel)
	if err != nil {
		return rep, fmt.Errorf("options select: %w", err)
	}
	type optRow struct{ k, v string }
	var opts []optRow
	for rows.Next() {
		var o optRow
		if err := rows.Scan(&o.k, &o.v); err != nil {
			rows.Close()
			return rep, fmt.Errorf("options scan: %w", err)
		}
		opts = append(opts, o)
	}
	rows.Close()
	rep.Tables["options"] = TableStat{Selected: int64(len(opts))}
	seenOpt := map[string]bool{}
	for _, o := range opts {
		if seenOpt[o.k] {
			rep.Warnings = append(rep.Warnings, fmt.Sprintf("options %q duplicated in source — kept first (lowest id), skipped later", o.k))
			st := rep.Tables["options"]
			st.Skipped++
			rep.Tables["options"] = st
			continue
		}
		seenOpt[o.k] = true
		if len(o.v) > 65535 { // target options.v is TEXT (64KiB); LONGTEXT source can exceed it
			rep.Warnings = append(rep.Warnings, fmt.Sprintf("options %q value exceeds 64KiB — skipped", o.k))
			st := rep.Tables["options"]
			st.Skipped++
			rep.Tables["options"] = st
			continue
		}
		if opt.DryRun {
			continue
		}
		if _, err := opt.Target.ExecContext(ctx,
			`INSERT IGNORE INTO options (k, v) VALUES (?,?)`,
			o.k, o.v); err != nil {
			return rep, fmt.Errorf("options insert %s: %w", o.k, err)
		}
		st := rep.Tables["options"]
		st.Inserted++
		rep.Tables["options"] = st
	}

	// ---- user_closet (keyless: per-row NOT EXISTS) ----
	sel = fmt.Sprintf(`SELECT user_uid, texture_tid, item_name FROM %suser_closet`, p)
	rows, err = opt.Source.QueryContext(ctx, sel)
	if err != nil {
		return rep, fmt.Errorf("closet select: %w", err)
	}
	type closetRow struct {
		uid, tid int64
		itemName sql.NullString
	}
	var closet []closetRow
	for rows.Next() {
		var c closetRow
		if err := rows.Scan(&c.uid, &c.tid, &c.itemName); err != nil {
			rows.Close()
			return rep, fmt.Errorf("closet scan: %w", err)
		}
		closet = append(closet, c)
	}
	rows.Close()
	rep.Tables["user_closet"] = TableStat{Selected: int64(len(closet))}
	for _, c := range closet {
		if opt.DryRun {
			continue
		}
		res, err := opt.Target.ExecContext(ctx, `
			INSERT INTO user_closet (user_uid, texture_tid, item_name)
			SELECT ?,?,? FROM DUAL
			WHERE NOT EXISTS (
				SELECT 1 FROM user_closet
				WHERE user_uid = ? AND texture_tid = ?
				  AND BINARY item_name <=> BINARY ?)`, // BINARY: exact-duplicate policy (review A1: ci/PAD-SPACE collapsed 'Hat'/'hat')
			c.uid, c.tid, c.itemName, c.uid, c.tid, c.itemName)
		if err != nil {
			return rep, fmt.Errorf("closet insert uid=%d tid=%d: %w", c.uid, c.tid, err)
		}
		n, _ := res.RowsAffected()
		st := rep.Tables["user_closet"]
		st.Inserted += n
		st.Skipped += 1 - n
		rep.Tables["user_closet"] = st
	}

	// ---- invitation_codes (preserved PK) ----
	sel = fmt.Sprintf(`
		SELECT id, code, generated_at, used_by, used_at, description
		FROM %sinvitation_codes ORDER BY id`, p)
	rows, err = opt.Source.QueryContext(ctx, sel)
	if err != nil {
		return rep, fmt.Errorf("invitation select: %w", err)
	}
	type invRow struct {
		id, usedBy  int64
		code        string
		generatedAt sql.NullTime
		usedAt      sql.NullTime
		description sql.NullString
	}
	var invs []invRow
	for rows.Next() {
		var i invRow
		if err := rows.Scan(&i.id, &i.code, &i.generatedAt, &i.usedBy, &i.usedAt, &i.description); err != nil {
			rows.Close()
			return rep, fmt.Errorf("invitation scan: %w", err)
		}
		invs = append(invs, i)
	}
	rows.Close()
	rep.Tables["invitation_codes"] = TableStat{Selected: int64(len(invs))}
	for _, i := range invs {
		if opt.DryRun {
			continue
		}
		res, err := opt.Target.ExecContext(ctx, `
			INSERT IGNORE INTO invitation_codes (id, code, generated_at, used_by, used_at, description)
			VALUES (?,?,?,?,?,?)`,
			i.id, i.code,
			safeTime(i.generatedAt, fmt.Sprintf("invitation id=%d generated_at", i.id), true, &rep),
			i.usedBy,
			safeTime(i.usedAt, fmt.Sprintf("invitation id=%d used_at", i.id), false, &rep),
			i.description)
		if err != nil {
			return rep, fmt.Errorf("invitation insert id=%d: %w", i.id, err)
		}
		n, _ := res.RowsAffected()
		st := rep.Tables["invitation_codes"]
		st.Inserted += n
		st.Skipped += 1 - n
		rep.Tables["invitation_codes"] = st
	}

	// ---- texture files (byte-exact copy + tree audit) ----
	if opt.TexturesSrc != "" {
		scan, err := ScanTextureTree(opt.TexturesSrc)
		if err != nil {
			return rep, fmt.Errorf("texture scan: %w", err)
		}
		rep.Textures = scan
		if opt.TexturesDst != nil && !opt.DryRun {
			entries, err := os.ReadDir(opt.TexturesSrc)
			if err != nil {
				return rep, err
			}
			for _, e := range entries {
				if e.IsDir() || !isHexOfLen(e.Name(), 64) {
					continue // broken/anomalous names are in the scan report already
				}
				src := filepath.Join(opt.TexturesSrc, e.Name())
				// warn-and-continue (review A1: one anomalous file used to
				// abort the run AFTER all DB rows committed, and the report
				// was discarded — scan.go treats the same condition as
				// report-only)
				if _, err := MigrateFile(opt.TexturesDst, src, e.Name()); err != nil {
					rep.Warnings = append(rep.Warnings, fmt.Sprintf("texture file %s: %v — skipped", e.Name(), err))
					continue
				}
				rep.FilesCopied++
			}
		}
	}
	return rep, nil
}

// ValidateDSNs enforces the driver settings the importer depends on
// (review A1): parseTime=true on both sides (otherwise every time.Time scan
// fails at runtime) and identical loc= (otherwise wall-clock timestamps
// shift by the UTC offset between the two sides).
func ValidateDSNs(src, tgt string) error {
	if !strings.Contains(src, "parseTime=true") || !strings.Contains(tgt, "parseTime=true") {
		return fmt.Errorf("import: both DSNs must carry parseTime=true")
	}
	if locOf(src) != locOf(tgt) {
		return fmt.Errorf("import: DSNs must use the same loc (got %q vs %q)", locOf(src), locOf(tgt))
	}
	return nil
}

// locOf extracts the loc= query parameter ("" = driver default UTC).
func locOf(dsn string) string {
	q := dsn
	if i := strings.Index(dsn, "?"); i >= 0 {
		q = dsn[i+1:]
	}
	for _, kv := range strings.Split(q, "&") {
		if v, ok := strings.CutPrefix(kv, "loc="); ok {
			return v
		}
	}
	return ""
}

// safeTime normalizes a scanned legacy timestamp (review A1/A2: NULL or
// '0000-00-00' values become driver zero-time and strict-mode inserts abort
// the whole run). required -> substitute now(); nullable -> collapse to NULL.
// Both paths leave a warning as evidence.
func safeTime(t sql.NullTime, label string, required bool, rep *ImportReport) sql.NullTime {
	if t.Valid && !t.Time.IsZero() {
		return t
	}
	rep.Warnings = append(rep.Warnings, label+" is NULL/zero date")
	if required {
		return sql.NullTime{Time: time.Now(), Valid: true}
	}
	return sql.NullTime{}
}

// normalizeUUID defensively canonicalizes a legacy uuid string to the
// 32-lowercase-hex form uuid_map stores (legacy values are already clean,
// but dashes/case would violate CHAR(32)). Non-hex garbage is left as-is;
// callers gate with isHexOfLen(r.UUID, 32) before insert.
func normalizeUUID(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	return strings.ReplaceAll(s, "-", "")
}
