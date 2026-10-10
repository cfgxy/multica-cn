package daemon

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
)

// RecycleGuardDryScan walks a workspaces root and runs the recycle guard's
// scan over every candidate the GC could ever recycle — each task directory
// and each bare repo cache under .repos — without deleting anything. It is
// the engine behind `multica daemon gc --dry-run`.
//
// A live daemon skips active task dirs; this walk has no view of the
// daemon's activity set, so it scans everything and the caller's report says
// so: a pass verdict here means "the guard would not block this recycle",
// not "the daemon will recycle it now". Scan failures fail closed inside the
// scan (verdict blocked), so a corrupt repo reads as blocked, never empty.
func RecycleGuardDryScan(workspacesRoot string) ([]*execenv.RecycleScan, error) {
	var scans []*execenv.RecycleScan
	entries, err := os.ReadDir(workspacesRoot)
	if err != nil {
		return nil, err
	}
	scanTarget := func(scan func(context.Context) *execenv.RecycleScan) {
		ctx, cancel := context.WithTimeout(context.Background(), execenv.GuardScanTimeout)
		defer cancel()
		scans = append(scans, scan(ctx))
	}

	for _, ws := range entries {
		if !ws.IsDir() || strings.HasPrefix(ws.Name(), ".") {
			continue
		}
		wsDir := filepath.Join(workspacesRoot, ws.Name())
		tasks, err := os.ReadDir(wsDir)
		if err != nil {
			continue
		}
		for _, t := range tasks {
			if !t.IsDir() {
				continue
			}
			taskDir := filepath.Join(wsDir, t.Name())
			scanTarget(func(ctx context.Context) *execenv.RecycleScan {
				scan := execenv.ScanTaskDirForRecycle(ctx, taskDir)
				scan.DryRun = true
				return scan
			})
		}
	}

	// Bare repo caches: same layout pruneRepoWorktreesContext walks.
	reposRoot := filepath.Join(workspacesRoot, reposDirName)
	wsEntries, err := os.ReadDir(reposRoot)
	if err != nil {
		return scans, nil
	}
	for _, wsEntry := range wsEntries {
		if !wsEntry.IsDir() {
			continue
		}
		wsRepoDir := filepath.Join(reposRoot, wsEntry.Name())
		repoEntries, err := os.ReadDir(wsRepoDir)
		if err != nil {
			continue
		}
		for _, repoEntry := range repoEntries {
			if !repoEntry.IsDir() {
				continue
			}
			barePath := filepath.Join(wsRepoDir, repoEntry.Name())
			if !isBareRepo(barePath) {
				continue
			}
			scanTarget(func(ctx context.Context) *execenv.RecycleScan {
				rootScan := execenv.ScanBareRepoForRecycle(ctx, barePath)
				return &execenv.RecycleScan{
					Target:    barePath,
					Kind:      execenv.RecycleKindBareRepo,
					DryRun:    true,
					Verdict:   rootScan.Verdict,
					Reasons:   rootScan.Reasons,
					Roots:     []execenv.RecycleRootScan{rootScan},
					ScannedAt: time.Now().UTC(),
				}
			})
		}
	}
	return scans, nil
}

// RecycleGuardDryScanSummary folds a dry scan into headline numbers.
func RecycleGuardDryScanSummary(scans []*execenv.RecycleScan) (blocked, evidence, passed int) {
	for _, s := range scans {
		switch s.Verdict {
		case execenv.RecycleBlocked:
			blocked++
		case execenv.RecycleEvidence:
			evidence++
		default:
			passed++
		}
	}
	return blocked, evidence, passed
}

// DescribeRecycleScan renders one scan as a report line for text output.
func DescribeRecycleScan(s *execenv.RecycleScan) string {
	reasons := s.Reasons
	if len(reasons) == 0 {
		for i := range s.Roots {
			reasons = append(reasons, s.Roots[i].Reasons...)
		}
	}
	joined := strings.Join(reasons, "; ")
	if joined == "" {
		joined = "-"
	}
	return fmt.Sprintf("%s\t%s\t%s\t%s", s.Target, s.Kind, s.Verdict, joined)
}
