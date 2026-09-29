package daemon

// Knowledge pipeline, daemon side (RUYI-289): everything that touches a bd
// database runs here — where the paths live — while the server stays the only
// state authority. There is no server→daemon RPC, so each cycle is a pull:
// the daemon fetches its work plan (discovery roots, hosted directories with
// mirror SHA maps, the workspace ultimate, queued adoption transfers), runs
// it locally, and reports results back over the same authenticated channel.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/internal/cli"
)

const (
	// DefaultKnowledgeScanInterval paces the pull cycle; consecutive failures
	// back off up to DefaultKnowledgeScanMaxBackoff.
	DefaultKnowledgeScanInterval   = time.Minute
	DefaultKnowledgeScanMaxBackoff = 10 * time.Minute
)

// --- wire types (mirror of the server's daemon-facing contract) -------------

type KnowledgePlan struct {
	Workspaces []KnowledgePlanWorkspace `json:"workspaces"`
}

type KnowledgePlanWorkspace struct {
	WorkspaceID    string                  `json:"workspace_id"`
	HasUltimate    bool                    `json:"has_ultimate"`
	Ultimate       *KnowledgePlanUltimate  `json:"ultimate,omitempty"`
	DiscoveryRoots []string                `json:"discovery_roots"`
	Dirs           []KnowledgePlanDir      `json:"dirs"`
	Adoptions      []KnowledgePlanAdoption `json:"adoptions"`
}

type KnowledgePlanUltimate struct {
	DirID string `json:"dir_id"`
	Path  string `json:"path"`
}

type KnowledgePlanDir struct {
	DirID         string `json:"dir_id"`
	Kind          string `json:"kind"`
	Path          string `json:"path"`
	Bound         bool   `json:"bound"`
	ScanRequested bool   `json:"scan_requested"`
	// ScanRequired is true when the server has never recorded a scan for the
	// directory or an explicit refresh was requested — the daemon must report
	// even a byte-identical source so the first scan lands.
	ScanRequired bool              `json:"scan_required"`
	Mirror       map[string]string `json:"mirror"`
}

type KnowledgePlanAdoption struct {
	Kind          string `json:"kind"` // entry | proposal
	ID            string `json:"id"`
	Key           string `json:"key"`
	Content       string `json:"content"`
	Actor         string `json:"actor"`
	UltimateDirID string `json:"ultimate_dir_id"`
	UltimatePath  string `json:"ultimate_path"`
}

type KnowledgeResults struct {
	Discoveries []KnowledgeDiscoveryReport `json:"discoveries,omitempty"`
	Ultimates   []KnowledgeUltimateReport  `json:"ultimates,omitempty"`
	Scans       []KnowledgeScanReport      `json:"scans,omitempty"`
	Adoptions   []KnowledgeAdoptReport     `json:"adoptions,omitempty"`
}

type KnowledgeResultsAck struct {
	DiscoveriesRegistered int `json:"discoveries_registered"`
	UltimatesRegistered   int `json:"ultimates_registered"`
	UltimateConflicts     int `json:"ultimate_conflicts"`
	ScansApplied          int `json:"scans_applied"`
	AdoptionsResolved     int `json:"adoptions_resolved"`
}

type KnowledgeDiscoveryReport struct {
	WorkspaceID string `json:"workspace_id"`
	Path        string `json:"path"`
	ProjectID   string `json:"project_id,omitempty"`
	Label       string `json:"label"`
}

type KnowledgeUltimateReport struct {
	WorkspaceID string `json:"workspace_id"`
	Path        string `json:"path"`
}

type KnowledgeScanReport struct {
	DirID string `json:"dir_id"`
	// scheduled | manual | initial
	TriggerSource string `json:"trigger_source"`
	OK            bool   `json:"ok"`
	Error         string `json:"error,omitempty"`
	// Unchanged reports a byte-identical source without resending memories;
	// the server logs a zero-change batch and clears a pending refresh.
	Unchanged bool              `json:"unchanged,omitempty"`
	Memories  map[string]string `json:"memories,omitempty"`
}

type KnowledgeAdoptReport struct {
	Kind string `json:"kind"` // entry | proposal
	ID   string `json:"id"`
	OK   bool   `json:"ok"`
	// UltimateDirID is the directory the transfer actually landed in.
	UltimateDirID string `json:"ultimate_dir_id,omitempty"`
	Error         string `json:"error,omitempty"`
}

// --- client -----------------------------------------------------------------

func (c *Client) GetKnowledgePlan(ctx context.Context, daemonID string) (*KnowledgePlan, error) {
	var plan KnowledgePlan
	path := "/api/daemon/knowledge/plan?daemon_id=" + url.QueryEscape(daemonID)
	if err := c.getJSON(ctx, path, &plan); err != nil {
		return nil, err
	}
	return &plan, nil
}

func (c *Client) PostKnowledgeResults(ctx context.Context, daemonID string, results *KnowledgeResults) (*KnowledgeResultsAck, error) {
	var ack KnowledgeResultsAck
	path := "/api/daemon/knowledge/results?daemon_id=" + url.QueryEscape(daemonID)
	if err := c.postJSON(ctx, path, results, &ack); err != nil {
		return nil, err
	}
	return &ack, nil
}

// --- cycle ------------------------------------------------------------------

func (d *Daemon) knowledgeLoop(ctx context.Context) {
	timer := time.NewTimer(jitterDuration(knowledgeScanInterval()))
	defer timer.Stop()

	var consecutiveFailures int
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			if err := d.knowledgeSync(ctx); err != nil {
				consecutiveFailures++
				d.logger.Warn("knowledge sync failed", "error", err)
			} else {
				consecutiveFailures = 0
			}
			interval := knowledgeScanInterval()
			for i := 0; i < consecutiveFailures && interval < DefaultKnowledgeScanMaxBackoff/2; i++ {
				interval *= 2
			}
			if interval > DefaultKnowledgeScanMaxBackoff {
				interval = DefaultKnowledgeScanMaxBackoff
			}
			timer.Reset(jitterDuration(interval))
		}
	}
}

func knowledgeScanInterval() time.Duration {
	if raw := strings.TrimSpace(os.Getenv("MULTICA_KNOWLEDGE_SCAN_INTERVAL")); raw != "" {
		if parsed, err := time.ParseDuration(raw); err == nil && parsed > 0 {
			return parsed
		}
	}
	return DefaultKnowledgeScanInterval
}

// knowledgeSync runs one pull cycle. Workspaces are isolated: a failure in
// one never stops the others, and the report is a single POST covering all
// of them.
func (d *Daemon) knowledgeSync(ctx context.Context) error {
	plan, err := d.client.GetKnowledgePlan(ctx, d.cfg.DaemonID)
	if err != nil {
		return fmt.Errorf("fetch knowledge plan: %w", err)
	}
	results := &KnowledgeResults{}
	for i := range plan.Workspaces {
		d.knowledgeSyncWorkspace(ctx, &plan.Workspaces[i], results)
	}
	if len(results.Discoveries) == 0 && len(results.Ultimates) == 0 &&
		len(results.Scans) == 0 && len(results.Adoptions) == 0 {
		return nil
	}
	if _, err := d.client.PostKnowledgeResults(ctx, d.cfg.DaemonID, results); err != nil {
		return fmt.Errorf("report knowledge results: %w", err)
	}
	return nil
}

func (d *Daemon) knowledgeSyncWorkspace(ctx context.Context, ws *KnowledgePlanWorkspace, results *KnowledgeResults) {
	// Ultimate auto-designation (RUYI-289 §2): a workspace with no active
	// ultimate gets one on a daemon-managed stable path; the server's
	// uniqueness index settles multi-daemon races, and losing is fine.
	if !ws.HasUltimate {
		if path, err := ensureUltimateDir(ws.WorkspaceID); err != nil {
			d.logger.Warn("ultimate designation skipped", "workspace", ws.WorkspaceID, "error", err)
		} else {
			results.Ultimates = append(results.Ultimates, KnowledgeUltimateReport{WorkspaceID: ws.WorkspaceID, Path: path})
		}
	}

	registered := make(map[string]bool, len(ws.Dirs))
	for _, dir := range ws.Dirs {
		registered[dir.Path] = true
	}
	// Discovery (RUYI-289 §1): a project directory pinned to this daemon that
	// carries a bd database and is not registered yet becomes a candidate.
	for _, root := range ws.DiscoveryRoots {
		if registered[root] || !hasBeadsDatabase(root) {
			continue
		}
		results.Discoveries = append(results.Discoveries, KnowledgeDiscoveryReport{
			WorkspaceID: ws.WorkspaceID,
			Path:        root,
			Label:       filepath.Base(root),
		})
	}

	for _, dir := range ws.Dirs {
		if report := d.scanKnowledgeDir(ctx, dir); report != nil {
			results.Scans = append(results.Scans, *report)
		}
	}

	// Adoption transfers (RUYI-289 §3): the server queued an Owner decision;
	// the write happens here, the read-back proves it, and only then does the
	// server record 'adopted'.
	for _, job := range ws.Adoptions {
		if report, err := d.runKnowledgeAdoption(ctx, job); err != nil {
			d.logger.Warn("knowledge adoption failed", "kind", job.Kind, "id", job.ID, "error", err)
			results.Adoptions = append(results.Adoptions, KnowledgeAdoptReport{
				Kind: job.Kind, ID: job.ID, UltimateDirID: job.UltimateDirID, Error: err.Error(),
			})
		} else {
			results.Adoptions = append(results.Adoptions, report)
		}
	}
}

// scanKnowledgeDir reads one source directory and decides what to report.
// Unclaimed directories (daemon_id '') are only scanned when the path is
// actually usable here; failures on them stay silent because another daemon
// may host the path — the first successful scan claims the directory.
func (d *Daemon) scanKnowledgeDir(ctx context.Context, dir KnowledgePlanDir) *KnowledgeScanReport {
	report := &KnowledgeScanReport{DirID: dir.DirID}
	switch {
	case dir.ScanRequested:
		report.TriggerSource = "manual"
	case dir.ScanRequired:
		report.TriggerSource = "initial"
	default:
		report.TriggerSource = "scheduled"
	}
	if _, err := os.Stat(dir.Path); err != nil {
		if !dir.Bound {
			return nil
		}
		report.Error = "path is not accessible on this daemon"
		return report
	}
	if !hasBeadsDatabase(dir.Path) {
		if !dir.Bound {
			return nil
		}
		report.Error = "no bd database found at the registered path"
		return report
	}
	memories, err := bdListMemories(ctx, dir.Path)
	if err != nil {
		if !dir.Bound {
			return nil
		}
		report.Error = err.Error()
		return report
	}
	// An unchanged source is reported only when the server must hear back
	// (first scan, explicit refresh); otherwise the mirror SHA map from the
	// plan already proves freshness and nothing is sent.
	if dir.ScanRequired || dir.ScanRequested {
		report.OK = true
		report.Memories = memories
		if !knowledgeMirrorChanged(dir.Mirror, memories) {
			report.Unchanged = true
			report.Memories = nil
		}
		return report
	}
	if !knowledgeMirrorChanged(dir.Mirror, memories) {
		return nil
	}
	report.OK = true
	report.Memories = memories
	return report
}

func knowledgeMirrorChanged(mirror map[string]string, memories map[string]string) bool {
	if len(mirror) != len(memories) {
		return true
	}
	for key, content := range memories {
		if mirror[key] != knowledgeContentSHA(content) {
			return true
		}
	}
	return false
}

func knowledgeContentSHA(content string) string {
	digest := sha256.Sum256([]byte(content))
	return hex.EncodeToString(digest[:])
}

// runKnowledgeAdoption executes one queued transfer: remember into the
// workspace's ultimate directory, then read the value back through bd recall
// — the read-back is what makes the server's later 'adopted' trustworthy.
func (d *Daemon) runKnowledgeAdoption(ctx context.Context, job KnowledgePlanAdoption) (KnowledgeAdoptReport, error) {
	report := KnowledgeAdoptReport{Kind: job.Kind, ID: job.ID, UltimateDirID: job.UltimateDirID}
	if err := ensureBdReady(job.UltimatePath); err != nil {
		return report, fmt.Errorf("prepare ultimate directory: %w", err)
	}
	// The ultimate library stays deduplicated: identical content under a new
	// key is refused with a pointer to the existing key, mirroring the
	// pre-daemon adoption behavior (RUYI-265).
	existing, err := bdListMemories(ctx, job.UltimatePath)
	if err != nil {
		return report, fmt.Errorf("inspect ultimate directory: %w", err)
	}
	for existingKey, existingContent := range existing {
		if strings.TrimSpace(existingContent) == strings.TrimSpace(job.Content) {
			return report, fmt.Errorf("identical content already exists in the ultimate library under key %q", existingKey)
		}
	}
	if err := bdRemember(ctx, job.UltimatePath, job.Key, job.Content, job.Actor); err != nil {
		return report, fmt.Errorf("bd remember: %w", err)
	}
	recalled, err := bdRecall(ctx, job.UltimatePath, job.Key)
	if err != nil {
		return report, fmt.Errorf("bd recall read-back: %w", err)
	}
	if strings.TrimSpace(recalled) != strings.TrimSpace(job.Content) {
		return report, fmt.Errorf("bd recall read-back mismatch for key %s", job.Key)
	}
	report.OK = true
	return report, nil
}

// --- ultimate placement -----------------------------------------------------

// knowledgeStateRoot hosts daemon-managed knowledge state. The env override
// keeps tests (and exotic deployments) away from the operator's profile dir.
func knowledgeStateRoot() (string, error) {
	if root := strings.TrimSpace(os.Getenv("MULTICA_KNOWLEDGE_STATE_DIR")); root != "" {
		return root, nil
	}
	return cli.ProfileDir("")
}

// ultimateDirPath is the daemon-managed stable path for a workspace's
// ultimate knowledge directory: deterministic per workspace ID, so daemon
// restarts and competing daemons converge on the same location.
func ultimateDirPath(workspaceID string) (string, error) {
	root, err := knowledgeStateRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "daemon-knowledge", "ultimate", workspaceID), nil
}

func ensureUltimateDir(workspaceID string) (string, error) {
	path, err := ultimateDirPath(workspaceID)
	if err != nil {
		return "", err
	}
	if err := ensureBdReady(path); err != nil {
		return "", err
	}
	return path, nil
}

// ensureBdReady makes dir an initialized bd project: create it, then run
// `bd init` when no .beads database exists yet.
func ensureBdReady(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	if hasBeadsDatabase(dir) {
		return nil
	}
	return bdInit(context.Background(), dir)
}

// --- bd IO ------------------------------------------------------------------

// knowledgeBdBin resolves the bd executable; tests redirect it at a fake.
func knowledgeBdBin() string {
	if bin := strings.TrimSpace(os.Getenv("MULTICA_KNOWLEDGE_BD_BIN")); bin != "" {
		return bin
	}
	return "bd"
}

// bdInit initializes a bd project. The bd CLI resolves the target from the
// process working directory for init (unlike every other subcommand), so the
// command must run with cmd.Dir set rather than passing -C.
func bdInit(ctx context.Context, dir string) error {
	cmd := exec.CommandContext(ctx, knowledgeBdBin(), "init")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("bd init in %s: %w: %s", dir, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// bdListMemories reads a source's memories read-only. bd envelopes a numeric
// schema_version into the same flat JSON object as the memories, so the
// decode tolerates the envelope and never mirrors metadata as an entry.
func bdListMemories(ctx context.Context, dir string) (map[string]string, error) {
	cmd := exec.CommandContext(ctx, knowledgeBdBin(), "memories", "--json", "--readonly", "-C", dir)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("bd memories in %s: %w", dir, err)
	}
	memories := map[string]string{}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(out, &envelope); err != nil {
		return memories, nil
	}
	for key, raw := range envelope {
		if key == "" || key == "schema_version" {
			continue
		}
		var content string
		if json.Unmarshal(raw, &content) == nil {
			memories[key] = content
		}
	}
	return memories, nil
}

func bdRemember(ctx context.Context, dir, key, content, actor string) error {
	cmd := exec.CommandContext(ctx, knowledgeBdBin(), "remember", content, "--key", key, "--actor", actor, "-C", dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("bd remember %s: %w: %s", key, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func bdRecall(ctx context.Context, dir, key string) (string, error) {
	cmd := exec.CommandContext(ctx, knowledgeBdBin(), "recall", key, "-C", dir)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("bd recall %s: %w", key, err)
	}
	return strings.TrimSpace(string(out)), nil
}

func hasBeadsDatabase(path string) bool {
	info, err := os.Stat(filepath.Join(path, ".beads"))
	return err == nil && info.IsDir()
}
