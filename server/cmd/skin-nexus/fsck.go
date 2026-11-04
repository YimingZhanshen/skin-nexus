package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"

	"skin-nexus/server/internal/avatargen"
	"skin-nexus/server/internal/config"
	"skin-nexus/server/internal/dialect"
	"skin-nexus/server/internal/store"
	"skin-nexus/server/internal/texture"
)

// fsckFindings is what one pass over the database and the texture directory
// concludes. Every list is sorted so two runs read the same way.
type fsckFindings struct {
	OrphanFiles   []string // bytes no row can reach and no tombstone cites
	StruckFiles   []string // bytes no row reaches, but a verdict is on record for them
	MissingFiles  []string // a row cites content the disk does not have
	UnknownNames  []string // files not named by a content address
	RecycledSlots []store.RecycledSlot

	StoredFiles int
}

// classifyFSCK compares the rows against the directory listing. It is pure on
// purpose: the judgment is the part worth testing, and the caller owns the disk.
func classifyFSCK(usage map[string]int, tombstoned map[string]bool, stored []string, slots []store.RecycledSlot) fsckFindings {
	out := fsckFindings{
		OrphanFiles:   []string{},
		StruckFiles:   []string{},
		MissingFiles:  []string{},
		UnknownNames:  []string{},
		RecycledSlots: slots,
	}
	have := make(map[string]bool, len(stored))
	for _, name := range stored {
		have[name] = true
		out.StoredFiles++
	}
	for _, name := range stored {
		switch {
		case !texture.IsContentAddress(name):
			out.UnknownNames = append(out.UnknownNames, name)
		case usage[name] > 0:
		case tombstoned[name]:
			// Struck content stays on disk only when a delete failed to unlink it. The
			// file is the last proof of what the verdict was about, so this tool never
			// removes it — it reports it instead.
			out.StruckFiles = append(out.StruckFiles, name)
		default:
			out.OrphanFiles = append(out.OrphanFiles, name)
		}
	}
	for hash := range usage {
		if !have[hash] {
			out.MissingFiles = append(out.MissingFiles, hash)
		}
	}
	for _, list := range [][]string{out.OrphanFiles, out.StruckFiles, out.MissingFiles, out.UnknownNames} {
		sort.Strings(list)
	}
	return out
}

// runFSCK reports what the library and its files disagree about, and can unlink the
// one class of disagreement that is safe to repair without a human.
//
// Dry-run by default, like redetect-models: this deletes files, and the difference
// between "orphan" and "the tombstone of a banned skin" is exactly the kind of
// distinction an operator should see before anything is removed.
// overRenderCeiling picks the files whose *declared* size is one the renderer
// refuses to allocate. avatargen caps decoding (an unauthenticated route reaches
// it, so a declared 8192×8192 used to cost 268 MB per request), and a migrated
// legacy sheet can be over that cap. This tool cannot fix it — resampling a
// skin is a judgment about that player's texture — so it names the files the
// operator has to decide about.
func overRenderCeiling(dims map[string][2]uint32, ceiling int) []string {
	var out []string
	for name, wh := range dims {
		if int(wh[0])*int(wh[1]) > ceiling {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// declaredSize reads just enough of a stored file to see its IHDR.
func declaredSize(ts *texture.Store, name string) (w, h uint32, err error) {
	head := make([]byte, 33)
	f, err := ts.Open(name)
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()
	if _, err := io.ReadFull(f, head); err != nil {
		return 0, 0, err
	}
	return texture.ScanLegacyIHDR(head)
}

func runFSCK(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("fsck", flag.ContinueOnError)
	fs.SetOutput(out)
	fix := fs.Bool("fix", false, "unlink files that neither a row nor a tombstone cites (default: report only)")
	deep := fs.Bool("deep", false, "re-hash every stored file and compare it with its name")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	st, err := store.Open("", cfg.DatabaseDSN, nil)
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}
	defer st.Close()
	ts, err := texture.NewStore(cfg.TexturesDir)
	if err != nil {
		return fmt.Errorf("textures: %w", err)
	}

	ctx := context.Background()
	usage, err := st.FSCKHashUsage(ctx)
	if err != nil {
		return fmt.Errorf("hash usage: %w", err)
	}
	tombstoned, err := st.FSCKTombstonedHashes(ctx)
	if err != nil {
		return fmt.Errorf("tombstones: %w", err)
	}
	slots, err := st.FSCKRecycledSlots(ctx)
	if err != nil {
		return fmt.Errorf("recycled slots: %w", err)
	}
	rows, tombs, openReports, err := st.FSCKCounts(ctx)
	if err != nil {
		return fmt.Errorf("counts: %w", err)
	}
	entries, err := os.ReadDir(cfg.TexturesDir)
	if err != nil {
		return fmt.Errorf("read %s: %w", cfg.TexturesDir, err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}

	found := classifyFSCK(usage, tombstoned, names, slots)
	dupPairs, dupExtra, err := st.FSCKDuplicateCloset(ctx)
	if err != nil {
		return fmt.Errorf("closet duplicates: %w", err)
	}
	dangling, err := st.FSCKClosetOnMissingTextures(ctx)
	if err != nil {
		return fmt.Errorf("closet rows without a texture: %w", err)
	}
	pub, listed, err := st.FSCKSitemapCoverage(ctx)
	if err != nil {
		return fmt.Errorf("sitemap coverage: %w", err)
	}
	fmt.Fprintf(out, "fsck %s engine=%s rows=%d addresses=%d tombstones=%d open_reports=%d\n",
		cfg.TexturesDir, st.Engine(), rows, len(usage), tombs, openReports)
	fmt.Fprintf(out, "files=%d orphans=%d struck_only=%d missing=%d unknown_names=%d recycled_slots=%d\n",
		found.StoredFiles, len(found.OrphanFiles), len(found.StruckFiles),
		len(found.MissingFiles), len(found.UnknownNames), len(found.RecycledSlots))
	// Three measurements the panel cannot show and the repair tools need as a
	// precondition, all read-only: how much like-history is doubled, how much of
	// the public library the sitemap can no longer reach, and which wardrobe rows
	// outlive their texture.
	fmt.Fprintf(out, "closet_duplicate_pairs=%d closet_extra_rows=%d closet_rows_without_texture=%d\n",
		dupPairs, dupExtra, dangling)
	fmt.Fprintf(out, "sitemap public=%d listed=%d omitted=%d (limit %d, lowest ids win, so the newest skins are the ones missing)\n",
		pub, listed, pub-listed, store.SitemapIDLimit)
	if dupExtra > 0 {
		fmt.Fprintln(out, "note: the library's like counts are a COUNT over that pivot, so every extra row is a ♥ that never happened; a unique index is the fix and it cannot be added before these rows are resolved")
	}
	listNames(out, "orphan", found.OrphanFiles)
	listNames(out, "missing", found.MissingFiles)
	listNames(out, "unknown", found.UnknownNames)
	for _, s := range found.RecycledSlots {
		fmt.Fprintf(out, "recycled id=%d tombstone=%s live=%s\n",
			s.TextureID, short(s.TombstoneHash), short(s.LiveHash))
	}
	listNames(out, "struck", found.StruckFiles)
	if len(found.RecycledSlots) > 0 {
		fmt.Fprintln(out, "note: a recycled id predates the AUTOINCREMENT fix; the tombstone and the row cannot both be right, so both are reported and neither is touched")
	}

	if *deep {
		bad := 0
		for _, name := range found.OrphanFiles {
			if !texture.IsContentAddress(name) {
				continue
			}
			if err := texture.VerifyStored(ts, name); err != nil {
				bad++
				fmt.Fprintf(out, "content mismatch: %s\n", short(name))
			}
		}
		if bad == 0 {
			fmt.Fprintln(out, "deep: every checked file matches its name")
		}
		// Same pass, different question: which stored sheets are bigger than the
		// renderer will now allocate. Unreachable files are skipped — nothing
		// renders them — and a file that cannot even be read is reported as such
		// rather than counted as oversized.
		dims := map[string][2]uint32{}
		for _, name := range names {
			if !texture.IsContentAddress(name) || usage[name] == 0 {
				continue
			}
			w, h, err := declaredSize(ts, name)
			if err != nil {
				fmt.Fprintf(out, "unreadable header: %s (%v)\n", short(name), err)
				continue
			}
			dims[name] = [2]uint32{w, h}
		}
		oversized := overRenderCeiling(dims, avatargen.MaxSkinPixels)
		fmt.Fprintf(out, "deep: checked=%d over_render_ceiling=%d (ceiling %d pixels)\n",
			len(dims), len(oversized), avatargen.MaxSkinPixels)
		listNames(out, "oversized", oversized)
		if len(oversized) > 0 {
			fmt.Fprintln(out, "note: those files are refused by /avatar, /preview and /texture-render rather than allocating their declared size; resampling one is a decision about a player's texture, so this tool only names it")
		}
	}

	if !*fix {
		fmt.Fprintf(out, "no files removed: pass --fix to unlink the %d orphan(s)\n", len(found.OrphanFiles))
		return nil
	}
	if st.Engine() == dialect.SQLite {
		// SQLite has no cross-process advisory lock, so the hash lock only holds
		// within this process. The server must be stopped for the guard to mean
		// anything (docs/deploy.md says the same for the other CLI writes).
		fmt.Fprintln(out, "warning: sqlite cannot lock across processes — stop the server before trusting this sweep")
	}
	removed := 0
	for _, hash := range found.OrphanFiles {
		if err := st.WithHashLock(ctx, hash, func(ctx context.Context) error {
			// Re-read inside the lock: an upload racing this run may have just made the
			// file reachable, and deleting it then would break a player's skin.
			usage, err := st.FSCKHashUsage(ctx)
			if err != nil {
				return err
			}
			if usage[hash] > 0 {
				return nil
			}
			return ts.Remove(hash)
		}); err != nil {
			return fmt.Errorf("unlink %s: %w", short(hash), err)
		}
		removed++
	}
	fmt.Fprintf(out, "unlinked %d orphan file(s)\n", removed)
	return nil
}

const listCap = 20

func listNames(out io.Writer, kind string, names []string) {
	for i, n := range names {
		if i == listCap {
			fmt.Fprintf(out, "%s: ... and %d more\n", kind, len(names)-listCap)
			return
		}
		fmt.Fprintf(out, "%s: %s\n", kind, n)
	}
}
