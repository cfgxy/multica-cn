package handler

// Knowledge directory mirroring and the Owner adoption transfer at the HTTP
// boundary (RUYI-265, spec §K).
//
// bd is faked by re-executing this test binary in helper-process mode
// (TestFakeBd below): the state file inside the -C directory plays the bd
// library, and every invocation is logged so the tests can assert the scan
// path carries --readonly against the source and that the only writes ever
// issued target the ultimate directory.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
)

// --- fake bd (helper process) ----------------------------------------------

// TestFakeBd is never a real test in this run: when the binary is re-executed
// with a "-- fakebd" marker argv (see fakeBdSetup's wrapper script) it plays
// the bd CLI for one invocation and exits, so no framework output pollutes
// the captured stdout.
func TestFakeBd(t *testing.T) {
	for i, arg := range os.Args {
		if arg == "--" && i+2 < len(os.Args) && os.Args[i+1] == "fakebd" {
			runFakeBd(os.Args[i+2:])
		}
	}
}

func runFakeBd(args []string) {
	dir := fakeBdFlag(args, "-C")
	statePath := filepath.Join(dir, "state.json")
	if log, err := os.OpenFile(filepath.Join(dir, "invocations.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
		fmt.Fprintf(log, "%s\n", strings.Join(args, "\x1f"))
		log.Close()
	}
	// The payload goes to a file, not stdout: the re-executed test binary
	// prints its own TestMain banner there before TestFakeBd runs, and the
	// wrapper script cats the file on success so the caller sees only it.
	payload := ""
	// The positional argument (content for remember, key for recall) always
	// follows the subcommand immediately in knowledge.go's invocations.
	switch args[0] {
	case "memories":
		out, _ := json.Marshal(fakeBdRead(statePath))
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
	// "null" JSON (a nil seed marshalled) unmarshals into a nil map, which
	// would panic on the first remember write — normalize to empty.
	if state == nil {
		state = map[string]string{}
	}
	return state
}

// fakeBdSetup redirects the knowledge flow's bd binary at this test binary
// re-executed in fake-bd mode via a tiny wrapper script (the extra
// "-test.run" keeps the subprocess from running the whole suite; the payload
// cat keeps the binary's own banner out of the captured stdout).
func fakeBdSetup(t *testing.T) {
	t.Helper()
	script := filepath.Join(t.TempDir(), "fake-bd")
	body := `#!/bin/sh
dir=""
prev=""
for a in "$@"; do
  if [ "$prev" = "-C" ]; then dir="$a"; fi
  prev="$a"
done
"${GO_FAKE_BD_TARGET}" -test.run=^TestFakeBd$ -- fakebd "$@" >/dev/null 2>"$dir/subprocess.stderr"
status=$?
if [ "$status" -eq 0 ] && [ -n "$dir" ] && [ -f "$dir/stdout.payload" ]; then
  cat "$dir/stdout.payload"
fi
if [ "$status" -ne 0 ] && [ -n "$dir" ] && [ -s "$dir/subprocess.stderr" ]; then
  cat "$dir/subprocess.stderr" >&2
fi
exit $status
`
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatalf("write fake bd wrapper: %v", err)
	}
	t.Setenv("MULTICA_KNOWLEDGE_BD_BIN", script)
	t.Setenv("GO_FAKE_BD_TARGET", os.Args[0])
}

// fakeBdDir creates a directory whose fake bd state is seeded.
func fakeBdDir(t *testing.T, seed map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	fakeBdReplace(t, dir, seed)
	return dir
}

// fakeBdReplace overwrites the whole fake bd state — the test-side way to
// simulate source edits and deletions between scans.
func fakeBdReplace(t *testing.T, dir string, state map[string]string) {
	t.Helper()
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal fake bd state: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "state.json"), data, 0o644); err != nil {
		t.Fatalf("write fake bd state: %v", err)
	}
}

func fakeBdState(t *testing.T, dir string) map[string]string {
	t.Helper()
	return fakeBdRead(filepath.Join(dir, "state.json"))
}

func fakeBdInvocations(t *testing.T, dir string) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "invocations.log"))
	if err != nil {
		return nil
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	for i, line := range lines {
		lines[i] = strings.ReplaceAll(line, "\x1f", " ")
	}
	return lines
}

// --- shared fixtures ---------------------------------------------------------

// registerKnowledgeDir registers one directory and registers cleanup for
// its mirror rows, batches and the directory itself.
func registerKnowledgeDir(t *testing.T, kind, path string) string {
	t.Helper()
	var created struct{ ID, Status string }
	testutil.Call(t, testHandler.PostKnowledgeDir, newRequest(http.MethodPost, "/api/knowledge/dirs",
		map[string]any{"kind": kind, "path": path})).Want(http.StatusCreated).JSON(&created)
	// Cleanup is registered before any assertion so a failed status check
	// cannot leak a registered directory into later tests (the unique-path
	// and single-ultimate gates would 409 on it).
	ids := []string{created.ID}
	t.Cleanup(func() {
		ctx := context.Background()
		testPool.Exec(ctx, `DELETE FROM knowledge_entry WHERE dir_id = ANY($1::uuid[])`, ids)
		testPool.Exec(ctx, `DELETE FROM knowledge_scan_batch WHERE dir_id = ANY($1::uuid[])`, ids)
		testPool.Exec(ctx, `DELETE FROM knowledge_dir WHERE id = ANY($1::uuid[])`, ids)
	})
	if created.Status != "scanned" {
		t.Fatalf("register %s dir: initial scan status=%q, want scanned", kind, created.Status)
	}
	return created.ID
}

func knowledgeEntries(t *testing.T, dirID string) []KnowledgeEntryResponse {
	t.Helper()
	var entries []KnowledgeEntryResponse
	testutil.Call(t, testHandler.GetKnowledgeEntries,
		newRequest(http.MethodGet, "/api/knowledge/entries?dir_id="+dirID, nil)).
		Want(http.StatusOK).JSON(&entries)
	return entries
}

func knowledgeEntryByKey(t *testing.T, dirID, key string) KnowledgeEntryResponse {
	t.Helper()
	for _, entry := range knowledgeEntries(t, dirID) {
		if entry.Key == key {
			return entry
		}
	}
	t.Fatalf("no mirrored entry with key %q in dir %s", key, dirID)
	return KnowledgeEntryResponse{}
}

func scanKnowledgeDir(t *testing.T, dirID string) KnowledgeScanBatchResponse {
	t.Helper()
	var batch KnowledgeScanBatchResponse
	testutil.Call(t, testHandler.ScanKnowledgeDir,
		withURLParam(newRequest(http.MethodPost, "/api/knowledge/dirs/"+dirID+"/scan", nil), "id", dirID)).
		Want(http.StatusOK).JSON(&batch)
	return batch
}

// --- tests -------------------------------------------------------------------

// TestKnowledgeAdoptRequiresUltimateDir pins the precondition: with no
// ultimate directory designated, adoption refuses instead of inventing a
// destination, and the entry is marked failed with that reason.
func TestKnowledgeAdoptRequiresUltimateDir(t *testing.T) {
	if testHandler == nil {
		t.Fatal("database fixture is required")
	}
	fakeBdSetup(t)

	candidate := registerKnowledgeDir(t, "candidate_cli", fakeBdDir(t, map[string]string{"k": "v"}))
	entry := knowledgeEntryByKey(t, candidate, "k")
	testutil.Call(t, testHandler.AdoptKnowledgeEntry,
		withURLParam(newRequest(http.MethodPost, "/api/knowledge/entries/"+entry.ID+"/adopt", nil), "id", entry.ID)).
		Want(http.StatusConflict)
	failed := knowledgeEntryByKey(t, candidate, "k")
	if failed.AdoptionState != "failed" || !strings.Contains(failed.AdoptionError, "ultimate") {
		t.Fatalf("after refused adoption: state=%q error=%q, want failed + ultimate-missing reason", failed.AdoptionState, failed.AdoptionError)
	}
}

// TestKnowledgeMirrorScanAndAdopt walks the §K loop end to end:
// registration mirrors the source on the spot, rescans log added / noop /
// source_deleted batches faithfully, adoption transfers into the ultimate
// bd (verified by reading the ultimate state back), and the scan path never
// issues a write against the source directory.
func TestKnowledgeMirrorScanAndAdopt(t *testing.T) {
	if testHandler == nil {
		t.Fatal("database fixture is required")
	}
	fakeBdSetup(t)

	ultimatePath := fakeBdDir(t, map[string]string{"existing": "untouched ultimate content"})
	ultimateID := registerKnowledgeDir(t, "ultimate", ultimatePath)

	candidatePath := fakeBdDir(t, map[string]string{
		"tip-1": "restart the daemon after editing its env file",
		"tip-2": "read the migration ledger before renumbering",
	})
	candidate := registerKnowledgeDir(t, "candidate_cli", candidatePath)

	// Initial scan mirrored both keys.
	if entries := knowledgeEntries(t, candidate); len(entries) != 2 {
		t.Fatalf("initial mirror: got %d entries, want 2", len(entries))
	}

	// An unchanged rescan is a logged no-op.
	if batch := scanKnowledgeDir(t, candidate); batch.Result != "noop" || batch.Added+batch.Updated+batch.Removed != 0 {
		t.Fatalf("unchanged rescan: %+v, want noop with zero deltas", batch)
	}

	// Source evolves: tip-2 edited, tip-3 added, tip-1 deleted.
	fakeBdReplace(t, candidatePath, map[string]string{
		"tip-2": "always diff the ledger against the disk stems",
		"tip-3": "a candidate memory up for adoption",
	})
	batch := scanKnowledgeDir(t, candidate)
	if batch.Result != "changed" || batch.Added != 1 || batch.Updated != 1 || batch.Removed != 1 {
		t.Fatalf("changed rescan: %+v, want changed 1/1/1", batch)
	}
	deleted := knowledgeEntryByKey(t, candidate, "tip-1")
	if deleted.MirrorState != "source_deleted" {
		t.Fatalf("vanished key tip-1: mirror_state=%q, want source_deleted", deleted.MirrorState)
	}
	edited := knowledgeEntryByKey(t, candidate, "tip-2")
	if edited.Content != "always diff the ledger against the disk stems" {
		t.Fatalf("edited key tip-2: content=%q", edited.Content)
	}

	// Owner adopts tip-3: the transfer must be visible in the ultimate bd.
	entry := knowledgeEntryByKey(t, candidate, "tip-3")
	testutil.Call(t, testHandler.AdoptKnowledgeEntry,
		withURLParam(newRequest(http.MethodPost, "/api/knowledge/entries/"+entry.ID+"/adopt", nil), "id", entry.ID)).
		Want(http.StatusOK)
	if content := fakeBdState(t, ultimatePath)["tip-3"]; content != "a candidate memory up for adoption" {
		t.Fatalf("ultimate state after adoption: tip-3=%q", content)
	}
	adopted := knowledgeEntryByKey(t, candidate, "tip-3")
	if adopted.AdoptionState != "adopted" || adopted.UltimateDirID == nil || *adopted.UltimateDirID != ultimateID {
		t.Fatalf("adopted entry: state=%q ultimate=%v, want adopted + provenance", adopted.AdoptionState, adopted.UltimateDirID)
	}
	if adopted.AdoptedFromDirID == nil || *adopted.AdoptedFromDirID != candidate ||
		adopted.AdoptedFromKey == nil || *adopted.AdoptedFromKey != "tip-3" {
		t.Fatalf("adopted entry provenance: from=%v key=%v", adopted.AdoptedFromDirID, adopted.AdoptedFromKey)
	}
	// The pre-existing ultimate memory is untouched, and re-adoption refuses.
	if content := fakeBdState(t, ultimatePath)["existing"]; content != "untouched ultimate content" {
		t.Fatalf("ultimate existing key rewritten: %q", content)
	}
	testutil.Call(t, testHandler.AdoptKnowledgeEntry,
		withURLParam(newRequest(http.MethodPost, "/api/knowledge/entries/"+entry.ID+"/adopt", nil), "id", entry.ID)).
		Want(http.StatusConflict)

	// Same content under a different key: the ultimate library stays
	// deduplicated — adoption refuses and names the existing key.
	dup := knowledgeEntryByKey(t, candidate, "tip-2") // any not-yet-adopted entry
	dbfx.Exec(t, `UPDATE knowledge_entry SET content = 'untouched ultimate content' WHERE id = $1`, dup.ID)
	testutil.Call(t, testHandler.AdoptKnowledgeEntry,
		withURLParam(newRequest(http.MethodPost, "/api/knowledge/entries/"+dup.ID+"/adopt", nil), "id", dup.ID)).
		Want(http.StatusConflict)
	if err := strings.Contains(knowledgeEntryByKey(t, candidate, "tip-2").AdoptionError, "existing"); !err {
		t.Fatalf("duplicate-content refusal must name the existing key")
	}

	// The scan path is read-only against the source: every memories call
	// carried --readonly, and no remember ever targeted the candidate dir.
	for _, line := range fakeBdInvocations(t, candidatePath) {
		if strings.HasPrefix(line, "memories") && !strings.Contains(line, "--readonly") {
			t.Fatalf("memories call without --readonly against the source: %q", line)
		}
		if strings.HasPrefix(line, "remember") {
			t.Fatalf("write issued against the candidate source: %q", line)
		}
	}

	// Unregistering keeps history: entries flip to source_removed, rows stay.
	testutil.Call(t, testHandler.DeleteKnowledgeDir,
		withURLParam(newRequest(http.MethodDelete, "/api/knowledge/dirs/"+candidate, nil), "id", candidate)).
		Want(http.StatusOK)
	retired := knowledgeEntryByKey(t, candidate, "tip-2")
	if retired.MirrorState != "source_removed" {
		t.Fatalf("after unregister: tip-2 mirror_state=%q, want source_removed", retired.MirrorState)
	}
}

// TestKnowledgeTransferFailureMarksEntryForRetry pins G1's failure half: a
// bd write failure must leave the entry explicitly failed with the reason —
// never silently adopted — and a retry after the source heals succeeds.
func TestKnowledgeTransferFailureMarksEntryForRetry(t *testing.T) {
	if testHandler == nil {
		t.Fatal("database fixture is required")
	}
	fakeBdSetup(t)

	ultimatePath := fakeBdDir(t, nil)
	registerKnowledgeDir(t, "ultimate", ultimatePath)
	candidate := registerKnowledgeDir(t, "candidate_cli", fakeBdDir(t, map[string]string{"flaky": "content worth retrying"}))

	entry := knowledgeEntryByKey(t, candidate, "flaky")
	if err := os.WriteFile(filepath.Join(ultimatePath, "FAIL_REMEMBER"), []byte("1"), 0o644); err != nil {
		t.Fatalf("plant failure flag: %v", err)
	}
	testutil.Call(t, testHandler.AdoptKnowledgeEntry,
		withURLParam(newRequest(http.MethodPost, "/api/knowledge/entries/"+entry.ID+"/adopt", nil), "id", entry.ID)).
		Want(http.StatusConflict)
	failed := knowledgeEntryByKey(t, candidate, "flaky")
	if failed.AdoptionState != "failed" || !strings.Contains(failed.AdoptionError, "transfer failed") {
		t.Fatalf("after failed transfer: state=%q error=%q", failed.AdoptionState, failed.AdoptionError)
	}

	if err := os.Remove(filepath.Join(ultimatePath, "FAIL_REMEMBER")); err != nil {
		t.Fatalf("clear failure flag: %v", err)
	}
	testutil.Call(t, testHandler.AdoptKnowledgeEntry,
		withURLParam(newRequest(http.MethodPost, "/api/knowledge/entries/"+entry.ID+"/adopt", nil), "id", entry.ID)).
		Want(http.StatusOK)
	if content := fakeBdState(t, ultimatePath)["flaky"]; content != "content worth retrying" {
		t.Fatalf("ultimate state after retry: %q", content)
	}
}
