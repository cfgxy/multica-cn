package execenv

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/pkg/taskfailure"
)

// Local worktree mode gives every task on a local_directory resource its own
// git worktree of the user's repo, created inside the daemon-owned env root.
// Tasks on the same directory then run concurrently instead of queueing on the
// per-path mutex, and each one delivers its work as a branch in the user's own
// repo — discoverable with `git branch`, no new result channel needed.
//
// Three properties this file exists to guarantee:
//
//  1. The agent sees what the user sees. `git worktree add` alone would check
//     out HEAD, silently hiding the user's uncommitted work. We replay a
//     snapshot of their directory into the worktree instead — tracked edits and
//     untracked files as one tree (captureUserSnapshot).
//  2. The user's directory is never written to. Everything — including the
//     sidecar context files Prepare writes — lands inside the worktree, which
//     is disposable. What lasts in the user's repo is the branch, plus one
//     hidden ref per branch recording who owns it and what it carries.
//  3. Nothing is silently discarded. Whatever the agent leaves uncommitted is
//     committed to the branch before the worktree goes away, and an edit of the
//     user's that could not be merged is offered again next turn rather than
//     recorded as delivered.

const (
	// localWorktreeDirName is the env-root-relative directory holding the
	// worktree. Kept short: on Windows the worktree path plus the deepest
	// repo path must stay under MAX_PATH for tools that predate long paths.
	localWorktreeDirName = "worktree"

	// gitTimeout bounds every git invocation this file makes. Almost all are
	// local-only operations; the one exception is the `git fetch` a NEW
	// branch's origin-anchored base runs (anchorNewBranchBase, RUYI-579 W1) —
	// a slow fetch fails closed there (the local remote-tracking tip is used
	// instead), while a slow local operation means a wedged index lock, where
	// failing the task beats hanging a daemon slot forever.
	gitTimeout = 2 * time.Minute

	// maxUntrackedFiles / maxUntrackedBytes bound the untracked content a task
	// will replay. `--exclude-standard` already drops anything gitignored
	// (node_modules, build output, venvs), so a repo hitting these limits has an
	// unusual amount of untracked-but-not-ignored content, and snapshotting it
	// would write every byte of it into the user's own object database. The
	// task refuses instead, naming the fix.
	maxUntrackedFiles = 2000
	maxUntrackedBytes = 200 << 20 // 200 MiB

	// excludedAsideInfix marks the directory a finalized worktree's budget
	// exclusions are preserved under, alongside the worktree's own
	// .trash-<ts> fallback; both live in the env root and are reclaimed by
	// the workspace GC with it.
	excludedAsideInfix = ".excluded-"
)

// stagingBudget is the untracked-content budget the finalize/baseline staging
// add is held to. The numbers reuse maxUntrackedFiles/maxUntrackedBytes on
// purpose: those already bound how much untracked content the daemon will
// replay through a worktree at Prepare time, and git hashing the same order
// of magnitude at finalize is what the OOM budget can absorb (a research run
// once handed the finalize add ~15 GB of model-weight caches, RUYI-337).
var untrackedStageBudget = stagingBudget{files: maxUntrackedFiles, bytes: maxUntrackedBytes}

type stagingBudget struct {
	files int
	bytes int64
}

const (
	// snapshotIndexFileName is the private index captureUserSnapshot builds the
	// user's snapshot in. It lives in the task's env root, never in the user's
	// repository: pointing GIT_INDEX_FILE at our own file is what keeps the
	// capture off the user's index entirely — no writes to it, and no wait on
	// .git/index.lock, which used to be able to end the task (#7434).
	snapshotIndexFileName = ".multica-snapshot-index"

	// localStateRefPrefix namespaces the per-branch record of the user's
	// directory. Outside refs/heads so it never appears in the user's
	// `git branch`, and a ref rather than a loose object so `git gc` in their
	// repo cannot reclaim a snapshot between two turns.
	localStateRefPrefix = "refs/multica/local-state/"

	// guardRefusalRefPrefix namespaces the delivery guard's refusal markers:
	// one ref per branch, pointing at the tip the guard refused to record
	// (RUYI-479). The next prepare on that branch reads it to tell a refusal
	// it can heal — a reset whose only dropped commits are chore(agent)
	// checkpoints — from any other divergence, which stays refused. Same
	// ref-not-object reasoning as the state records: `git gc` must not eat
	// the handshake between two runs.
	guardRefusalRefPrefix = "refs/multica/guard-refusal/"

	// gitlinkEmbedRefPrefix namespaces the momentary ref a gitlink child's
	// snapshot is fetched through (see embedGitlinkChild). Created and deleted
	// inside one embed; the name carries the creation time so a crashed embed's
	// leftovers can be pruned by age without touching a concurrent embed from
	// a different parent repo.
	gitlinkEmbedRefPrefix = "refs/multica/tmp-embed-"

	// maxGitlinkEmbedDepth is how deep gitlink children are embedded as real
	// trees in a snapshot. Depth 0 is the resource's own repository; its direct
	// gitlink children (depth 1) are embedded, anything deeper stays a gitlink
	// and materialises as an empty directory — the documented limitation.
	maxGitlinkEmbedDepth = 1
)

// LocalWorktreeParams describes the worktree Prepare should build for a
// local_directory task running in worktree mode.
type LocalWorktreeParams struct {
	// LocalPath is the user's configured directory. It may be the repo root
	// or any subdirectory of it; the worktree always covers the whole repo,
	// and the agent's cwd is the matching subdirectory inside it.
	LocalPath string
	// EnvRoot is the daemon-owned task env root. The worktree is created
	// inside it so the ordinary env-root GC reclaims it.
	EnvRoot string
	// AgentName and TaskID name the branch a task with no conversation behind
	// it gets: agent/<name>/<short-task-id>.
	AgentName string
	TaskID    string
	// ConversationKey names the work line this task belongs to — its issue, or
	// its chat session. Tasks sharing a key share one branch, so the second
	// turn of a conversation continues the first turn's work instead of
	// forking from HEAD again (MUL-6881). Empty for a task with no durable
	// conversation behind it; those keep the task-scoped branch.
	//
	// It is a DISPLAY key: `mul-6881` is what the user recognises in
	// `git branch`, and two workspaces can produce the same one. Continuing a
	// branch is therefore never decided by the name — see WorkspaceID /
	// AgentID / ConversationID, which are what a branch's recorded owner is
	// compared against.
	ConversationKey string
	// WorkspaceID, AgentID and ConversationID identify the conversation
	// itself. They are recorded with the branch and re-checked before any
	// later task continues it, so a same-named branch belonging to the user,
	// to another agent, or to another workspace is never adopted. All three
	// empty means "no conversation": the task gets a task-scoped branch.
	WorkspaceID    string
	AgentID        string
	ConversationID string
}

// owner is the identity a branch created for this task is recorded under.
func (p LocalWorktreeParams) owner() branchOwner {
	return branchOwner{WorkspaceID: p.WorkspaceID, AgentID: p.AgentID, ConversationID: p.ConversationID}
}

// localWorktreeConversation names the work line a worktree task belongs to, so
// every turn of it delivers onto one branch (MUL-6881).
//
// Two values, because they answer different questions. The key is what the
// branch is CALLED — the issue identifier is preferred there because it is what
// the user recognises in `git branch`, agent/j/mul-6881 rather than a uuid
// tail. The id is what the branch BELONGS to, and only it decides whether a
// later task may continue that branch: identifiers are per-workspace and
// human-chosen, so two workspaces can mint the same one for different issues.
// Tasks with neither an issue nor a chat session have no conversation to
// continue and get "", "".
func localWorktreeConversation(params PrepareParams) (key, id string) {
	if params.Task.IssueID != "" {
		if params.IssueIdentifier != "" {
			return params.IssueIdentifier, params.Task.IssueID
		}
		return taskKey(params.Task.IssueID), params.Task.IssueID
	}
	if params.Task.ChatSessionID != "" {
		return "chat-" + taskKey(params.Task.ChatSessionID), params.Task.ChatSessionID
	}
	return "", ""
}

// LocalWorktree is a prepared worktree plus everything the daemon needs to
// finalize it after the agent exits.
type LocalWorktree struct {
	// GitRoot is the user's repository root — the repo that owns the branch.
	GitRoot string
	// Path is the worktree root inside the env root.
	Path string
	// WorkDir is the agent's cwd: Path, plus the offset of LocalPath inside
	// the repo when the user pointed the resource at a subdirectory.
	WorkDir string
	// Branch is the branch created for this task, in the user's repo.
	Branch string
	// BaseCommit is the commit the worktree started from — this turn's baseline
	// when one was committed, otherwise the commit the branch was created on (a
	// clean start sits on the user's own HEAD). Finalize
	// compares the delivered tip against it twice: to decide whether the task
	// produced anything, and to decide whether it may be recorded at all.
	//
	// It is the strongest commit this turn can point at, which is why the second
	// question uses it rather than the checkpoint an earlier turn recorded. That
	// checkpoint is only an ancestor of it: the user may have committed on the
	// delivered branch since, and this turn's own baseline sits later still.
	// Measuring against the older commit let a run reset away everything this
	// turn replayed and still be recorded as having delivered it, which is how
	// the user's edits went missing from the turn after (MUL-6881 review).
	BaseCommit string
	// DirtyBaseCaptured records that the user had uncommitted tracked edits
	// which were replayed into the worktree.
	DirtyBaseCaptured bool
	// Continued reports that this worktree checked out a branch an earlier turn
	// of the same conversation left behind, instead of forking a new one from
	// the user's HEAD.
	Continued bool
	// ReplayConflicts names the files where the user's edits since the previous
	// turn could not be merged with what the branch already carries. The
	// worktree is handed to the agent WITH those conflicts in it — resolving
	// them is ordinary git work, and it is the only party that can judge which
	// version is right — so this is what the turn's prompt tells it to fix.
	// Finalize refuses to deliver while any of them are still unmerged.
	ReplayConflicts []string
	// createdBranch records that this prepare put the branch where it is, so
	// dropping it discards nothing an earlier turn delivered. False for a
	// continued branch: that one has to survive even a turn that produced
	// nothing, because it carries every turn before it.
	createdBranch bool
	// userState is the commit describing the user's directory as this task saw
	// it: their tracked edits and untracked files in one tree. Recorded against
	// the branch once it actually carries them, so the next turn replays only
	// what changed after that.
	userState string
	// owner is the conversation this branch belongs to, recorded with the
	// snapshot so a later task can prove the branch is its own before
	// continuing it.
	owner branchOwner
	// tracksState is false for a branch no later turn will continue — a
	// task-scoped branch, or the one a busy sibling forked. Recording a
	// snapshot for those would leave a ref nothing ever reads.
	tracksState bool
	// priorState is the snapshot the branch carried when this turn started, and
	// the one to record when this turn could not get its own into the branch.
	priorState string
	// snapshotPending is set when Prepare left the user's edits unmerged in the
	// worktree: the branch does not carry userState yet, and only a commit made
	// after the agent resolves can put it there.
	snapshotPending bool
	// aborted, when set, makes Finalize refuse to commit or remove anything.
	// Set by the daemon when a pre-commit step failed in a way that would make
	// the committed branch wrong (see AbortWithReason).
	aborted error
}

// MarshalJSON / UnmarshalJSON carry this struct's unexported state across the
// preparation helper boundary.
//
// Prepare runs in a short-lived helper process (PrepareIsolated) and the
// Environment it built comes back to the daemon as JSON, so everything Finalize
// needs has to be on the wire. Ordinary struct marshalling drops unexported
// fields silently: the daemon then finalized a worktree whose owner, snapshot
// and tracksState were all zero, which turned every proof this file makes into
// a no-op — no delivery verification, no record of the delivered tip, and a
// read-only turn keeping its branch. Nothing failed; the guarantees were simply
// absent in production while every in-process test still passed.
//
// aborted is deliberately NOT carried: it is set by the daemon after Prepare
// returns (AbortWithReason), so it belongs to the parent process only.
func (w *LocalWorktree) MarshalJSON() ([]byte, error) {
	type wire LocalWorktree
	return json.Marshal(struct {
		*wire
		CreatedBranch   bool        `json:"created_branch"`
		UserState       string      `json:"user_state"`
		PriorState      string      `json:"prior_state"`
		Owner           branchOwner `json:"owner"`
		TracksState     bool        `json:"tracks_state"`
		SnapshotPending bool        `json:"snapshot_pending"`
	}{
		wire:            (*wire)(w),
		CreatedBranch:   w.createdBranch,
		UserState:       w.userState,
		PriorState:      w.priorState,
		Owner:           w.owner,
		TracksState:     w.tracksState,
		SnapshotPending: w.snapshotPending,
	})
}

func (w *LocalWorktree) UnmarshalJSON(data []byte) error {
	type wire LocalWorktree
	aux := struct {
		*wire
		CreatedBranch   bool        `json:"created_branch"`
		UserState       string      `json:"user_state"`
		PriorState      string      `json:"prior_state"`
		Owner           branchOwner `json:"owner"`
		TracksState     bool        `json:"tracks_state"`
		SnapshotPending bool        `json:"snapshot_pending"`
	}{wire: (*wire)(w)}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	w.createdBranch = aux.CreatedBranch
	w.userState = aux.UserState
	w.priorState = aux.PriorState
	w.owner = aux.Owner
	w.tracksState = aux.TracksState
	w.snapshotPending = aux.SnapshotPending
	return nil
}

// LocalWorktreeOutcome is what a finished worktree task delivered.
type LocalWorktreeOutcome struct {
	// Branch is the branch holding the task's work, or "" when the task made
	// no changes at all (a read-only run) — in that case the branch is deleted
	// so it never shows up in the user's `git branch` as an empty artifact.
	Branch string
	// AutoCommitted is true when the agent left uncommitted changes that
	// Finalize committed so they would survive the worktree's removal.
	AutoCommitted bool
	// PreservedPath is set only when Finalize could NOT commit the agent's
	// changes. The worktree at this path was intentionally left on disk because
	// it is the only remaining copy of that work.
	PreservedPath string
	// Excluded lists the untracked top-level entries the staging budget guard
	// kept out of the delivered commit. They are never deleted: Finalize moves
	// each one out of the worktree before removing it, and AsidePath records
	// where that copy now lives. Empty on the ordinary path.
	Excluded []StagedExclusion
}

// StagedExclusion is one top-level entry the staging budget guard kept out of
// a commit, with the size that earned the exclusion and, once Finalize has
// run, the path the content was preserved at.
type StagedExclusion struct {
	// Name is the entry's path relative to the worktree root — a directory
	// ("hf_cache") or a single top-level file ("weights.bin").
	Name string
	// Files and Bytes are the untracked regular-file count and byte total
	// under (or in) Name at the time it was measured.
	Files int
	Bytes int64
	// AsidePath is where Finalize moved the content when the worktree itself
	// was removed; empty until then.
	AsidePath string
	// dir distinguishes a whole top-level directory from a single top-level
	// file; it decides the pathspec shape that keeps the entry out of the
	// add. Bookkeeping for the staging path, not part of the outcome.
	dir bool
}

// PrepareLocalWorktree creates the task's worktree and replays the user's
// uncommitted state into it. It never writes to the user's working tree: the
// snapshot is built in a private index inside the task's env root, which leaves
// their index, their files and their refs exactly as they were.
func PrepareLocalWorktree(params LocalWorktreeParams, logger *slog.Logger) (*LocalWorktree, error) {
	if params.LocalPath == "" {
		return nil, errors.New("execenv: local worktree requires a local path")
	}
	if params.EnvRoot == "" {
		return nil, errors.New("execenv: local worktree requires an env root")
	}
	if params.TaskID == "" {
		return nil, errors.New("execenv: local worktree requires a task id")
	}

	gitRoot, err := resolveGitRoot(params.LocalPath)
	if err != nil {
		return nil, err
	}

	// The agent's cwd keeps the user's chosen depth: a resource pointed at
	// <repo>/services/api must land the agent in <worktree>/services/api, not
	// at the repo root, or the task's whole notion of "the project" shifts.
	//
	// Canonicalise before the comparison: gitRoot comes back canonical, while
	// the configured path routinely isn't (on macOS every /tmp and /var path is
	// a symlink into /private). Comparing the two forms directly reads a repo
	// root as "outside itself".
	localPath := params.LocalPath
	if resolved, evalErr := filepath.EvalSymlinks(localPath); evalErr == nil {
		localPath = resolved
	}
	rel, err := filepath.Rel(gitRoot, localPath)
	if err != nil {
		return nil, fmt.Errorf("execenv: locate %q inside repo %q: %w", localPath, gitRoot, err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("execenv: %q is not inside its repository root %q", localPath, gitRoot)
	}

	worktreePath := filepath.Join(params.EnvRoot, localWorktreeDirName)

	// Everything below mutates the repo's worktree admin state or its refs, so
	// take the per-repo lock first. It covers the stale-path cleanup, which runs
	// `git worktree remove` and would otherwise race a sibling task's `worktree
	// add`, and the branch decision, which has to see a branch that another task
	// is creating at the same moment. The lock is cross-process because every
	// prepare runs in its own helper process (#7434).
	unlock, err := lockGitRoot(gitRoot, logger)
	if err != nil {
		return nil, err
	}
	defer unlock()

	if _, statErr := os.Stat(worktreePath); statErr == nil {
		// Prepare wipes and recreates envRoot, so an existing worktree path
		// means a stale registration in the user's repo pointing here. Remove
		// both rather than failing the task.
		removeLocalWorktreeDir(gitRoot, worktreePath, logger)
	}

	// Self-heal registrations orphaned by a crashed daemon: their env roots are
	// long gone, but the user's repo still lists them. Prune only drops entries
	// whose directory no longer exists, so it can never disturb a live task.
	if out, pruneErr := runGit(gitRoot, "worktree", "prune"); pruneErr != nil && logger != nil {
		logger.Warn("execenv: git worktree prune failed (non-fatal)",
			"git_root", gitRoot, "output", out, "error", pruneErr)
	}
	pruneOrphanedStateRefs(gitRoot, logger)

	headSHA, err := runGitTrimmed(gitRoot, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return nil, fmt.Errorf("execenv: repository %q has no commit to branch from "+
			"(worktree mode needs at least one commit; make an initial commit or switch the resource back to in_place): %w", gitRoot, err)
	}

	// Refuse an untracked payload too large to reproduce BEFORE snapshotting it:
	// the snapshot writes every one of those bytes into the user's own object
	// database, which is not something to do by accident on a directory full of
	// un-ignored build output.
	if err := checkUntrackedReplayable(gitRoot, logger); err != nil {
		return nil, err
	}
	// Gitlink children are embedded as real trees (embedGitlinkChildren), so
	// their untracked payload lands in the parent's object database just like
	// the parent's own untracked files — it must fit the same budget, judged by
	// the child's ignore rules. A child past the budget fails the task before
	// anything is written, same fail-closed contract as the check above.
	// Stale transfer refs from a crashed embed are swept here too, while we
	// already hold the parent's lock.
	for _, childDir := range trackedGitlinkDirs(gitRoot) {
		childRoot, ok := childRepoRoot(childDir)
		if !ok {
			continue
		}
		pruneGitlinkEmbedRefs(childRoot, logger)
		if err := checkUntrackedReplayable(childRoot, logger); err != nil {
			return nil, err
		}
	}

	// The commit describing the user's directory as this task sees it — their
	// tracked edits and their untracked files in one tree. Everything below
	// reasons about the user's state through this single object.
	userState, err := captureUserSnapshot(gitRoot, params.EnvRoot, headSHA, logger)
	if err != nil {
		// Fail closed. The promise of this mode is that the agent reasons about
		// the code the user actually has; silently starting from HEAD instead
		// would have it review a tree the user never saw and report confidently
		// on it. A task that does not start is recoverable — one that answers
		// from the wrong sources is not.
		return nil, fmt.Errorf("execenv: could not capture the uncommitted changes in %q, "+
			"so the worktree would not match what you have on disk: %w", gitRoot, err)
	}

	plan := resolveTaskBranch(gitRoot, params, headSHA, logger)
	actualBranch, createdBranch, err := addLocalWorktree(gitRoot, worktreePath, plan, params.TaskID)
	if err != nil {
		return nil, err
	}

	wt := &LocalWorktree{
		GitRoot:       gitRoot,
		Path:          worktreePath,
		WorkDir:       filepath.Join(worktreePath, rel),
		Branch:        actualBranch,
		BaseCommit:    plan.base,
		Continued:     plan.continues,
		createdBranch: createdBranch,
		userState:     userState,
		priorState:    plan.priorState,
		owner:         plan.owner,
		// A branch a sibling task forked because the conversation's own branch
		// was busy is delivered once and never continued, so it records nothing.
		tracksState: plan.tracksState && actualBranch == plan.name,
	}

	// Tear the worktree back down on every failure below. A half-replayed tree
	// is the worst outcome available: it looks like a working checkout, so
	// nothing downstream questions it, while the agent silently reads different
	// code than the user has. The branch goes with it only when this prepare
	// created it — a continued branch carries earlier turns' work, and dropping
	// it because THIS turn could not start would destroy the very thing the
	// task was meant to build on.
	rollback := func() {
		removeLocalWorktreeDir(gitRoot, worktreePath, logger)
		if createdBranch {
			dropBranch(gitRoot, actualBranch, logger)
		}
	}

	// Replay the user's directory into the worktree.
	//
	// A branch forked from HEAD carries none of it, so the whole snapshot goes
	// in. A continued branch already carries the edits the previous turn
	// replayed, and the agent's commits sit on top of them, so only the user's
	// new edits are new information there.
	replay, replayErr := replayUserState(worktreePath, plan, userState, logger)
	if replayErr != nil {
		rollback()
		return nil, replayErr
	}
	wt.ReplayConflicts = replay.conflicts
	// An unresolved merge means the branch does not carry this turn's snapshot
	// yet; only a commit after the agent resolves can put it there.
	wt.snapshotPending = len(replay.conflicts) > 0
	// Whether the user has uncommitted work at all — replayed by this turn or
	// already carried by the branch it continued.
	_, diffErr := runGit(gitRoot, "diff", "--quiet", headSHA, userState)
	wt.DirtyBaseCaptured = diffErr != nil

	// Commit the replayed state as a baseline so "did this task change
	// anything?" has an exact answer later. Without it the user's own
	// uncommitted work counts as a change: a read-only task on a repo with an
	// untracked scratch file would auto-commit that file at the end and leave
	// behind a branch the agent never touched. The baseline also makes the
	// delivered branch readable — `git diff <baseline>..<branch>` is precisely
	// the agent's work, with the user's WIP as its own labelled commit.
	//
	// Skipped entirely while the replay is unresolved: an index with unmerged
	// entries cannot be committed without committing conflict markers, and the
	// agent has not had its turn at them yet.
	if len(replay.conflicts) == 0 {
		dirty, dirtyErr := worktreeIsDirty(worktreePath)
		if dirtyErr != nil {
			rollback()
			return nil, fmt.Errorf("execenv: could not inspect the prepared worktree for %q: %w", gitRoot, dirtyErr)
		}
		// Only a worktree carrying replayed user work commits a baseline. A clean
		// start used to pin an empty "the task worktree started here" marker onto
		// every new branch, and that marker rode into every delivered PR
		// (RUYI-229). A clean branch needs no commit of its own to stay readable —
		// everything the agent does sits on the user's HEAD, so
		// `git diff <HEAD>..<branch>` is still precisely the agent's work — and a
		// turn that produces nothing drops its branch outright, so the branch is
		// never left sitting there unowned.
		if dirty {
			baseline, baseErr := commitBaseline(worktreePath, plan.continues)
			if baseErr != nil {
				// Without a baseline the task cannot tell the user's work from the
				// agent's, so it would later commit the user's files as if the agent
				// had produced them. Refuse rather than deliver a misleading branch.
				rollback()
				return nil, fmt.Errorf("execenv: could not record a baseline commit for the replayed state of %q: %w", gitRoot, baseErr)
			}
			// The branch now stands on a commit that carries this turn's snapshot,
			// and everything downstream measures the delivery against it.
			wt.BaseCommit = baseline
		}
		// Record the branch as this conversation's as early as it can honestly be
		// recorded: only once the branch carries a commit of this conversation's
		// own — the baseline just made, or the prior work a continued branch
		// stands on. A clean new branch has no such commit yet; recording its tip
		// would pin the user's own HEAD as the ownership proof, which is exactly
		// where a branch they later delete and recreate lands (MUL-6881). Its
		// record waits for Finalize and the delivery itself. Recording here as
		// well as at Finalize is what keeps a turn that never reaches Finalize
		// recoverable on every branch that could carry work into it.
		if dirty || plan.continues {
			if err := wt.recordState(wt.BaseCommit, logger); err != nil && logger != nil {
				logger.Warn("execenv: could not record the task branch before the run (non-fatal; Finalize records the delivered tip)",
					"branch", wt.Branch, "error", err)
			}
		}
	}

	// Note on keeping sidecars out of the delivered branch: we deliberately do
	// NOT write .git/info/exclude here. A linked worktree reads info/exclude
	// from the repo's COMMON git dir, so the only file that would take effect
	// is the user's own .git/info/exclude — editing it would change what `git
	// status` shows in the user's checkout, which is theirs, not ours. Instead
	// the daemon runs the existing CleanupRuntimeConfig + CleanupSidecars pass
	// over the worktree before Finalize, so the sidecars are simply gone by the
	// time anything is committed. That also preserves a genuine agent edit to a
	// tracked CLAUDE.md, which a blanket exclude would have swallowed.
	//
	// The state directories the runtimes write themselves (.omc/, .zcode/, and
	// the rest of runtimeStateDirNames) get no cleanup pass — nothing the
	// daemon owns — so the staging pathspecs (stagingExcludes) prune them from
	// the snapshot and the delivered branch instead. Tracked files under them
	// are repo content, not noise, and `add -u` still commits those.

	if logger != nil {
		logger.Info("execenv: local worktree ready",
			"git_root", gitRoot,
			"path", worktreePath,
			"branch", actualBranch,
			"base", wt.BaseCommit,
			"continued", wt.Continued,
			"dirty_base_captured", wt.DirtyBaseCaptured,
			"replay_conflicts", len(wt.ReplayConflicts),
		)
	}
	return wt, nil
}

// Finalize commits whatever the agent left behind, removes the worktree, and
// reports the branch. Called after the agent exits, before the env root is
// handed to the GC.
//
// The auto-commit is the reason a worktree task can't lose work: `git worktree
// remove --force` would happily delete uncommitted edits, and the user would
// have no way to get them back. Committing first turns "the agent edited files"
// into "the branch has a commit", which is the delivery contract for this mode.
//
// If that commit cannot be made — a repo with commit.gpgSign and no signing key
// available to the daemon, a full disk, a ref lock we lost — Finalize returns an
// error and DELIBERATELY LEAVES THE WORKTREE IN PLACE. Removing it would be the
// one operation in this file that destroys work with no way back, and a warning
// in the daemon log is not an acceptable substitute for the user's changes. The
// surviving worktree stays registered in the user's repo, so `git worktree list`
// points straight at it.
func (w *LocalWorktree) Finalize(logger *slog.Logger) (LocalWorktreeOutcome, error) {
	if w == nil {
		return LocalWorktreeOutcome{}, nil
	}
	outcome := LocalWorktreeOutcome{Branch: w.Branch}

	unlock, err := lockGitRoot(w.GitRoot, logger)
	if err != nil {
		// Nothing has been committed or removed yet, so the agent's work is
		// still sitting in the worktree. Report it as preserved rather than
		// naming a branch that does not carry it.
		outcome.Branch = ""
		outcome.PreservedPath = w.Path
		return outcome, fmt.Errorf("could not lock %q to finalize branch %s: %w; "+
			"the work is preserved in the worktree at %s", w.GitRoot, w.Branch, err, w.Path)
	}
	defer unlock()

	// Something before the commit went wrong in a way that would make the
	// delivered branch misleading. Commit nothing and keep the worktree: the
	// agent's work is still in it, and so is whatever the caller could not
	// clean up, which a human can now look at directly.
	if w.aborted != nil {
		// Report NO branch. One exists in the user's repo, but nothing was
		// committed to it, so naming it as this task's result would point them
		// at a branch that is missing the very work they are looking for. The
		// preserved worktree path below is the honest pointer.
		outcome.Branch = ""
		outcome.PreservedPath = w.Path
		if logger != nil {
			logger.Error("execenv: worktree finalize aborted; nothing committed, worktree kept for inspection",
				"path", w.Path, "branch", w.Branch, "git_root", w.GitRoot, "error", w.aborted)
		}
		return outcome, fmt.Errorf(
			"refusing to deliver branch %s: %w; the task worktree is preserved at %s (listed by `git worktree list` in %s)",
			w.Branch, w.aborted, w.Path, w.GitRoot)
	}

	// An unresolved merge is never committed. The worktree may be carrying the
	// user's edits from before this turn (replayUserState hands the conflict to
	// the agent rather than dropping it), and `git add -A` would turn conflict
	// markers into a delivered commit — the one way this mode can produce a
	// branch that compiles nowhere and looks deliberate. Keep the worktree, say
	// which files are still open, and leave the snapshot unrecorded so the next
	// turn replays the same edits instead of assuming they landed.
	if unmerged, unmergedErr := unmergedPaths(w.Path); unmergedErr != nil || len(unmerged) > 0 {
		outcome.Branch = ""
		outcome.PreservedPath = w.Path
		if unmergedErr != nil {
			return outcome, fmt.Errorf("could not check %s for an unresolved merge: %w; "+
				"the work is preserved in the worktree at %s", w.Branch, unmergedErr, w.Path)
		}
		if logger != nil {
			logger.Error("execenv: worktree left with an unresolved merge; nothing committed, worktree kept",
				"path", w.Path, "branch", w.Branch, "files", unmerged)
		}
		return outcome, fmt.Errorf(
			"refusing to deliver branch %s: your local edits to %s are still unmerged in the task worktree; "+
				"the worktree is preserved at %s (listed by `git worktree list` in %s) — resolve the conflict there, "+
				"or re-run the task and let the agent finish the merge",
			w.Branch, quotedPaths(unmerged), w.Path, w.GitRoot)
	}

	// Treat "can't tell" like "dirty": committing costs an empty commit at
	// worst, while assuming clean risks deleting the agent's edits.
	dirty, statusErr := worktreeIsDirty(w.Path)
	if statusErr != nil {
		if logger != nil {
			logger.Warn("execenv: inspect worktree status failed; committing defensively",
				"path", w.Path, "error", statusErr)
		}
		dirty = true
	}
	if dirty {
		committed, excluded, err := w.commitAll(logger)
		if err != nil {
			outcome.PreservedPath = w.Path
			if logger != nil {
				logger.Error("execenv: could not commit the agent's changes; keeping the worktree so the work is recoverable",
					"path", w.Path, "branch", w.Branch, "git_root", w.GitRoot, "error", err)
			}
			return outcome, fmt.Errorf(
				"could not commit the agent's changes to branch %s: %w; the work is preserved in the worktree at %s (listed by `git worktree list` in %s) — recover it before that directory is reclaimed",
				w.Branch, err, w.Path, w.GitRoot)
		}
		outcome.AutoCommitted = committed
		outcome.Excluded = excluded
	}

	// A branch still sitting exactly on its base commit means the task changed
	// nothing — the read-only case. Delete it so the user's branch list only
	// ever grows for tasks that actually produced work. Only ever the branch
	// this task created: a continued branch sits on its base precisely because
	// this turn added nothing to what earlier turns delivered, and deleting it
	// would take their work with it.
	tip, err := runGitTrimmed(w.Path, "rev-parse", "--verify", "HEAD")
	producedWork := err != nil || tip != w.BaseCommit
	dropped := false
	if !producedWork && w.createdBranch {
		// A detached checkout can return to the base while the task branch still
		// carries work. Only discard a branch that itself remains at the base.
		branchTip, branchErr := runGitTrimmed(w.GitRoot, "rev-parse", "--verify", "refs/heads/"+w.Branch)
		dropped = branchErr == nil && branchTip == tip
	}

	// A turn that started mid-merge only gets to advance the branch's recorded
	// state if it committed something after resolving. When the branch is still
	// where THIS turn found it, nothing distinguishes "the agent resolved in
	// favour of the version already on the branch" from "the agent threw the
	// merge away" — the tree is identical either way — so the record keeps the
	// state the branch is known to carry, and the next turn offers the user's
	// edits again.
	//
	// The cost is a merge the agent may have to conclude more than once, in the
	// one case where its conclusion left no trace. The alternative is recording
	// edits as delivered that may have been discarded, which is how they go
	// missing without anyone seeing it.
	if w.snapshotPending && tip == w.BaseCommit {
		if w.priorState == "" {
			outcome.Branch = ""
			outcome.PreservedPath = w.Path
			return outcome, fmt.Errorf(
				"refusing to record branch %s: the merge with your local edits was never concluded and this branch has no earlier "+
					"state to fall back on; the task worktree is preserved at %s (listed by `git worktree list` in %s)",
				w.Branch, w.Path, w.GitRoot)
		}
		if logger != nil {
			logger.Info("execenv: the run concluded its merge without committing; keeping the branch's previous recorded state so the edits are offered again",
				"path", w.Path, "branch", w.Branch)
		}
		w.userState = w.priorState
	}

	// Record BEFORE the worktree goes away, and treat a failure as a failure to
	// deliver. The record is what makes the branch continuable: without it the
	// next turn cannot prove the branch is this conversation's and starts a new
	// line of work, stranding what this turn produced. Ordering it first is what
	// makes that recoverable — the worktree is still there to preserve, exactly
	// as for a commit that could not be made.
	if !dropped {
		healAttempted := false
		// Not an if-scoped short declaration: the self-heal below rewrites
		// verifyErr, and the refusal after it must see the rewritten value.
		verifyErr := w.verifyDeliveryPoint(tip)
		if verifyErr != nil {
			// Self-heal before refusing (RUYI-579 W2): when the refusal is
			// only the ancestor break — the tip is the branch's, but a
			// mid-turn reset/rebase took the base commit out of its ancestry —
			// the same tree re-committed onto the base is the identical
			// delivery on a legal ancestry. Any other shape (or any failed
			// step) leaves verifyErr standing and the refusal below runs
			// exactly as it did before the heal existed.
			if reTip, healErr := w.reAnchorDelivery(tip, logger); healErr == nil {
				healAttempted = true
				if reErr := w.verifyDeliveryPoint(reTip); reErr != nil {
					// Unreachable while reAnchor's last step moved the branch
					// and verified conditions by construction; kept so the
					// refusal never trusts the heal without re-proving it.
					verifyErr = reErr
				} else {
					tip = reTip
					verifyErr = nil
				}
			} else if !errors.Is(healErr, errNotHealable) {
				healAttempted = true
			}
		}
		if verifyErr != nil {
			outcome.Branch = ""
			outcome.PreservedPath = w.Path
			// Pin the refused tip for the retry: its prepare reads this
			// marker to heal a reset whose only dropped commits are the
			// daemon's own checkpoints (healGuardRefusal). Best-effort —
			// without it the retry forks a fresh branch, which is exactly
			// what it did before the marker existed.
			if _, mErr := runGit(w.GitRoot, "update-ref", guardRefusalRef(w.Branch), tip); mErr != nil && logger != nil {
				logger.Warn("execenv: could not record the delivery-guard refusal marker (non-fatal; the retry will fork a fresh branch)",
					"branch", w.Branch, "tip", tip, "error", mErr)
			}
			guardErr := &DeliveryGuardError{Err: verifyErr, HealAttempted: healAttempted}
			var kindErr *DeliveryGuardError
			if errors.As(verifyErr, &kindErr) {
				guardErr.Kind = kindErr.Kind
			}
			if logger != nil {
				logger.Error("execenv: the run's delivery point cannot be recorded as this conversation's; nothing recorded, worktree kept",
					"path", w.Path, "branch", w.Branch, "git_root", w.GitRoot, "tip", tip, "base", w.BaseCommit,
					"guard_kind", guardErr.Kind, "heal_attempted", healAttempted, "error", verifyErr)
			}
			return outcome, fmt.Errorf(
				"refusing to record branch %s: %w; the task worktree is preserved at %s (listed by `git worktree list` in %s) — "+
					"recover the work from there, and let the run keep the commit the worktree started from instead of resetting past it",
				w.Branch, guardErr, w.Path, w.GitRoot)
		}
		if recErr := w.recordState(tip, logger); recErr != nil {
			outcome.PreservedPath = w.Path
			if logger != nil {
				logger.Error("execenv: could not record the delivered task branch; keeping the worktree",
					"path", w.Path, "branch", w.Branch, "git_root", w.GitRoot, "error", recErr)
			}
			return outcome, fmt.Errorf(
				"could not record branch %s as this conversation's: %w; the work is committed to that branch and the "+
					"task worktree is preserved at %s (listed by `git worktree list` in %s) — a follow-up run will start "+
					"a new branch instead of continuing this one",
				w.Branch, recErr, w.Path, w.GitRoot)
		}
		// A real delivery retires any refusal this branch still carries: the
		// handshake is done, and a stale marker must not authorise a later
		// prepare to rewrite the branch.
		if _, err := runGit(w.GitRoot, "update-ref", "-d", guardRefusalRef(w.Branch)); err != nil && logger != nil {
			logger.Debug("execenv: no delivery-guard refusal marker to clear", "branch", w.Branch)
		}
	}

	// Budget exclusions are preserved, never deleted: the excluded content is
	// regenerable, but silent loss is exactly what this outcome must not do.
	// Move each excluded top-level entry out of the worktree first (same-
	// filesystem renames, so multi-GB entries move instantly), then remove
	// the worktree around the hole. If any move fails, rename the whole
	// worktree aside — the same inert leftover removal's own failure path
	// leaves — because deleting it would destroy content the guard promised
	// to keep. The aside directories live next to the worktree in the env
	// root and are reclaimed by the workspace GC with it.
	if len(outcome.Excluded) > 0 {
		asideDir := fmt.Sprintf("%s%s%d", w.Path, excludedAsideInfix, time.Now().Unix())
		if moveErr := moveExcludedAside(w.Path, asideDir, outcome.Excluded); moveErr != nil {
			trash := fmt.Sprintf("%s.trash-%d", w.Path, time.Now().Unix())
			if renameErr := os.Rename(w.Path, trash); renameErr == nil {
				for i := range outcome.Excluded {
					if outcome.Excluded[i].AsidePath == "" {
						outcome.Excluded[i].AsidePath = filepath.Join(trash, outcome.Excluded[i].Name)
					}
				}
				if logger != nil {
					logger.Warn("execenv: budget-excluded entries could not be moved aside; renamed the finalized worktree aside whole",
						"path", w.Path, "aside", trash, "error", moveErr)
				}
			} else {
				// Nothing was deleted: the worktree stays put and keeps every
				// excluded entry. The delivered work is unaffected.
				outcome.PreservedPath = w.Path
				if logger != nil {
					logger.Warn("execenv: finalized worktree cleanup failed; the delivered work is unaffected",
						"path", w.Path, "move_error", moveErr, "rename_error", renameErr)
				}
			}
		} else {
			if logger != nil {
				logger.Info("execenv: staging budget excluded untracked content; preserved aside",
					"path", w.Path, "aside", asideDir, "entries", outcome.Excluded)
			}
			removeFinalizedWorktree(w.GitRoot, w.Path, &outcome, logger)
		}
	} else {
		removeFinalizedWorktree(w.GitRoot, w.Path, &outcome, logger)
	}

	if dropped {
		dropBranch(w.GitRoot, w.Branch, logger)
		outcome.Branch = ""
	}

	if logger != nil {
		logger.Info("execenv: local worktree finalized",
			"git_root", w.GitRoot,
			"branch", outcome.Branch,
			"auto_committed", outcome.AutoCommitted,
			"produced_work", producedWork,
			"continued", w.Continued,
		)
	}
	return outcome, nil
}

// moveExcludedAside moves the staging budget's excluded entries out of the
// worktree into asideDir, before the worktree directory itself is removed.
// Same-filesystem renames, so multi-GB entries move instantly. Each entry's
// AsidePath is filled as it moves, so a mid-way failure still reports where
// the entries that did move ended up. Names are worktree-relative paths —
// top-level for budget exclusions, arbitrarily nested for build-artifact
// units — so the aside parent is created per entry and the relative layout is
// preserved inside asideDir.
func moveExcludedAside(worktreePath, asideDir string, excluded []StagedExclusion) error {
	if err := os.MkdirAll(asideDir, 0o755); err != nil {
		return fmt.Errorf("create exclusion aside dir %q: %w", asideDir, err)
	}
	for i := range excluded {
		dest := filepath.Join(asideDir, excluded[i].Name)
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return fmt.Errorf("create exclusion aside parent for %q: %w", excluded[i].Name, err)
		}
		if err := os.Rename(filepath.Join(worktreePath, excluded[i].Name), dest); err != nil {
			return fmt.Errorf("preserve excluded entry %q: %w", excluded[i].Name, err)
		}
		excluded[i].AsidePath = dest
	}
	return nil
}

// removeFinalizedWorktree removes the worktree directory once everything
// worth keeping is committed and recorded. Best-effort only: a failed
// directory removal must never flip the run to failed. Orphaned tool
// processes from an earlier daemon generation can hold cwd or open files
// inside the tree and repopulate it mid-removal — untidy, not a delivery
// failure. Rename aside what cannot be deleted so the handoff path stays
// clear for the next task; the renamed directory is inert and reclaimed by
// the workspace GC.
func removeFinalizedWorktree(gitRoot, worktreePath string, outcome *LocalWorktreeOutcome, logger *slog.Logger) {
	if removeErr := removeLocalWorktreeDir(gitRoot, worktreePath, logger); removeErr != nil {
		aside := fmt.Sprintf("%s.trash-%d", worktreePath, time.Now().Unix())
		if renameErr := os.Rename(worktreePath, aside); renameErr == nil {
			if logger != nil {
				logger.Warn("execenv: finalized worktree could not be removed (held files?); renamed aside for GC",
					"path", worktreePath, "aside", aside, "error", removeErr)
			}
		} else {
			outcome.PreservedPath = worktreePath
			if logger != nil {
				logger.Warn("execenv: finalized worktree cleanup failed; the delivered work is unaffected",
					"path", worktreePath, "remove_error", removeErr, "rename_error", renameErr)
			}
		}
	}
}

// Discard tears a worktree down without delivering anything: unregister it,
// delete its directory, drop its branch.
//
// For the abandon-before-the-agent-ran case only. Finalize is the path that
// preserves work; this one assumes there is none to preserve, so callers must
// be sure nothing has run in the worktree yet.
func (w *LocalWorktree) Discard(logger *slog.Logger) {
	if w == nil {
		return
	}
	unlock, err := lockGitRoot(w.GitRoot, logger)
	if err != nil {
		// Best-effort by contract: every step below only logs on failure. The
		// registration this leaves behind is pruned by the next prepare on
		// this repo, which is the same self-heal path a crashed daemon uses.
		if logger != nil {
			logger.Warn("execenv: could not lock the repository to discard the task worktree",
				"git_root", w.GitRoot, "path", w.Path, "branch", w.Branch, "error", err)
		}
		return
	}
	defer unlock()
	removeLocalWorktreeDir(w.GitRoot, w.Path, logger)
	// Same rule as every other teardown: a branch this prepare did not create
	// belongs to the turns before it and outlives this task.
	if w.createdBranch {
		dropBranch(w.GitRoot, w.Branch, logger)
	}
	if logger != nil {
		logger.Info("execenv: local worktree discarded before the agent ran",
			"git_root", w.GitRoot, "path", w.Path, "branch", w.Branch, "branch_dropped", w.createdBranch)
	}
}

// AbortWithReason marks the worktree undeliverable. Finalize will then commit
// nothing, remove nothing, and return an error naming the preserved path.
//
// This exists because the decision "is this branch safe to deliver?" is made
// outside this package — the daemon knows whether its own sidecar cleanup
// succeeded — while the only code that can act on it is Finalize. The first
// reason wins: it is the one closest to the root cause.
func (w *LocalWorktree) AbortWithReason(err error) {
	if w == nil || err == nil || w.aborted != nil {
		return
	}
	w.aborted = err
}

// commitBaseline records the user's replayed uncommitted state as its own
// commit on the task branch, returning the new tip. On a continued branch that
// state is the increment since the previous turn, so the message says so rather
// than claiming to be the branch's baseline.
func commitBaseline(worktreePath string, continued bool) (string, error) {
	message := "chore(agent): baseline — uncommitted work from the local directory"
	if continued {
		message = "chore(agent): uncommitted work from the local directory since the previous turn"
	}
	// Baseline exclusions are not preserved like finalize's: this runs right
	// after the replay, whose own budget (checkUntrackedReplayable) has just
	// bounded the same content, so the guard here is defence in depth. Should
	// it ever fire, the entries stay in the worktree and finalize's guard
	// preserves them when the task ends.
	if _, _, err := commitEverything(worktreePath, message); err != nil {
		return "", err
	}
	tip, err := runGitTrimmed(worktreePath, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return "", fmt.Errorf("resolve baseline commit: %w", err)
	}
	return tip, nil
}

// commitAll stages and commits everything the agent left behind. Returns
// whether a commit was actually created, plus the staging budget's exclusions
// for the caller to preserve; an error means the changes are still only on
// disk and the caller must not delete the worktree.
func (w *LocalWorktree) commitAll(logger *slog.Logger) (bool, []StagedExclusion, error) {
	// Never --allow-empty here: an empty commit would make a read-only turn look
	// like it produced work and leave its branch behind.
	return commitEverything(w.Path, "chore(agent): uncommitted changes from task")
}

// planUntrackedStaging meters the untracked content a staging `git add -A`
// would pick up — the same --exclude-standard view git itself uses, so
// .gitignore is honoured by construction — and excludes two kinds of content
// unconditionally: build artifacts (buildArtifactUnit), whose presence in a
// chore(agent) checkpoint is what drives sessions to rewrite the branch
// history afterwards (RUYI-479), and, when the remainder exceeds
// untrackedStageBudget, whole top-level entries, largest first, until it fits.
// Excluding largest-first is what rescues small real deliverables: a research
// run's multi-GB caches go, its few-MB results stay. A stat-level walk only;
// no file content is read.
func planUntrackedStaging(worktreePath string) ([]StagedExclusion, error) {
	out, err := runGitStdout(worktreePath, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return nil, fmt.Errorf("execenv: could not meter the untracked content in %q: %w", worktreePath, err)
	}
	tops := map[string]*StagedExclusion{}
	artifacts := map[string]*StagedExclusion{}
	var totalFiles int
	var totalBytes int64
	for _, rel := range strings.Split(out, "\x00") {
		// The name lists are pruned from the add itself, so metering them
		// would only spend the budget on content that is not going in anyway.
		if rel == "" || isMulticaSidecarPath(rel) || isRuntimeStatePath(rel) || isReplayableCachePath(rel) {
			continue
		}
		info, statErr := os.Lstat(filepath.Join(worktreePath, rel))
		if statErr != nil {
			// Listed a moment ago, gone now: runtimes churn state right up to
			// process exit, and git will not find the file either.
			continue
		}
		if !info.Mode().IsRegular() {
			// Symlinks are recorded as the link itself and git never adds
			// sockets, FIFOs or devices, so none of them costs memory.
			continue
		}
		// Artifacts stage never, budget or no budget, and their bytes stay out
		// of the budget accounting: a large .apk must not push the run's real
		// deliverables out of the add.
		if unit, isArtifact := buildArtifactUnit(rel); isArtifact {
			e := artifacts[unit]
			if e == nil {
				e = &StagedExclusion{Name: unit, dir: unit != rel}
				artifacts[unit] = e
			}
			e.Files++
			e.Bytes += info.Size()
			continue
		}
		top, isDir := rel, false
		if i := strings.IndexByte(rel, '/'); i >= 0 {
			top, isDir = rel[:i], true
		}
		e := tops[top]
		if e == nil {
			e = &StagedExclusion{Name: top, dir: isDir}
			tops[top] = e
		}
		e.Files++
		e.Bytes += info.Size()
		totalFiles++
		totalBytes += info.Size()
	}
	excluded := make([]StagedExclusion, 0, len(artifacts))
	artifactNames := make([]string, 0, len(artifacts))
	for name := range artifacts {
		artifactNames = append(artifactNames, name)
	}
	sort.Strings(artifactNames)
	for _, name := range artifactNames {
		excluded = append(excluded, *artifacts[name])
	}
	if totalFiles <= untrackedStageBudget.files && totalBytes <= untrackedStageBudget.bytes {
		return excluded, nil
	}
	names := make([]string, 0, len(tops))
	for name := range tops {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool { return tops[names[i]].Bytes > tops[names[j]].Bytes })
	for _, name := range names {
		if totalFiles <= untrackedStageBudget.files && totalBytes <= untrackedStageBudget.bytes {
			break
		}
		e := tops[name]
		excluded = append(excluded, *e)
		totalFiles -= e.Files
		totalBytes -= e.Bytes
	}
	return excluded, nil
}

// buildArtifactDirNames are the untracked directories that are build or QA
// output by convention strong enough that nothing source-shaped lives in them
// untracked. dist is the near-universal bundler output directory; qa-artifacts
// is this platform's own QA delivery convention. Build-artifact matching is an
// untracked-staging rule only — tracked files under these directories are repo
// content and ride the `add -u` step like any other tracked edit.
var buildArtifactDirNames = []string{
	"dist",
	"qa-artifacts",
}

// buildArtifactUnit reports the staging exclusion unit for an untracked path
// that is a build artifact: the matched file itself for installer formats and
// their split parts (RUYI-471's apk-parts shreds), or the matched directory's
// path when an artifact directory name appears at any depth. Empty and false
// when the path is not an artifact.
func buildArtifactUnit(rel string) (string, bool) {
	segs := strings.Split(filepath.ToSlash(rel), "/")
	for i, seg := range segs {
		for _, name := range buildArtifactDirNames {
			if seg == name {
				return strings.Join(segs[:i+1], "/"), true
			}
		}
	}
	base := strings.ToLower(segs[len(segs)-1])
	if strings.HasSuffix(base, ".apk") || strings.HasSuffix(base, ".aab") ||
		strings.HasSuffix(base, ".ipa") || strings.Contains(base, ".apk.part-") {
		return rel, true
	}
	return "", false
}

// sweepStagedArtifacts drops artifact-shaped paths out of the index before the
// checkpoint commit. Only new introductions are swept — a path already in
// HEAD is tracked repo content, and its staged edit rides the commit like any
// other tracked edit, the same rule the untracked metering follows. Unstaging
// is per path so a tracked neighbour under the same dist/ directory keeps its
// staged edit; the recorded exclusion is still the whole unit, which is what
// finalize's aside preserves. `rm --cached` leaves the file on disk, so a
// baseline sweep leaves the artifact in the worktree for the agent and a
// finalize sweep hands it to the caller's aside.
func sweepStagedArtifacts(worktreePath string, excluded []StagedExclusion) ([]StagedExclusion, error) {
	out, err := runGitStdout(worktreePath, "diff", "--cached", "--name-only", "-z")
	if err != nil {
		return nil, fmt.Errorf("execenv: could not list the staged content in %q: %w", worktreePath, err)
	}
	recorded := map[string]bool{}
	for _, path := range strings.Split(out, "\x00") {
		unit, isArtifact := buildArtifactUnit(path)
		if !isArtifact {
			continue
		}
		if inHead, err := runGit(worktreePath, "ls-tree", "HEAD", "--", path); err == nil && strings.TrimSpace(inHead) != "" {
			continue
		}
		if rmOut, err := runGit(worktreePath, "rm", "--cached", "-q", "--", path); err != nil {
			return nil, fmt.Errorf("execenv: could not keep build artifact %q out of the checkpoint: %s: %w",
				path, strings.TrimSpace(rmOut), err)
		}
		if !recorded[unit] {
			recorded[unit] = true
			excluded = append(excluded, StagedExclusion{Name: unit, dir: unit != path})
		}
	}
	return excluded, nil
}

// exclusionSpecs turns budget exclusions into the pathspecs that keep each
// entry out of the add: a directory excludes everything under it (git records
// no empty directories, so the directory entry itself needs no spec), a
// single top-level file excludes the path itself.
func exclusionSpecs(excluded []StagedExclusion) []string {
	specs := make([]string, 0, len(excluded))
	for _, e := range excluded {
		if e.dir {
			specs = append(specs, ":(exclude,glob)"+e.Name+"/**")
		} else {
			specs = append(specs, ":(exclude,glob)"+e.Name)
		}
	}
	return specs
}

// commitEverything returns (false, nil) for the benign "there was nothing to
// commit" case and (false, err) for a real failure — the distinction callers
// need to decide whether the tree is safe to discard. The middle return value
// lists the top-level entries the staging budget kept out of the add; their
// content stays on disk until the caller preserves or discards it.
func commitEverything(worktreePath, message string) (bool, []StagedExclusion, error) {
	// Two steps rather than one `add -A`. Agent runtimes create and delete
	// state files (.omc/, .zcode/, ...) in the worktree right up to the moment
	// the agent exits, and a plain `add -A` walks those untracked directories:
	// a file removed between the walk and the stat made the whole add die with
	// "unable to stat ... No such file or directory" (exit 128), failing the
	// task with its work stranded uncommitted. `add -u` touches only tracked
	// index entries, so it neither scans the churning directories nor races on
	// them; the second step then stages untracked work with those directories
	// pruned by pathspec. A genuinely tracked file under one of them is repo
	// content, not noise, and the first step still commits it.
	if out, err := runGit(worktreePath, "add", "-u"); err != nil {
		return false, nil, fmt.Errorf("git add -u: %s: %w", strings.TrimSpace(out), err)
	}
	// The staging budget keeps the second step from hashing an unbounded tree:
	// a research run leaving ~15 GB of regenerable caches untracked used to
	// OOM-kill this add (signal: killed) and mislabel an already-delivered run
	// as a final failure. The meter ran a moment before the add, so a file
	// created in between can still slip in — this bounds the common case, it
	// is not a lock (RUYI-337).
	excluded, err := planUntrackedStaging(worktreePath)
	if err != nil {
		return false, nil, err
	}
	addArgs := append([]string{"add", "-A", "--"}, stagingExcludes()...)
	addArgs = append(addArgs, exclusionSpecs(excluded)...)
	if out, err := runGit(worktreePath, addArgs...); err != nil {
		return false, nil, fmt.Errorf("git add: %s: %w", strings.TrimSpace(out), err)
	}
	// Content can reach the index without passing the add's pathspec gate: the
	// user-state replay stages its cherry-pick before this function runs, and
	// a file created between the meter and the add slips past the meter (this
	// bounds the common case, it is not a lock — RUYI-337). Sweep the staged
	// set once for artifact-shaped paths; what the meter already excluded
	// never got staged, so the two lists do not overlap.
	excluded, err = sweepStagedArtifacts(worktreePath, excluded)
	if err != nil {
		return false, nil, err
	}
	// Nothing staged means nothing to record. Asking the index directly rather
	// than parsing git's wording: with the runtime directories excluded, the
	// leftover untracked files make commit phrase it as "nothing added to
	// commit but untracked files present" — a shape the old "nothing to
	// commit" match never saw, and one that read like a real failure. No caller
	// of this function may create an empty commit: a read-only turn must never
	// look like it produced work.
	staged, err := runGit(worktreePath, "diff", "--cached", "--name-only")
	if err != nil {
		return false, nil, fmt.Errorf("git diff --cached: %s: %w", strings.TrimSpace(staged), err)
	}
	if strings.TrimSpace(staged) == "" {
		return false, excluded, nil
	}
	// --no-verify: the user's commit hooks are written for the user's own
	// workflow (interactive linters, test suites, signing prompts) and a hook
	// failure here would mean losing the agent's work to save a lint run. Note
	// it does NOT disable commit.gpgSign, which is why the caller has to treat
	// a commit failure as "keep the worktree" rather than a warning.
	args := append(commitIdentityArgs(worktreePath), "commit", "--no-verify")
	args = append(args, "-m", message)
	if out, err := runGit(worktreePath, args...); err != nil {
		if strings.Contains(out, "nothing to commit") {
			return false, excluded, nil
		}
		return false, nil, fmt.Errorf("git commit: %s: %w", strings.TrimSpace(out), err)
	}
	return true, excluded, nil
}

// commitIdentityArgs supplies a committer identity only when the repo doesn't
// already have one. A repo with user.email configured keeps it, so commits
// still look like they came from the user's own setup.
func commitIdentityArgs(dir string) []string {
	if email, err := runGitTrimmed(dir, "config", "user.email"); err == nil && email != "" {
		return nil
	}
	return []string{
		"-c", "user.name=Multica Agent",
		"-c", "user.email=agent@multica.local",
	}
}

func worktreeIsDirty(worktreePath string) (bool, error) {
	out, err := runGit(worktreePath, "status", "--porcelain")
	if err != nil {
		return false, fmt.Errorf("git status: %s: %w", strings.TrimSpace(out), err)
	}
	return strings.TrimSpace(out) != "", nil
}

// removeLocalWorktreeDir unregisters the worktree from the user's repo and
// deletes its directory. The branch is deliberately left alone — it is the
// task's deliverable.
func removeLocalWorktreeDir(gitRoot, worktreePath string, logger *slog.Logger) error {
	var removeErr error
	if out, err := runGit(gitRoot, "worktree", "remove", "--force", worktreePath); err != nil {
		removeErr = err
		if logger != nil {
			logger.Warn("execenv: git worktree remove failed; pruning registration",
				"path", worktreePath, "output", out, "error", err)
		}
		// Fall back to deleting the directory ourselves and dropping the now
		// dangling registration, so the user's repo isn't left listing a
		// worktree that no longer exists.
		if rmErr := os.RemoveAll(worktreePath); rmErr != nil {
			removeErr = errors.Join(removeErr, rmErr)
			if logger != nil {
				logger.Warn("execenv: remove worktree directory failed", "path", worktreePath, "error", rmErr)
			}
		}
		if out, pruneErr := runGit(gitRoot, "worktree", "prune"); pruneErr != nil && logger != nil {
			logger.Warn("execenv: git worktree prune failed", "output", out, "error", pruneErr)
		}
	}
	// Lstat verifies the path entry itself is gone. Stat would treat a broken
	// symlink as absent even though a stale entry still occupies the handoff path.
	if _, statErr := os.Lstat(worktreePath); errors.Is(statErr, os.ErrNotExist) {
		return nil
	} else if statErr != nil {
		return fmt.Errorf("confirm worktree removal: %w", statErr)
	}
	if removeErr != nil {
		return fmt.Errorf("worktree directory still exists after removal fallback: %w", removeErr)
	}
	return errors.New("worktree directory still exists after git removal reported success")
}

// deleteBranch drops a task branch that carries nothing worth keeping — an
// empty read-only run, or a prepare that aborted partway. Best-effort: a
// leftover branch is untidy, never harmful.
func deleteBranch(gitRoot, branch string, logger *slog.Logger) {
	if branch == "" {
		return
	}
	if out, err := runGit(gitRoot, "branch", "-D", branch); err != nil && logger != nil {
		logger.Warn("execenv: delete task branch failed (non-fatal)",
			"branch", branch, "output", out, "error", err)
	}
}

// resolveGitRoot returns the repository root containing dir. Worktree mode is
// opt-in per resource, so a non-git directory here is a misconfiguration the
// user needs to see and fix — we fail closed with an actionable message rather
// than silently degrading to the in-place lock, which would leave the user
// wondering why their tasks still queue.
func resolveGitRoot(dir string) (string, error) {
	root, err := runGitTrimmed(dir, "rev-parse", "--show-toplevel")
	if err != nil || root == "" {
		return "", fmt.Errorf("execenv: local_directory %q is not a git repository, "+
			"but its project resource is set to execution_mode=worktree; "+
			"initialise a repository there or switch the resource back to in_place", dir)
	}
	// EvalSymlinks so the root matches the path git reports from inside the
	// worktree later — on macOS /tmp vs /private/tmp otherwise produce two
	// different lock keys for one repo.
	if resolved, evalErr := filepath.EvalSymlinks(root); evalErr == nil {
		root = resolved
	}
	return filepath.Clean(root), nil
}

// captureUserSnapshot records the user's working directory as one commit: their
// tracked modifications AND their untracked-but-not-ignored files, in a single
// tree parented at their HEAD.
//
// One snapshot rather than the older split — a `git stash create` for tracked
// edits and a file copy for untracked ones — because a continued branch has to
// answer "what has the user changed since the turn I already carry?", and that
// question is unanswerable for a file no snapshot ever recorded: an untracked
// file the agent then edited would be re-copied from the user's older version
// every turn, or its later deletion would never carry. A tree also expresses
// deletions and mode changes, which a copy cannot.
//
// Nothing here touches the user's index, working tree or refs. GIT_INDEX_FILE
// points at a private index in the task's env root, so `git add` writes only
// blob objects and that file — which is also what makes the capture immune to
// the .git/index.lock races that used to be able to end the task (#7434): the
// only lock taken is on our own temporary file.
func captureUserSnapshot(gitRoot, envRoot, headSHA string, logger *slog.Logger) (string, error) {
	return captureUserSnapshotAt(gitRoot, envRoot, headSHA, logger, 0)
}

// captureUserSnapshotAt is captureUserSnapshot for one level of the gitlink
// tree. depth separates the private index files of parent and child so an
// embed never clobbers the staging its parent already did, and gates the
// embedding itself (see maxGitlinkEmbedDepth).
func captureUserSnapshotAt(gitRoot, envRoot, headSHA string, logger *slog.Logger, depth int) (string, error) {
	if envRoot == "" {
		return "", errors.New("execenv: user snapshot requires an env root to build its index in")
	}
	indexPath := filepath.Join(envRoot, fmt.Sprintf("%s-d%d", snapshotIndexFileName, depth))
	if err := os.Remove(indexPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("clear snapshot index: %w", err)
	}
	// It is scratch space, not task state: the env root is handed to the agent.
	defer os.Remove(indexPath)
	env := []string{"GIT_INDEX_FILE=" + indexPath}

	// Seed from the user's own index so git can trust its stat cache instead of
	// re-hashing the whole repository on every turn. A missing or torn copy is
	// not a failure — read-tree rebuilds a correct index, merely a colder one —
	// so the fallback runs on any error from the add, not just from the copy.
	seeded := seedSnapshotIndex(gitRoot, indexPath)
	// Tracked content everywhere first, then everything the pathspecs leave.
	// `-u` walks only the index, so it neither reads nor races on the untracked
	// runtime state the second step excludes — while a genuinely tracked file
	// under an excluded directory (a user-committed .vscode/settings.json, say)
	// still reaches the snapshot with its working-tree content.
	stage := func() error {
		if out, err := runGitEnv(gitRoot, env, "add", "-u"); err != nil {
			return fmt.Errorf("git add -u: %s: %w", strings.TrimSpace(out), err)
		}
		addArgs := append([]string{"add", "-A", "--"}, stagingExcludes()...)
		if out, err := runGitEnv(gitRoot, env, addArgs...); err != nil {
			return fmt.Errorf("git add: %s: %w", strings.TrimSpace(out), err)
		}
		return nil
	}
	if err := stage(); err != nil {
		if !seeded {
			return "", err
		}
		if logger != nil {
			logger.Debug("execenv: snapshot index seeded from the repository index was unusable; rebuilding it",
				"git_root", gitRoot, "error", err)
		}
		if out, resetErr := runGitEnv(gitRoot, env, "read-tree", headSHA); resetErr != nil {
			return "", fmt.Errorf("git read-tree: %s: %w", strings.TrimSpace(out), resetErr)
		}
		if err := stage(); err != nil {
			return "", err
		}
	}
	// Gitlink children are opaque to every step above: `git add` does not
	// descend into a nested repository, so a checkout of this tree would
	// materialise each child as an empty directory and the agent would review
	// a project with no code in it. Replace the staged gitlink entries with
	// the child repositories' own snapshots (real trees) before writing the
	// parent tree; everything downstream — replay, finalize, the delivery
	// branch — is tree-level and treats the embedded content as ordinary
	// files with no further changes.
	if err := embedGitlinkChildren(gitRoot, env, envRoot, depth, logger); err != nil {
		return "", err
	}
	tree, err := runGitTrimmedEnv(gitRoot, env, "write-tree")
	if err != nil {
		return "", fmt.Errorf("git write-tree: %w", err)
	}
	// The identity args cover a repo with no user.email configured: writing a
	// commit object needs a committer, and without them the user's uncommitted
	// work would be dropped on a technicality.
	args := append(commitIdentityArgs(gitRoot), "commit-tree", tree, "-p", headSHA, "-m",
		"multica: local directory snapshot\n\nThe tree of this commit is the user's working directory as a task saw it.")
	snapshot, err := runGitTrimmed(gitRoot, args...)
	if err != nil {
		return "", fmt.Errorf("git commit-tree: %w", err)
	}
	return snapshot, nil
}

// seedSnapshotIndex copies the repository's index to path, reporting whether it
// got one. Read as a plain file rather than through git: git would want the
// index lock, and this copy exists precisely to avoid waiting on it.
func seedSnapshotIndex(gitRoot, path string) bool {
	src, err := runGitTrimmed(gitRoot, "rev-parse", "--git-path", "index")
	if err != nil || src == "" {
		return false
	}
	if !filepath.IsAbs(src) {
		src = filepath.Join(gitRoot, src)
	}
	return copyFile(src, path) == nil
}

// stagedGitlink is one mode-160000 index entry: a path the parent repo tracks
// as a pointer to a nested repository, with the commit it points at.
type stagedGitlink struct {
	commit string
	path   string
}

// trackedGitlinkDirs lists the repository-root directories of the gitlinks
// the user's index currently tracks. Read from the user's index (a lock-free
// read) rather than a staged one because this runs before any staging exists;
// for the budget check, what the user tracks is the honest set.
func trackedGitlinkDirs(gitRoot string) []string {
	links, err := stagedGitlinks(gitRoot, nil)
	if err != nil {
		return nil
	}
	dirs := make([]string, 0, len(links))
	for _, link := range links {
		dir, ok := gitlinkPathWithinRoot(gitRoot, link.path)
		if !ok {
			continue
		}
		dirs = append(dirs, dir)
	}
	return dirs
}

// gitlinkPathWithinRoot resolves a gitlink's index path against gitRoot and
// reports whether the result stays inside it. Git's own tree format already
// rejects "." and ".." path components, so a well-formed index cannot produce
// an escaping entry — but embedGitlinkChild goes on to lock, read HEAD from,
// and fetch objects out of whatever this resolves to, so the boundary is
// checked explicitly here rather than trusted as an upstream invariant.
func gitlinkPathWithinRoot(gitRoot, relPath string) (string, bool) {
	if relPath == "" || filepath.IsAbs(relPath) {
		return "", false
	}
	joined := filepath.Join(gitRoot, filepath.FromSlash(relPath))
	rel, err := filepath.Rel(gitRoot, joined)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return joined, true
}

// stagedGitlinks lists the gitlink entries in the snapshot index being built.
// Parsed with -z: core.quotepath (on by default) would otherwise deliver a
// non-ASCII path as a quoted octal-escape string, and every consumer below —
// childRepoRoot, update-index, read-tree --prefix — needs the literal bytes.
func stagedGitlinks(gitRoot string, env []string) ([]stagedGitlink, error) {
	out, err := runGitEnv(gitRoot, env, "ls-files", "-s", "-z")
	if err != nil {
		return nil, fmt.Errorf("git ls-files -s: %s: %w", strings.TrimSpace(out), err)
	}
	var links []stagedGitlink
	for _, record := range strings.Split(out, "\x00") {
		parts := strings.SplitN(record, "\t", 2)
		if len(parts) != 2 {
			continue
		}
		meta := strings.Fields(parts[0])
		if len(meta) != 3 || meta[0] != "160000" {
			continue
		}
		links = append(links, stagedGitlink{commit: meta[1], path: parts[1]})
	}
	return links, nil
}

// embedGitlinkChildren replaces every gitlink entry staged in the snapshot
// index with the embedded tree of the repository that entry points at.
//
// The child's tree is its own snapshot — HEAD plus its uncommitted and
// untracked-not-ignored files, under the child's OWN ignore rules — so the
// agent sees exactly the code the user has, not the remote's idea of it.
// Objects cross into the parent's database through a local `git fetch`, which
// is what keeps the whole manoeuvre cheap: content-addressed blobs that are
// already there (every file the parent ever embedded and that has not changed)
// transfer as zero bytes.
//
// A gitlink whose directory is missing, not a repository, or has no commits is
// skipped with a warning: it stays a gitlink and the worktree keeps an empty
// directory there, which is what worktree mode did before embedding existed.
func embedGitlinkChildren(gitRoot string, env []string, envRoot string, depth int, logger *slog.Logger) error {
	if depth >= maxGitlinkEmbedDepth {
		return nil
	}
	children, err := stagedGitlinks(gitRoot, env)
	if err != nil {
		return err
	}
	for _, link := range children {
		if err := embedGitlinkChild(gitRoot, env, envRoot, depth, link, logger); err != nil {
			return err
		}
	}
	return nil
}

// embedGitlinkChild embeds one gitlink child into the parent's staged index.
func embedGitlinkChild(gitRoot string, env []string, envRoot string, depth int, link stagedGitlink, logger *slog.Logger) error {
	childDir, ok := gitlinkPathWithinRoot(gitRoot, link.path)
	if !ok {
		return fmt.Errorf("execenv: gitlink entry %q escapes its repository root %q", link.path, gitRoot)
	}
	childRoot, ok := childRepoRoot(childDir)
	if !ok {
		if logger != nil {
			logger.Warn("execenv: gitlink child is not a usable repository; leaving it as an empty directory in the worktree",
				"parent", gitRoot, "path", link.path)
		}
		return nil
	}

	// The child is the user's repository: everything we do inside it — reading
	// its state, writing blobs, a momentary ref — runs under its own cross-
	// process lock, the same contract the parent enjoys. Lock order is always
	// parent → child, the only direction embedding goes, so this cannot
	// deadlock against another embed.
	unlock, err := lockGitRoot(childRoot, logger)
	if err != nil {
		return fmt.Errorf("could not lock the gitlink child %q to snapshot it: %w", link.path, err)
	}
	defer unlock()

	childHead, err := runGitTrimmed(childRoot, "rev-parse", "--verify", "HEAD")
	if err != nil {
		if logger != nil {
			logger.Warn("execenv: gitlink child has no commits; leaving it as an empty directory in the worktree",
				"parent", gitRoot, "path", link.path)
		}
		return nil
	}
	snapshot, err := captureUserSnapshotAt(childRoot, envRoot, childHead, logger, depth+1)
	if err != nil {
		return fmt.Errorf("snapshot the gitlink child %q: %w", link.path, err)
	}
	if err := fetchSnapshotObjects(gitRoot, childRoot, snapshot, logger); err != nil {
		return err
	}
	tree, err := runGitTrimmed(childRoot, "rev-parse", snapshot+"^{tree}")
	if err != nil || tree == "" {
		return fmt.Errorf("resolve the snapshot tree of the gitlink child %q: %w", link.path, err)
	}
	// A git index holds blobs and gitlinks, never directories: --cacheinfo
	// cannot insert the child tree (mode 040000 is the sparse-index's, and git
	// rejects it here). The classic subtree-merge pair does it instead — drop
	// the gitlink entry, then read the child's tree in under the same prefix,
	// where write-tree folds it back into the parent tree.
	if out, err := runGitEnv(gitRoot, env, "update-index", "--force-remove", link.path); err != nil {
		return fmt.Errorf("drop the gitlink entry %q from the snapshot index: %s: %w", link.path, strings.TrimSpace(out), err)
	}
	prefix := strings.TrimSuffix(link.path, "/") + "/"
	if out, err := runGitEnv(gitRoot, env, "read-tree", "--prefix="+prefix, tree); err != nil {
		return fmt.Errorf("embed the gitlink child %q into the snapshot index: %s: %w", link.path, strings.TrimSpace(out), err)
	}
	if logger != nil {
		logger.Info("execenv: embedded gitlink child into the task worktree snapshot",
			"parent", gitRoot, "path", link.path, "child_root", childRoot)
	}
	return nil
}

// childRepoRoot reports the repository root when dir is itself the top of a
// git working tree — a gitlink points at exactly that. A dir nested deeper
// inside some other repository (tolerated by git, meaningless to embed) and a
// plain non-repository directory both report false.
func childRepoRoot(dir string) (string, bool) {
	root, err := resolveGitRoot(dir)
	if err != nil {
		return "", false
	}
	canonical := dir
	if resolved, evalErr := filepath.EvalSymlinks(dir); evalErr == nil {
		canonical = resolved
	}
	return root, root == filepath.Clean(canonical)
}

// fetchSnapshotObjects copies the objects the child snapshot needs from the
// child's object database into the parent's, by fetching a momentary ref.
// Git's local fetch moves exactly the missing objects in one pack, so an
// unchanged child costs nothing on every later turn.
//
// The ref name embeds its creation time: a crash between update-ref and delete
// leaves it behind in the CHILD's repo, and pruneGitlinkEmbedRefs can then age
// it out without ever racing a live embed from another parent (whose ref, by
// construction, is younger).
func fetchSnapshotObjects(parentRoot, childRoot, snapshot string, logger *slog.Logger) error {
	childGitDir, err := runGitTrimmed(childRoot, "rev-parse", "--absolute-git-dir")
	if err != nil || childGitDir == "" {
		return fmt.Errorf("locate the object database of the gitlink child %q: %w", childRoot, err)
	}
	ref := fmt.Sprintf("%s%d-%s", gitlinkEmbedRefPrefix, time.Now().UnixNano(), randomHex(6))
	if out, err := runGit(childRoot, "update-ref", ref, snapshot); err != nil {
		return fmt.Errorf("stage the gitlink child snapshot for transfer: %s: %w", strings.TrimSpace(out), err)
	}
	defer func() {
		if out, delErr := runGit(childRoot, "update-ref", "-d", ref); delErr != nil && logger != nil {
			logger.Warn("execenv: could not drop the momentary gitlink transfer ref (non-fatal)",
				"child_root", childRoot, "ref", ref, "output", strings.TrimSpace(out), "error", delErr)
		}
	}()
	if out, err := runGit(parentRoot, "fetch", "--quiet", "--no-tags", childGitDir, ref); err != nil {
		return fmt.Errorf("copy the gitlink child's objects into %q: %s: %w", parentRoot, strings.TrimSpace(out), err)
	}
	return nil
}

// pruneGitlinkEmbedRefs drops momentary transfer refs a crashed embed left in
// a child repository. Anything older than an hour cannot belong to a live
// embed — an embed holds the PARENT's lock for its whole duration, and this
// prune only runs while preparing the same parent, so the only refs it can
// see are leftovers or a concurrent embed through a different parent, whose
// ref is minutes old at most.
func pruneGitlinkEmbedRefs(childRoot string, logger *slog.Logger) {
	out, err := runGitTrimmed(childRoot, "for-each-ref", "--format=%(refname)", gitlinkEmbedRefPrefix)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-time.Hour).Unix()
	for _, ref := range strings.Split(out, "\n") {
		ref = strings.TrimSpace(ref)
		name := strings.TrimPrefix(ref, gitlinkEmbedRefPrefix)
		stampStr, _, _ := strings.Cut(name, "-")
		stamp, parseErr := strconv.ParseInt(stampStr, 10, 64)
		if parseErr != nil || stamp >= cutoff {
			continue
		}
		if _, delErr := runGit(childRoot, "update-ref", "-d", ref); delErr != nil && logger != nil {
			logger.Warn("execenv: could not drop a stale gitlink transfer ref (non-fatal)",
				"child_root", childRoot, "ref", ref)
		}
	}
}

// randomHex returns n random bytes hex-encoded, for ref-name uniqueness.
func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		// Degenerate fallback: the nanosecond stamp already makes the name
		// unique for any practical purpose.
		return "000000000000"
	}
	return hex.EncodeToString(b)
}

// stagingExcludes are the pathspecs every daemon-driven `git add -A` carries —
// the user's snapshot and a task worktree's finalize alike. They keep out the
// daemon's own sidecars, which an in_place task leaves untracked in the user's
// directory whenever it is mid-flight on the same path or was killed before its
// cleanup ran: carrying them would put another issue's brief inside this
// task's worktree — where the agent would read it as its own context — and
// commit it to the branch. They also prune the state directories agent CLIs
// and editors churn while a task runs (runtimeStateDirNames), whose vanishing
// files used to kill the finalize add outright, and the dependency/cache
// directories (replayableCacheDirNames) a research run can fill with gigabytes
// of regenerable content. Matched at any depth, because a resource may point
// at a subdirectory of this repo.
func stagingExcludes() []string {
	names := make([]string, 0, len(multicaSidecarDirNames)+len(runtimeStateDirNames)+len(replayableCacheDirNames))
	names = append(names, multicaSidecarDirNames...)
	names = append(names, runtimeStateDirNames...)
	names = append(names, replayableCacheDirNames...)
	specs := make([]string, 0, len(names))
	for _, name := range names {
		specs = append(specs, ":(exclude,glob)**/"+name+"/**")
	}
	return specs
}

// branchOwner is the conversation a task branch belongs to. Recorded with the
// branch's snapshot and compared before any later task continues it: the branch
// NAME carries a human-readable issue key, which the user can also type and
// which two workspaces can mint identically, so the name alone can never
// establish that a branch is ours to append to (MUL-6881 review).
type branchOwner struct {
	WorkspaceID    string
	AgentID        string
	ConversationID string
	// TaskID is set only on a task-scoped branch, and it is what makes such a
	// branch continuable: a retried task keeps its id, so attempt 2 can prove
	// the branch attempt 1 left behind is its own instead of failing on the
	// name collision (RUYI-116). Empty on a conversation branch, which is
	// shared by every task of that conversation by design.
	TaskID string
}

func (o branchOwner) valid() bool {
	return o.WorkspaceID != "" && o.AgentID != "" && o.ConversationID != ""
}

// validTask reports whether this owner identifies one task well enough to
// record a task-scoped branch under it. The conversation id is not required:
// a task with no issue and no chat session still has a workspace, an agent and
// an id of its own.
func (o branchOwner) validTask() bool {
	return o.WorkspaceID != "" && o.AgentID != "" && o.TaskID != ""
}

// fingerprint is the stable, collision-resistant form of the same identity,
// used to name a branch when the readable name is already taken by someone
// else's. Stable across turns, so the fallback branch is continued too.
func (o branchOwner) fingerprint() string {
	sum := sha256.Sum256([]byte(o.WorkspaceID + "\x00" + o.AgentID + "\x00" + o.ConversationID))
	return hex.EncodeToString(sum[:])[:12]
}

const (
	ownerTrailerWorkspace    = "Multica-Workspace"
	ownerTrailerAgent        = "Multica-Agent"
	ownerTrailerConversation = "Multica-Conversation"
	ownerTrailerTask         = "Multica-Task"
)

// branchRecord is what refs/multica/local-state/<branch> holds: a commit whose
// TREE is the user's directory as the branch last carried it, whose SECOND
// PARENT is the branch tip at that moment, and whose message names the owner.
//
// The checkpoint is what makes the record about this BRANCH rather than merely
// about its name. Owner alone proved only that Multica once wrote a branch
// called this, and that stayed true after the user deleted it and created their
// own under the same name — the next task then continued into their work
// (MUL-6881 review). Requiring the checkpoint to still be an ancestor of the
// tip is the continuity proof: a branch deleted and recreated, force-moved onto
// unrelated history, or rebased no longer contains the commit recorded here.
type branchRecord struct {
	// state is the commit whose tree is the user snapshot the branch carries.
	state string
	// checkpoint is the branch tip this record was written against.
	checkpoint string
	owner      branchOwner
}

// writeBranchRecord records the branch as carrying userState at checkpoint, and
// points the branch's ref at that record.
//
// The checkpoint is passed in, never re-read from the branch ref here: the
// caller knows which commit it actually delivered, while the ref is the user's
// and can move between the delivery and this write. Recording what we delivered
// means a branch that moved in that window simply fails the ancestor test next
// time, which is the safe direction.
func writeBranchRecord(gitRoot, branch, userState, checkpoint string, owner branchOwner) (string, error) {
	if checkpoint == "" {
		return "", fmt.Errorf("no checkpoint to record for branch %s", branch)
	}
	args := append(commitIdentityArgs(gitRoot), "commit-tree", userState+"^{tree}",
		"-p", userState, "-p", checkpoint, "-m", branchRecordMessage(owner))
	record, err := runGitTrimmed(gitRoot, args...)
	if err != nil {
		return "", fmt.Errorf("git commit-tree: %w", err)
	}
	if out, err := runGit(gitRoot, "update-ref", userStateRef(branch), record); err != nil {
		return "", fmt.Errorf("git update-ref: %s: %w", strings.TrimSpace(out), err)
	}
	return record, nil
}

func branchRecordMessage(owner branchOwner) string {
	var b strings.Builder
	b.WriteString("multica: task branch record\n\n")
	b.WriteString("Written by Multica for a local_directory task running in worktree mode. Its\n")
	b.WriteString("tree is the user's working directory as this branch last carried it, and its\n")
	b.WriteString("second parent is the branch tip at that moment — together they let the next\n")
	b.WriteString("turn replay only what changed since, and prove the branch is still the one\n")
	b.WriteString("recorded here. Safe to delete along with the branch.\n\n")
	fmt.Fprintf(&b, "%s: %s\n", ownerTrailerWorkspace, owner.WorkspaceID)
	fmt.Fprintf(&b, "%s: %s\n", ownerTrailerAgent, owner.AgentID)
	fmt.Fprintf(&b, "%s: %s\n", ownerTrailerConversation, owner.ConversationID)
	if owner.TaskID != "" {
		fmt.Fprintf(&b, "%s: %s\n", ownerTrailerTask, owner.TaskID)
	}
	return b.String()
}

// readBranchRecord reads back what a branch is recorded as carrying. A commit
// without all three trailers or without a second parent — anything not written
// by writeBranchRecord — yields a record that can never match a valid owner.
func readBranchRecord(gitRoot, commit string) (branchRecord, error) {
	body, err := runGitTrimmed(gitRoot, "log", "-1", "--format=%B", commit)
	if err != nil {
		return branchRecord{}, err
	}
	record := branchRecord{state: commit}
	for _, line := range strings.Split(body, "\n") {
		key, value, found := strings.Cut(strings.TrimSpace(line), ":")
		if !found {
			continue
		}
		value = strings.TrimSpace(value)
		switch key {
		case ownerTrailerWorkspace:
			record.owner.WorkspaceID = value
		case ownerTrailerAgent:
			record.owner.AgentID = value
		case ownerTrailerConversation:
			record.owner.ConversationID = value
		case ownerTrailerTask:
			record.owner.TaskID = value
		}
	}
	if checkpoint, err := runGitTrimmed(gitRoot, "rev-parse", "--verify", "--quiet", commit+"^2"); err == nil {
		record.checkpoint = checkpoint
	}
	return record, nil
}

// addLocalWorktree creates the worktree, retrying once under a suffixed branch
// name when the branch already exists (a re-dispatched task keeps its id, so
// its branch can survive from the previous run).
// taskBranchPlan is the decision about which branch a task's worktree checks
// out and which commit it starts from.
type taskBranchPlan struct {
	// name is the branch to check out or create.
	name string
	// base is the commit the worktree starts from.
	base string
	// continues is true when base is an earlier turn's branch tip rather than
	// the user's HEAD, so the checkout already carries that turn's work.
	continues bool
	// priorState is the user snapshot that branch is recorded as already
	// carrying. Set only when continues is true; the edit set that snapshot
	// holds against the checkout HEAD it was captured on is what this turn's
	// replay treats as already in the branch.
	priorState string
	// priorCheckpoint is the commit that branch was recorded at and still
	// contains — this turn's proof that the branch is the conversation's. The
	// branch tip this turn starts from descends from it, so everything after
	// prepare measures against that tip instead.
	priorCheckpoint string
	// reset is true when the conversation's branch exists but is fully merged
	// into HEAD, and this task restarts it there.
	reset bool
	// conversational is true when name is keyed to the conversation rather than
	// to this one task, so a sibling task may legitimately want it too.
	conversational bool
	// tracksState is true when a later turn may continue this branch, and it
	// therefore has to record what the user's directory looked like.
	tracksState bool
	// owner is the identity the branch is recorded under.
	owner branchOwner
}

// altName disambiguates a branch a live sibling already holds.
func (p taskBranchPlan) altName(taskID string) string {
	if p.conversational {
		// Task-scoped, and readable next to the conversation branch it forked
		// from: agent/j/mul-6881-<task>.
		return p.name + "-" + taskKey(taskID)
	}
	// Already task-scoped, so only a re-run of the same task can collide here.
	return fmt.Sprintf("%s-%d", p.name, time.Now().Unix())
}

// resolveTaskBranch picks the branch for this task.
//
// Tasks that belong to a conversation — the turns of one issue, one chat
// session — share a branch, because they are one line of work: the user says
// "now also fix the caller" and expects the agent to be standing on what it
// wrote a minute ago. Keying the branch to the task instead gave every turn its
// own branch forked from HEAD, so turn two started in a tree that did not
// contain turn one's work and nothing said so (MUL-6881). Per-task env roots
// stay as they are — sibling tasks still run concurrently, they just deliver
// onto the branch their conversation owns.
//
// Continuing a branch is decided by its recorded OWNER, never by its name.
// `agent/j/mul-6881` is a name the user can type, another agent of the same
// display name can produce, and a second workspace can mint for a different
// issue; appending to any of those would silently mix two lines of work. So a
// same-named branch we cannot prove is ours pushes this task onto
// `agent/j/mul-6881-<fingerprint>` — stable for this exact conversation, so its
// own follow-ups continue it — and, if that is somehow taken too, onto a
// task-scoped branch that continues nothing.
func resolveTaskBranch(gitRoot string, params LocalWorktreeParams, headSHA string, logger *slog.Logger) (plan taskBranchPlan) {
	// A branch this prepare is about to CREATE starts from origin's tip, not
	// from local HEAD (RUYI-579 W1): the daemon's local HEAD may carry
	// un-pushed work — a commit only some agent branch has — and a conversation
	// forked there diverges from the mainline from birth, so the delivery
	// guard refuses the turn the member aligns onto the latest code. The
	// origin tip keeps new work on the shared baseline; local commits that are
	// not pushed are deliberately NOT carried forward (recorded as a
	// branch_base_divergence event). Every other shape — continuing a branch,
	// resetting a merged one, a healed refusal — keeps the base it already
	// resolved, and a repository with no usable origin anchor keeps local HEAD.
	defer func() {
		if !plan.continues && !plan.reset && plan.base == headSHA && !branchExists(gitRoot, plan.name) {
			plan.base = anchorNewBranchBase(gitRoot, headSHA, logger)
		}
	}()

	agentSegment := agentBranchSegment(params)

	owner := params.owner()
	if params.ConversationKey != "" && owner.valid() {
		preferred := fmt.Sprintf("agent/%s/%s", agentSegment, sanitizeName(params.ConversationKey))
		for _, name := range []string{preferred, preferred + "-" + owner.fingerprint()} {
			plan, ok := planForExistingBranch(gitRoot, name, headSHA, owner, true, logger)
			if ok {
				return plan
			}
			if logger != nil {
				logger.Info("execenv: branch exists but is not this conversation's; not continuing it",
					"git_root", gitRoot, "branch", name)
			}
		}
	}

	// Task-scoped branch. A retried task keeps its id, so this name is exactly
	// the one attempt 1 may have left behind — the same ownership proof the
	// conversation branch uses decides whether attempt 2 may take it over
	// (RUYI-116). Without an identity to record under, the branch stays
	// un-continuable and a collision falls through to addLocalWorktree's
	// suffixed name.
	taskOwner := branchOwner{WorkspaceID: params.WorkspaceID, AgentID: params.AgentID, TaskID: params.TaskID}
	taskScoped := taskBranchPlan{name: fmt.Sprintf("agent/%s/%s", agentSegment, taskKey(params.TaskID)), base: headSHA}
	if !taskOwner.validTask() {
		return taskScoped
	}
	plan, ok := planForExistingBranch(gitRoot, taskScoped.name, headSHA, taskOwner, false, logger)
	if !ok {
		if logger != nil {
			logger.Info("execenv: branch exists but is not this task's; not continuing it",
				"git_root", gitRoot, "branch", taskScoped.name)
		}
		return taskScoped
	}
	return plan
}

// anchorNewBranchBase resolves the base commit for a branch this prepare is
// about to create: origin's default-branch tip when the repository has a
// usable origin anchor, local HEAD otherwise (RUYI-579 W1).
//
// Fail-closed by construction: no origin remote, no resolvable
// refs/remotes/origin/HEAD, or no fetched tip all return headSHA unchanged,
// which is the pre-W1 behaviour. The fetch is best-effort — an offline host
// still anchors at the last-fetched tracking tip rather than at local HEAD.
// When the local HEAD is NOT contained in the chosen tip (un-pushed work, or
// a fork), the divergence is recorded as a structured branch_base_divergence
// event and the task proceeds from the origin tip: un-pushed commits are not
// carried into new work lines, and the diff noise that creates is the
// agent's to handle at turn start, which beats the delivery guard refusing
// the whole turn at finalize.
func anchorNewBranchBase(gitRoot, headSHA string, logger *slog.Logger) string {
	ref, err := runGitTrimmed(gitRoot, "symbolic-ref", "--quiet", "refs/remotes/origin/HEAD")
	if err != nil || ref == "" {
		return headSHA
	}
	// Only a NEW branch fetches; continued branches skip this entirely.
	if out, fetchErr := runGit(gitRoot, "fetch", "--quiet", "origin"); fetchErr != nil && logger != nil {
		logger.Warn("execenv: could not fetch origin before anchoring a new branch (non-fatal; anchoring at the last fetched tip)",
			"git_root", gitRoot, "output", strings.TrimSpace(out), "error", fetchErr)
	}
	tip, err := runGitTrimmed(gitRoot, "rev-parse", "--verify", "--quiet", ref)
	if err != nil || tip == "" {
		return headSHA
	}
	if _, err := runGit(gitRoot, "merge-base", "--is-ancestor", headSHA, tip); err != nil && logger != nil {
		logger.Info("branch_base_divergence",
			"git_root", gitRoot,
			"head_sha", headSHA,
			"origin_tip", tip,
			"origin_ref", ref,
			"note", "local HEAD is not contained in the origin default branch; the new branch starts from the origin tip",
		)
	}
	return tip
}

// agentBranchSegment is the agent's segment of a branch name.
//
// An agent named entirely in a non-Latin script sanitises to nothing, and the
// literal "agent" fallback put every such agent's branches under
// `agent/agent/...` — readable for nobody and identical between agents
// (RUYI-116). The agent id's short form is stable, distinct per agent, and
// still a valid branch segment, so it stands in when the name cannot.
func agentBranchSegment(params LocalWorktreeParams) string {
	if s := sanitizeSegment(params.AgentName); s != "" {
		return s
	}
	if params.AgentID != "" {
		return "agent-" + taskKey(params.AgentID)
	}
	return "agent"
}

// planForExistingBranch reports how this task would use one candidate branch
// name, and whether it may use it at all.
func planForExistingBranch(gitRoot, name, headSHA string, owner branchOwner, conversational bool, logger *slog.Logger) (taskBranchPlan, bool) {
	plan := taskBranchPlan{
		name:           name,
		base:           headSHA,
		conversational: conversational,
		tracksState:    true,
		owner:          owner,
	}
	tip, err := runGitTrimmed(gitRoot, "rev-parse", "--verify", "--quiet", "refs/heads/"+name)
	if err != nil || tip == "" {
		// Free to create.
		return plan, true
	}
	record, owned := branchOwnedBy(gitRoot, name, owner, logger)
	if !owned {
		// The record's checkpoint may be gone because a session reset the
		// branch's history away mid-run — the shape the delivery guard then
		// refuses at finalize (RUYI-479). The refusal marker decides: with
		// one, and only checkpoints were dropped, the retry re-anchors this
		// branch instead of forking the conversation onto a fresh name.
		if plan, healed := healGuardRefusal(gitRoot, name, owner, conversational, logger); healed {
			return plan, true
		}
		return taskBranchPlan{}, false
	}
	// The user merged it: the branch tip carries nothing HEAD does not, so
	// continuing from it would strand this task behind their own commits.
	if _, mergedErr := runGit(gitRoot, "merge-base", "--is-ancestor", name, "HEAD"); mergedErr == nil {
		plan.reset = true
		return plan, true
	}
	// Owned and unmerged, but a refusal marker may still pin a delivered tip
	// the branch never took (a run that ended detached from its branch). When
	// the marker's tip carries only daemon checkpoints beyond this one,
	// adopting it is what the retry is for.
	if plan, healed := healGuardRefusal(gitRoot, name, owner, conversational, logger); healed {
		return plan, true
	}
	plan.base = tip
	plan.continues = true
	plan.priorState = record.state
	plan.priorCheckpoint = record.checkpoint
	if logger != nil {
		logger.Info("execenv: continuing the conversation's existing branch",
			"git_root", gitRoot, "branch", name, "tip", tip)
	}
	return plan, true
}

// branchOwnedBy reports whether a branch is still the one this conversation
// recorded, returning the user snapshot it carries.
//
// Two questions, and both have to hold. Is the record ours — workspace, agent
// and conversation ids. And is the branch still the one it was written against
// — the recorded checkpoint has to be an ancestor of the current tip. The
// second is not pedantry: a branch the user deleted and recreated under the
// same name still satisfies the first, and continuing it would append this
// conversation onto their unrelated work.
//
// A branch with no record is not ours by definition: every branch this code
// creates writes one before its task is allowed to run.
func branchOwnedBy(gitRoot, branch string, owner branchOwner, logger *slog.Logger) (branchRecord, bool) {
	ref, err := readUserStateRef(gitRoot, branch)
	if err != nil || ref == "" {
		return branchRecord{}, false
	}
	record, err := readBranchRecord(gitRoot, ref)
	if err != nil || record.owner != owner || record.checkpoint == "" {
		return branchRecord{}, false
	}
	if _, err := runGit(gitRoot, "merge-base", "--is-ancestor", record.checkpoint, "refs/heads/"+branch); err != nil {
		if logger != nil {
			logger.Info("execenv: branch no longer contains the commit it was recorded at; not continuing it",
				"git_root", gitRoot, "branch", branch, "checkpoint", record.checkpoint)
		}
		return branchRecord{}, false
	}
	return record, true
}

// healGuardRefusal repairs a branch the delivery guard refused one run ago,
// when the damage is the daemon's own (RUYI-479).
//
// A refusal means the run's worktree no longer proved its branch — almost
// always a session rewriting the branch's history mid-run, dropping the
// checkpoint commits the daemon made. Before this heal, every retry of that
// task forked a fresh branch: the ownership proof (the recorded checkpoint is
// still an ancestor of the tip) was broken, and the conversation's work sat
// orphaned on a branch nothing would continue. The refusal marker the guard
// writes turns that fork into a decision:
//
//   - No marker: this divergence never came from a refusal — a foreign reset,
//     a user rewrite. Stay refused; the fork is the safe answer it always was.
//   - Marker present, and the refused tip carries the branch's history forward
//     (fast-forward), and every commit the tip has that the branch lacks —
//     plus everything reachable from the record's dropped checkpoint — is a
//     chore(agent) checkpoint the daemon wrote itself: the damage is
//     self-inflicted, so the branch moves to the refused tip and the plan
//     continues from there. The next commitBaseline and recordState re-anchor
//     the record at the healed tip.
//   - Anything else in the dropped set — a session commit, a user commit — is
//     work the daemon has no authority to rewrite away. Stay refused; the
//     preserved worktree and the marker both stay for a human.
//
// The branch's holder worktree (the refused run's preserved worktree) is
// released first: it holds the branch checked out, and git grants one
// worktree per branch. Its committed work is the refused tip itself; its
// untracked content — the staging exclusions the refusal stopped short of
// preserving — is moved aside before removal, same as finalize's.
//
// Every failure mode returns false, and the caller behaves exactly as before
// the heal existed. Best-effort by construction.
func healGuardRefusal(gitRoot, name string, owner branchOwner, conversational bool, logger *slog.Logger) (taskBranchPlan, bool) {
	refusedTip, err := runGitTrimmed(gitRoot, "rev-parse", "--verify", "--quiet", guardRefusalRef(name))
	if err != nil || refusedTip == "" {
		return taskBranchPlan{}, false
	}
	ref, err := readUserStateRef(gitRoot, name)
	if err != nil || ref == "" {
		return taskBranchPlan{}, false
	}
	record, err := readBranchRecord(gitRoot, ref)
	if err != nil || record.owner != owner || record.checkpoint == "" {
		return taskBranchPlan{}, false
	}
	branchTip, err := runGitTrimmed(gitRoot, "rev-parse", "--verify", "--quiet", "refs/heads/"+name)
	if err != nil || branchTip == "" {
		return taskBranchPlan{}, false
	}
	if branchTip != refusedTip {
		if _, ffErr := runGit(gitRoot, "merge-base", "--is-ancestor", branchTip, refusedTip); ffErr != nil {
			if logger != nil {
				logger.Info("execenv: not healing a refused branch whose history diverged from the refused tip",
					"git_root", gitRoot, "branch", name, "tip", branchTip, "refused_tip", refusedTip)
			}
			return taskBranchPlan{}, false
		}
	}
	// Everything the branch is missing, from both the refused tip and the
	// record's checkpoint, must be a checkpoint this codebase wrote. One
	// session or user commit in the set and the rewrite stays the user's
	// problem, not ours to rubber-stamp.
	out, err := runGitStdout(gitRoot, "rev-list", "--format=%s", record.checkpoint, refusedTip, "--not", "refs/heads/"+name)
	if err != nil {
		if logger != nil {
			logger.Warn("execenv: could not enumerate the commits a refused branch lost (non-fatal; not healing)",
				"git_root", gitRoot, "branch", name, "error", err)
		}
		return taskBranchPlan{}, false
	}
	lines := strings.Split(out, "\n")
	for i, line := range lines {
		if !strings.HasPrefix(line, "commit ") {
			continue
		}
		if i+1 >= len(lines) || !strings.HasPrefix(lines[i+1], "chore(agent):") {
			if logger != nil {
				logger.Info("execenv: not healing a refused branch whose dropped commits include non-checkpoint work",
					"git_root", gitRoot, "branch", name, "subject", strings.TrimSpace(strings.TrimPrefix(line, "commit ")))
			}
			return taskBranchPlan{}, false
		}
	}
	if branchTip != refusedTip {
		if out, err := runGit(gitRoot, "update-ref", "refs/heads/"+name, refusedTip, branchTip); err != nil {
			if logger != nil {
				logger.Warn("execenv: could not move a refused branch to its refused tip (non-fatal; not healing)",
					"branch", name, "output", strings.TrimSpace(out), "error", err)
			}
			return taskBranchPlan{}, false
		}
	}
	releaseGuardHolder(gitRoot, name, refusedTip, logger)
	if _, err := runGit(gitRoot, "update-ref", "-d", guardRefusalRef(name)); err != nil && logger != nil {
		logger.Debug("execenv: no refusal marker to clear after healing", "branch", name)
	}
	if logger != nil {
		logger.Info("execenv: healed a delivery-guard refusal; the branch continues from its refused tip",
			"git_root", gitRoot, "branch", name, "refused_tip", refusedTip)
	}
	// Replay posture follows what the branch still proves. A refusal that
	// left the record's checkpoint an ancestor of the tip — a delivery that
	// landed off-branch — keeps the continue-style replay: the checkout
	// carries the recorded edits, and the healed tip only adds checkpoints
	// on top of them. When the checkpoint is gone from the branch — the
	// reset case — its content may hold user work that exists nowhere else,
	// so the turn must not trust the branch to carry it: priorState stays
	// empty and the replay proposes the user's whole current edit set over
	// the healed tip, exactly the way a fresh fork's does. That is what
	// puts the dropped edits back instead of silently skipping them.
	priorState := ""
	if _, err := runGit(gitRoot, "merge-base", "--is-ancestor", record.checkpoint, "refs/heads/"+name); err == nil {
		priorState = record.state
	}
	return taskBranchPlan{
		name:            name,
		base:            refusedTip,
		continues:       true,
		priorState:      priorState,
		priorCheckpoint: refusedTip,
		conversational:  conversational,
		tracksState:     true,
		owner:           owner,
	}, true
}

// releaseGuardHolder removes the preserved worktree a refusal left holding
// the branch, so the healed turn can check it out again. The holder's
// committed work IS the refused tip — nothing is deleted that git does not
// still have — but its untracked content (the staging exclusions a refused
// finalize never got to preserve) is moved aside first, into a sibling
// directory that outlives the removal. Best-effort throughout: a holder that
// survives here just sends addLocalWorktree down its existing fork fallback.
func releaseGuardHolder(gitRoot, branch, refusedTip string, logger *slog.Logger) {
	out, err := runGitStdout(gitRoot, "worktree", "list", "--porcelain")
	if err != nil {
		return
	}
	for _, block := range strings.Split(out, "\n\n") {
		var path, head, branchRef string
		for _, line := range strings.Split(block, "\n") {
			switch {
			case strings.HasPrefix(line, "worktree "):
				path = strings.TrimPrefix(line, "worktree ")
			case strings.HasPrefix(line, "HEAD "):
				head = strings.TrimPrefix(line, "HEAD ")
			case strings.HasPrefix(line, "branch "):
				branchRef = strings.TrimPrefix(line, "branch ")
			}
		}
		if path == "" || path == gitRoot {
			continue
		}
		if branchRef != "refs/heads/"+branch && head != refusedTip {
			continue
		}
		salvageHolderUntracked(path, logger)
		if err := removeLocalWorktreeDir(gitRoot, path, logger); err != nil && logger != nil {
			logger.Warn("execenv: could not release the preserved worktree holding a healed branch (non-fatal; the retry will fork)",
				"path", path, "error", err)
		} else if logger != nil {
			logger.Info("execenv: released the preserved worktree of a healed refusal",
				"git_root", gitRoot, "path", path, "branch", branch)
		}
	}
}

// salvageHolderUntracked moves the holder's untracked, non-regenerable
// entries into a sibling directory before its removal. Runtime state and
// replayable caches stay behind — they are excluded from snapshots for the
// same reason. Unlike finalize's aside this has no budget: the holder's
// content is bounded by the replay check that admitted it.
func salvageHolderUntracked(holderPath string, logger *slog.Logger) {
	out, err := runGitStdout(holderPath, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		if logger != nil {
			logger.Warn("execenv: could not enumerate a holder's untracked content before removal (non-fatal)",
				"path", holderPath, "error", err)
		}
		return
	}
	tops := map[string]bool{}
	for _, rel := range strings.Split(out, "\x00") {
		if rel == "" || isMulticaSidecarPath(rel) || isRuntimeStatePath(rel) || isReplayableCachePath(rel) {
			continue
		}
		if i := strings.IndexByte(rel, '/'); i >= 0 {
			rel = rel[:i]
		}
		tops[rel] = true
	}
	if len(tops) == 0 {
		return
	}
	aside := holderPath + "-guard-aside"
	if err := os.MkdirAll(aside, 0o755); err != nil {
		if logger != nil {
			logger.Warn("execenv: could not create the aside directory for a holder's untracked content (non-fatal)",
				"aside", aside, "error", err)
		}
		return
	}
	for name := range tops {
		if err := os.Rename(filepath.Join(holderPath, name), filepath.Join(aside, name)); err != nil && logger != nil {
			logger.Warn("execenv: could not preserve a holder's untracked entry (non-fatal)",
				"entry", name, "error", err)
		}
	}
	if logger != nil {
		logger.Info("execenv: preserved a holder's untracked content aside before its removal",
			"holder", holderPath, "aside", aside, "entries", len(tops))
	}
}

// addLocalWorktree materialises the planned branch as a worktree and reports
// the branch actually used, plus whether this call is the one that put it
// there — the caller may only delete a branch it created itself.
//
// The fallback covers a sibling task already holding the conversation's branch:
// git allows one worktree per branch, and refusing to run is worse than
// delivering onto a task-scoped branch. It forks from the same base, so the
// sibling still stands on the conversation's latest work.
func addLocalWorktree(gitRoot, worktreePath string, plan taskBranchPlan, taskID string) (string, bool, error) {
	var args []string
	switch {
	case plan.continues:
		args = []string{"worktree", "add", worktreePath, plan.name}
	case plan.reset:
		// -B moves the branch to base. Nothing is lost: this path only runs
		// once the branch has been proven an ancestor of HEAD.
		args = []string{"worktree", "add", "-B", plan.name, worktreePath, plan.base}
	default:
		// Ask before creating rather than reading the refusal afterwards. The
		// error text is the fragile half of this decision — it is git's prose,
		// and until gitMessageEnv pinned the locale a translated one silently
		// skipped the fallback entirely (RUYI-116). A ref query answers with an
		// exit code in every language.
		if branchExists(gitRoot, plan.name) {
			plan.name = plan.altName(taskID)
		}
		args = []string{"worktree", "add", "-b", plan.name, worktreePath, plan.base}
	}
	out, err := runGit(gitRoot, args...)
	if err == nil {
		return plan.name, !plan.continues, nil
	}
	if !branchUnavailable(out) {
		return "", false, fmt.Errorf("execenv: git worktree add: %s: %w", strings.TrimSpace(out), err)
	}
	alt := plan.altName(taskID)
	if out, err := runGit(gitRoot, "worktree", "add", "-b", alt, worktreePath, plan.base); err != nil {
		return "", false, fmt.Errorf("execenv: git worktree add: %s: %w", strings.TrimSpace(out), err)
	}
	return alt, true, nil
}

// branchExists reports whether a local branch of this name is already there.
func branchExists(gitRoot, name string) bool {
	tip, err := runGitTrimmed(gitRoot, "rev-parse", "--verify", "--quiet", "refs/heads/"+name)
	return err == nil && tip != ""
}

// branchUnavailable recognises git refusing a branch that another worktree
// holds, or that already exists under a name we meant to create.
func branchUnavailable(out string) bool {
	lower := strings.ToLower(out)
	return strings.Contains(lower, "already exists") ||
		strings.Contains(lower, "already checked out") ||
		strings.Contains(lower, "already used by worktree")
}

// replayResult is what the replay left in the worktree.
type replayResult struct {
	// conflicts names the files git could not merge. Non-empty means the
	// worktree holds an unresolved merge, on purpose.
	conflicts []string
}

// replayUserState brings the user's directory into the worktree.
//
// Both branch kinds cherry-pick the user's edits and differ only in which of
// them are new here. A branch forked from HEAD carries none, so the whole
// snapshot applies and cannot conflict. A continued branch already carries the
// edit set the previous turn replayed, and each snapshot is the user's edits
// against the checkout's HEAD as of its capture, so the new information is the
// delta between that carried edit set and the snapshot's — taken against the
// current checkout with the carried edits applied on top (replayBaseCommit),
// not between the two snapshots' trees: a pull between turns would otherwise
// ride in as if the user had typed the whole mainline advance (RUYI-380).
//
// Diffing against the user's HEAD itself is the tempting version and it is
// wrong: that merge takes the user's HEAD as its base, so it re-proposes
// work the branch already has, and conflicts against the agent's edits to the
// same lines — which is to say, it conflicts exactly when the agent did what it
// was asked to do. Verified: with the user's directory untouched between turns,
// a plain `stash apply` onto the branch tip already fails.
//
// A conflict here is a real disagreement — the user rewrote lines the agent
// also rewrote — and it stays in the worktree for the agent to resolve with
// ordinary git commands, which is both what the agent is for and the only way
// the user's newer edit survives. Dropping it would lose that edit twice over:
// once from this turn's tree, and again from every later turn, because the
// snapshot would advance past a change the branch never took.
func replayUserState(worktreePath string, plan taskBranchPlan, snapshot string, logger *slog.Logger) (replayResult, error) {
	// A continued branch normally replays against the snapshot it already
	// carries (plan.priorState). A healed refusal keeps continues — the turn
	// still checks the branch out in place — but when the record's checkpoint
	// no longer proves the checkout carries that snapshot, healGuardRefusal
	// leaves priorState empty. For that case carried stays the healed tip and
	// only feeds the nothing-to-do check below; the merge base itself is
	// picked in the switch underneath, and picking the healed tip there would
	// propose deleting the branch's task work.
	carried := plan.base
	if plan.continues && plan.priorState != "" {
		carried = plan.priorState
	}
	if carried == "" {
		return replayResult{}, fmt.Errorf("execenv: no baseline to replay the local directory against for branch %s", plan.name)
	}
	// Nothing new since the state this checkout already carries. On a follow-up
	// turn that is the ordinary case: the user commented, they did not edit.
	// Equal trees also mean equal edit sets — the checkout's own files are what
	// carries a snapshot's tree beyond its edit set, so a HEAD that moved
	// without the tree moving has nothing to replay either.
	if _, err := runGit(worktreePath, "diff", "--quiet", carried, snapshot); err == nil {
		return replayResult{}, nil
	}

	// A commit whose parent is what the checkout already carries of the user's
	// work and whose tree is the user's current one. Its parent is what git
	// uses as the merge base, and that is the entire point: it is not reachable
	// any other way.
	base := carried
	switch {
	case !plan.continues:
		// A fresh fork. With delivery-guard branch-base anchoring, a new branch
		// may be checked out at the origin's tip rather than the checkout the
		// snapshot was captured against, so carried is no longer guaranteed to
		// be the snapshot's parent. Diffing against carried would then propose
		// the whole tree distance between the two checkouts — mainline
		// movement dressed up as pending reversals, plus local un-pushed work
		// no task branch should carry (RUYI-380). The snapshot's own parent is
		// the honest base in both worlds: the increment is exactly the user's
		// edit set.
		if _, err := runGit(worktreePath, "diff", "--quiet", snapshot+"^", snapshot); err == nil {
			// The user has no edits of their own; the carried-vs-snapshot
			// difference above is all mainline drift. Nothing to replay.
			return replayResult{}, nil
		}
		base = snapshot + "^"
	case plan.priorState == "":
		// A healed refusal (healGuardRefusal leaves priorState empty). The
		// healed tip carries the branch's task work that the user's directory
		// does not, so the tip itself must not be the merge base — a diff
		// against it would propose deleting that work. The snapshot's own
		// parent is the honest base: only the user's edits since their HEAD
		// are proposed, onto whatever the branch now holds. The user's
		// dropped WIP rides in with the snapshot, which is the point.
		if _, err := runGit(worktreePath, "diff", "--quiet", snapshot+"^", snapshot); err == nil {
			// The user has no edits of their own; the tip-vs-snapshot
			// difference above is all task work. Nothing to replay.
			return replayResult{}, nil
		}
		base = snapshot + "^"
	default:
		commit, err := replayBaseCommit(worktreePath, carried, snapshot)
		if err != nil {
			return replayResult{}, err
		}
		if commit == "" {
			// The edit set is unchanged and the trees differ only by the
			// checkout advancing under the user. Replay nothing and invent no
			// baseline: a mainline pull is not the user's work (RUYI-380).
			return replayResult{}, nil
		}
		base = commit
	}
	args := append(commitIdentityArgs(worktreePath), "commit-tree", snapshot+"^{tree}", "-p", base,
		"-m", "multica: local directory edits to replay")
	increment, err := runGitTrimmed(worktreePath, args...)
	if err != nil || increment == "" {
		return replayResult{}, fmt.Errorf("execenv: could not describe your local edits for replay into the task worktree: %w", err)
	}

	out, pickErr := runGit(worktreePath, "cherry-pick", "--no-commit", increment)
	if pickErr == nil {
		return replayResult{}, nil
	}
	conflicts, listErr := unmergedPaths(worktreePath)
	if listErr != nil || len(conflicts) == 0 {
		// Not a conflict, so the replay failed for a reason the agent cannot
		// resolve. Fail closed rather than start on a half-applied tree.
		abortCherryPick(worktreePath, logger)
		return replayResult{}, fmt.Errorf("execenv: could not replay your local edits into the task worktree "+
			"(the agent would have seen a different tree than you have): %s: %w", strings.TrimSpace(out), pickErr)
	}
	if !plan.continues {
		// Reachable since branch-base anchoring: a user edit colliding with
		// mainline movement between the checkout the snapshot was captured
		// against and the branch's anchor. A conflicted fresh checkout is not
		// a tree the user would recognise, so fail closed rather than hand the
		// agent a merge it never agreed to.
		abortCherryPick(worktreePath, logger)
		return replayResult{}, fmt.Errorf("execenv: could not replay your local edits onto a fresh task worktree: %s: %w",
			strings.TrimSpace(out), pickErr)
	}

	// Keep the conflict, drop only the sequencer state: the agent should see an
	// ordinary conflicted worktree it can resolve with `git status` / `git add`,
	// not a cherry-pick it is expected to conclude with a command it never
	// started.
	if out, quitErr := runGit(worktreePath, "cherry-pick", "--quit"); quitErr != nil && logger != nil {
		logger.Warn("execenv: could not clear the cherry-pick state after a conflicting replay (non-fatal)",
			"path", worktreePath, "output", strings.TrimSpace(out), "error", quitErr)
	}
	if logger != nil {
		logger.Warn("execenv: your local edits since the previous turn conflict with the work on this branch; handing the conflict to the agent",
			"path", worktreePath, "branch", plan.name, "files", conflicts)
	}
	return replayResult{conflicts: conflicts}, nil
}

// replayBaseCommit builds the parent of a continued turn's replay commit: the
// checkout's current tree with the edit set the branch already carries applied
// on top. Each snapshot is rooted at the checkout's HEAD as of its capture, so
// diffing the fresh snapshot against this composite leaves exactly the user's
// new edits — a pull moved the snapshot's parent without touching either edit
// set, so the advance itself is never replay content (RUYI-380).
//
// An empty string means the two edit sets are identical and there is nothing
// to replay. An error means the carried edits and the checkout's advance
// cannot be reconciled at all — unreachable for a pull, which refuses to
// touch files the user has dirty — and fails the turn closed rather than
// starting on a tree the user would not recognise.
func replayBaseCommit(worktreePath, priorState, snapshot string) (string, error) {
	priorBase, err := carriedSnapshotHead(worktreePath, priorState)
	if err != nil {
		return "", err
	}
	snapshotBase, err := runGitTrimmed(worktreePath, "rev-parse", "--verify", snapshot+"^")
	if err != nil {
		return "", fmt.Errorf("execenv: could not resolve the checkout state behind the current snapshot (%s): %w", snapshot, err)
	}
	merged, err := runGitTrimmed(worktreePath, "merge-tree", "--write-tree",
		"--merge-base="+priorBase, snapshotBase, priorState)
	if err != nil {
		return "", fmt.Errorf("execenv: cannot line the branch's carried edits up against the checkout's current state: %w", err)
	}
	tree, err := runGitTrimmed(worktreePath, "rev-parse", "--verify", snapshot+"^{tree}")
	if err != nil {
		return "", fmt.Errorf("execenv: could not resolve the current snapshot's tree (%s): %w", snapshot, err)
	}
	if merged == tree {
		return "", nil
	}
	commit, err := runGitTrimmed(worktreePath, append(commitIdentityArgs(worktreePath), "commit-tree", merged,
		"-m", "multica: replay base — the checkout now, with the edits this branch already carries")...)
	if err != nil || commit == "" {
		return "", fmt.Errorf("execenv: could not build the replay base for the task worktree: %w", err)
	}
	return commit, nil
}

// carriedSnapshotHead resolves the checkout HEAD the branch's carried edit set
// is defined against. What a branch records is a wrapper commit — its tree is
// the user snapshot's tree, and it is parented on that snapshot and on the
// branch tip; a re-record without a delivered commit wraps the previous
// wrapper. The first-parent line therefore walks down wrappers to the first
// single-parent commit — the snapshot itself, whose parent is the checkout's
// HEAD as of its capture (RUYI-380).
func carriedSnapshotHead(worktreePath, priorState string) (string, error) {
	cur := priorState
	for hop := 0; hop < 8; hop++ {
		out, err := runGitTrimmed(worktreePath, "rev-list", "--parents", "-n", "1", cur)
		if err != nil {
			return "", fmt.Errorf("execenv: could not walk the branch's recorded state (%s) to the user snapshot: %w", priorState, err)
		}
		fields := strings.Fields(out)
		if len(fields) < 2 {
			return "", fmt.Errorf("execenv: the branch's recorded state (%s) has no parent to define its edits against", priorState)
		}
		if len(fields) == 2 {
			return fields[1], nil
		}
		cur = fields[1]
	}
	return "", fmt.Errorf("execenv: the branch's recorded state (%s) wraps too many re-records to walk to the user snapshot", priorState)
}

// quotedPaths renders repository paths for a human-facing message. Quoted
// because a git path may contain newlines, quotes or control characters, and
// this string is read in logs and task errors where a raw one would look like
// several entries.
func quotedPaths(paths []string) string {
	quoted := make([]string, 0, len(paths))
	for _, path := range paths {
		quoted = append(quoted, strconv.Quote(path))
	}
	return strings.Join(quoted, ", ")
}

// verifyDeliveryPoint checks that tip is a commit this conversation can put its
// name on before it becomes the branch's recorded checkpoint.
//
// Two things are asserted, and they are the two ways a delivery can be
// something other than what this task built. The tip has to BE the task's
// branch — a run that checked out something else, or a branch someone moved
// underneath it, delivers a commit this record has no business describing. And
// it has to still contain the commit this turn started from — this turn's own
// baseline when it made one, otherwise the commit the branch was created on.
// A run that resets its worktree back to the user's own HEAD passes neither test but the
// second is the one that matters, twice over: recording a plain user commit as
// the checkpoint is what makes a branch they later recreate there look like
// ours, and a tip without this turn's starting point no longer carries the
// snapshot about to be recorded as delivered (MUL-6881 review).
// DeliveryGuardError marks a Finalize refusal from verifyDeliveryPoint: the
// run delivered onto a branch that no longer proves where the turn started,
// so the daemon declined to stamp its record over it. The wrapper exists to
// be read structurally — taskRunFailureReason maps it to the dedicated,
// retryable delivery_guard reason instead of letting the prose fall into
// taskfailure.Classify's agent_error.unknown, which is off the retry
// allowlist and would leave every refused task to a human (RUYI-479).
//
// Kind and HealAttempted are the structured classification behind the
// failure event's guard_kind / guard_heal_attempted fields (RUYI-579): Kind
// names the refusal shape (the values are taskfailure.GuardKind*), so a user
// sees which recovery applies instead of parsing prose; HealAttempted says
// the finalize self-heal (reAnchorDelivery) was tried and did not clear the
// refusal.
type DeliveryGuardError struct {
	Err           error
	Kind          string
	HealAttempted bool
}

func (e *DeliveryGuardError) Error() string { return e.Err.Error() }
func (e *DeliveryGuardError) Unwrap() error { return e.Err }

// errNotHealable marks a refusal reAnchorDelivery declines to touch: the
// shape is not the ancestor break the self-heal owns. The caller refuses
// exactly as it did before the heal existed.
var errNotHealable = errors.New("execenv: delivery is not self-healable")

// guardRefusalRef is where a branch's refusal marker lives: the tip the guard
// refused to record. Cleared when a later delivery succeeds, dropped with the
// branch, pruned when the branch is gone.
func guardRefusalRef(branch string) string {
	return guardRefusalRefPrefix + branch
}

func (w *LocalWorktree) verifyDeliveryPoint(tip string) error {
	if !w.tracksState {
		// Nothing will be recorded for this branch, so there is nothing to prove.
		return nil
	}
	if tip == "" {
		return errors.New("the task worktree has no resolvable HEAD")
	}
	branchTip, err := runGitTrimmed(w.GitRoot, "rev-parse", "--verify", "refs/heads/"+w.Branch)
	if err != nil {
		return fmt.Errorf("resolve branch %s: %w", w.Branch, err)
	}
	if branchTip != tip {
		return &DeliveryGuardError{
			Err: fmt.Errorf("the worktree delivered %s while branch %s points at %s, so the run did not deliver onto its own branch",
				shortID(tip), w.Branch, shortID(branchTip)),
			Kind: taskfailure.GuardKindBranchMismatch,
		}
	}
	if w.BaseCommit == "" {
		return fmt.Errorf("branch %s has no commit of this task's own to prove it by", w.Branch)
	}
	if _, err := runGit(w.GitRoot, "merge-base", "--is-ancestor", w.BaseCommit, tip); err != nil {
		return &DeliveryGuardError{
			Err: fmt.Errorf("the delivered commit %s no longer contains %s, the commit this turn started from",
				shortID(tip), shortID(w.BaseCommit)),
			Kind: taskfailure.GuardKindAncestorBreak,
		}
	}
	return nil
}

// reAnchorDelivery repairs the one refusal shape the daemon can own outright
// (RUYI-579 W2): the delivered tree is fine, but the tip it sits on lost the
// turn's base commit from its ancestry — the shape a reset/rebase onto the
// mainline mid-turn produces. Replaying the delivery is lossless by
// definition: the same tree re-committed with its original author and message
// directly onto the base. Anything else — a branch the tip does not match
// (off-branch or detached delivery), a missing base, or any git step that
// fails — returns errNotHealable or the step's error, and the caller refuses
// exactly as it did before the heal existed.
//
// The rebuilt commit collapses the delivery to a single commit on top of the
// base; intermediate commits are not preserved. The delivered branch state is
// identical, and the original tip and the re-anchor are named in the message.
func (w *LocalWorktree) reAnchorDelivery(tip string, logger *slog.Logger) (string, error) {
	if !w.tracksState || tip == "" || w.BaseCommit == "" || w.userState == "" {
		return "", fmt.Errorf("%w: the refusal shape is not the ancestor break the self-heal owns", errNotHealable)
	}
	branchTip, err := runGitTrimmed(w.GitRoot, "rev-parse", "--verify", "refs/heads/"+w.Branch)
	if err != nil || branchTip != tip {
		return "", fmt.Errorf("%w: branch %s does not point at the delivered tip", errNotHealable, w.Branch)
	}
	if _, err := runGit(w.GitRoot, "merge-base", "--is-ancestor", w.BaseCommit, tip); err == nil {
		return "", fmt.Errorf("%w: the delivered tip still contains the turn's base", errNotHealable)
	}
	// Content gate (RUYI-579 W2, fail-closed): the snapshot commit's parent is
	// the HEAD this turn started from, so its diff is the user's uncommitted
	// edit set. A delivery that touches one of those paths may have reverted
	// the edits — re-anchoring would launder that into a legal ancestry, and
	// the next turn's replay, trusting the recorded snapshot, would offer
	// nothing and silently drop them (MUL-6881). The heal owns only
	// deliveries that leave the user's edit paths alone; those shapes stay
	// refused for the retry to replay the edits from the preserved worktree.
	edits, err := runGitStdout(w.GitRoot, "diff", "--name-only", w.userState+"^", w.userState)
	if err != nil {
		return "", fmt.Errorf("execenv: list the user's uncommitted edits of %s: %w", shortID(w.userState), err)
	}
	if strings.TrimSpace(edits) != "" {
		touched, err := runGitStdout(w.GitRoot, "diff", "--name-only", w.userState, tip)
		if err != nil {
			return "", fmt.Errorf("execenv: diff the delivery against the user's directory: %w", err)
		}
		editPaths := strings.Split(strings.TrimSpace(edits), "\n")
		for _, path := range strings.Split(strings.TrimSpace(touched), "\n") {
			if path == "" {
				continue
			}
			if slices.Contains(editPaths, path) {
				return "", fmt.Errorf("%w: the delivered tip touches %s, a path the user's uncommitted edits live on", errNotHealable, path)
			}
		}
	}
	tree, err := runGitTrimmed(w.GitRoot, "rev-parse", "--verify", tip+"^{tree}")
	if err != nil {
		return "", fmt.Errorf("execenv: resolve the delivered tree of %s: %w", shortID(tip), err)
	}
	// Carry the original authorship over: the re-anchored commit is the same
	// delivery, only its parent changed. %x1f (unit separator) joins the three
	// fields; author names may contain any other character.
	ident, err := runGitTrimmed(w.GitRoot, "show", "-s", "--format=%an%x1f%ae%x1f%aI", tip)
	if err != nil {
		return "", fmt.Errorf("execenv: read the author of %s: %w", shortID(tip), err)
	}
	parts := strings.SplitN(ident, "\x1f", 3)
	if len(parts) != 3 || parts[0] == "" || parts[2] == "" {
		return "", fmt.Errorf("execenv: unexpected author identity for %s: %q", shortID(tip), ident)
	}
	msg, err := runGitTrimmed(w.GitRoot, "log", "-1", "--format=%B", tip)
	if err != nil {
		return "", fmt.Errorf("execenv: read the message of %s: %w", shortID(tip), err)
	}
	// The message rides in a file: -m would treat embedded blank lines as
	// paragraph separators with quoting hazards, and runGit has no stdin.
	note := fmt.Sprintf("\nmultica: re-anchored onto %s after a delivery-guard refusal; the original delivered tip was %s.\n",
		shortID(w.BaseCommit), shortID(tip))
	msgFile, err := os.CreateTemp("", "multica-reanchor-*.msg")
	if err != nil {
		return "", fmt.Errorf("execenv: stage the re-anchor message: %w", err)
	}
	defer os.Remove(msgFile.Name())
	if _, err := msgFile.WriteString(msg + note); err != nil {
		msgFile.Close()
		return "", fmt.Errorf("execenv: write the re-anchor message: %w", err)
	}
	if err := msgFile.Close(); err != nil {
		return "", fmt.Errorf("execenv: write the re-anchor message: %w", err)
	}
	env := []string{
		"GIT_AUTHOR_NAME=" + parts[0],
		"GIT_AUTHOR_EMAIL=" + parts[1],
		"GIT_AUTHOR_DATE=" + parts[2],
	}
	newTip, err := runGitTrimmedEnv(w.GitRoot, env, append(commitIdentityArgs(w.GitRoot), "commit-tree", tree, "-p", w.BaseCommit, "-F", msgFile.Name())...)
	if err != nil {
		return "", fmt.Errorf("execenv: commit the re-anchored delivery: %w", err)
	}
	// Compare-and-swap on the old tip: the branch must not move between the
	// check above and this move. Losing the race just declines the heal.
	if out, err := runGit(w.GitRoot, "update-ref", "refs/heads/"+w.Branch, newTip, tip); err != nil {
		return "", fmt.Errorf("execenv: move branch %s to the re-anchored tip: %s: %w", w.Branch, strings.TrimSpace(out), err)
	}
	if logger != nil {
		logger.Info("delivery_guard_selfheal",
			"git_root", w.GitRoot,
			"branch", w.Branch,
			"original_tip", tip,
			"new_tip", newTip,
			"base", w.BaseCommit,
		)
	}
	return newTip, nil
}

// unmergedPaths lists the files git considers unresolved in a worktree.
func unmergedPaths(worktreePath string) ([]string, error) {
	out, err := runGitStdout(worktreePath, "diff", "--name-only", "--diff-filter=U", "-z")
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, name := range strings.Split(out, "\x00") {
		if name != "" {
			paths = append(paths, name)
		}
	}
	return paths, nil
}

// abortCherryPick returns the worktree to the branch tip. Used only where the
// conflict is not something the agent can act on; the ordinary conflict path
// deliberately leaves the worktree as git left it.
func abortCherryPick(worktreePath string, logger *slog.Logger) {
	for _, args := range [][]string{{"cherry-pick", "--quit"}, {"reset", "--hard", "HEAD"}, {"clean", "-fdq"}} {
		if out, err := runGit(worktreePath, args...); err != nil && logger != nil {
			logger.Warn("execenv: could not restore the task worktree after a failed replay",
				"path", worktreePath, "command", args[0], "output", strings.TrimSpace(out), "error", err)
		}
	}
}

// userStateRef is where a branch's record lives: the snapshot of the user's
// directory it already carries, the conversation it belongs to, and the tip it
// was recorded at.
func userStateRef(branch string) string {
	return localStateRefPrefix + branch
}

// readUserStateRef returns the recorded snapshot, or "" when the branch has
// none.
func readUserStateRef(gitRoot, branch string) (string, error) {
	return runGitTrimmed(gitRoot, "rev-parse", "--verify", "--quiet", userStateRef(branch))
}

// recordState pins the user's directory as this task saw it together with the
// commit the branch stands at. Two things depend on the record: the next turn
// replays from its tree, and every later task proves the branch is still its
// own from its owner and checkpoint.
//
// The checkpoint must be a commit the branch could not plausibly be sitting at
// WITHOUT this conversation's work — that is the whole proof. A tip that is
// still the user's own HEAD is not one: it is exactly where a branch they
// delete and recreate lands, and recording it would authorise a later turn to
// append onto their unrelated work. Prepare therefore records only once the
// branch carries a commit of ours, and Finalize records the tip it actually
// delivered.
func (w *LocalWorktree) recordState(checkpoint string, logger *slog.Logger) error {
	if w == nil || !w.tracksState || w.Branch == "" || w.userState == "" {
		return nil
	}
	if _, err := writeBranchRecord(w.GitRoot, w.Branch, w.userState, checkpoint, w.owner); err != nil {
		return err
	}
	if logger != nil {
		logger.Debug("execenv: recorded the local-directory snapshot for the task branch",
			"branch", w.Branch, "checkpoint", checkpoint)
	}
	return nil
}

// dropBranch deletes a task branch that carries nothing worth keeping, together
// with its recorded snapshot — the two are meaningless apart.
func dropBranch(gitRoot, branch string, logger *slog.Logger) {
	if branch == "" {
		return
	}
	deleteBranch(gitRoot, branch, logger)
	if out, err := runGit(gitRoot, "update-ref", "-d", userStateRef(branch)); err != nil && logger != nil {
		logger.Debug("execenv: no local-directory snapshot to drop for task branch",
			"branch", branch, "output", strings.TrimSpace(out))
	}
	// Same for a refusal marker: without the branch there is nothing for a
	// retry's prepare to heal, and a leftover ref would pin the refused tip
	// against `git gc` forever.
	if out, err := runGit(gitRoot, "update-ref", "-d", guardRefusalRef(branch)); err != nil && logger != nil {
		logger.Debug("execenv: no delivery-guard refusal marker to drop for task branch",
			"branch", branch, "output", strings.TrimSpace(out))
	}
}

// pruneOrphanedStateRefs drops the snapshot of any branch that is no longer
// there. Multica deletes both together, but the branch is the user's to delete,
// rename or merge away at any time, and a ref left behind would pin their whole
// working tree as of some past turn against `git gc` forever.
//
// Best-effort and non-fatal: this is housekeeping in the user's repository, not
// a precondition for the task.
func pruneOrphanedStateRefs(gitRoot string, logger *slog.Logger) {
	pruneOrphanedRefs(gitRoot, localStateRefPrefix, "local-directory snapshot", logger)
	pruneOrphanedRefs(gitRoot, guardRefusalRefPrefix, "delivery-guard refusal marker", logger)
}

func pruneOrphanedRefs(gitRoot, refPrefix, what string, logger *slog.Logger) {
	out, err := runGitTrimmed(gitRoot, "for-each-ref", "--format=%(refname)", refPrefix)
	if err != nil {
		if logger != nil {
			logger.Debug("execenv: could not list per-branch refs", "git_root", gitRoot, "error", err)
		}
		return
	}
	for _, ref := range strings.Split(out, "\n") {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			continue
		}
		branch := strings.TrimPrefix(ref, refPrefix)
		if branch == ref {
			continue
		}
		if _, headErr := runGit(gitRoot, "show-ref", "--verify", "--quiet", "refs/heads/"+branch); headErr == nil {
			continue
		}
		if out, delErr := runGit(gitRoot, "update-ref", "-d", ref); delErr != nil {
			if logger != nil {
				logger.Warn("execenv: could not drop a deleted task branch's ref (non-fatal)",
					"ref", ref, "output", strings.TrimSpace(out), "error", delErr)
			}
			continue
		}
		if logger != nil {
			logger.Info("execenv: dropped the ref of a branch that no longer exists",
				"git_root", gitRoot, "branch", branch, "what", what)
		}
	}
}

// checkUntrackedReplayable refuses a directory whose untracked content is too
// large to reproduce faithfully, before anything is written anywhere.
//
// The bounds are the same ones the older file-copy replay enforced, and they
// exist for the same reason: `--exclude-standard` already drops everything
// gitignored, so a repo past them is one whose build output was never ignored.
// Snapshotting it would write every byte into the user's own object database.
// The untracked symlink case is refused for a narrower reason — it is content
// the user can see, and this replay does not decide whether to reproduce the
// link or its target, including targets outside the repo.
func checkUntrackedReplayable(gitRoot string, logger *slog.Logger) error {
	out, err := runGitStdout(gitRoot, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return fmt.Errorf("execenv: could not list the untracked files in %q: %w", gitRoot, err)
	}
	var (
		files   int
		budget  int64 = maxUntrackedBytes
		skipped int
	)
	for _, rel := range strings.Split(out, "\x00") {
		// Sidecars and runtime state are pruned from the snapshot itself, so
		// they never reach a worktree; counting them here would refuse a whole
		// directory because its runtime wrote a large session tree, for content
		// that will never be replayed anyway.
		if rel == "" || isMulticaSidecarPath(rel) || isRuntimeStatePath(rel) {
			continue
		}
		info, statErr := os.Lstat(filepath.Join(gitRoot, rel))
		if statErr != nil {
			// Listed a moment ago, unreadable now: the tree changed under us.
			// git will simply not find it either, so this is not a refusal.
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 {
			skipped++
			if logger != nil {
				logger.Warn("execenv: untracked symlink cannot be replayed into a worktree", "file", rel)
			}
			continue
		}
		if !info.Mode().IsRegular() {
			// Sockets, FIFOs, devices: not content, and git will not add them.
			continue
		}
		files++
		budget -= info.Size()
		if files > maxUntrackedFiles || budget < 0 {
			skipped++
		}
	}
	if skipped == 0 {
		return nil
	}
	return fmt.Errorf("execenv: cannot replay every untracked file from %q into a task worktree "+
		"(%d left over; the replay covers regular files up to %d files / %d MiB and does not follow symlinks) "+
		"— gitignore or clean up the untracked files, or switch the resource back to in_place",
		gitRoot, skipped, maxUntrackedFiles, maxUntrackedBytes>>20)
}

// multicaSidecarDirNames are the directories Prepare writes into a workdir. A
// task running in_place on the same directory leaves these present as
// untracked files for the length of its run, so a concurrent worktree snapshot
// sees them. CLAUDE.md / AGENTS.md are deliberately absent: those are
// ordinarily the user's own tracked files, and the runtime only injects a
// marker block into them, which CleanupRuntimeConfig removes.
var multicaSidecarDirNames = []string{
	".agent_context",
	".multica",
}

// isMulticaSidecarPath reports whether a repo-relative path is one of the
// daemon's own sidecars rather than the user's content. Matched as a whole
// path segment at ANY depth, not just the repo root: an in_place resource may
// point at a subdirectory of this repo, in which case its sidecars sit at
// <subdir>/.agent_context — replaying those would put another issue's brief
// inside this task's worktree and commit it to the delivered branch.
func isMulticaSidecarPath(rel string) bool {
	for _, seg := range strings.Split(filepath.ToSlash(rel), "/") {
		for _, name := range multicaSidecarDirNames {
			if seg == name {
				return true
			}
		}
	}
	return false
}

// runtimeStateDirNames are the state directories agent CLIs and editors write
// wherever they happen to run — in the task worktree while the task runs, and
// in the user's own checkout between tasks. Session logs, cooldown files,
// discovery state: they churn and vanish for the runtime's own bookkeeping
// with no daemon in the loop, which is exactly the shape that made a
// finalizing `git add -A` die on "unable to stat" (exit 128) and strand
// finished tasks with their work uncommitted. None of it belongs on a
// delivered branch or in a replayed snapshot, so every staging path prunes
// them. Unlike multicaSidecarDirNames nothing here is written or cleaned by
// the daemon itself, so a cleanup pass cannot cover them.
var runtimeStateDirNames = []string{
	".agent",
	".claude",
	".codex",
	".kimi",
	".omc",
	".omx",
	".vscode",
	".zcode",
}

// isRuntimeStatePath reports whether a repo-relative path lives under one of
// the runtime state directories. Matched as a whole path segment at ANY depth,
// like isMulticaSidecarPath: a resource may point at a subdirectory, and the
// runtimes write their state below wherever they are rooted.
func isRuntimeStatePath(rel string) bool {
	for _, seg := range strings.Split(filepath.ToSlash(rel), "/") {
		for _, name := range runtimeStateDirNames {
			if seg == name {
				return true
			}
		}
	}
	return false
}

// replayableCacheDirNames are the dependency and cache directories no normal
// repository tracks — package installs, virtualenvs, bytecode and tool
// caches. They are pruned from every staging add like the lists above, and
// skipped by the staging budget meter, because a research run can fill them
// with gigabytes of perfectly regenerable content (model-weight caches alone
// killed a finalize add with 8+ GB, RUYI-337). Deliberately conservative:
// build output directories (dist/, build/, target/) are NOT listed — some
// repos do commit them — so those are the staging budget's job instead.
var replayableCacheDirNames = []string{
	".cache",
	".mypy_cache",
	".pytest_cache",
	".venv",
	"__pycache__",
	"node_modules",
	"venv",
}

// isReplayableCachePath reports whether a repo-relative path lives under a
// dependency/cache directory, matched as a whole path segment at ANY depth
// like isRuntimeStatePath.
func isReplayableCachePath(rel string) bool {
	for _, seg := range strings.Split(filepath.ToSlash(rel), "/") {
		for _, name := range replayableCacheDirNames {
			if seg == name {
				return true
			}
		}
	}
	return false
}

// runGit runs git in dir and returns combined output. Callers inspect the
// output for git's own error text, so stdout and stderr stay merged.
func runGit(dir string, args ...string) (string, error) {
	return runGitEnv(dir, nil, args...)
}

// runGitEnv is runGit with extra environment entries, for the one caller that
// has to redirect GIT_INDEX_FILE.
func runGitEnv(dir string, extraEnv []string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()

	full := append([]string{"-C", dir}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Env = append(gitMessageEnv(), extraEnv...)
	cmd.WaitDelay = 5 * time.Second
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// gitMessageEnv pins git's own messages to C so the callers that read them
// keep working on a localised machine.
//
// This file decides control flow from git's prose — "already exists" picks the
// fallback branch, "nothing to commit" distinguishes an empty commit from a
// failed one. On a zh_CN daemon git answered 「致命错误：一个名为 … 的分支已经
// 存在」, no branch of those matched, and a task whose branch already existed
// failed to prepare at all instead of falling back (RUYI-116). LANGUAGE has to
// go too: gettext lets it override LC_ALL for message catalogues.
func gitMessageEnv() []string {
	env := os.Environ()
	out := env[:0:0]
	for _, kv := range env {
		switch {
		case strings.HasPrefix(kv, "LC_ALL="),
			strings.HasPrefix(kv, "LC_MESSAGES="),
			strings.HasPrefix(kv, "LANG="),
			strings.HasPrefix(kv, "LANGUAGE="):
			continue
		}
		out = append(out, kv)
	}
	return append(out, "LC_ALL=C", "LANGUAGE=")
}

// runGitTrimmed runs git for its stdout value, discarding stderr so a
// diagnostic line can't be mistaken for the value (`rev-parse` output, a
// config value, a stash sha).
func runGitTrimmed(dir string, args ...string) (string, error) {
	out, err := runGitStdout(dir, args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// runGitTrimmedEnv is runGitTrimmed with extra environment entries.
func runGitTrimmedEnv(dir string, extraEnv []string, args ...string) (string, error) {
	out, err := runGitEnv(dir, extraEnv, args...)
	if err != nil {
		return "", fmt.Errorf("%s: %w", strings.TrimSpace(out), err)
	}
	return strings.TrimSpace(out), nil
}

// runGitStdout is runGitTrimmed without the trimming, for output where
// whitespace is significant — NUL-separated file listings, where a leading or
// trailing space is part of a filename.
func runGitStdout(dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()

	full := append([]string{"-C", dir}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Env = gitMessageEnv()
	cmd.WaitDelay = 5 * time.Second
	out, err := cmd.Output()
	if err != nil {
		return "", withGitStderr(err)
	}
	return string(out), nil
}

// withGitStderr renders git's stderr into the error.
//
// cmd.Output() already captures stderr into ExitError.Stderr, but nothing ever
// read it back out, so every failure from this path reached the user as a bare
// "exit status 1" — git's own explanation was collected and then thrown away
// at the point it was needed. Discarding stderr from the RESULT is deliberate
// (see runGitTrimmed: a diagnostic line must never be mistaken for a value);
// discarding it from the error was not.
func withGitStderr(err error) error {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if msg := strings.TrimSpace(string(exitErr.Stderr)); msg != "" {
			return fmt.Errorf("%s: %w", msg, err)
		}
	}
	return err
}
