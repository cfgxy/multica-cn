// Package supervisor gives Multica task workers a lifecycle that is
// independent of the daemon process (RUYI-349 Phase 1).
//
// The daemon launches each task worker inside a systemd transient user unit
// (multica-run-<run_id>.service) whose payload is a resident launcher — this
// daemon binary re-executed with a spec file. The launcher is the worker's
// parent: it appends the worker's stdout/stderr to per-run log files, bridges
// the worker's stdin from a per-run Unix domain socket, and records the
// proven exit in the run manifest. The daemon consumes the log files through
// a resumable tailer and speaks to the launcher over the socket; both survive
// daemon death, so a restarted daemon reattaches to a running worker and
// converges an exited one from recorded evidence instead of guessing.
//
// Every component here treats its own death as routine: manifests are written
// atomically, offsets are persisted as data is drained, and reconciliation
// (reconcile.go) is a pure function over manifest ∩ live units ∩ liveness
// probes so daemon startup decisions are auditable and testable.
package supervisor

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

// unitPrefix is the strict namespace every managed unit, socket and file
// carries. The supervisor refuses to touch anything outside it.
const unitPrefix = "multica-run-"

// runIDPattern constrains run IDs to a UUID-ish shape. They end up in unit
// names, socket paths and directory names; the pattern is what makes
// systemd-run and UDS arguments injection-proof.
var runIDPattern = regexp.MustCompile(`^[0-9a-f][0-9a-f-]{7,63}$`)

// Manifest states.
const (
	StateRunning = "running"
	StateExited  = "exited"
)

// ExitRecord sources.
const (
	ExitSourceLauncher   = "launcher"   // launcher observed the worker exit itself
	ExitSourceSupervisor = "supervisor" // supervisor stopped the unit; cgroup teardown is the evidence
	// ExitSourceLost marks a synthesized exit for a worker that is provably
	// gone with nobody having recorded its exit (RUYI-592 fix 2): the unit
	// disappeared, no exit record ever landed, and nothing holds the run's
	// lock. It is convergence evidence, not an observed death — exactly the
	// Decide matrix's lost corner, persisted so downstream readers converge
	// on one record instead of re-deriving the loss.
	ExitSourceLost = "lost"
)

// Manifest is the run's persisted identity. Written by the daemon before
// launch, updated by the launcher (PID, exit) and by the supervisor (evidence
// on supervisor-initiated stops). Always replaced atomically — readers never
// see a partial file, and a daemon crash mid-write leaves the previous state.
type Manifest struct {
	Version int    `json:"version"`
	RunID   string `json:"run_id"`
	TaskID  string `json:"task_id"`
	Runtime string `json:"runtime"`
	// Owner is the RUYI-607 identity of the daemon that launched this run
	// (OwnerIdentity of its profile). Empty on pre-RUYI-607 manifests:
	// ownership is then unproven, and reconcile never takes a destructive
	// action on an unproven owner.
	Owner         string      `json:"owner,omitempty"`
	Unit          string      `json:"unit"`
	State         string      `json:"state"`
	WorkerPID     int         `json:"worker_pid"`
	LauncherPID   int         `json:"launcher_pid"`
	ControlSocket string      `json:"control_socket"`
	StdoutLog     string      `json:"stdout_log"`
	StderrLog     string      `json:"stderr_log"`
	StartedAt     time.Time   `json:"started_at"`
	Exit          *ExitRecord `json:"exit,omitempty"`
	// DaemonID is the persistent identity of the daemon that launched the
	// run (RUYI-592 fix 1). Reconciliation's one irreversible action — the
	// stop_orphan kill — requires the manifest to attribute the unit to the
	// reconciling daemon: a second daemon on the same host sees foreign
	// workers only through its in-flight blind spot, and that blind spot
	// must never be the evidence a kill rides on. Written once at Launch;
	// manifests from before the field existed carry no owner, which an
	// identified daemon treats as foreign (quarantine, never kill).
	DaemonID string `json:"daemon_id,omitempty"`
	// ConvergedAt is set by the daemon task layer once a finished run's
	// output and exit have been reported to the server and folded into the
	// task's final state (RUYI-464). Reconciliation skips converged runs;
	// their cleanup is retention GC.
	ConvergedAt *time.Time `json:"converged_at,omitempty"`
}

// ExitRecord is proven terminal evidence. Source says who saw it.
type ExitRecord struct {
	Code   int       `json:"code"` // -1 when signalled
	Signal string    `json:"signal,omitempty"`
	At     time.Time `json:"at"`
	Source string    `json:"source"`
}

// launchSpec is the launcher's private brief: everything needed to spawn the
// worker. Stored 0600 next to the manifest — the environment may carry
// credentials, exactly like the daemon process env it came from.
type launchSpec struct {
	Version   int      `json:"version"`
	RunID     string   `json:"run_id"`
	Path      string   `json:"path"`
	Args      []string `json:"args"`
	Env       []string `json:"env"`
	Dir       string   `json:"dir"`
	StdoutLog string   `json:"stdout_log"`
	StderrLog string   `json:"stderr_log"`
	Socket    string   `json:"socket"`
	Manifest  string   `json:"manifest"`
}

// streamReadState is the daemon's per-stream read cursor for the resumable
// tail: how much of the stream's log has already been consumed, plus a ring
// of recent line hashes so a crash between delivering a line to the consumer
// and persisting the offset does not re-deliver it after restart (line-level
// dedup).
//
// One cursor file per stream (RUYI-529): the two tail pumps run
// concurrently, and a single shared file turned every persist into a
// load-merge-store over the sibling stream's fields — concurrent persists
// lost updates, and the shared fixed-name temp file tore under interleaved
// writes, so a corrupt file could kill a pump on its next load. Separate
// files give each pump a single-writer file; the race is gone by
// construction.
type streamReadState struct {
	Offset int64    `json:"offset"`
	Hashes []uint64 `json:"hashes,omitempty"`
}

// maxDedupRing bounds the hash ring. Lines older than this that were delivered
// but not offset-persisted are re-delivered on restart — the window is one
// tailer poll wide, so 4096 lines is orders of magnitude past what a crash
// can straddle.
const maxDedupRing = 4096

// Manager owns the on-disk run registry.
type Manager struct {
	root string
}

// NewManager points a Manager at root (created on demand).
func NewManager(root string) (*Manager, error) {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("supervisor: create runs root: %w", err)
	}
	return &Manager{root: root}, nil
}

// Root is the runs root directory.
func (m *Manager) Root() string { return m.root }

// ValidateRunID enforces the identity grammar every unit/socket/path is built
// from.
func ValidateRunID(runID string) error {
	if !runIDPattern.MatchString(runID) {
		return fmt.Errorf("supervisor: invalid run id %q", runID)
	}
	return nil
}

// UnitName is the transient unit for a run.
func UnitName(runID string) string { return unitPrefix + runID + ".service" }

// Dir is the run's directory.
func (m *Manager) Dir(runID string) string { return filepath.Join(m.root, runID) }

// ListRunIDs enumerates run directories on disk.
func (m *Manager) ListRunIDs() ([]string, error) {
	entries, err := os.ReadDir(m.root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var ids []string
	for _, e := range entries {
		if e.IsDir() && runIDPattern.MatchString(e.Name()) {
			ids = append(ids, e.Name())
		}
	}
	return ids, nil
}

// WriteManifest atomically replaces the run's manifest.
func (m *Manager) WriteManifest(man *Manifest) error {
	if err := ValidateRunID(man.RunID); err != nil {
		return err
	}
	return writeJSONFile(filepath.Join(m.Dir(man.RunID), "manifest.json"), man, 0o600)
}

// ReadManifest loads the run's manifest.
func (m *Manager) ReadManifest(runID string) (*Manifest, error) {
	if err := ValidateRunID(runID); err != nil {
		return nil, err
	}
	var man Manifest
	if err := readJSONFile(filepath.Join(m.Dir(runID), "manifest.json"), &man); err != nil {
		return nil, err
	}
	return &man, nil
}

// WriteSpec stores the launcher brief 0600. The path is handed to the launcher
// through its environment, never argv: /proc/PID/cmdline is world-readable,
// /proc/PID/environ is not.
func (m *Manager) WriteSpec(runID string, spec *launchSpec) (string, error) {
	if err := ValidateRunID(runID); err != nil {
		return "", err
	}
	path := filepath.Join(m.Dir(runID), "spec.json")
	if err := writeJSONFile(path, spec, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// ReadSpec loads the launcher brief.
func ReadSpec(path string) (*launchSpec, error) {
	var spec launchSpec
	if err := readJSONFile(path, &spec); err != nil {
		return nil, err
	}
	return &spec, nil
}

// stream names double as filename fragments, so they are validated as
// strictly as run IDs.
func validateStream(stream string) error {
	if stream != "stdout" && stream != "stderr" {
		return fmt.Errorf("supervisor: invalid stream %q", stream)
	}
	return nil
}

// readStatePath is the daemon-owned per-stream read cursor file.
func (m *Manager) readStatePath(runID, stream string) string {
	return filepath.Join(m.Dir(runID), "read-"+stream+".json")
}

// legacyReadStateName was the pre-RUYI-529 combined cursor file. It is only
// ever read, to seed a stream's cursor on first load after the upgrade.
const legacyReadStateName = "read.json"

// LoadStreamReadState loads the stream's persisted cursor. A missing file is
// a zero cursor; runs last cursored by the legacy combined file keep
// resuming from it, and the per-stream file becomes authoritative once this
// stream first persists. A corrupt legacy file — only possible from the old
// code's concurrent writes — degrades to a zero cursor instead of killing
// the pump.
func (m *Manager) LoadStreamReadState(runID, stream string) (streamReadState, error) {
	if err := ValidateRunID(runID); err != nil {
		return streamReadState{}, err
	}
	if err := validateStream(stream); err != nil {
		return streamReadState{}, err
	}
	var rs streamReadState
	err := readJSONFile(m.readStatePath(runID, stream), &rs)
	if err == nil {
		return rs, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return rs, err
	}
	var legacy struct {
		StdoutOffset int64    `json:"stdout_offset"`
		StdoutHashes []uint64 `json:"stdout_hashes"`
		StderrOffset int64    `json:"stderr_offset"`
		StderrHashes []uint64 `json:"stderr_hashes"`
	}
	if err := readJSONFile(filepath.Join(m.Dir(runID), legacyReadStateName), &legacy); err != nil {
		return streamReadState{}, nil
	}
	if stream == "stdout" {
		return streamReadState{Offset: legacy.StdoutOffset, Hashes: legacy.StdoutHashes}, nil
	}
	return streamReadState{Offset: legacy.StderrOffset, Hashes: legacy.StderrHashes}, nil
}

// StoreStreamReadState persists the stream's cursor atomically. Each stream
// owns its file exclusively — the two tail pumps never write the same path.
func (m *Manager) StoreStreamReadState(runID, stream string, rs streamReadState) error {
	if err := ValidateRunID(runID); err != nil {
		return err
	}
	if err := validateStream(stream); err != nil {
		return err
	}
	return writeJSONFile(m.readStatePath(runID, stream), &rs, 0o600)
}

// QuarantineRecord marks a run the reconcile matrix could not classify — the
// audit trail that proves unknown units are recorded, never silently killed.
type QuarantineRecord struct {
	RunID     string    `json:"run_id"`
	Unit      string    `json:"unit"`
	Reason    string    `json:"reason"`
	FirstSeen time.Time `json:"first_seen"`
}

func (m *Manager) WriteQuarantine(rec *QuarantineRecord) error {
	if err := ValidateRunID(rec.RunID); err != nil {
		return err
	}
	path := filepath.Join(m.Dir(rec.RunID), "quarantine.json")
	existing := &QuarantineRecord{}
	if readJSONFile(path, existing) == nil && existing.FirstSeen.After(time.Time{}) {
		rec.FirstSeen = existing.FirstSeen // keep the original first-seen
	}
	return writeJSONFile(path, rec, 0o600)
}

func (m *Manager) ReadQuarantine(runID string) (*QuarantineRecord, error) {
	rec := &QuarantineRecord{}
	if err := readJSONFile(filepath.Join(m.Dir(runID), "quarantine.json"), rec); err != nil {
		return nil, err
	}
	return rec, nil
}

// MarkConverged stamps the run as consumed by the daemon task layer: the
// run's proven exit and drained output have been reported to the server as
// the task's terminal state (RUYI-464). Only callable after the worker is
// terminal — the launcher (the other manifest writer) is gone by then.
func (m *Manager) MarkConverged(runID string) error {
	man, err := m.ReadManifest(runID)
	if err != nil {
		return err
	}
	if man.ConvergedAt != nil {
		return nil
	}
	now := time.Now().UTC()
	man.ConvergedAt = &now
	return m.WriteManifest(man)
}

// hashLine folds a log line into a 64-bit fingerprint for the dedup ring.
func hashLine(line []byte) uint64 {
	sum := sha256.Sum256(line)
	return binary.BigEndian.Uint64(sum[:8])
}

func writeJSONFile(path string, v any, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	// Unique temp name: two writers targeting the same file (RUYI-529) must
	// never share one — interleaved truncation could rename a torn file into
	// place.
	f, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := f.Name()
	defer os.Remove(tmpPath) // no-op once the rename below has succeeded
	if err := f.Chmod(mode); err != nil {
		f.Close()
		return err
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func readJSONFile(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}
