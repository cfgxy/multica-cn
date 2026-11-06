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
	"time"
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

func fakeBdHasArg(args []string, want string) bool {
	for _, arg := range args {
		if arg == want {
			return true
		}
	}
	return false
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
		if fakeBdHasArg(args, "--proxied-server") ||
			!fakeBdHasArg(args, "--non-interactive") ||
			!fakeBdHasArg(args, "--skip-hooks") ||
			!fakeBdHasArg(args, "--skip-agents") ||
			!fakeBdHasArg(args, "--init-if-missing") {
			fmt.Fprintln(os.Stderr, "fake bd init: local init flags are required")
			os.Exit(1)
		}
		if _, err := os.Stat(filepath.Join(wd, ".git")); err != nil {
			fmt.Fprintln(os.Stderr, "fake bd init: git root is required")
			os.Exit(1)
		}
		if err := os.MkdirAll(filepath.Join(wd, ".beads", "embeddeddolt"), 0o755); err != nil {
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
		if _, err := os.Stat(filepath.Join(dir, "FAIL_MEMORIES")); err == nil {
			fmt.Fprintln(os.Stderr, "simulated bd memories IO failure")
			os.Exit(1)
		}
		if _, err := os.Stat(filepath.Join(dir, "MALFORMED_MEMORIES")); err == nil {
			payload = "{"
			break
		}
		if _, err := os.Stat(filepath.Join(dir, "INVALID_MEMORIES")); err == nil {
			payload = "{not-json"
			break
		}
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
		if _, err := os.Stat(filepath.Join(dir, "FAIL_RECALL")); err == nil {
			fmt.Fprintln(os.Stderr, "simulated bd recall IO failure")
			os.Exit(1)
		}
		if _, err := os.Stat(filepath.Join(dir, "FAIL_RECALL_"+args[1])); err == nil {
			fmt.Fprintln(os.Stderr, "simulated bd recall IO failure")
			os.Exit(1)
		}
		content, found := fakeBdRead(statePath)[args[1]]
		if !found {
			fmt.Fprintln(os.Stderr, "fake bd: memory not found")
			os.Exit(1)
		}
		payload = content
	case "forget":
		if _, err := os.Stat(filepath.Join(dir, "FAIL_FORGET")); err == nil {
			fmt.Fprintln(os.Stderr, "simulated bd forget failure")
			os.Exit(1)
		}
		if _, err := os.Stat(filepath.Join(dir, "FAIL_FORGET_"+args[1])); err == nil {
			fmt.Fprintln(os.Stderr, "simulated bd forget failure")
			os.Exit(1)
		}
		state := fakeBdRead(statePath)
		if _, found := state[args[1]]; !found {
			fmt.Fprintln(os.Stderr, "fake bd: memory not found")
			os.Exit(1)
		}
		delete(state, args[1])
		data, _ := json.Marshal(state)
		os.WriteFile(statePath, data, 0o644)
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
	mu                 sync.Mutex
	plan               KnowledgePlan
	results            []KnowledgeResults
	resultFailuresLeft map[string]int
	srv                *httptest.Server
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
		workspaceID := knowledgeResultWorkspaceID(results)
		if fake.resultFailuresLeft[workspaceID] > 0 {
			fake.resultFailuresLeft[workspaceID]--
			fake.mu.Unlock()
			http.Error(w, "simulated knowledge callback failure", http.StatusInternalServerError)
			return
		}
		fake.results = append(fake.results, results)
		fake.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(&KnowledgeResultsAck{})
	})
	fake.srv = httptest.NewServer(mux)
	t.Cleanup(fake.srv.Close)
	return fake
}

func knowledgeResultWorkspaceID(results KnowledgeResults) string {
	if len(results.Ultimates) > 0 {
		return results.Ultimates[0].WorkspaceID
	}
	if len(results.Discoveries) > 0 {
		return results.Discoveries[0].WorkspaceID
	}
	return ""
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
	d := &Daemon{
		cfg:    Config{DaemonID: "daemon-under-test"},
		client: NewClient(fake.srv.URL),
		logger: slog.Default(),
	}
	for _, workspace := range fake.plan.Workspaces {
		d.client.SetKnowledgeToken(workspace.WorkspaceID, "mdt-test-"+workspace.WorkspaceID)
	}
	return d
}

func TestPostKnowledgeResultsUsesWorkspaceDaemonToken(t *testing.T) {
	var authorization string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"discoveries_registered":1}`))
	}))
	defer srv.Close()

	client := NewClient(srv.URL)
	client.SetToken("mul-profile-pat")
	client.SetKnowledgeToken("workspace-1", "mdt-registration-token")
	if _, err := client.PostKnowledgeResults(context.Background(), "daemon-1", "workspace-1", &KnowledgeResults{}); err != nil {
		t.Fatalf("post knowledge results: %v", err)
	}
	if authorization != "Bearer mdt-registration-token" {
		t.Fatalf("authorization = %q, want workspace daemon token", authorization)
	}
}

func TestPostKnowledgeResultsRenewsOnlyTheRejectedWorkspaceToken(t *testing.T) {
	var authorizations []string
	var refreshes int
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/daemon/knowledge/results", func(w http.ResponseWriter, r *http.Request) {
		authorization := r.Header.Get("Authorization")
		authorizations = append(authorizations, authorization)
		if authorization == "Bearer mdt-old" && refreshes == 0 {
			http.Error(w, "expired", http.StatusUnauthorized)
			return
		}
		if authorization != "Bearer mdt-old" {
			http.Error(w, "unexpected credential", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	})
	mux.HandleFunc("POST /api/daemon/knowledge/token/refresh", func(w http.ResponseWriter, r *http.Request) {
		refreshes++
		if got := r.Header.Get("Authorization"); got != "Bearer mdt-old" {
			http.Error(w, "refresh used wrong credential", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"renewed":true}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := NewClient(srv.URL)
	client.SetToken("mul-profile-pat")
	if _, err := client.PostKnowledgeResults(context.Background(), "daemon-1", "workspace-missing", &KnowledgeResults{}); err == nil {
		t.Fatal("callback without a workspace daemon credential succeeded")
	}
	client.SetKnowledgeToken("workspace-1", "mdt-old")
	client.SetKnowledgeToken("workspace-2", "mdt-other")
	if _, err := client.PostKnowledgeResults(context.Background(), "daemon-1", "workspace-1", &KnowledgeResults{}); err != nil {
		t.Fatalf("post knowledge results after renewal: %v", err)
	}
	if refreshes != 1 || len(authorizations) != 2 || authorizations[0] != "Bearer mdt-old" || authorizations[1] != "Bearer mdt-old" {
		t.Fatalf("credential sequence = refreshes:%d authorizations:%v", refreshes, authorizations)
	}
	if got := client.knowledgeToken("workspace-1"); got != "mdt-old" {
		t.Fatalf("renewed workspace token = %q, want unchanged token", got)
	}
	if got := client.knowledgeToken("workspace-2"); got != "mdt-other" {
		t.Fatalf("other workspace token changed to %q", got)
	}
}

func TestRefreshKnowledgeTokenIfNeededRenewsBeforeExpiry(t *testing.T) {
	var refreshes int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/daemon/knowledge/token/refresh" {
			t.Fatalf("request path = %s", r.URL.Path)
		}
		refreshes++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"renewed":true}`))
	}))
	defer srv.Close()

	client := NewClient(srv.URL)
	client.SetKnowledgeToken("workspace-1", "mdt-old")
	client.knowledgeTokenMu.Lock()
	client.knowledgeTokenIssuedAt["workspace-1"] = time.Now().Add(-knowledgeTokenRenewalInterval)
	client.knowledgeTokenMu.Unlock()
	if err := client.RefreshKnowledgeTokenIfNeeded(context.Background(), "workspace-1"); err != nil {
		t.Fatalf("refresh expiring credential: %v", err)
	}
	if refreshes != 1 || client.knowledgeToken("workspace-1") != "mdt-old" {
		t.Fatalf("credential refresh = %d, token = %q", refreshes, client.knowledgeToken("workspace-1"))
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

// TestDaemonKnowledgeSourceDeletedReappearance 验证 source_deleted key 即使
// 内容仍与镜像 SHA 一致且未排队手动扫描，重新出现时也会被上报以供恢复。
func TestDaemonKnowledgeSourceDeletedReappearance(t *testing.T) {
	fakeKnowledgeBdSetup(t)
	source := t.TempDir()
	if err := os.MkdirAll(filepath.Join(source, ".beads"), 0o755); err != nil {
		t.Fatalf("seed .beads: %v", err)
	}
	fakeKnowledgeBdReplace(t, source, map[string]string{"tip": "stable"})

	plan := KnowledgePlan{Workspaces: []KnowledgePlanWorkspace{{
		WorkspaceID: "ws-1",
		Dirs: []KnowledgePlanDir{{
			DirID: "dir-1", Kind: "candidate_cli", Path: source, Bound: true,
			Mirror:        map[string]string{"tip": knowledgeContentSHA("stable")},
			SourceDeleted: map[string]bool{"tip": true},
		}},
	}}}
	fake := newFakeKnowledgeServer(t, plan)
	d := newKnowledgeTestDaemon(t, fake)
	if err := d.knowledgeSync(context.Background()); err != nil {
		t.Fatalf("cycle: %v", err)
	}
	posted := fake.posted(t)
	if len(posted) != 1 || len(posted[0].Scans) != 1 {
		t.Fatalf("恢复扫描上报=%+v，期望一条扫描", posted)
	}
	scan := posted[0].Scans[0]
	if !scan.OK || scan.Unchanged || scan.TriggerSource != "scheduled" || scan.Memories["tip"] != "stable" {
		t.Fatalf("恢复扫描=%+v，期望 scheduled 全量 source 上报", scan)
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
	if !hasLocalBeadsDatabase(want) {
		t.Fatalf("ultimate path did not use a local embedded bd store")
	}
	if _, err := os.Stat(filepath.Join(want, ".git")); err != nil {
		t.Fatalf("ultimate path did not establish its own git root: %v", err)
	}
	invocations, err := os.ReadFile(filepath.Join(want, "invocations.log"))
	if err != nil || !strings.Contains(string(invocations), "init\x1f--non-interactive\x1f--skip-hooks\x1f--skip-agents\x1f--init-if-missing") || strings.Contains(string(invocations), "--proxied-server") {
		t.Fatalf("ultimate init invocation = %q, err = %v", invocations, err)
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

// TestDaemonKnowledgeClaimsAccessibleManualUltimate ensures a manually
// registered target becomes claimable only after the hosting daemon confirms
// the same path contains its local bd database.
func TestDaemonKnowledgeClaimsAccessibleManualUltimate(t *testing.T) {
	fakeKnowledgeBdSetup(t)
	ultimate := t.TempDir()
	if err := os.MkdirAll(filepath.Join(ultimate, ".beads"), 0o755); err != nil {
		t.Fatalf("seed manual ultimate .beads: %v", err)
	}

	plan := KnowledgePlan{Workspaces: []KnowledgePlanWorkspace{{
		WorkspaceID:     "ws-manual",
		HasUltimate:     true,
		UnboundUltimate: &KnowledgePlanUltimate{DirID: "dir-manual", Path: ultimate},
	}}}
	fake := newFakeKnowledgeServer(t, plan)
	d := newKnowledgeTestDaemon(t, fake)
	if err := d.knowledgeSync(context.Background()); err != nil {
		t.Fatalf("accessible target cycle: %v", err)
	}
	posted := fake.posted(t)
	if len(posted) != 1 || len(posted[0].Ultimates) != 1 {
		t.Fatalf("accessible manual target report: %+v", posted)
	}
	if report := posted[0].Ultimates[0]; report.WorkspaceID != "ws-manual" || report.Path != ultimate {
		t.Fatalf("manual target report: %+v", report)
	}

	fake.mu.Lock()
	fake.plan.Workspaces[0].UnboundUltimate = &KnowledgePlanUltimate{DirID: "dir-missing", Path: t.TempDir()}
	fake.mu.Unlock()
	if err := d.knowledgeSync(context.Background()); err != nil {
		t.Fatalf("inaccessible target cycle: %v", err)
	}
	if posted = fake.posted(t); len(posted) != 1 {
		t.Fatalf("inaccessible manual target must not be claimed: %+v", posted)
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

	// An existing key cannot be overwritten with different content, but replaying
	// the exact same transfer is safe and does not need another bd write.
	fake.mu.Lock()
	fake.plan.Workspaces[0].Adoptions = []KnowledgePlanAdoption{{
		Kind: "entry", ID: "entry-conflict", Key: "tip-x", Content: "replacement content",
		UltimateDirID: "dir-ult", UltimatePath: ultimate,
	}}
	fake.mu.Unlock()
	if err := d.knowledgeSync(context.Background()); err != nil {
		t.Fatalf("same-key conflict cycle: %v", err)
	}
	posted = fake.posted(t)
	report = posted[len(posted)-1].Adoptions[0]
	if report.OK || !strings.Contains(report.Error, "different content") {
		t.Fatalf("same-key conflict report: %+v", report)
	}
	if state := fakeKnowledgeBdState(t, ultimate); state["tip-x"] != "a memory worth keeping" {
		t.Fatalf("same-key conflict overwrote the existing memory: %v", state)
	}

	fake.mu.Lock()
	fake.plan.Workspaces[0].Adoptions = []KnowledgePlanAdoption{{
		Kind: "entry", ID: "entry-replay", Key: "tip-x", Content: "a memory worth keeping",
		UltimateDirID: "dir-ult", UltimatePath: ultimate,
	}}
	fake.mu.Unlock()
	if err := d.knowledgeSync(context.Background()); err != nil {
		t.Fatalf("same-key replay cycle: %v", err)
	}
	posted = fake.posted(t)
	report = posted[len(posted)-1].Adoptions[0]
	if !report.OK {
		t.Fatalf("same-key replay report: %+v", report)
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

func TestKnowledgeAdoptionRejectsUnparseableUltimateStore(t *testing.T) {
	fakeKnowledgeBdSetup(t)
	ultimate := t.TempDir()
	if err := os.MkdirAll(filepath.Join(ultimate, ".beads"), 0o755); err != nil {
		t.Fatalf("seed ultimate .beads: %v", err)
	}
	fakeKnowledgeBdReplace(t, ultimate, map[string]string{"key": "existing content"})
	if err := os.WriteFile(filepath.Join(ultimate, "MALFORMED_MEMORIES"), []byte("1"), 0o644); err != nil {
		t.Fatalf("plant malformed memories marker: %v", err)
	}
	d := &Daemon{logger: slog.Default()}
	_, err := d.runKnowledgeAdoption(context.Background(), KnowledgePlanAdoption{
		Kind: "entry", ID: "entry-1", Key: "key", Content: "replacement content", UltimatePath: ultimate,
	})
	if err == nil || !strings.Contains(err.Error(), "inspect ultimate directory") {
		t.Fatalf("unparseable ultimate adoption error = %v", err)
	}
	if state := fakeKnowledgeBdState(t, ultimate); state["key"] != "existing content" {
		t.Fatalf("unparseable ultimate store was overwritten: %v", state)
	}
}

// TestDaemonKnowledgeOperations proves the mutation half of the pull protocol:
// an edit and merge are read back before reporting success, merge sources and
// confirmed deletes are forgotten, a partially completed merge reports
// failure then converges on replay, and an already-absent key converges
// after a lost result replay.
func TestDaemonKnowledgeOperations(t *testing.T) {
	fakeKnowledgeBdSetup(t)
	ultimate := t.TempDir()
	if err := os.MkdirAll(filepath.Join(ultimate, ".beads"), 0o755); err != nil {
		t.Fatalf("seed ultimate .beads: %v", err)
	}
	fakeKnowledgeBdReplace(t, ultimate, map[string]string{
		"target": "original", "source": "merge source", "gone": "delete me",
	})

	plan := KnowledgePlan{Workspaces: []KnowledgePlanWorkspace{{
		WorkspaceID: "ws-operations",
		HasUltimate: true,
		Operations: []KnowledgePlanOperation{
			{ID: "edit-1", OperationType: "edit", Key: "target", Content: "edited", Actor: "multica-owner", UltimatePath: ultimate},
			{ID: "merge-1", OperationType: "merge", Key: "target", Content: "consolidated", DeleteKeys: []string{"source"}, Actor: "multica-owner", UltimatePath: ultimate},
			{ID: "delete-1", OperationType: "delete", Key: "gone", UltimatePath: ultimate},
		},
	}}}
	fake := newFakeKnowledgeServer(t, plan)
	d := newKnowledgeTestDaemon(t, fake)
	if err := d.knowledgeSync(context.Background()); err != nil {
		t.Fatalf("successful operations cycle: %v", err)
	}
	state := fakeKnowledgeBdState(t, ultimate)
	if state["target"] != "consolidated" || state["source"] != "" || state["gone"] != "" {
		t.Fatalf("knowledge state after operations: %v", state)
	}
	posted := fake.posted(t)
	if len(posted) != 1 || len(posted[0].Operations) != 3 {
		t.Fatalf("operation report: %+v", posted)
	}
	for _, report := range posted[0].Operations {
		if !report.OK || report.Error != "" {
			t.Fatalf("successful operation report: %+v", report)
		}
	}

	if err := os.WriteFile(filepath.Join(ultimate, "FAIL_FORGET"), []byte("1"), 0o644); err != nil {
		t.Fatalf("plant forget failure flag: %v", err)
	}
	fake.mu.Lock()
	fake.plan.Workspaces[0].Operations = []KnowledgePlanOperation{{
		ID: "delete-fails", OperationType: "delete", Key: "target", UltimatePath: ultimate,
	}}
	fake.mu.Unlock()
	if err := d.knowledgeSync(context.Background()); err != nil {
		t.Fatalf("failed delete cycle: %v", err)
	}
	posted = fake.posted(t)
	report := posted[len(posted)-1].Operations[0]
	if report.OK || !strings.Contains(report.Error, "bd forget target") {
		t.Fatalf("failed delete report: %+v", report)
	}
	if state = fakeKnowledgeBdState(t, ultimate); state["target"] != "consolidated" {
		t.Fatalf("failed delete removed memory: %v", state)
	}
	if err := os.Remove(filepath.Join(ultimate, "FAIL_FORGET")); err != nil {
		t.Fatalf("clear forget failure flag: %v", err)
	}

	// A merge can write its target and delete its first source before a later
	// forget fails. The next queued replay must finish from that partial bd
	// state instead of treating the first failure as terminal.
	fakeKnowledgeBdReplace(t, ultimate, map[string]string{
		"target": "before merge", "source-one": "first source", "source-two": "second source",
	})
	if err := os.WriteFile(filepath.Join(ultimate, "FAIL_FORGET_source-two"), []byte("1"), 0o644); err != nil {
		t.Fatalf("plant second source forget failure flag: %v", err)
	}
	fake.mu.Lock()
	fake.plan.Workspaces[0].Operations = []KnowledgePlanOperation{{
		ID: "merge-retry", OperationType: "merge", Key: "target", Content: "consolidated again",
		DeleteKeys: []string{"source-one", "source-two"}, Actor: "multica-owner", UltimatePath: ultimate,
	}}
	fake.mu.Unlock()
	if err := d.knowledgeSync(context.Background()); err != nil {
		t.Fatalf("partially completed merge cycle: %v", err)
	}
	posted = fake.posted(t)
	report = posted[len(posted)-1].Operations[0]
	if report.OK || !strings.Contains(report.Error, "bd forget source-two") {
		t.Fatalf("partial merge failure report: %+v", report)
	}
	if state = fakeKnowledgeBdState(t, ultimate); state["target"] != "consolidated again" || state["source-one"] != "" || state["source-two"] != "second source" {
		t.Fatalf("partial merge bd state: %v", state)
	}
	if err := os.Remove(filepath.Join(ultimate, "FAIL_FORGET_source-two")); err != nil {
		t.Fatalf("clear second source forget failure flag: %v", err)
	}
	if err := d.knowledgeSync(context.Background()); err != nil {
		t.Fatalf("partially completed merge replay cycle: %v", err)
	}
	posted = fake.posted(t)
	report = posted[len(posted)-1].Operations[0]
	if !report.OK || report.Error != "" {
		t.Fatalf("partial merge replay report: %+v", report)
	}
	if state = fakeKnowledgeBdState(t, ultimate); state["target"] != "consolidated again" || state["source-one"] != "" || state["source-two"] != "" {
		t.Fatalf("partial merge replay bd state: %v", state)
	}

	// A replay after bd already removed the key still returns success: the
	// handler can complete the durable operation without treating it as a new
	// failure merely because the first report was lost.
	fake.mu.Lock()
	fake.plan.Workspaces[0].Operations = []KnowledgePlanOperation{{
		ID: "delete-replay", OperationType: "delete", Key: "already-absent", UltimatePath: ultimate,
	}}
	fake.mu.Unlock()
	if err := d.knowledgeSync(context.Background()); err != nil {
		t.Fatalf("idempotent delete cycle: %v", err)
	}
	posted = fake.posted(t)
	report = posted[len(posted)-1].Operations[0]
	if !report.OK || report.Error != "" {
		t.Fatalf("idempotent delete report: %+v", report)
	}

	// A malformed read-only list must remain visible to the server for a later
	// retry; delete and merge must never infer absence from recall stderr or
	// write a merge target before parsing the source library.
	fakeKnowledgeBdReplace(t, ultimate, map[string]string{
		"delete-hard": "delete me", "merge-hard": "merge source",
	})
	if err := os.WriteFile(filepath.Join(ultimate, "MALFORMED_MEMORIES"), []byte("1"), 0o644); err != nil {
		t.Fatalf("plant malformed memories marker: %v", err)
	}
	fake.mu.Lock()
	fake.plan.Workspaces[0].Operations = []KnowledgePlanOperation{
		{ID: "delete-hard", OperationType: "delete", Key: "delete-hard", UltimatePath: ultimate},
		{ID: "merge-hard", OperationType: "merge", Key: "target", Content: "merged", DeleteKeys: []string{"merge-hard"}, UltimatePath: ultimate},
	}
	fake.mu.Unlock()
	if err := d.knowledgeSync(context.Background()); err != nil {
		t.Fatalf("hard memories failure cycle: %v", err)
	}
	posted = fake.posted(t)
	for _, report := range posted[len(posted)-1].Operations {
		if report.OK || !strings.Contains(report.Error, "bd memories") {
			t.Fatalf("hard memories report: %+v", report)
		}
	}
	if state = fakeKnowledgeBdState(t, ultimate); state["delete-hard"] != "delete me" || state["merge-hard"] != "merge source" || state["target"] != "" {
		t.Fatalf("malformed memories changed bd state: %v", state)
	}
}

func TestDaemonKnowledgeCallbackFailureDoesNotBlockOtherWorkspaces(t *testing.T) {
	fakeKnowledgeBdSetup(t)
	failingUltimate := t.TempDir()
	healthyUltimate := t.TempDir()
	for _, path := range []string{failingUltimate, healthyUltimate} {
		if err := os.MkdirAll(filepath.Join(path, ".beads"), 0o755); err != nil {
			t.Fatalf("seed workspace ultimate %s: %v", path, err)
		}
	}
	fake := newFakeKnowledgeServer(t, KnowledgePlan{Workspaces: []KnowledgePlanWorkspace{
		{
			WorkspaceID: "ws-failing", HasUltimate: true,
			UnboundUltimate: &KnowledgePlanUltimate{DirID: "dir-failing", Path: failingUltimate},
		},
		{
			WorkspaceID: "ws-healthy", HasUltimate: true,
			UnboundUltimate: &KnowledgePlanUltimate{DirID: "dir-healthy", Path: healthyUltimate},
		},
	}})
	fake.resultFailuresLeft = map[string]int{"ws-failing": 1}
	d := newKnowledgeTestDaemon(t, fake)
	if err := d.knowledgeSync(context.Background()); err == nil || !strings.Contains(err.Error(), "ws-failing") {
		t.Fatalf("first cycle error = %v, want failing workspace diagnostic", err)
	}
	posted := fake.posted(t)
	if len(posted) != 1 || knowledgeResultWorkspaceID(posted[0]) != "ws-healthy" {
		t.Fatalf("healthy workspace was not reported after failure: %+v", posted)
	}
	if err := d.knowledgeSync(context.Background()); err != nil {
		t.Fatalf("retry cycle: %v", err)
	}
	posted = fake.posted(t)
	foundFailing := false
	for _, result := range posted {
		foundFailing = foundFailing || knowledgeResultWorkspaceID(result) == "ws-failing"
	}
	if !foundFailing {
		t.Fatalf("failed workspace was not retried: %+v", posted)
	}
}

func TestDaemonKnowledgeWritebackAppliesDirectSourceMutations(t *testing.T) {
	fakeKnowledgeBdSetup(t)
	source := t.TempDir()
	if err := os.MkdirAll(filepath.Join(source, ".beads"), 0o755); err != nil {
		t.Fatalf("seed source .beads: %v", err)
	}
	fakeKnowledgeBdReplace(t, source, map[string]string{"retire": "verified value"})
	d := newKnowledgeTestDaemon(t, newFakeKnowledgeServer(t, KnowledgePlan{}))

	remove := KnowledgePlanWriteback{
		EventID: "event-remove", EntryID: "entry-1", Operation: "remove", Path: source,
		Key: "retire", ContentSha256: knowledgeContentSHA("verified value"),
	}
	report, err := d.runKnowledgeWriteback(context.Background(), remove)
	if err != nil || !report.OK {
		t.Fatalf("remove write-back: report=%+v err=%v", report, err)
	}
	if _, found := fakeKnowledgeBdState(t, source)["retire"]; found {
		t.Fatalf("remove write-back kept retired source entry")
	}

	restore := KnowledgePlanWriteback{
		EventID: "event-restore", EntryID: "entry-1", Operation: "restore", Path: source,
		Key: "retire", Content: "restored value", ContentSha256: knowledgeContentSHA("restored value"), Actor: "multica-owner",
	}
	report, err = d.runKnowledgeWriteback(context.Background(), restore)
	if err != nil || !report.OK {
		t.Fatalf("restore write-back: report=%+v err=%v", report, err)
	}
	if got := fakeKnowledgeBdState(t, source)["retire"]; got != "restored value" {
		t.Fatalf("restore write-back source value = %q, want restored value", got)
	}
}

func TestDaemonKnowledgeWritebackReportsDirectMutationFailure(t *testing.T) {
	fakeKnowledgeBdSetup(t)
	source := t.TempDir()
	if err := os.MkdirAll(filepath.Join(source, ".beads"), 0o755); err != nil {
		t.Fatalf("seed source .beads: %v", err)
	}
	fakeKnowledgeBdReplace(t, source, map[string]string{"retire": "verified value"})
	if err := os.WriteFile(filepath.Join(source, "FAIL_FORGET"), []byte("1"), 0o644); err != nil {
		t.Fatalf("plant forget failure: %v", err)
	}

	d := newKnowledgeTestDaemon(t, newFakeKnowledgeServer(t, KnowledgePlan{}))
	report, err := d.runKnowledgeWriteback(context.Background(), KnowledgePlanWriteback{
		EventID: "event-remove", EntryID: "entry-1", Operation: "remove", Path: source, Key: "retire",
	})
	if err == nil || report.OK || !strings.Contains(err.Error(), "bd forget") {
		t.Fatalf("remove failure must remain observable: report=%+v err=%v", report, err)
	}
	if got := fakeKnowledgeBdState(t, source)["retire"]; got != "verified value" {
		t.Fatalf("failed delete changed source to %q", got)
	}
}

func TestDaemonKnowledgeWritebackRejectsChangedSource(t *testing.T) {
	fakeKnowledgeBdSetup(t)
	source := t.TempDir()
	if err := os.MkdirAll(filepath.Join(source, ".beads"), 0o755); err != nil {
		t.Fatalf("seed source .beads: %v", err)
	}
	fakeKnowledgeBdReplace(t, source, map[string]string{"retire": "newer source content"})

	d := newKnowledgeTestDaemon(t, newFakeKnowledgeServer(t, KnowledgePlan{}))
	report, err := d.runKnowledgeWriteback(context.Background(), KnowledgePlanWriteback{
		EventID: "event-remove", EntryID: "entry-1", Operation: "remove", Path: source, Key: "retire",
		ExpectedContentSha256: knowledgeContentSHA("reviewed source content"),
	})
	if err == nil || report.OK || !strings.Contains(err.Error(), "source content changed") {
		t.Fatalf("changed source must reject deletion: report=%+v err=%v", report, err)
	}
	if got := fakeKnowledgeBdState(t, source)["retire"]; got != "newer source content" {
		t.Fatalf("stale delete changed source to %q", got)
	}
}

func TestDaemonKnowledgeWritebackRejectsUnreadableSource(t *testing.T) {
	fakeKnowledgeBdSetup(t)
	source := t.TempDir()
	if err := os.MkdirAll(filepath.Join(source, ".beads"), 0o755); err != nil {
		t.Fatalf("seed source .beads: %v", err)
	}
	fakeKnowledgeBdReplace(t, source, map[string]string{"retire": "verified value"})
	if err := os.WriteFile(filepath.Join(source, "INVALID_MEMORIES"), []byte("1"), 0o644); err != nil {
		t.Fatalf("plant invalid-memory response flag: %v", err)
	}

	d := newKnowledgeTestDaemon(t, newFakeKnowledgeServer(t, KnowledgePlan{}))
	report, err := d.runKnowledgeWriteback(context.Background(), KnowledgePlanWriteback{
		EventID: "event-remove", EntryID: "entry-1", Operation: "remove", Path: source, Key: "retire",
	})
	if err == nil || report.OK || !strings.Contains(err.Error(), "decode bd memories") {
		t.Fatalf("unreadable source must reject deletion: report=%+v err=%v", report, err)
	}
	if got := fakeKnowledgeBdState(t, source)["retire"]; got != "verified value" {
		t.Fatalf("unreadable source changed entry to %q", got)
	}
	log, err := os.ReadFile(filepath.Join(source, "invocations.log"))
	if err != nil {
		t.Fatalf("read bd invocation log: %v", err)
	}
	if strings.Contains(string(log), "forget") {
		t.Fatalf("unreadable source must not invoke bd forget: %s", log)
	}
}
