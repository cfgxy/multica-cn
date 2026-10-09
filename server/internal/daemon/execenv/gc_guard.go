package execenv

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// guardLinkedDirModes are the mode bits marking a directory entry as a link
// the scan must not descend through (see the daemon package's linkedDirModes
// for the junction rationale — the same Windows behaviour applies here).
const guardLinkedDirModes = os.ModeSymlink | os.ModeIrregular

// GuardScanTimeout bounds one whole guard scan (a handful of git calls). The
// GC paths already tolerate multi-minute git maintenance, and a blocked scan
// must win over a fast delete.
const GuardScanTimeout = 10 * time.Minute

// Recycle guard: scan-before-delete for every path that removes a git-bearing
// directory. Each scan classifies the deletion into one of three verdicts:
//
//	pass     — nothing the deletion could lose (L0): proceed unchanged.
//	evidence — the root holds unpushed commits, but their references survive
//	           outside the deletion scope (L1, e.g. a linked worktree whose
//	           branch lives in the shared .git): record what was there, proceed.
//	blocked  — the deletion would kill the last reference to something (L2):
//	           refuse the recycle, keep the scene, leave evidence for a human.
//
// The predicates deliberately mirror scripts/lib-recycle-guard.sh and the
// agent-branch-cleanup.sh "never delete unpushed work" stance: git log/rev-list
// against --branches/--remotes, plus stash and dirty-state checks. A commit the
// daemon would strand is worth one subprocess; this file is the single Go home
// for that judgement (scripts share one bash file the same way), so a revert
// removes the whole behaviour at once.
//
// Evidence files land OUTSIDE every deletion scope, in a dot directory under
// the workspaces root (.recycle-evidence) that no GC walk descends into: the
// workspace walk skips dot-prefixed entries, and task-dir walks only descend
// into per-workspace directories. They must outlive the directories they
// describe — a blocked recycle keeps its dir, an L1 recycle does not, and in
// both cases the evidence is what answers "what was scanned, and what would
// have been lost" after the fact (RUYI-594).

const (
	// RecyclePass / RecycleEvidence / RecycleBlocked are the three verdicts
	// every recycle path routes on. They double as the guard= log field values.
	RecyclePass     = "pass"
	RecycleEvidence = "evidence"
	RecycleBlocked  = "blocked"

	// Recycle topologies. linked: .git is a file pointing into a surviving
	// git dir (a linked worktree). standalone: .git is a directory inside the
	// deletion scope. bare: the root IS a git dir (repo cache). none: the
	// target holds no git root at the top level (may still hold nested ones).
	RecycleTopologyLinked     = "linked"
	RecycleTopologyStandalone = "standalone"
	RecycleTopologyBare       = "bare"
	RecycleTopologyNone       = "none"

	// Scan kinds, recorded in evidence and filenames.
	RecycleKindTaskDir  = "task-dir"
	RecycleKindWorktree = "worktree"
	RecycleKindBareRepo = "bare-cache"

	// recycleEvidenceListCap bounds every predicate list in evidence. A human
	// reading a blocked recycle needs the shape of the loss, not every sha of
	// a 10k-commit orphaned fork.
	recycleEvidenceListCap = 50

	// recycleNestedGitDepth bounds the search for nested repositories inside a
	// task dir. Nested clones are a real loss surface (cleanTaskArtifacts
	// skips .git subtrees, but the whole-dir removal does not), while an
	// unbounded find would race the agent for the whole tree.
	recycleNestedGitDepth = 4

	// DefaultRecycleEvidenceTTL is how long evidence files are kept. It only
	// bounds the guard's own sweeper — nothing else deletes from the evidence
	// directory, and the GC paths that could reach it skip dot directories by
	// construction, so files younger than this TTL always survive.
	DefaultRecycleEvidenceTTL = 30 * 24 * time.Hour
)

// RecyclePredicate is one measured question about a git root: the command as
// run (verbatim, for after-the-fact reproduction), its exit code, and what it
// found. An NA predicate does not apply to this root (e.g. rev-list on an
// unborn HEAD); a failed one (non-zero exit, not NA) fails the whole scan
// closed — an unreadable repo must not read as an empty one.
type RecyclePredicate struct {
	Name        string   `json:"name"`
	Command     string   `json:"command"`
	ExitCode    int      `json:"exit_code"`
	Count       int      `json:"count"`
	List        []string `json:"list,omitempty"`
	ListCommand string   `json:"list_command,omitempty"`
	Truncated   bool     `json:"truncated,omitempty"`
	NA          bool     `json:"na,omitempty"`
	Error       string   `json:"error,omitempty"`
}

// RecycleRootScan is the scan of one git root: how it is wired into the
// repository graph, what the predicates found, and what this deletion would
// do to it.
type RecycleRootScan struct {
	Root       string             `json:"root"`
	Topology   string             `json:"topology"`
	Predicates []RecyclePredicate `json:"predicates"`
	Verdict    string             `json:"verdict"`
	Reasons    []string           `json:"reasons,omitempty"`
}

// RecycleScan is the evidence record for one recycle decision — one file, one
// deletion scope, one or more git roots.
type RecycleScan struct {
	Target    string            `json:"target"`
	Kind      string            `json:"kind"`
	DryRun    bool              `json:"dry_run,omitempty"`
	Override  bool              `json:"override,omitempty"`
	Verdict   string            `json:"verdict"`
	Reasons   []string          `json:"reasons,omitempty"`
	Roots     []RecycleRootScan `json:"roots"`
	ScannedAt time.Time         `json:"scanned_at"`
	Error     string            `json:"error,omitempty"`

	// EvidencePath is filled by the writer after a successful write so the
	// caller can log where the record landed. It is not serialized into the
	// file itself.
	EvidencePath string `json:"-"`
}

// WorstVerdict folds root verdicts into the scope verdict: any blocked blocks;
// else any evidence leaves evidence; else pass.
func WorstVerdict(verdicts ...string) string {
	worst := RecyclePass
	for _, v := range verdicts {
		switch v {
		case RecycleBlocked:
			return RecycleBlocked
		case RecycleEvidence:
			worst = RecycleEvidence
		}
	}
	return worst
}

// RecycleGuardConfig controls the execenv-side hooks. The daemon installs it
// from its Config at startup; tests install their own. Zero value is a fully
// disabled guard, so every pre-existing test and caller keeps today's
// behaviour unless something opts in.
type RecycleGuardConfig struct {
	Enabled     bool
	EvidenceDir string        // outside every deletion scope
	EvidenceTTL time.Duration // sweeper-only bound; 0 means DefaultRecycleEvidenceTTL
}

var recycleGuardMu sync.RWMutex
var recycleGuardConf RecycleGuardConfig

// ConfigureRecycleGuard installs the process-wide guard configuration. Best
// called once at startup; the daemon and the daemon gc CLI both do.
func ConfigureRecycleGuard(cfg RecycleGuardConfig) {
	recycleGuardMu.Lock()
	defer recycleGuardMu.Unlock()
	if cfg.EvidenceTTL <= 0 {
		cfg.EvidenceTTL = DefaultRecycleEvidenceTTL
	}
	recycleGuardConf = cfg
}

// CurrentRecycleGuardConfig returns the installed configuration.
func CurrentRecycleGuardConfig() RecycleGuardConfig {
	recycleGuardMu.RLock()
	defer recycleGuardMu.RUnlock()
	return recycleGuardConf
}

// bareFetchTimeout bounds the one network call the bare-cache scan makes.
// Repo maintenance already tolerates multi-minute git operations, and a
// blocked eviction only costs a retry on the next idle cycle.
const bareFetchTimeout = 2 * time.Minute

// runGitPredicateTimeout is runGitPredicate with a non-default timeout.
func runGitPredicateTimeout(ctx context.Context, dir string, timeout time.Duration, args ...string) (string, int, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmdArgs := append([]string{"-C", dir}, args...)
	cmd := exec.CommandContext(ctx, "git", cmdArgs...)
	cmd.Env = append(gitMessageEnv(), "GIT_OPTIONAL_LOCKS=0")
	cmd.WaitDelay = 5 * time.Second
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		code = -1
		var exitErr *exec.ExitError
		if asExit(err, &exitErr) {
			code = exitErr.ExitCode()
		}
	}
	return string(out), code, err
}

// runGitPredicate runs one git predicate and reports combined output plus the
// process exit code. A missing binary or a killed process yields exit -1 with
// the Go error text — still a failure, still fail-closed.
func runGitPredicate(ctx context.Context, dir string, args ...string) (string, int, error) {
	ctx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()

	cmdArgs := append([]string{"-C", dir}, args...)
	cmd := exec.CommandContext(ctx, "git", cmdArgs...)
	cmd.Env = append(gitMessageEnv(), "GIT_OPTIONAL_LOCKS=0")
	cmd.WaitDelay = 5 * time.Second
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		code = -1
		var exitErr *exec.ExitError
		if asExit(err, &exitErr) {
			code = exitErr.ExitCode()
		}
	}
	return string(out), code, err
}

func asExit(err error, target **exec.ExitError) bool {
	if e, ok := err.(*exec.ExitError); ok {
		*target = e
		return true
	}
	return false
}

// capLines trims a predicate output into a capped list and reports whether
// anything was cut.
func capLines(out string, cap int) ([]string, bool, int) {
	trimmed := strings.TrimSpace(out)
	if trimmed == "" {
		return nil, false, 0
	}
	lines := strings.Split(trimmed, "\n")
	total := len(lines)
	truncated := false
	if total > cap {
		lines = lines[:cap]
		truncated = true
	}
	return lines, truncated, total
}

// unbornHead reports whether a rev-list failure means "no commits yet" — an
// empty repo has nothing to lose, which is a measurement, not a scan failure.
func unbornHead(out string) bool {
	o := strings.ToLower(out)
	return strings.Contains(o, "unknown revision") ||
		strings.Contains(o, "bad revision") ||
		strings.Contains(o, "does not have any commits yet")
}

// scanHeadRoot runs the HEAD-bearing predicate suite on a linked or standalone
// git root and routes the verdict:
//
//   - sole-ref commits (reachable from HEAD but from no branch or remote) —
//     the deletion kills their last reference: blocked.
//   - stash entries in a standalone root — the stash ref lives in the git dir
//     that dies with the deletion: blocked. In a linked root the shared .git
//     survives, so stashes are recorded but do not block (topology-aware
//     stash routing, RUYI-594 review note).
//   - uncommitted tracked changes — work recorded nowhere: blocked. Untracked
//     entries are counted for evidence only; task dirs accumulate build
//     output and logs, and blocking on those would teach operators to disable
//     the guard (dirty routing, RUYI-594 dispatch decision).
//   - any other unpushed commits — linked roots only: the branch refs live
//     in the surviving shared git dir, so the commits survive: evidence.
//     A standalone root holds its own git dir, so unpushed commits die with
//     the deletion: blocked.
func scanHeadRoot(ctx context.Context, root string, standalone bool) RecycleRootScan {
	scan := RecycleRootScan{Root: root, Verdict: RecyclePass}

	if standalone {
		scan.Topology = RecycleTopologyStandalone
	} else {
		scan.Topology = RecycleTopologyLinked
	}

	// Sole-reference commits: the deletion would strand them.
	soleCmd := []string{"rev-list", "--count", "HEAD", "--not", "--branches", "--remotes"}
	sole := RecyclePredicate{Name: "sole_ref"}
	if out, code, err := runGitPredicate(ctx, root, soleCmd...); err != nil && code != 0 {
		if code < 0 || !unbornHead(out) {
			scan.fail(&sole, soleCmd, code, out, err)
			return scan.blocked("git scan failed; refusing to read a broken repo as an empty one")
		}
		sole.NA = true
		sole.Command = strings.Join(append([]string{"git", "-C", root}, soleCmd...), " ")
		sole.Error = strings.TrimSpace(out)
	} else {
		sole.Command = strings.Join(append([]string{"git", "-C", root}, soleCmd...), " ")
		sole.ExitCode = code
		sole.Count, _ = strconv.Atoi(strings.TrimSpace(out))
		if sole.Count > 0 {
			listCmd := []string{"rev-list", "HEAD", "--not", "--branches", "--remotes"}
			list := RecyclePredicate{Name: "sole_ref_list"}
			fillList(ctx, root, &list, listCmd)
			scan.Predicates = append(scan.Predicates, list)
			scan.blocked(fmt.Sprintf("%d commit(s) would lose their last reference", sole.Count))
		}
	}
	scan.Predicates = append(scan.Predicates, sole)

	// Unpushed commits: survive somewhere outside this deletion, or not.
	unpushedCmd := []string{"rev-list", "--count", "HEAD", "--not", "--remotes"}
	unpushed := RecyclePredicate{Name: "unpushed"}
	if out, code, err := runGitPredicate(ctx, root, unpushedCmd...); err != nil && code != 0 {
		if code < 0 || !unbornHead(out) {
			scan.fail(&unpushed, unpushedCmd, code, out, err)
			return scan.blocked("git scan failed; refusing to read a broken repo as an empty one")
		}
		unpushed.NA = true
		unpushed.Command = strings.Join(append([]string{"git", "-C", root}, unpushedCmd...), " ")
		unpushed.Error = strings.TrimSpace(out)
	} else {
		unpushed.Command = strings.Join(append([]string{"git", "-C", root}, unpushedCmd...), " ")
		unpushed.ExitCode = code
		unpushed.Count, _ = strconv.Atoi(strings.TrimSpace(out))
		if unpushed.Count > 0 {
			listCmd := []string{"rev-list", "HEAD", "--not", "--remotes"}
			list := RecyclePredicate{Name: "unpushed_list"}
			fillList(ctx, root, &list, listCmd)
			scan.Predicates = append(scan.Predicates, list)
		}
	}
	scan.Predicates = append(scan.Predicates, unpushed)

	// Stash entries, routed by topology: they die only with the git dir.
	stash := scanPredicate(ctx, root, "stash", []string{"stash", "list"})
	scan.Predicates = append(scan.Predicates, stash)
	switch {
	case stash.ExitCode != 0:
		return scan.blocked("git scan failed; refusing to read a broken repo as an empty one")
	case stash.Count > 0 && standalone:
		scan.blocked(fmt.Sprintf("%d stash entrie(s) live in the git dir being deleted", stash.Count))
	case stash.Count > 0:
		scan.Reasons = append(scan.Reasons,
			fmt.Sprintf("%d stash entrie(s) survive in the shared git dir outside the deletion scope", stash.Count))
	}

	// Dirty state: tracked changes are unrecorded work; untracked entries are
	// noise (build output, logs) and evidence-only.
	dirty := scanPredicate(ctx, root, "dirty_tracked", []string{"status", "--porcelain", "--untracked-files=no"})
	scan.Predicates = append(scan.Predicates, dirty)
	if dirty.ExitCode != 0 {
		return scan.blocked("git scan failed; refusing to read a broken repo as an empty one")
	}
	if dirty.Count > 0 {
		scan.blocked(fmt.Sprintf("%d tracked path(s) with uncommitted changes", dirty.Count))
	}

	untracked := scanPredicate(ctx, root, "untracked", []string{"status", "--porcelain"})
	scan.Predicates = append(scan.Predicates, untracked)
	if untracked.ExitCode != 0 {
		return scan.blocked("git scan failed; refusing to read a broken repo as an empty one")
	}
	if untracked.Count > 0 && untracked.Count != dirty.Count {
		scan.Reasons = append(scan.Reasons,
			fmt.Sprintf("%d untracked entrie(s) recorded for evidence only", untracked.Count))
	}

	if unpushed.Count > 0 && scan.Verdict == RecyclePass {
		if standalone {
			scan.blocked(fmt.Sprintf("%d unpushed commit(s); the git dir holding them is deleted with this directory", unpushed.Count))
		} else {
			scan.Verdict = RecycleEvidence
			scan.Reasons = append(scan.Reasons,
				fmt.Sprintf("%d unpushed commit(s); their refs survive in the shared git dir outside the deletion scope", unpushed.Count))
		}
	}
	return scan
}

// scanBareRoot runs the bare-repo predicate suite. A bare cache has no HEAD
// semantics: the eviction deletes the whole git dir, so every commit not on
// the remote dies with it.
//
// A bare clone carries no remote-tracking refs of its own — refs/heads/* IS
// the clone-time snapshot of the remote — so the textbook
// `rev-list --branches --not --remotes` counts every commit in a pristine
// cache and would block every eviction. The scan therefore fetches the
// remote into refs/remotes/origin/* first and compares against that. A
// fetch failure (offline, remote gone) fails closed: the eviction is
// blocked with the reason recorded, and the next idle cycle retries.
// Stashes cannot exist in a bare repo (git refuses without a worktree), so
// the stash predicate is recorded NA rather than run.
func scanBareRoot(ctx context.Context, barePath string) RecycleRootScan {
	scan := RecycleRootScan{Root: barePath, Topology: RecycleTopologyBare, Verdict: RecyclePass}

	fetchCmd := []string{"fetch", "--prune", "--no-tags", "origin", "+refs/heads/*:refs/remotes/origin/*"}
	fetch := RecyclePredicate{Name: "fetch_remote_refs"}
	out, code, err := runGitPredicateTimeout(ctx, barePath, bareFetchTimeout, fetchCmd...)
	fetch.Command = strings.Join(append([]string{"git", "-C", barePath}, fetchCmd...), " ")
	fetch.ExitCode = code
	if err != nil && code != 0 {
		fetch.Error = strings.TrimSpace(out)
		if fetch.Error == "" && err != nil {
			fetch.Error = err.Error()
		}
		scan.Predicates = append(scan.Predicates, fetch)
		return scan.blocked("remote unreachable; cannot prove eviction would lose nothing")
	}
	scan.Predicates = append(scan.Predicates, fetch)

	branches := RecyclePredicate{Name: "bare_unpushed_branches"}
	branchCmd := []string{"rev-list", "--count", "--branches", "--not", "--remotes"}
	out, code, err = runGitPredicate(ctx, barePath, branchCmd...)
	if err != nil && code != 0 {
		scan.fail(&branches, branchCmd, code, out, err)
		scan.Predicates = append(scan.Predicates, branches)
		return scan.blocked("git scan failed; refusing to read a broken repo as an empty one")
	}
	branches.Command = strings.Join(append([]string{"git", "-C", barePath}, branchCmd...), " ")
	branches.ExitCode = code
	branches.Count, _ = strconv.Atoi(strings.TrimSpace(out))
	if branches.Count > 0 {
		list := RecyclePredicate{Name: "bare_unpushed_branches_list"}
		fillList(ctx, barePath, &list, []string{"rev-list", "--branches", "--not", "--remotes"})
		scan.Predicates = append(scan.Predicates, list)
	}
	scan.Predicates = append(scan.Predicates, branches)

	stash := RecyclePredicate{
		Name:     "stash",
		Command:  strings.Join(append([]string{"git", "-C", barePath}, "stash", "list"), " "),
		ExitCode: 0,
		NA:       true,
		Error:    "bare repositories cannot hold stashes (git requires a worktree); nothing to count",
	}
	scan.Predicates = append(scan.Predicates, stash)

	if branches.Count > 0 {
		scan.blocked(fmt.Sprintf("%d commit(s) on local branches are not on any remote", branches.Count))
	}
	return scan
}

// scanPredicate runs one list-shaped predicate and fills name/command/exit/
// count/list in one go.
func scanPredicate(ctx context.Context, dir, name string, args []string) RecyclePredicate {
	p := RecyclePredicate{Name: name}
	out, code, err := runGitPredicate(ctx, dir, args...)
	p.Command = strings.Join(append([]string{"git", "-C", dir}, args...), " ")
	if err != nil && code != 0 {
		p.ExitCode = code
		p.Error = strings.TrimSpace(out)
		return p
	}
	p.ExitCode = code
	p.List, p.Truncated, p.Count = capLines(out, recycleEvidenceListCap)
	return p
}

// fillList runs a list command into a capped predicate list. A list failure
// keeps the counting predicate's verdict — the count is already evidence —
// and records the error.
func fillList(ctx context.Context, dir string, p *RecyclePredicate, args []string) {
	p.Command = strings.Join(append([]string{"git", "-C", dir}, args...), " ")
	out, code, err := runGitPredicate(ctx, dir, args...)
	p.ExitCode = code
	if err != nil && code != 0 {
		p.Error = strings.TrimSpace(out)
		return
	}
	p.List, p.Truncated, _ = capLines(out, recycleEvidenceListCap)
}

func (s *RecycleRootScan) fail(p *RecyclePredicate, args []string, code int, out string, err error) {
	p.Command = strings.Join(append([]string{"git", "-C", s.Root}, args...), " ")
	p.ExitCode = code
	p.Error = strings.TrimSpace(out)
	if p.Error == "" && err != nil {
		p.Error = err.Error()
	}
}

func (s *RecycleRootScan) blocked(reason string) RecycleRootScan {
	s.Verdict = RecycleBlocked
	s.Reasons = append(s.Reasons, reason)
	return *s
}

// detectTopology classifies a directory's git wiring from the .git entry
// itself: file = linked worktree, dir = standalone repo, missing = not a git
// root (nested-repo scan covers deeper roots).
func detectTopology(dir string) string {
	fi, err := os.Lstat(filepath.Join(dir, ".git"))
	if err != nil {
		return RecycleTopologyNone
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		// A .git symlink behaves like a .git file (gitdir pointer).
		return RecycleTopologyLinked
	}
	if fi.IsDir() {
		return RecycleTopologyStandalone
	}
	return RecycleTopologyLinked
}

// ScanGitRootForRecycle scans a single git root (linked worktree or
// standalone repo) for the recycle guard.
func ScanGitRootForRecycle(ctx context.Context, root string, standalone bool) RecycleRootScan {
	return scanHeadRoot(ctx, root, standalone)
}

// ScanBareRepoForRecycle scans a bare repo cache for the recycle guard.
func ScanBareRepoForRecycle(ctx context.Context, barePath string) RecycleRootScan {
	return scanBareRoot(ctx, barePath)
}

// ScanTaskDirForRecycle scans every git root inside a task dir — the top
// level wiring plus nested repositories down to recycleNestedGitDepth — and
// folds them into one evidence record whose verdict is the worst root
// verdict. A task dir with no git root at all scans clean: deleting it loses
// no commits by definition.
func ScanTaskDirForRecycle(ctx context.Context, taskDir string) *RecycleScan {
	scan := &RecycleScan{
		Target:    taskDir,
		Kind:      RecycleKindTaskDir,
		Verdict:   RecyclePass,
		ScannedAt: time.Now().UTC(),
	}

	roots := collectGitRoots(taskDir)
	if len(roots) == 0 {
		scan.Roots = append(scan.Roots, RecycleRootScan{
			Root:     taskDir,
			Topology: RecycleTopologyNone,
			Verdict:  RecyclePass,
		})
		return scan
	}
	for _, r := range roots {
		standalone := detectTopology(r) == RecycleTopologyStandalone
		rootScan := scanHeadRoot(ctx, r, standalone)
		scan.Roots = append(scan.Roots, rootScan)
		scan.Reasons = append(scan.Reasons, rootScan.Reasons...)
		scan.Verdict = WorstVerdict(scan.Verdict, rootScan.Verdict)
	}
	return scan
}

// collectGitRoots lists the git roots inside dir: dir itself when it carries
// a .git entry, plus nested .git entries down to recycleNestedGitDepth. The
// walk never descends through links (linkedDirModes) and never descends into
// a .git directory once found.
func collectGitRoots(dir string) []string {
	var roots []string
	if detectTopology(dir) != RecycleTopologyNone {
		roots = append(roots, dir)
	}
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // best-effort: unreadable subtrees just are not scanned
		}
		if path != dir && d.Name() == ".git" {
			roots = append(roots, filepath.Dir(path))
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		if path != dir && d.Type()&guardLinkedDirModes != 0 {
			return filepath.SkipDir
		}
		if path != dir && strings.Count(strings.TrimPrefix(path, dir), string(filepath.Separator)) >= recycleNestedGitDepth {
			return filepath.SkipDir
		}
		return nil
	})
	return roots
}

// EvidenceFileName builds the evidence file name for a scan: a sortable
// timestamp, the kind, and a filesystem-safe fragment of the target.
func EvidenceFileName(scan *RecycleScan, now time.Time) string {
	base := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		case r == '-', r == '_', r == '.':
			return r
		default:
			return '_'
		}
	}, filepath.Base(filepath.Clean(scan.Target)))
	if len(base) > 60 {
		base = base[len(base)-60:]
	}
	return fmt.Sprintf("%s_%s_%s.json", now.UTC().Format("20060102T150405.000000000"), scan.Kind, base)
}

// WriteRecycleEvidence writes the scan record into the configured evidence
// directory. Returns the file path. Best-effort by contract: a failed write
// must never block or fail the recycle decision — the caller logs it.
func WriteRecycleEvidence(scan *RecycleScan) (string, error) {
	cfg := CurrentRecycleGuardConfig()
	if !cfg.Enabled || cfg.EvidenceDir == "" {
		return "", nil
	}
	if err := os.MkdirAll(cfg.EvidenceDir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(cfg.EvidenceDir, EvidenceFileName(scan, time.Now()))
	data, err := json.MarshalIndent(scan, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", err
	}
	scan.EvidencePath = path
	return path, nil
}

// SweepRecycleEvidence removes evidence files older than ttl. It is the only
// deleter of evidence: the GC walks skip the evidence directory by
// construction (dot-prefixed root entry), so a file younger than ttl always
// survives every recycle path.
func SweepRecycleEvidence(ttl time.Duration, now time.Time) int {
	cfg := CurrentRecycleGuardConfig()
	if cfg.EvidenceDir == "" || ttl <= 0 {
		return 0
	}
	entries, err := os.ReadDir(cfg.EvidenceDir)
	if err != nil {
		return 0
	}
	removed := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if now.Sub(info.ModTime()) > ttl {
			if os.Remove(filepath.Join(cfg.EvidenceDir, e.Name())) == nil {
				removed++
			}
		}
	}
	return removed
}

// GuardWorktreeEvidence records what a worktree directory held at removal
// time. Worktree removal never blocks on the guard — a linked worktree's
// commits and stashes live in the surviving shared .git — but the removal is
// exactly the moment an L1 claim ("nothing was lost") is made, so it is the
// moment that claim gets its evidence. Best-effort: scan or write failures
// log and return; the removal proceeds.
func GuardWorktreeEvidence(gitRoot, worktreePath string, logger *slog.Logger) {
	cfg := CurrentRecycleGuardConfig()
	if !cfg.Enabled || cfg.EvidenceDir == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), GuardScanTimeout)
	defer cancel()
	scan := &RecycleScan{
		Target:    worktreePath,
		Kind:      RecycleKindWorktree,
		Verdict:   RecyclePass,
		ScannedAt: time.Now().UTC(),
	}
	if detectTopology(worktreePath) == RecycleTopologyNone {
		// Stale registration or already-removed dir: nothing to record.
		return
	}
	rootScan := scanHeadRoot(ctx, worktreePath, detectTopology(worktreePath) == RecycleTopologyStandalone)
	scan.Roots = append(scan.Roots, rootScan)
	scan.Verdict = rootScan.Verdict
	scan.Reasons = rootScan.Reasons
	if path, err := WriteRecycleEvidence(scan); err != nil {
		if logger != nil {
			logger.Warn("execenv: recycle guard evidence write failed (non-fatal)", "path", worktreePath, "error", err)
		}
		return
	} else if path != "" && logger != nil && scan.Verdict != RecyclePass {
		logger.Info("execenv: recycle guard recorded worktree removal evidence",
			"guard", scan.Verdict, "path", worktreePath, "evidence", path)
	}
}
