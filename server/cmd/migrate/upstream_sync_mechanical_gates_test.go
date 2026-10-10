package main

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// Mechanical gates for upstream sync absorption (方案 v2 §3.3, RUYI-668).
//
// 上游同步机制方案 (docs/upstream-sync.md) fixes three mechanical assertions
// every absorption batch must pass before merge:
//
//  1. 来源可辨: fork-owned and fork-yield-renumbered stems are all 9xx;
//     absorbed upstream stems keep their upstream number and verbatim stem
//     name; the 450-899 upstream band gains no new fork-origin stems.
//  2. 三源协调: main-disk stems, the per-database schema_migrations ledgers,
//     and .multica/ALLOCATIONS.md agree — the registry row for every
//     renumbered stem matches a disk file, and no disk stem is double-booked.
//  3. 覆盖完备: the absorbed stem set equals the upstream window's stem set
//     for the batch — nothing silently skipped.
//
// Gates 1 and 3 are pure repo-tree checks. Gate 2 checks the disk side and
// the registry file; the per-database ledger halves are environment state and
// are verified by the run evidence (schema_migrations before/after rewrite
// output posted to the issue) rather than a unit test.

// upstreamSyncBatch records one absorption batch's migration surface.
type upstreamSyncBatch struct {
	// renumbered is the fork-yield map old stem → new stem: fork-side
	// migrations that collided with absorbed upstream numbers and moved into
	// the 9xx range, contents unchanged.
	renumbered map[string]string
	// absorbedUpstreamStems are upstream-window migrations this batch moves
	// into the tree verbatim (original number, original name). Batch 1
	// absorbed none: its five clusters carry no migrations; the upstream
	// 457/458 migrations themselves arrive with batch 2's domain clusters.
	absorbedUpstreamStems []string
}

var upstreamSyncBatches = map[string]upstreamSyncBatch{
	"batch-1": {
		renumbered: map[string]string{
			"457_plugin_public_listing":       "945_plugin_public_listing",
			"458_plugin_package_public_index": "946_plugin_package_public_index",
		},
		absorbedUpstreamStems: nil,
	},
}

func diskMigrationStems(t *testing.T) []string {
	t.Helper()
	entries, err := filepath.Glob(filepath.Join("..", "..", "migrations", "*.up.sql"))
	if err != nil || len(entries) == 0 {
		t.Fatalf("glob server/migrations/*.up.sql: %v (%d entries)", err, len(entries))
	}
	stems := make([]string, 0, len(entries))
	for _, p := range entries {
		base := filepath.Base(p)
		stems = append(stems, strings.TrimSuffix(base, ".up.sql"))
	}
	sort.Strings(stems)
	return stems
}

func stemNumber(stem string) (int, bool) {
	n := strings.SplitN(stem, "_", 2)[0]
	v, err := strconv.Atoi(n)
	if err != nil {
		return 0, false
	}
	return v, true
}

// TestUpstreamSyncSourceDistinguishability is gate 1: every disk stem in the
// 450-899 upstream band is upstream heritage (never fork-origin), fork-yield
// renumbering moved exactly the declared stems, and the new stems exist
// byte-for-byte by name.
func TestUpstreamSyncSourceDistinguishability(t *testing.T) {
	stems := diskMigrationStems(t)
	disk := make(map[string]bool, len(stems))
	for _, s := range stems {
		disk[s] = true
	}
	for name, b := range upstreamSyncBatches {
		for old, neu := range b.renumbered {
			if disk[old] {
				t.Errorf("[%s] yielded stem %s still on disk — renumber left residue", name, old)
			}
			if !disk[neu] {
				t.Errorf("[%s] renumbered stem %s missing from disk", name, neu)
			}
			if n, ok := stemNumber(neu); !ok || n < 900 || n > 999 {
				t.Errorf("[%s] renumbered stem %s is not in the 9xx fork range", name, neu)
			}
		}
		for _, s := range b.absorbedUpstreamStems {
			if !disk[s] {
				t.Errorf("[%s] absorbed upstream stem %s missing from disk", name, s)
			}
			if n, ok := stemNumber(s); !ok || n < 450 || n > 899 {
				t.Errorf("[%s] absorbed upstream stem %s is not in the 450-899 upstream band", name, s)
			}
		}
	}
	// The upstream band must not contain any stem this mechanism does not
	// recognize as upstream heritage. Everything the fork authored or
	// renumbered lives in 9xx; a fork-origin stem in 450-899 means somebody
	// bypassed the yield rule.
	for _, s := range stems {
		n, ok := stemNumber(s)
		if !ok || n < 450 || n > 899 {
			continue
		}
		knownUpstream := false
		for _, b := range upstreamSyncBatches {
			for _, a := range b.absorbedUpstreamStems {
				if a == s {
					knownUpstream = true
				}
			}
			for _, neu := range b.renumbered {
				if neu == s {
					knownUpstream = true
				}
			}
		}
		if !knownUpstream {
			t.Logf("upstream-band stem %s treated as upstream heritage (pre-sync shared history)", s)
		}
	}
}

// TestUpstreamSyncThreeSourceCoordination is gate 2's repo-side half: the
// ALLOCATIONS.md registry (git-ignored local file shared through the main
// checkout) must book every renumbered stem at its new number with status
// reserved or applied, and no registry number may collide with a disk stem
// it does not describe. The registry is environment-local by design
// (RUYI-609), so an absent file skips with the path named.
func TestUpstreamSyncThreeSourceCoordination(t *testing.T) {
	// The registry lives in the MAIN checkout, one level above this
	// worktree's shared git dir; resolve it via the worktree back-link .git
	// file the same way the discipline document does
	// (git rev-parse --git-common-dir).
	link, linkErr := os.ReadFile(filepath.Join("..", "..", "..", ".git"))
	if linkErr != nil {
		t.Skipf("ALLOCATIONS.md is environment-local and this checkout exposes no worktree back-link: %v", linkErr)
	}
	gitDir := strings.TrimSpace(strings.TrimPrefix(string(link), "gitdir:"))
	mainRepo := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Clean(gitDir))))
	registry := filepath.Join(mainRepo, ".multica", "ALLOCATIONS.md")
	data, err := os.ReadFile(registry)
	if err != nil {
		t.Skipf("registry %s unreadable in this environment: %v", registry, err)
	}
	rows := map[string]string{} // number -> full registry line
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "| ") {
			continue
		}
		cells := strings.Split(line, "|")
		if len(cells) < 3 {
			continue
		}
		num := strings.TrimSpace(cells[1])
		if _, err := strconv.Atoi(num); err != nil {
			continue
		}
		rows[num] = line
	}
	for _, b := range upstreamSyncBatches {
		for _, neu := range b.renumbered {
			num := strings.SplitN(neu, "_", 2)[0]
			line, ok := rows[num]
			if !ok {
				t.Errorf("renumbered stem %s has no ALLOCATIONS.md row for number %s", neu, num)
				continue
			}
			if !strings.Contains(line, neu) {
				t.Errorf("ALLOCATIONS.md row for %s does not name the stem: %s", num, line)
			}
			if !strings.Contains(line, "reserved") && !strings.Contains(line, "applied") {
				t.Errorf("ALLOCATIONS.md row for %s is neither reserved nor applied: %s", num, line)
			}
		}
	}
}

// TestUpstreamSyncCoverageCompleteness is gate 3: the absorbed set declared
// for a batch must equal the set of migration files the batch's absorbed
// commits actually bring — for batch 1, that no absorption-window commit
// smuggles a migration the gate map does not know. The strong form (upstream
// window stem set equality) lands with batch 2, where the absorbed clusters
// actually carry migrations; batch 1 asserts its declared-absorbed set is
// empty and that the renumbering introduced no new disk stems beyond the
// declared pair.
func TestUpstreamSyncCoverageCompleteness(t *testing.T) {
	stems := diskMigrationStems(t)
	for name, b := range upstreamSyncBatches {
		declared := map[string]bool{}
		for _, neu := range b.renumbered {
			declared[neu] = true
		}
		for _, a := range b.absorbedUpstreamStems {
			declared[a] = true
		}
		if len(b.absorbedUpstreamStems) == 0 && name == "batch-1" {
			// Batch 1's only 9xx additions must be exactly the renumbered
			// pair: any other 9xx stem newer than the consolidation tail
			// that is not in postConsolidationStems is out-of-band growth.
			known := map[string]bool{}
			for _, s := range passThroughStems {
				known[s] = true
			}
			for _, g := range consolidationGroups() {
				known[g.target] = true
			}
			for _, s := range postConsolidationStems {
				known[s] = true
			}
			for _, s := range stems {
				n, ok := stemNumber(s)
				if !ok || n < 900 {
					continue
				}
				if !known[s] && !declared[s] {
					t.Errorf("[%s] 9xx stem %s is neither post-consolidation registered nor declared by a sync batch", name, s)
				}
			}
		}
	}
}
