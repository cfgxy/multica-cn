package daemon

// Daemon-side knowledge cycle against a fake bd binary and a fake server
// (RUYI-289): discovery reporting, first/changed/unchanged scan decisions,
// ultimate auto-designation on the daemon-managed stable path, and adoption
// transfers with read-back proof.

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// --- fake bd (helper process) ----------------------------------------------

// TestFakeBd is never a real test in this run: when the binary is re-executed
// with a "-- fakebd" marker argv (see fakeKnowledgeBdSetup's wrapper script)
// it plays the bd CLI for one invocation and exits.
func TestFakeBd(t *testing.T) {
	for i, arg := range os.Args {
		if arg == "--" && i+2 < len(os.Args) && os.Args[i+1] == "fakebd" {
			runFakeKnowledgeBd(os.Args[i+2:])
		}
	}
}

func fakeBdFlag(args []string, name string) string {
	for i := 1; i < len(args); i++ {
		if args[i] == name && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func fakeBdRead(path string) map[string]string {
	state := map[string]string{}
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &state)
	}
	if state == nil {
		state = map[string]string{}
	}
	return state
}

func fakeBdLog(dir string, args []string) {
	if log, err := os.OpenFile(filepath.Join(dir, "invocations.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
		fmt.Fprintf(log, "%s\n", strings.Join(args, "\x1f"))
		log.Close()
	}
}

func runFakeKnowledgeBd(args []string) {
	// `bd init` runs with the target as cwd and carries no -C argument —
	// the real CLI resolves the target from the working directory here.
	if len(args) > 0 && args[0] == "init" {
		wd, _ := os.Getwd()
		fakeBdLog(wd, args)
		if err := os.MkdirAll(filepath.Join(wd, ".beads"), 0o755); err != nil {
			fmt.Fprintln(os.Stderr, "fake bd init:", err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	dir := fakeBdFlag(args, "-C")
	statePath := filepath.Join(dir, "state.json")
	fakeBdLog(dir, args)
	// The payload goes to a file, not stdout: the re-executed test binary
	// prints its own framework banner there first, and the wrapper script
	// cats the file on success so the caller sees only it.
	payload := ""
	switch args[0] {
	case "memories":
		// Real bd envelopes a numeric schema_version into the same flat
		// object as the memories; mirror that shape so the tolerant decode
		// is exercised and the metadata field is never mirrored.
		envelope := map[string]any{"schema_version": 1}
		for key, content := range fakeBdRead(statePath) {
			envelope[key] = content
		}
		out, _ := json.Marshal(envelope)
		payload = string(out)
	case "remember":
		if _, err := os.Stat(filepath.Join(dir, "FAIL_REMEMBER")); err == nil {
			fmt.Fprintln(os.Stderr, "simulated bd write failure")
			os.Exit(1)
		}
		state := fakeBdRead(statePath)
		state[fakeBdFlag(args, "--key")] = args[1]
		data, _ := json.Marshal(state)
		os.WriteFile(statePath, data, 0o644)
	case "recall":
		payload = fakeBdRead(statePath)[args[1]]
	default:
		fmt.Fprintf(os.Stderr, "fake bd: unknown subcommand %q\n", args[0])
		os.Exit(1)
	}
	os.WriteFile(filepath.Join(dir, "stdout.payload"), []byte(payload), 0o644)
	os.Exit(0)
}

// fakeKnowledgeBdSetup redirects the knowledge flow's bd binary at this test
// binary re-executed in fake-bd mode via a wrapper script.
func fakeKnowledgeBdSetup(t *testing.T) {
	t.Helper()
	script := filepath.Join(t.TempDir(), "fake-bd")
	body := `#!/bin/sh
dir=""
prev=""
for a in "$@"; do
  if [ "$prev" = "-C" ]; then dir="$a"; fi
  prev="$a"
done
errfile="$dir/subprocess.stderr"
if [ -z "$dir" ]; then errfile="$(mktemp)"; fi
"${GO_FAKE_BD_TARGET}" -test.run=^TestFakeBd$ -- fakebd "$@" >/dev/null 2>"$errfile"
status=$?
if [ "$status" -eq 0 ] && [ -n "$dir" ] && [ -f "$dir/stdout.payload" ]; then
  cat "$dir/stdout.payload"
fi
if [ "$status" -ne 0 ] && [ -s "$errfile" ]; then
  cat "$errfile" >&2
fi
[ -n "$dir" ] || rm -f "$errfile"
exit $status
`
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatalf("write fake bd wrapper: %v", err)
	}
	t.Setenv("MULTICA_KNOWLEDGE_BD_BIN", script)
	t.Setenv("GO_FAKE_BD_TARGET", os.Args[0])
	// Ultimate paths land in a test-owned root instead of the profile dir.
	t.Setenv("MULTICA_KNOWLEDGE_STATE_DIR", t.TempDir())
}

// fakeKnowledgeBdReplace overwrites the whole fake bd state of a source.
func fakeKnowledgeBdReplace(t *testing.T, dir string, state map[string]string) {
	t.Helper()
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal fake bd state: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "state.json"), data, 0o644); err != nil {
		t.Fatalf("write fake bd state: %v", err)
	}
}

func fakeKnowledgeBdState(t *testing.T, dir string) map[string]string {
	t.Helper()
	return fakeBdRead(filepath.Join(dir, "state.json"))
}

// --- fake server -------------------------------------------------------------

// fakeKnowledgeServer serves the two daemon-facing endpoints from an in-memory
// plan and captures every posted report.
type fakeKnowledgeServer struct {
	mu      sync.Mutex
	plan    KnowledgePlan
	results []KnowledgeResults
	srv     *httptest.Server
}

func newFakeKnowledgeServer(t *testing.T, plan KnowledgePlan) *fakeKnowledgeServer {
	t.Helper()
	fake := &fakeKnowledgeServer{plan: plan}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/daemon/knowledge/plan", func(w http.ResponseWriter, _ *http.Request) {
		fake.mu.Lock()
		defer fake.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(fake.plan)
	})
	mux.HandleFunc("POST /api/daemon/knowledge/results", func(w http.ResponseWriter, r *http.Request) {
		var results KnowledgeResults
		if err := json.NewDecoder(r.Body).Decode(&results); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		fake.mu.Lock()
		fake.results = append(fake.results, results)
		fake.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(&KnowledgeResultsAck{})
	})
	fake.srv = httptest.NewServer(mux)
	t.Cleanup(fake.srv.Close)
	return fake
}

func (f *fakeKnowledgeServer) posted(t *testing.T) []KnowledgeResults {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]KnowledgeResults(nil), f.results...)
}

// newKnowledgeTestDaemon builds a daemon talking to the fake server.
func newKnowledgeTestDaemon(t *testing.T, fake *fakeKnowledgeServer) *Daemon {
	t.Helper()
	return &Daemon{
		cfg:    Config{DaemonID: "daemon-under-test"},
		client: NewClient(fake.srv.URL),
		logger: slog.Default(),
	}
}

// --- tests -------------------------------------------------------------------

// TestDaemonKnowledgeDiscoveryAndScan walks the discovery half-cycle: a
// project root carrying .beads is reported once, its first scan reports the
// memories, an unchanged source is silent, and a changed source reports the
// new map. Discovery never re-reports a registered path.
func TestDaemonKnowledgeDiscoveryAndScan(t *testing.T) {
	fakeKnowledgeBdSetup(t)
	source := t.TempDir()
	if err := os.MkdirAll(filepath.Join(source, ".beads"), 0o755); err != nil {
		t.Fatalf("seed .beads: %v", err)
	}
	fakeKnowledgeBdReplace(t, source, map[string]string{"tip-1": "value one"})

	// Cycle 1: the root is discovered, nothing else yet.
	plan := KnowledgePlan{Workspaces: []KnowledgePlanWorkspace{{
		WorkspaceID:    "ws-1",
		DiscoveryRoots: []string{source},
	}}}
	fake := newFakeKnowledgeServer(t, plan)
	d := newKnowledgeTestDaemon(t, fake)
	if err := d.knowledgeSync(context.Background()); err != nil {
		t.Fatalf("cycle 1: %v", err)
	}
	posted := fake.posted(t)
	if len(posted) != 1 || len(posted[0].Discoveries) != 1 {
		t.Fatalf("cycle 1 report: %+v, want one discovery", posted)
	}
	disc := posted[0].Discoveries[0]
	if disc.Path != source || disc.WorkspaceID != "ws-1" || disc.Label != filepath.Base(source) {
		t.Fatalf("discovery report: %+v", disc)
	}

	// Cycle 2: the registered directory needs its first scan; the discovery
	// is silent this time, and the server now knows the ultimate.
	fake.mu.Lock()
	fake.plan.Workspaces[0].HasUltimate = true
	fake.plan.Workspaces[0].Dirs = []KnowledgePlanDir{{
		DirID: "dir-1", Kind: "candidate_auto", Path: source, Bound: true,
		ScanRequired: true, Mirror: map[string]string{},
	}}
	fake.mu.Unlock()
	if err := d.knowledgeSync(context.Background()); err != nil {
		t.Fatalf("cycle 2: %v", err)
	}
	posted = fake.posted(t)
	if len(posted) != 2 || len(posted[1].Discoveries) != 0 || len(posted[1].Scans) != 1 {
		t.Fatalf("cycle 2 report: %+v, want one scan and no discovery", posted)
	}
	scan := posted[1].Scans[0]
	if !scan.OK || scan.TriggerSource != "initial" || scan.Memories["tip-1"] != "value one" {
		t.Fatalf("first scan report: %+v", scan)
	}

	// Cycle 3: byte-identical source, no refresh queued — nothing is sent.
	fake.mu.Lock()
	fake.plan.Workspaces[0].Dirs[0].ScanRequired = false
	fake.plan.Workspaces[0].Dirs[0].Mirror = map[string]string{
		"tip-1": knowledgeContentSHA("value one"),
	}
	fake.mu.Unlock()
	if err := d.knowledgeSync(context.Background()); err != nil {
		t.Fatalf("cycle 3: %v", err)
	}
	if posted = fake.posted(t); len(posted) != 2 {
		t.Fatalf("unchanged cycle must not report, got %+v", posted[len(posted)-1:])
	}

	// Cycle 4: the source changed — the new map is reported as scheduled.
	fakeKnowledgeBdReplace(t, source, map[string]string{"tip-1": "value one edited", "tip-2": "added"})
	if err := d.knowledgeSync(context.Background()); err != nil {
		t.Fatalf("cycle 4: %v", err)
	}
	posted = fake.posted(t)
	if len(posted) != 3 || len(posted[2].Scans) != 1 || posted[2].Scans[0].TriggerSource != "scheduled" {
		t.Fatalf("cycle 4 report: %+v", posted[len(posted)-1:])
	}
	if scan = posted[2].Scans[0]; !scan.OK || scan.Memories["tip-2"] != "added" {
		t.Fatalf("changed scan report: %+v", scan)
	}
}

// TestDaemonKnowledgeUnchangedRefresh pins the manual-refresh half: an
// unchanged source with a queued scan request answers with Unchanged (no
// memories payload) instead of staying silent.
func TestDaemonKnowledgeUnchangedRefresh(t *testing.T) {
	fakeKnowledgeBdSetup(t)
	source := t.TempDir()
	if err := os.MkdirAll(filepath.Join(source, ".beads"), 0o755); err != nil {
		t.Fatalf("seed .beads: %v", err)
	}
	fakeKnowledgeBdReplace(t, source, map[string]string{"k": "stable"})

	plan := KnowledgePlan{Workspaces: []KnowledgePlanWorkspace{{
		WorkspaceID: "ws-1",
		Dirs: []KnowledgePlanDir{{
			DirID: "dir-1", Kind: "candidate_cli", Path: source, Bound: true,
			ScanRequested: true, ScanRequired: true, Mirror: map[string]string{"k": knowledgeContentSHA("stable")},
		}},
	}}}
	fake := newFakeKnowledgeServer(t, plan)
	d := newKnowledgeTestDaemon(t, fake)
	if err := d.knowledgeSync(context.Background()); err != nil {
		t.Fatalf("cycle: %v", err)
	}
	posted := fake.posted(t)
	if len(posted) != 1 || len(posted[0].Scans) != 1 {
		t.Fatalf("refresh report: %+v", posted)
	}
	scan := posted[0].Scans[0]
	if !scan.OK || !scan.Unchanged || scan.TriggerSource != "manual" || len(scan.Memories) != 0 {
		t.Fatalf("unchanged refresh report: %+v, want manual unchanged without memories", scan)
	}
}

// TestDaemonKnowledgeUltimateDesignation pins §2: a workspace without an
// active ultimate gets one reported on the daemon-managed stable path, and
// the path is an initialized bd project after the cycle.
func TestDaemonKnowledgeUltimateDesignation(t *testing.T) {
	fakeKnowledgeBdSetup(t)

	plan := KnowledgePlan{Workspaces: []KnowledgePlanWorkspace{{WorkspaceID: "ws-7"}}}
	fake := newFakeKnowledgeServer(t, plan)
	d := newKnowledgeTestDaemon(t, fake)
	if err := d.knowledgeSync(context.Background()); err != nil {
		t.Fatalf("cycle: %v", err)
	}
	posted := fake.posted(t)
	if len(posted) != 1 || len(posted[0].Ultimates) != 1 {
		t.Fatalf("report: %+v, want one ultimate", posted)
	}
	ult := posted[0].Ultimates[0]
	want := filepath.Join(os.Getenv("MULTICA_KNOWLEDGE_STATE_DIR"), "daemon-knowledge", "ultimate", "ws-7")
	if ult.WorkspaceID != "ws-7" || ult.Path != want {
		t.Fatalf("ultimate report: %+v, want path %s", ult, want)
	}
	if _, err := os.Stat(filepath.Join(want, ".beads")); err != nil {
		t.Fatalf("ultimate path not bd-initialized: %v", err)
	}

	// A workspace that already has an ultimate is left alone.
	fake.mu.Lock()
	fake.plan.Workspaces[0].HasUltimate = true
	fake.mu.Unlock()
	if err := d.knowledgeSync(context.Background()); err != nil {
		t.Fatalf("cycle 2: %v", err)
	}
	if posted = fake.posted(t); len(posted) != 1 {
		t.Fatalf("cycle 2 must not report: %+v", posted)
	}
}

// TestDaemonKnowledgeAdoptionTransfer pins §3's daemon half: the job's
// content lands in the ultimate bd under the plan's key, the read-back
// verifies it, and success/failure/duplicate outcomes are reported so the
// server can land the terminal states.
func TestDaemonKnowledgeAdoptionTransfer(t *testing.T) {
	fakeKnowledgeBdSetup(t)
	ultimate := t.TempDir()
	if err := os.MkdirAll(filepath.Join(ultimate, ".beads"), 0o755); err != nil {
		t.Fatalf("seed ultimate .beads: %v", err)
	}
	fakeKnowledgeBdReplace(t, ultimate, map[string]string{"existing": "untouched ultimate content"})

	makePlan := func(jobs ...KnowledgePlanAdoption) KnowledgePlan {
		return KnowledgePlan{Workspaces: []KnowledgePlanWorkspace{{
			WorkspaceID: "ws-1",
			HasUltimate: true,
			Ultimate:    &KnowledgePlanUltimate{DirID: "dir-ult", Path: ultimate},
			Adoptions:   jobs,
		}}}
	}
	fake := newFakeKnowledgeServer(t, makePlan(KnowledgePlanAdoption{
		Kind: "entry", ID: "entry-1", Key: "tip-x", Content: "a memory worth keeping",
		Actor: "multica-owner", UltimateDirID: "dir-ult", UltimatePath: ultimate,
	}))
	d := newKnowledgeTestDaemon(t, fake)
	if err := d.knowledgeSync(context.Background()); err != nil {
		t.Fatalf("cycle: %v", err)
	}
	if state := fakeKnowledgeBdState(t, ultimate); state["tip-x"] != "a memory worth keeping" {
		t.Fatalf("ultimate state after transfer: %v", state)
	}
	if state := fakeKnowledgeBdState(t, ultimate); state["existing"] != "untouched ultimate content" {
		t.Fatalf("pre-existing ultimate memory was touched: %v", state)
	}
	posted := fake.posted(t)
	if len(posted) != 1 || len(posted[0].Adoptions) != 1 {
		t.Fatalf("adoption report: %+v", posted)
	}
	report := posted[0].Adoptions[0]
	if !report.OK || report.ID != "entry-1" || report.UltimateDirID != "dir-ult" {
		t.Fatalf("success report: %+v", report)
	}

	// A duplicate-content job fails with a pointer to the existing key.
	fake.mu.Lock()
	fake.plan.Workspaces[0].Adoptions = []KnowledgePlanAdoption{{
		Kind: "proposal", ID: "prop-1", Key: "proposal-deadbeef", Content: "untouched ultimate content",
		UltimateDirID: "dir-ult", UltimatePath: ultimate,
	}}
	fake.mu.Unlock()
	if err := d.knowledgeSync(context.Background()); err != nil {
		t.Fatalf("cycle 2: %v", err)
	}
	posted = fake.posted(t)
	report = posted[len(posted)-1].Adoptions[0]
	if report.OK || report.ID != "prop-1" || !strings.Contains(report.Error, "already exists") {
		t.Fatalf("duplicate report: %+v", report)
	}

	// A bd write failure reports the reason instead of pretending success.
	if err := os.WriteFile(filepath.Join(ultimate, "FAIL_REMEMBER"), []byte("1"), 0o644); err != nil {
		t.Fatalf("plant failure flag: %v", err)
	}
	fake.mu.Lock()
	fake.plan.Workspaces[0].Adoptions = []KnowledgePlanAdoption{{
		Kind: "entry", ID: "entry-2", Key: "tip-y", Content: "another memory",
		UltimateDirID: "dir-ult", UltimatePath: ultimate,
	}}
	fake.mu.Unlock()
	if err := d.knowledgeSync(context.Background()); err != nil {
		t.Fatalf("cycle 3: %v", err)
	}
	posted = fake.posted(t)
	report = posted[len(posted)-1].Adoptions[0]
	if report.OK || report.ID != "entry-2" || !strings.Contains(report.Error, "bd remember") {
		t.Fatalf("failure report: %+v", report)
	}
	if state := fakeKnowledgeBdState(t, ultimate); state["tip-y"] != "" {
		t.Fatalf("failed transfer must not write: %v", state)
	}
}
