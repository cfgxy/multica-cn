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
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
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
	WorkspaceID     string                   `json:"workspace_id"`
	HasUltimate     bool                     `json:"has_ultimate"`
	Ultimate        *KnowledgePlanUltimate   `json:"ultimate,omitempty"`
	UnboundUltimate *KnowledgePlanUltimate   `json:"unbound_ultimate,omitempty"`
	DiscoveryRoots  []string                 `json:"discovery_roots"`
	Dirs            []KnowledgePlanDir       `json:"dirs"`
	Adoptions       []KnowledgePlanAdoption  `json:"adoptions"`
	Operations      []KnowledgePlanOperation `json:"operations"`
	Writebacks      []KnowledgePlanWriteback `json:"writebacks"`
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
	ScanRequired  bool              `json:"scan_required"`
	Mirror        map[string]string `json:"mirror"`
	SourceDeleted map[string]bool   `json:"source_deleted"`
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

type KnowledgePlanOperation struct {
	ID            string   `json:"id"`
	OperationType string   `json:"operation_type"`
	Key           string   `json:"key"`
	Content       string   `json:"content"`
	DeleteKeys    []string `json:"delete_keys"`
	Actor         string   `json:"actor"`
	UltimateDirID string   `json:"ultimate_dir_id"`
	UltimatePath  string   `json:"ultimate_path"`
}

type KnowledgePlanWriteback struct {
	EventID               string `json:"event_id"`
	EntryID               string `json:"entry_id"`
	ClaimToken            string `json:"claim_token"`
	Operation             string `json:"operation"`
	Path                  string `json:"path"`
	Key                   string `json:"key"`
	Content               string `json:"content"`
	ContentSha256         string `json:"content_sha256"`
	ExpectedContentSha256 string `json:"expected_content_sha256,omitempty"`
	Actor                 string `json:"actor"`
}

type KnowledgeResults struct {
	Discoveries []KnowledgeDiscoveryReport `json:"discoveries,omitempty"`
	Ultimates   []KnowledgeUltimateReport  `json:"ultimates,omitempty"`
	Scans       []KnowledgeScanReport      `json:"scans,omitempty"`
	Adoptions   []KnowledgeAdoptReport     `json:"adoptions,omitempty"`
	Operations  []KnowledgeOperationReport `json:"operations,omitempty"`
	Writebacks  []KnowledgeWritebackReport `json:"writebacks,omitempty"`
}

type KnowledgeOperationReport struct {
	ID    string `json:"id"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

type KnowledgeResultsAck struct {
	DiscoveriesRegistered int `json:"discoveries_registered"`
	UltimatesRegistered   int `json:"ultimates_registered"`
	UltimateConflicts     int `json:"ultimate_conflicts"`
	ScansApplied          int `json:"scans_applied"`
	AdoptionsResolved     int `json:"adoptions_resolved"`
	OperationsResolved    int `json:"operations_resolved"`
	WritebacksResolved    int `json:"writebacks_resolved"`
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

type KnowledgeWritebackReport struct {
	EventID    string `json:"event_id"`
	EntryID    string `json:"entry_id"`
	ClaimToken string `json:"claim_token"`
	OK         bool   `json:"ok"`
	Error      string `json:"error,omitempty"`
}

// --- client -----------------------------------------------------------------

func (c *Client) GetKnowledgePlan(ctx context.Context, daemonID, workspaceID string) (*KnowledgePlan, error) {
	var plan KnowledgePlan
	token := c.knowledgeToken(workspaceID)
	if token == "" {
		return nil, fmt.Errorf("knowledge daemon credential is unavailable for workspace %s", workspaceID)
	}
	path := "/api/daemon/knowledge/plan?daemon_id=" + url.QueryEscape(daemonID)
	if err := c.getJSONWithToken(ctx, path, token, &plan); err != nil {
		return nil, err
	}
	return &plan, nil
}

func (c *Client) PostKnowledgeResults(ctx context.Context, daemonID, workspaceID string, results *KnowledgeResults) (*KnowledgeResultsAck, error) {
	var ack KnowledgeResultsAck
	path := "/api/daemon/knowledge/results?daemon_id=" + url.QueryEscape(daemonID)
	token := c.knowledgeToken(workspaceID)
	if token == "" {
		return nil, fmt.Errorf("knowledge callback credential is unavailable for workspace %s", workspaceID)
	}
	if err := c.postJSONWithToken(ctx, path, token, results, &ack); err != nil {
		if !isUnauthorizedError(err) {
			return nil, err
		}
		if err := c.RefreshKnowledgeToken(ctx, workspaceID); err != nil {
			return nil, fmt.Errorf("refresh knowledge callback credential: %w", err)
		}
		if err := c.postJSONWithToken(ctx, path, c.knowledgeToken(workspaceID), results, &ack); err != nil {
			return nil, err
		}
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

// knowledgeSync runs one pull cycle. Workspaces are isolated: each plan and
// result request uses the daemon token issued for that workspace, so a token
// can never claim or report work for a different workspace.
func (d *Daemon) knowledgeSync(ctx context.Context) error {
	d.mu.Lock()
	workspaceIDs := make([]string, 0, len(d.workspaces))
	for workspaceID := range d.workspaces {
		workspaceIDs = append(workspaceIDs, workspaceID)
	}
	d.mu.Unlock()
	sort.Strings(workspaceIDs)

	var cycleErrs []error
	for _, workspaceID := range workspaceIDs {
		if err := d.client.RefreshKnowledgeTokenIfNeeded(ctx, workspaceID); err != nil {
			cycleErrs = append(cycleErrs, fmt.Errorf("refresh knowledge credential for workspace %s: %w", workspaceID, err))
			continue
		}
		plan, err := d.client.GetKnowledgePlan(ctx, d.cfg.DaemonID, workspaceID)
		if err != nil {
			cycleErrs = append(cycleErrs, fmt.Errorf("fetch knowledge plan for workspace %s: %w", workspaceID, err))
			continue
		}
		for i := range plan.Workspaces {
			ws := &plan.Workspaces[i]
			if ws.WorkspaceID != workspaceID {
				continue
			}
			results := &KnowledgeResults{}
			d.knowledgeSyncWorkspace(ctx, ws, results)
			if len(results.Discoveries) == 0 && len(results.Ultimates) == 0 &&
				len(results.Scans) == 0 && len(results.Adoptions) == 0 &&
				len(results.Operations) == 0 && len(results.Writebacks) == 0 {
				continue
			}
			if _, err := d.client.PostKnowledgeResults(ctx, d.cfg.DaemonID, ws.WorkspaceID, results); err != nil {
				cycleErrs = append(cycleErrs, fmt.Errorf("report knowledge results for workspace %s: %w", ws.WorkspaceID, err))
			}
		}
	}
	return errors.Join(cycleErrs...)
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
	} else if ws.UnboundUltimate != nil && hasBeadsDatabase(ws.UnboundUltimate.Path) {
		// A manually registered target is never scanned, but the daemon that can
		// prove it hosts a bd database may claim it for adoption work.
		results.Ultimates = append(results.Ultimates, KnowledgeUltimateReport{
			WorkspaceID: ws.WorkspaceID,
			Path:        ws.UnboundUltimate.Path,
		})
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
	for _, operation := range ws.Operations {
		if err := d.runKnowledgeOperation(ctx, operation); err != nil {
			d.logger.Warn("knowledge operation failed", "operation", operation.OperationType, "id", operation.ID, "error", err)
			results.Operations = append(results.Operations, KnowledgeOperationReport{ID: operation.ID, Error: err.Error()})
		} else {
			results.Operations = append(results.Operations, KnowledgeOperationReport{ID: operation.ID, OK: true})
		}
	}
	for _, job := range ws.Writebacks {
		if report, err := d.runKnowledgeWriteback(ctx, job); err != nil {
			d.logger.Warn("knowledge write-back failed", "event", job.EventID, "operation", job.Operation, "error", err)
			results.Writebacks = append(results.Writebacks, KnowledgeWritebackReport{EventID: job.EventID, EntryID: job.EntryID, ClaimToken: job.ClaimToken, Error: err.Error()})
		} else {
			results.Writebacks = append(results.Writebacks, report)
		}
	}
}

// scanKnowledgeDir reads one source directory and decides what to report.
// Unclaimed directories (daemon_id ”) are only scanned when the path is
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
		if !knowledgeMirrorChanged(dir.Mirror, dir.SourceDeleted, memories) {
			report.Unchanged = true
			report.Memories = nil
		}
		return report
	}
	if !knowledgeMirrorChanged(dir.Mirror, dir.SourceDeleted, memories) {
		return nil
	}
	report.OK = true
	report.Memories = memories
	return report
}

func knowledgeMirrorChanged(mirror map[string]string, sourceDeleted map[string]bool, memories map[string]string) bool {
	for key := range sourceDeleted {
		if _, restored := memories[key]; restored {
			return true
		}
	}
	activeEntries := 0
	for key := range mirror {
		if !sourceDeleted[key] {
			activeEntries++
		}
	}
	if activeEntries != len(memories) {
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
	if existingContent, ok := existing[job.Key]; ok {
		if strings.TrimSpace(existingContent) == strings.TrimSpace(job.Content) {
			report.OK = true
			return report, nil
		}
		return report, fmt.Errorf("ultimate library key %q already contains different content", job.Key)
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

// runKnowledgeOperation executes a confirmed ultimate-library mutation. The
// target is always read back after remember, and every forget is followed by a
// failed recall; a lost result can safely replay an already-applied deletion.
func (d *Daemon) runKnowledgeOperation(ctx context.Context, operation KnowledgePlanOperation) error {
	if err := ensureBdReady(operation.UltimatePath); err != nil {
		return fmt.Errorf("prepare ultimate directory: %w", err)
	}
	if operation.OperationType == "merge" {
		// A merge writes its target before removing its sources. Prove the source
		// library is readable first so a list, Dolt, or permission failure cannot
		// leave a new target behind while reporting the operation as failed.
		if _, err := bdListMemories(ctx, operation.UltimatePath); err != nil {
			return fmt.Errorf("inspect memories before merging: %w", err)
		}
	}
	switch operation.OperationType {
	case "edit", "merge":
		if err := bdRemember(ctx, operation.UltimatePath, operation.Key, operation.Content, operation.Actor); err != nil {
			return fmt.Errorf("bd remember: %w", err)
		}
		recalled, err := bdRecall(ctx, operation.UltimatePath, operation.Key)
		if err != nil {
			return fmt.Errorf("bd recall read-back: %w", err)
		}
		if strings.TrimSpace(recalled) != strings.TrimSpace(operation.Content) {
			return fmt.Errorf("bd recall read-back mismatch for key %s", operation.Key)
		}
		if operation.OperationType == "edit" {
			return nil
		}
	case "delete":
		operation.DeleteKeys = []string{operation.Key}
	default:
		return fmt.Errorf("unsupported knowledge operation %q", operation.OperationType)
	}
	for _, key := range operation.DeleteKeys {
		memories, err := bdListMemories(ctx, operation.UltimatePath)
		if err != nil {
			return fmt.Errorf("inspect memories before forgetting %s: %w", key, err)
		}
		if _, exists := memories[key]; !exists {
			// A prior successful delete followed by a failed report may replay the
			// operation. A successful read-only list proves the key is absent.
			continue
		}
		if err := bdForget(ctx, operation.UltimatePath, key); err != nil {
			return fmt.Errorf("bd forget %s: %w", key, err)
		}
		memories, err = bdListMemories(ctx, operation.UltimatePath)
		if err != nil {
			return fmt.Errorf("inspect memories after forgetting %s: %w", key, err)
		}
		if _, exists := memories[key]; exists {
			return fmt.Errorf("bd forget read-back found key %s", key)
		}
	}
	return nil
}

func (d *Daemon) runKnowledgeWriteback(ctx context.Context, job KnowledgePlanWriteback) (KnowledgeWritebackReport, error) {
	report := KnowledgeWritebackReport{EventID: job.EventID, EntryID: job.EntryID, ClaimToken: job.ClaimToken}
	if strings.TrimSpace(job.Path) == "" || strings.TrimSpace(job.Key) == "" {
		return report, fmt.Errorf("knowledge write-back requires source path and key")
	}

	switch job.Operation {
	case "remove":
		memories, err := bdListMemories(ctx, job.Path)
		if err != nil {
			return report, fmt.Errorf("read source before deletion: %w", err)
		}
		content, found := memories[job.Key]
		if !found {
			break
		}
		if job.ExpectedContentSha256 != "" && knowledgeContentSHA(content) != job.ExpectedContentSha256 {
			return report, fmt.Errorf("source content changed for key %s", job.Key)
		}
		if err := bdForget(ctx, job.Path, job.Key); err != nil {
			return report, err
		}
		memories, err = bdListMemories(ctx, job.Path)
		if err != nil {
			return report, fmt.Errorf("verify source deletion: %w", err)
		}
		if _, found := memories[job.Key]; found {
			return report, fmt.Errorf("source deletion did not remove key %s", job.Key)
		}
	case "restore":
		memories, err := bdListMemories(ctx, job.Path)
		if err != nil {
			return report, fmt.Errorf("read source before restore: %w", err)
		}
		if content, found := memories[job.Key]; found && strings.TrimSpace(content) != strings.TrimSpace(job.Content) && job.ExpectedContentSha256 != "" && knowledgeContentSHA(content) != job.ExpectedContentSha256 {
			return report, fmt.Errorf("source content changed for key %s", job.Key)
		}
		if err := bdRemember(ctx, job.Path, job.Key, job.Content, job.Actor); err != nil {
			return report, err
		}
		recalled, err := bdRecall(ctx, job.Path, job.Key)
		if err != nil {
			return report, fmt.Errorf("verify source restore: %w", err)
		}
		if strings.TrimSpace(recalled) != strings.TrimSpace(job.Content) {
			return report, fmt.Errorf("source restore read-back mismatch for key %s", job.Key)
		}
	default:
		return report, fmt.Errorf("unsupported knowledge write-back operation %q", job.Operation)
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
	if err := os.MkdirAll(path, 0o755); err != nil {
		return "", fmt.Errorf("create %s: %w", path, err)
	}
	if err := ensureGitRoot(path); err != nil {
		return "", err
	}
	if err := ensureBdReady(path); err != nil {
		return "", err
	}
	if !hasLocalBeadsDatabase(path) {
		return "", fmt.Errorf("daemon-managed ultimate %s does not use a local bd store", path)
	}
	return path, nil
}

func ensureGitRoot(dir string) error {
	if info, err := os.Stat(filepath.Join(dir, ".git")); err == nil && info.IsDir() {
		return nil
	}
	cmd := exec.CommandContext(context.Background(), "git", "init", "--quiet")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git init in %s: %w: %s", dir, err, strings.TrimSpace(string(out)))
	}
	info, err := os.Stat(filepath.Join(dir, ".git"))
	if err != nil || !info.IsDir() {
		return fmt.Errorf("git init in %s did not create a repository root", dir)
	}
	return nil
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
	cmd := exec.CommandContext(ctx, knowledgeBdBin(), "init", "--non-interactive", "--skip-hooks", "--skip-agents", "--init-if-missing")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("bd init in %s: %w: %s", dir, err, strings.TrimSpace(string(out)))
	}
	if !hasLocalBeadsDatabase(dir) {
		return fmt.Errorf("bd init in %s did not create a local beads database", dir)
	}
	return nil
}

// bdListMemories reads a source's memories read-only. bd envelopes a numeric
// schema_version into the same flat JSON object as the memories, so the parser
// validates the envelope and every value before returning any entries.
func bdListMemories(ctx context.Context, dir string) (map[string]string, error) {
	cmd := exec.CommandContext(ctx, knowledgeBdBin(), "memories", "--json", "--readonly", "-C", dir)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("bd memories in %s: %w", dir, err)
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(out, &envelope); err != nil {
		return nil, fmt.Errorf("decode bd memories in %s: %w", dir, err)
	}
	if envelope == nil {
		return nil, fmt.Errorf("decode bd memories in %s: envelope must be an object", dir)
	}
	schemaVersion, ok := envelope["schema_version"]
	if !ok {
		return nil, fmt.Errorf("decode bd memories in %s: missing schema_version", dir)
	}
	var version int
	if err := json.Unmarshal(schemaVersion, &version); err != nil || version < 1 {
		if err == nil {
			err = fmt.Errorf("unsupported schema_version %d", version)
		}
		return nil, fmt.Errorf("decode bd memories in %s: invalid schema_version: %w", dir, err)
	}
	memories := make(map[string]string, len(envelope)-1)
	for key, raw := range envelope {
		if key == "schema_version" {
			continue
		}
		if key == "" {
			return nil, fmt.Errorf("decode bd memories in %s: memory key is empty", dir)
		}
		var content string
		if err := json.Unmarshal(raw, &content); err != nil {
			return nil, fmt.Errorf("decode bd memories in %s: value for %q is not a string: %w", dir, key, err)
		}
		memories[key] = content
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

var errKnowledgeMemoryNotFound = errors.New("knowledge memory not found")

func bdRecall(ctx context.Context, dir, key string) (string, error) {
	cmd := exec.CommandContext(ctx, knowledgeBdBin(), "recall", key, "-C", dir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		if strings.Contains(strings.ToLower(string(out)), "memory not found") {
			return "", fmt.Errorf("%w: %s", errKnowledgeMemoryNotFound, key)
		}
		return "", fmt.Errorf("bd recall %s: %w", key, err)
	}
	return strings.TrimSpace(string(out)), nil
}

func bdForget(ctx context.Context, dir, key string) error {
	cmd := exec.CommandContext(ctx, knowledgeBdBin(), "forget", key, "-C", dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("bd forget %s: %w: %s", key, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func hasBeadsDatabase(path string) bool {
	info, err := os.Stat(filepath.Join(path, ".beads"))
	return err == nil && info.IsDir()
}

func hasLocalBeadsDatabase(path string) bool {
	info, err := os.Stat(filepath.Join(path, ".beads", "embeddeddolt"))
	return err == nil && info.IsDir()
}
