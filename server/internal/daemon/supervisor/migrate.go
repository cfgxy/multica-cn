package supervisor

import (
	"log/slog"
	"os"
	"path/filepath"
)

// MigrationResult reports one legacy-store convergence pass, for the audit
// log line.
type MigrationResult struct {
	// Moved lists the run IDs adopted into this daemon's namespaced store.
	Moved []string
	// Left lists the run IDs found in the legacy root but deliberately not
	// moved: foreign-owned, unattributable, or already converged. Entries
	// with no readable run directory at all (foreign namespace segments,
	// quarantine debris) are invisible and listed in neither field.
	Left []string
}

// MigrateLegacyStore converges the pre-RUYI-607 shared run store (one flat
// directory holding every profile's runs) into this daemon's namespaced
// store, adopting ONLY the runs this daemon can prove it launched: the run
// carries a readable manifest, its task is in this daemon's own server
// in-flight set (task ids are server-scoped, so in-flight on my runtimes
// means launched by me), and no namespaced run already occupies the target
// ID. Everything else stays exactly where it is — the one thing migration
// must never do is move or mutate another daemon's evidence (RUYI-607
// acceptance: the upgrade window neither mis-kills nor drops tracking).
//
// Ordering is crash-safe: the owner stamp is written inside the legacy
// directory first, then the directory is renamed into the namespace; a crash
// between the two leaves an owner-stamped run in the legacy root, which the
// next pass re-adopts by the same attribution. A legacy directory with no
// top-level manifest.json is not a run directory at all (a foreign
// namespace segment, a quarantine-only shell) and is skipped unread.
func MigrateLegacyStore(legacyRoot string, mgr *Manager, owner string, owns func(taskID string) bool, log *slog.Logger) (MigrationResult, error) {
	res := MigrationResult{}
	entries, err := os.ReadDir(legacyRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return res, nil // no legacy store: nothing to converge
		}
		return res, err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		manPath := filepath.Join(legacyRoot, e.Name(), "manifest.json")
		if _, err := os.Stat(manPath); err != nil {
			continue // no top-level manifest: not a run directory
		}
		var man Manifest
		if err := readJSONFile(manPath, &man); err != nil {
			res.Left = append(res.Left, e.Name()) // corrupt evidence: leave for its owner
			continue
		}
		if man.Owner != "" && man.Owner != owner {
			res.Left = append(res.Left, e.Name())
			continue
		}
		if owns == nil || !owns(man.TaskID) {
			res.Left = append(res.Left, e.Name()) // no proof this daemon launched it
			continue
		}
		if _, err := os.Stat(mgr.Dir(e.Name())); err == nil {
			res.Left = append(res.Left, e.Name()) // already converged by a previous pass
			continue
		}
		man.Owner = owner
		if err := writeJSONFile(manPath, &man, 0o600); err != nil {
			res.Left = append(res.Left, e.Name())
			continue
		}
		if err := os.Rename(filepath.Join(legacyRoot, e.Name()), mgr.Dir(e.Name())); err != nil {
			res.Left = append(res.Left, e.Name())
			continue
		}
		res.Moved = append(res.Moved, e.Name())
	}
	if log != nil && (len(res.Moved) > 0 || len(res.Left) > 0) {
		log.Info("supervisor legacy run store migration",
			"legacy_root", legacyRoot, "owner", owner,
			"moved", res.Moved, "left", res.Left)
	}
	return res, nil
}
