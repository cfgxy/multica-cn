// reconcile_zombie_runs converges RUYI-424 zombie runs: agent work that
// finished (final output persisted, worker process exited with proof) while
// the server-side run row stayed "running" forever, because the supervised
// stdin-EOF bug blocked the daemon's terminal report.
//
// # Judgment (all three must hold, per run)
//
//  1. Server side: agent_task_queue row is 'running' AND started (started_at
//     not null) — never a queued or never-started row.
//  2. Final persisted: the task has task_message rows of type 'text' (the
//     agent's streamed final output) and NO message of any type newer than
//     --min-idle. A run that is still streaming output is by definition not
//     a zombie and is skipped.
//  3. Worker exit evidence: the supervisor manifest on the daemon host shows
//     state "exited" with an exit record, for EVERY attempt generation of the
//     run (base id and any "<id>-<n>" retry). A manifest without an exit
//     record means the worker may still be alive — the run is skipped, never
//     touched. By default the newest generation must also have exited CLEANLY
//     (code 0, no signal); pass --include-dirty-exit to relax, e.g. for runs
//     whose worker was killed after the business output was already complete.
//
// # Execution
//
// Dry-run by default: prints one verdict block per candidate with the full
// evidence. --apply flips qualified runs one transaction per run:
//
//	UPDATE agent_task_queue SET status='completed', completed_at=<manifest
//	exit time>, result=<result JSON with evidence>, prepare_lease_expires_at=NULL
//	WHERE id=$1 AND status='running'
//
// The status='running' predicate inside the UPDATE is the concurrency guard:
// a platform-side terminal write (cancel, complete) landing between judgment
// and apply wins silently and the run is reported as skipped. The evidence
// block embedded in result keeps the action auditable from the row itself;
// no separate side-table is introduced.
//
// Scope notes: this is a display-level convergence for the run row. It does
// not replay the service layer's completion side effects (chat session resume
// pointer, delegated-failure settlement, WS broadcast) — zombie runs are
// issue tasks whose business output already landed through the normal comment
// path. Exit code 0 means "the walk completed"; findings are in the output,
// not the exit code.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/multica-ai/multica/server/internal/daemon/supervisor"
)

const manifestFileName = "manifest.json"

type runFacts struct {
	TaskID    string
	StartedAt *time.Time

	TextCount int
	MaxSeq    int
	LastMsgAt *time.Time
	LastText  *string // newest text-type message content, reused as the result comment

	AttemptDirs []string // manifest-bearing attempt dirs, generation order
	ExitState   string   // newest generation's manifest state
	Exit        *supervisor.ExitRecord
	ExitProblem string // non-empty → exit evidence unusable
}

type verdict struct {
	RunID  string
	Flip   bool
	Reason string
	Facts  *runFacts
}

// judge applies the three-way RUYI-424 judgment to one run's facts. Pure so
// the decision table is unit-testable without a database or a manifest tree.
func judge(f *runFacts, minIdle time.Duration, includeDirtyExit bool, now time.Time) verdict {
	v := verdict{RunID: f.TaskID, Facts: f}
	switch {
	case f.StartedAt == nil:
		v.Reason = "skip: task never started"
	case f.TextCount == 0:
		v.Reason = "skip: no text message persisted (final output never landed)"
	case f.LastMsgAt != nil && now.Sub(*f.LastMsgAt) < minIdle:
		v.Reason = fmt.Sprintf("skip: output streamed %s ago, younger than --min-idle %s (not idle)",
			now.Sub(*f.LastMsgAt).Round(time.Second), minIdle)
	case len(f.AttemptDirs) == 0:
		v.Reason = "skip: no supervisor manifest found under --runs-dir (no worker-side evidence)"
	case f.ExitProblem != "":
		v.Reason = "skip: " + f.ExitProblem
	case f.Exit == nil:
		v.Reason = fmt.Sprintf("skip: manifest state=%s without exit record — worker may still be alive", f.ExitState)
	case !includeDirtyExit && (f.Exit.Code != 0 || f.Exit.Signal != ""):
		v.Reason = fmt.Sprintf("skip: worker exit not clean (code=%d signal=%q source=%s); pass --include-dirty-exit to override",
			f.Exit.Code, f.Exit.Signal, f.Exit.Source)
	default:
		v.Flip = true
		v.Reason = fmt.Sprintf("flip: final persisted (last seq=%d at %s), worker exited cleanly (code=%d source=%s at %s), server running since %s",
			f.MaxSeq, timePtrRFC3339(f.LastMsgAt), f.Exit.Code, f.Exit.Source, f.Exit.At.Format(time.RFC3339), f.StartedAt.Format(time.RFC3339))
	}
	return v
}

// gatherManifestEvidence loads every attempt generation's manifest for a task
// and reduces them to the newest generation's exit record. Every generation
// must be provably exited; anything else (unreadable manifest, a generation
// still running) poisons the evidence — the run is never touched on a maybe.
func gatherManifestEvidence(mgr *supervisor.Manager, taskID string) (*runFacts, error) {
	f := &runFacts{TaskID: taskID}
	entries, err := os.ReadDir(mgr.Root())
	if err != nil {
		return f, fmt.Errorf("runs dir unreadable: %w", err)
	}
	type gen struct {
		n    int
		name string
	}
	var gens []gen
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		switch {
		case name == taskID:
			gens = append(gens, gen{n: 0, name: name})
		case strings.HasPrefix(name, taskID+"-"):
			if n, err := strconv.Atoi(strings.TrimPrefix(name, taskID+"-")); err == nil {
				gens = append(gens, gen{n: n, name: name})
			}
		}
	}
	sort.Slice(gens, func(i, j int) bool { return gens[i].n < gens[j].n })

	for _, g := range gens {
		if _, err := os.Stat(filepath.Join(mgr.Dir(g.name), manifestFileName)); err != nil {
			continue // dir without a manifest is not an attempt of record
		}
		man, err := mgr.ReadManifest(g.name)
		if err != nil {
			f.AttemptDirs = nil
			f.ExitProblem = fmt.Sprintf("manifest %s unreadable: %v", g.name, err)
			return f, nil
		}
		f.AttemptDirs = append(f.AttemptDirs, mgr.Dir(g.name))
		if man.Exit == nil {
			// A generation without exit evidence may still be alive; the
			// newest state is reported, the run is disqualified.
			f.Exit = nil
			f.ExitState = man.State
			return f, nil
		}
		f.ExitState = man.State
		f.Exit = man.Exit
	}
	return f, nil
}

// flagArray is flag.Value for repeatable string flags.
type flagArray []string

func (a *flagArray) String() string { return strings.Join(*a, ",") }
func (a *flagArray) Set(v string) error {
	*a = append(*a, v)
	return nil
}

func main() {
	var (
		runsDir          = flag.String("runs-dir", "", "supervisor runs dir on this daemon host (manifest evidence source; required)")
		minIdle          = flag.Duration("min-idle", 30*time.Minute, "newest task_message must be older than this for a run to qualify")
		apply            = flag.Bool("apply", false, "flip qualifying runs to completed (default: dry-run report only)")
		includeDirtyExit = flag.Bool("include-dirty-exit", false, "also flip runs whose worker exited non-zero/signalled (default requires code 0)")
		limitRunIDs      flagArray
	)
	flag.Var(&limitRunIDs, "run-id", "restrict to this agent_task_queue id (repeatable)")
	flag.Parse()

	if *runsDir == "" {
		fmt.Fprintln(os.Stderr, "--runs-dir is required")
		flag.Usage()
		os.Exit(2)
	}
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		fmt.Fprintln(os.Stderr, "DATABASE_URL is required")
		os.Exit(2)
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		fatal("connect database: %v", err)
	}
	defer pool.Close()

	mgr, err := supervisor.NewManager(*runsDir)
	if err != nil {
		fatal("open runs dir: %v", err)
	}

	// Candidate runs: still running AND actually started. Message and
	// manifest evidence decides the rest.
	rows, err := pool.Query(ctx, `
		SELECT id::text, started_at
		FROM agent_task_queue
		WHERE status = 'running' AND started_at IS NOT NULL
		ORDER BY started_at`)
	if err != nil {
		fatal("list running tasks: %v", err)
	}
	type cand struct {
		id      string
		started time.Time
	}
	var candidates []cand
	for rows.Next() {
		var c cand
		if err := rows.Scan(&c.id, &c.started); err != nil {
			fatal("scan running task: %v", err)
		}
		candidates = append(candidates, c)
	}
	rows.Close()
	if len(limitRunIDs) > 0 {
		want := map[string]bool{}
		for _, id := range limitRunIDs {
			want[id] = true
		}
		var filtered []cand
		for _, c := range candidates {
			if want[c.id] {
				filtered = append(filtered, c)
			}
		}
		candidates = filtered
	}

	now := time.Now()
	flipped, skipped := 0, 0
	for _, c := range candidates {
		f := &runFacts{TaskID: c.id, StartedAt: &c.started}

		var lastText *string
		err := pool.QueryRow(ctx, `
			SELECT COUNT(*) FILTER (WHERE type = 'text'),
			       COALESCE(MAX(seq), 0),
			       MAX(created_at),
			       (SELECT content FROM task_message WHERE task_id = $1 AND type = 'text' ORDER BY seq DESC LIMIT 1)
			FROM task_message WHERE task_id = $1`, c.id).
			Scan(&f.TextCount, &f.MaxSeq, &f.LastMsgAt, &lastText)
		if err != nil {
			reportVerdict(verdict{RunID: c.id, Reason: "skip: task_message query failed: " + err.Error()}, nil)
			skipped++
			continue
		}
		f.LastText = lastText

		mf, err := gatherManifestEvidence(mgr, c.id)
		if err != nil {
			reportVerdict(verdict{RunID: c.id, Reason: "skip: " + err.Error()}, nil)
			skipped++
			continue
		}
		f.AttemptDirs, f.ExitState, f.Exit, f.ExitProblem = mf.AttemptDirs, mf.ExitState, mf.Exit, mf.ExitProblem

		v := judge(f, *minIdle, *includeDirtyExit, now)
		reportVerdict(v, f)
		if !v.Flip {
			skipped++
			continue
		}
		if !*apply {
			skipped++
			continue
		}
		if err := flipRun(ctx, pool, f, now); err != nil {
			fatal("flip run %s: %v", c.id, err)
		}
		flipped++
	}

	mode := "dry-run"
	if *apply {
		mode = "APPLY"
	}
	fmt.Printf("done: mode=%s candidates=%d flipped=%d skipped=%d\n", mode, len(candidates), flipped, skipped)
}

// flipRun converges one zombie run in a single guarded transaction. The
// status='running' predicate inside the UPDATE is the concurrency guard: a
// platform-side terminal write landing between judgment and apply makes this
// a reported no-op (0 rows), never a double-terminal.
func flipRun(ctx context.Context, pool *pgxpool.Pool, f *runFacts, now time.Time) error {
	result := map[string]any{
		"status":        "completed",
		"comment":       textOrEmpty(f.LastText),
		"reconciled_by": "reconcile_zombie_runs",
		"reconciled_at": now.Format(time.RFC3339),
		"zombie_evidence": map[string]any{
			"last_message_seq": f.MaxSeq,
			"last_message_at":  timePtrRFC3339(f.LastMsgAt),
			"manifest_state":   f.ExitState,
			"worker_exit": map[string]any{
				"code":   f.Exit.Code,
				"signal": f.Exit.Signal,
				"at":     f.Exit.At.Format(time.RFC3339),
				"source": f.Exit.Source,
			},
		},
	}
	blob, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("marshal result: %w", err)
	}
	completedAt := f.Exit.At
	tag, err := pool.Exec(ctx, `
		UPDATE agent_task_queue
		SET status = 'completed', completed_at = $2, result = $3, prepare_lease_expires_at = NULL
		WHERE id = $1 AND status = 'running'`, f.TaskID, completedAt, blob)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		fmt.Println("  -> skipped: run left 'running' before apply (concurrent terminal write won)")
		return nil
	}
	fmt.Printf("  -> flipped to completed (completed_at=%s)\n", completedAt.Format(time.RFC3339))
	return nil
}

func reportVerdict(v verdict, f *runFacts) {
	marker := "SKIP"
	if v.Flip {
		marker = "FLIP"
	}
	fmt.Printf("[%s] run %s\n  %s\n", marker, v.RunID, v.Reason)
	if f != nil {
		fmt.Printf("  evidence: text_messages=%d max_seq=%d last_msg_at=%s attempts=%d manifest_state=%s\n",
			f.TextCount, f.MaxSeq, timePtrRFC3339(f.LastMsgAt), len(f.AttemptDirs), f.ExitState)
	}
}

func textOrEmpty(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func timePtrRFC3339(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.Format(time.RFC3339)
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "reconcile_zombie_runs: "+format+"\n", args...)
	os.Exit(1)
}
