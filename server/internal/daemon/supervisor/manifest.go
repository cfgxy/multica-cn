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
)

// Manifest is the run's persisted identity. Written by the daemon before
// launch, updated by the launcher (PID, exit) and by the supervisor (evidence
// on supervisor-initiated stops). Always replaced atomically — readers never
// see a partial file, and a daemon crash mid-write leaves the previous state.
type Manifest struct {
	Version       int         `json:"version"`
	RunID         string      `json:"run_id"`
	TaskID        string      `json:"task_id"`
	Runtime       string      `json:"runtime"`
	Unit          string      `json:"unit"`
	State         string      `json:"state"`
	WorkerPID     int         `json:"worker_pid"`
	LauncherPID   int         `json:"launcher_pid"`
	ControlSocket string      `json:"control_socket"`
	StdoutLog     string      `json:"stdout_log"`
	StderrLog     string      `json:"stderr_log"`
	StartedAt     time.Time   `json:"started_at"`
	Exit          *ExitRecord `json:"exit,omitempty"`
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

// readState is the daemon's per-run read cursor for the resumable tail:
// how much of each log has already been consumed, plus a ring of recent line
// hashes so a crash between delivering a line to the consumer and persisting
// the offset does not re-deliver it after restart (line-level dedup).
type readState struct {
	StdoutOffset int64    `json:"stdout_offset"`
	StderrOffset int64    `json:"stderr_offset"`
	StdoutHashes []uint64 `json:"stdout_hashes,omitempty"`
	StderrHashes []uint64 `json:"stderr_hashes,omitempty"`
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

// readStatePath is the daemon-owned read cursor file.
func (m *Manager) readStatePath(runID string) string {
	return filepath.Join(m.Dir(runID), "read.json")
}

func (m *Manager) LoadReadState(runID string) (readState, error) {
	var rs readState
	err := readJSONFile(m.readStatePath(runID), &rs)
	if errors.Is(err, os.ErrNotExist) {
		return readState{}, nil
	}
	return rs, err
}

func (m *Manager) StoreReadState(runID string, rs readState) error {
	return writeJSONFile(m.readStatePath(runID), &rs, 0o600)
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
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

func readJSONFile(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}
