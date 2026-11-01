package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"

	"skin-nexus/server/internal/config"
	"skin-nexus/server/internal/store"
	"skin-nexus/server/internal/texture"
)

// modelDiff decides whether one content address needs work. stored is the set
// of model values its rows carry (an empty entry stands for NULL); a single
// disagreement is enough, because those rows all render the same pixels.
func modelDiff(stored []string, detected string) (string, bool) {
	old := strings.Join(stored, "/")
	for _, m := range stored {
		if m != detected {
			return old, true
		}
	}
	return old, false
}

// runRedetectModels re-reads the skin model from the stored pixels, for the
// library written before the pipeline started deciding it itself.
//
// Dry-run by default: this overwrites a column that used to come from the
// uploader, and the new value changes how the skin looks in-game, so the
// operator sees the diff before anything is written.
func runRedetectModels(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("redetect-models", flag.ContinueOnError)
	fs.SetOutput(out)
	apply := fs.Bool("apply", false, "write the detected model (default: report only)")
	limit := fs.Int("limit", 0, "process at most N skin hashes (canary run on large libraries)")
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
	groups, err := st.SkinModelGroups(ctx, *limit)
	if err != nil {
		return err
	}

	var scanned, changed, missing, failed int
	for _, g := range groups {
		scanned++
		f, err := ts.Open(g.Hash)
		if err != nil {
			missing++
			fmt.Fprintf(out, "unreadable %s: %v\n", short(g.Hash), err)
			continue
		}
		raw, readErr := io.ReadAll(io.LimitReader(f, texture.MaxUploadBytes+1))
		f.Close()
		if readErr != nil {
			failed++
			fmt.Fprintf(out, "read failed %s: %v\n", short(g.Hash), readErr)
			continue
		}
		detected, err := texture.DetectModelFromBytes(raw)
		if err != nil {
			failed++
			fmt.Fprintf(out, "not a decodable PNG %s: %v\n", short(g.Hash), err)
			continue
		}
		old, differs := modelDiff(g.Models, detected)
		if !differs {
			continue
		}
		changed++
		if !*apply {
			fmt.Fprintf(out, "%s %s -> %s\n", short(g.Hash), old, detected)
			continue
		}
		n, err := st.SetModelByHash(ctx, g.Hash, detected)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "%s %s -> %s (%d rows)\n", short(g.Hash), old, detected, n)
	}

	fmt.Fprintf(out, "redetect-models: scanned=%d need_change=%d unreadable=%d decode_failed=%d applied=%t\n",
		scanned, changed, missing, failed, *apply)
	if changed > 0 && !*apply {
		fmt.Fprintln(out, "dry run: nothing was written. Re-run with --apply to correct these rows;")
		fmt.Fprintln(out, "the model drives in-game arm width, /avatar, /preview and the steve/alex filter,")
		fmt.Fprintln(out, "so players will see the corrected skins change appearance.")
	}
	return nil
}

func short(hash string) string {
	if len(hash) > 12 {
		return hash[:12]
	}
	return hash
}
