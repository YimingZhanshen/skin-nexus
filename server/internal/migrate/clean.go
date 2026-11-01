package migrate

import (
	"fmt"
	"sort"
	"strings"
)

// UuidRow mirrors the legacy yggdrasil-connect `uuid` table
// (id, name, uuid, pid optional).
type UuidRow struct {
	ID   int64
	Name string
	UUID string
	PID  int64 // 0 = unlinked (legacy mojang-verification rows)
}

// foldName lowercases/trims a profile name for matching and dedup. Target
// keys are utf8mb4_ci (PK name / uq_uuid_map_uuid), so byte-exact Go maps
// would let 'Alice'/'alice' both survive the clean and then collide at
// INSERT — one row silently vanishing (review P1, 2026-10-23).
func foldName(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// CleanUuidTable runs the five steps of legacy `yggc:fix-uuid-table`
// (analysis/blessing-skin-plugins.md §2) WITHOUT touching the source:
// executed against a snapshot; the real run writes only the cleaned result
// to the new schema (uuid_map unique keys enforce steps 1-2 structurally).
//
// Step semantics preserved:
//  1. drop duplicate names  (keep lowest id)
//  2. drop duplicate uuids  (keep lowest id)
//  3. link pid by name match against known players (backfill)
//  4. drop orphans (no pid and no player match)
//  5. uniqueness is guaranteed by the target schema (uq_uuid_map_uuid, PK name)
//
// Hardening (review A1, 2026-10-23): rows are normalized (trimmed name,
// normalizeUUID) BEFORE dedup so the dedup keys equal the target's
// uniqueness keys; name matching is ci-collation-faithful (folded) like the
// legacy tool; input is sorted by id so keep-lowest-id no longer depends on
// caller ordering; dangling pids (pointing at deleted players) drop too.
func CleanUuidTable(rows []UuidRow, players map[string]int64) (cleaned []UuidRow, report CleanReport) {
	report.Input = len(rows)

	sorted := append([]UuidRow(nil), rows...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })
	for i := range sorted {
		sorted[i].Name = strings.TrimSpace(sorted[i].Name)
		sorted[i].UUID = normalizeUUID(sorted[i].UUID)
	}

	pidKnown := map[int64]bool{}
	folded := map[string]int64{}     // caller keys are folded here — the function
	for name, pid := range players { // is self-contained regardless of key form
		folded[foldName(name)] = pid
		pidKnown[pid] = true
	}

	// Step 1: dedupe by folded name, keep lowest id.
	seenName := map[string]bool{}
	var s1 []UuidRow
	for _, r := range sorted {
		k := foldName(r.Name)
		if seenName[k] {
			report.DupNameDropped++
			continue
		}
		seenName[k] = true
		s1 = append(s1, r)
	}

	// Step 2: dedupe by normalized uuid, keep lowest id.
	seenUUID := map[string]bool{}
	var s2 []UuidRow
	for _, r := range s1 {
		if seenUUID[r.UUID] {
			report.DupUUIDDropped++
			continue
		}
		seenUUID[r.UUID] = true
		s2 = append(s2, r)
	}

	// Step 3: backfill pid from players by folded name (the legacy tool
	// matched under the ci collation, not byte-exact).
	for _, r := range s2 {
		if r.PID == 0 {
			if pid, ok := folded[foldName(r.Name)]; ok {
				r.PID = pid
				report.PIDBackfilled++
			}
		}
		cleaned = append(cleaned, r)
	}

	// Step 4: drop orphans (no pid and no player match) and dangling pids
	// referencing players that no longer exist.
	var s4 []UuidRow
	for _, r := range cleaned {
		if r.PID == 0 || !pidKnown[r.PID] {
			report.OrphansDropped++
			continue
		}
		s4 = append(s4, r)
	}
	report.Output = len(s4)
	return s4, report
}

// CleanReport is the dry-run/apply evidence for the uuid table.
type CleanReport struct {
	Input          int
	DupNameDropped int
	DupUUIDDropped int
	PIDBackfilled  int
	OrphansDropped int
	Output         int
}

func (r CleanReport) String() string {
	return fmt.Sprintf("uuid clean: in=%d dupName=%d dupUUID=%d pidBackfill=%d orphans=%d out=%d",
		r.Input, r.DupNameDropped, r.DupUUIDDropped, r.PIDBackfilled, r.OrphansDropped, r.Output)
}
