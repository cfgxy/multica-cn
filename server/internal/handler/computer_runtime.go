package handler

import (
	"context"
	"net/http"
	"os"
	"path"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/multica-ai/multica/server/internal/computer"
	"github.com/multica-ai/multica/server/internal/sub2api"
	"github.com/multica-ai/multica/server/pkg/agent"
)

type runtimeTarget struct {
	id, name, command, install, source string
	supportsVersion                    bool
}

func runtimeTargets() []runtimeTarget {
	list := []runtimeTarget{}
	for _, r := range agent.BuiltinRuntimes {
		list = append(list, runtimeTarget{r.ID, r.DisplayName, r.DefaultCommand, r.InstallCommand, r.InstallerSource, !r.LatestOnly})
	}
	for _, r := range agent.ProtocolFamilyInstalls {
		list = append(list, runtimeTarget{r.ID, r.DisplayName, r.DefaultCommand, r.InstallCommand, r.InstallerSource, !r.LatestOnly})
	}
	list = append(list, runtimeTarget{id: "openclaw", name: "OpenClaw", command: "openclaw"})
	return list
}

type runtimeAsset struct {
	ModelKeyName      string `json:"model_key_name"`
	ModelID           string `json:"model_id"`
	ModelConfigStatus string `json:"model_config_status"`
	ModelConfigError  string `json:"model_config_error"`
	ModelTestStatus   string `json:"model_test_status"`

	computer.RuntimeVersion
	UpdateAvailable   *bool      `json:"update_available"`
	ID                string     `json:"id"`
	DisplayName       string     `json:"display_name"`
	InstalledVersion  string     `json:"installed_version"`
	CanInstall        bool       `json:"can_install"`
	VersionRequired   bool       `json:"version_required"`
	SupportsVersion   bool       `json:"supports_version"`
	ProbeError        string     `json:"probe_error"`
	ProbeState        string     `json:"probe_state"`
	ErrorCode         string     `json:"error_code"`
	ExecutablePath    string     `json:"executable_path"`
	InstallDir        string     `json:"install_dir"`
	RequestedVersion  string     `json:"requested_version"`
	ActualVersion     string     `json:"actual_version"`
	InstallerSource   string     `json:"installer_source"`
	InstalledAt       *time.Time `json:"installed_at"`
	CheckedAt         *time.Time `json:"checked_at"`
	ProbeEnvironment  string     `json:"probe_environment"`
	RegistrationState string     `json:"registration_state"`
}

// Reading a list never launches SSH; discovery is an explicit tracked action.
func (h *Handler) ComputerBindingRuntimes(w http.ResponseWriter, r *http.Request) {
	_, id, ok := h.bindingAccess(w, r, false)
	if !ok {
		return
	}
	list, err := h.runtimeAssets(r.Context(), id)
	if err != nil {
		writeError(w, 500, "Cannot read runtime assets")
		return
	}
	_, runtimeVersions := h.runtimeCache()
	for i, target := range runtimeTargets() {
		list[i].RuntimeVersion = runtimeVersions.Get(target.id, target.source)
		if list[i].LatestVersionState == "ready" && list[i].ProbeState == "installed" {
			list[i].UpdateAvailable = computer.RuntimeUpdateAvailable(list[i].InstalledVersion, list[i].LatestVersion)
		}
	}
	writeJSON(w, 200, list)
}
func (h *Handler) runtimeAssets(ctx context.Context, id string) ([]runtimeAsset, error) {
	rows, err := h.DB.Query(ctx, `SELECT runtime_id,executable_path,install_dir,requested_version,actual_version,installer_source,installed_at,checked_at,probe_state,error_code,probe_environment FROM computer_runtime_asset WHERE binding_id=$1`, id)
	if err != nil {
		return nil, err
	}
	assets := map[string]runtimeAsset{}
	for rows.Next() {
		var a runtimeAsset
		if err = rows.Scan(&a.ID, &a.ExecutablePath, &a.InstallDir, &a.RequestedVersion, &a.ActualVersion, &a.InstallerSource, &a.InstalledAt, &a.CheckedAt, &a.ProbeState, &a.ErrorCode, &a.ProbeEnvironment); err != nil {
			rows.Close()
			return nil, err
		}
		assets[a.ID] = a
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	registrations := map[string]string{}
	rows, err = h.DB.Query(ctx, `SELECT r.provider,CASE WHEN r.status='online' AND r.last_seen_at>now()-interval '30 seconds' THEN 'online' ELSE 'offline' END FROM agent_runtime r JOIN computer_binding b ON b.id=$1 WHERE r.daemon_id=b.id::text AND r.owner_id=b.user_id AND r.workspace_id=b.workspace_id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var provider, state string
		if err = rows.Scan(&provider, &state); err != nil {
			return nil, err
		}
		registrations[provider] = state
	}
	if rows.Err() != nil {
		return nil, rows.Err()
	}
	configs := map[string][5]string{}
	configRows, err := h.DB.Query(ctx, `SELECT c.runtime_kind,
CASE WHEN c.upstream_id IS NOT NULL AND (g.upstream_id IS NULL OR d.status IS DISTINCT FROM 'enabled' OR NOT COALESCE(d.allowed_runtimes ? c.runtime_kind,false) OR s.revision IS DISTINCT FROM d.source_revision) THEN 'authorization_invalid' ELSE c.status END,
CASE WHEN c.last_test_status='failed' OR c.status='needs_configuration' THEN c.last_test_error_code ELSE '' END,c.last_test_status,
CASE WHEN g.upstream_id IS NOT NULL AND d.status='enabled' AND d.allowed_runtimes ? c.runtime_kind AND s.revision=d.source_revision THEN d.api_key_name ELSE '' END,c.model_id
FROM computer_runtime_model_config c
JOIN computer_binding b ON b.id=c.binding_id
LEFT JOIN model_key_source s ON s.id=c.source_id
LEFT JOIN model_key_directory_entry d ON d.source_id=c.source_id AND d.upstream_id=c.upstream_id
LEFT JOIN user_model_key_grant g ON g.user_id=b.user_id AND g.source_id=c.source_id AND g.upstream_id=c.upstream_id
WHERE c.binding_id=$1`, id)
	if err != nil {
		return nil, err
	}
	for configRows.Next() {
		var kind, status, code, test, name, model string
		if err = configRows.Scan(&kind, &status, &code, &test, &name, &model); err != nil {
			configRows.Close()
			return nil, err
		}
		configs[kind] = [5]string{status, code, test, name, model}
	}
	err = configRows.Err()
	configRows.Close()
	if err != nil {
		return nil, err
	}
	list := []runtimeAsset{}
	for _, target := range runtimeTargets() {
		a, found := assets[target.id]
		a.ID = target.id
		a.ModelKeyName, a.ModelID = configs[target.id][3], configs[target.id][4]
		a.ModelConfigStatus, a.ModelConfigError, a.ModelTestStatus = configs[target.id][0], configs[target.id][1], configs[target.id][2]
		if a.ModelConfigStatus == "" {
			a.ModelConfigStatus = "unconfigured"
		}
		if a.ModelTestStatus == "" {
			a.ModelTestStatus = "untested"
		}
		a.DisplayName = target.name
		a.CanInstall = target.install != ""
		a.VersionRequired = false
		a.SupportsVersion = target.supportsVersion
		if !found {
			a.ProbeState = "unknown"
		}
		a.RegistrationState = registrations[target.id]
		if a.RegistrationState == "" {
			a.RegistrationState = "not_discovered"
		}
		if a.ProbeState != "missing" {
			a.InstalledVersion = a.ActualVersion
		}
		if a.ErrorCode != "" && a.ErrorCode != "cli_missing" {
			a.ProbeError = computer.ErrorSummary(a.ErrorCode)
		}
		list = append(list, a)
	}
	return list, nil
}
func (h *Handler) saveRuntimeProbe(ctx context.Context, operationID, bindingID string, target runtimeTarget, p computer.RuntimeProbe, version string, installed bool) error {
	dir := ""
	if p.Path != "" {
		dir = path.Dir(p.Path)
	}
	return h.withRunningOperation(ctx, operationID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO computer_runtime_asset(binding_id,runtime_id,executable_path,install_dir,requested_version,actual_version,installer_source,installed_at,probe_state,error_code,probe_environment)
 VALUES($1,$2,$3,$4,$5,$6,$7,CASE WHEN $8 THEN now() END,$9,$10,$11)
 ON CONFLICT(binding_id,runtime_id) DO UPDATE SET
 executable_path=CASE WHEN $3<>'' THEN $3 ELSE computer_runtime_asset.executable_path END,
 install_dir=CASE WHEN $4<>'' THEN $4 ELSE computer_runtime_asset.install_dir END,
 requested_version=CASE WHEN $8 THEN $5 ELSE computer_runtime_asset.requested_version END,
 actual_version=CASE WHEN $6<>'' THEN $6 ELSE computer_runtime_asset.actual_version END,
 installer_source=CASE WHEN $8 THEN $7 ELSE computer_runtime_asset.installer_source END,
 installed_at=CASE WHEN $8 THEN now() ELSE computer_runtime_asset.installed_at END,
 checked_at=now(),probe_state=$9,error_code=$10,probe_environment=$11`, bindingID, target.id, p.Path, dir, version, p.Version, target.source, installed, p.State, p.Code, computer.RuntimePath)
		return err
	})
}

func (h *Handler) DiscoverComputerBinding(w http.ResponseWriter, r *http.Request) {
	uid, ok := requireUserID(w, r)
	if !ok {
		return
	}
	m, user, id, ok := h.ownedRuntimeBinding(w, r, uid)
	if !ok {
		return
	}
	key := os.Getenv("MULTICA_COMPUTER_SSH_KEY")
	if key == "" {
		writeError(w, 503, "Computer provisioning is not configured")
		return
	}
	op, err := h.beginBindingOperation(r.Context(), uid, id, "runtime_discovery", "", "")
	if err != nil {
		operationStartError(w, err)
		return
	}
	remote := computer.SSHRemote{Host: m.Host, Port: m.Port, User: m.SSHUser, KeyPath: key, Timeout: 30 * time.Second}
	go h.runRemoteOperation(op, func(ctx context.Context) (string, error) {
		h.operationStep(op, "checking_runtimes")
		firstCode := ""
		for _, target := range runtimeTargets() {
			p, err := remote.ProbeRuntime(ctx, user, target.command)
			if err != nil {
				p = computer.RuntimeProbe{State: "check_failed", Code: computer.ErrorCode(err, "version_check_failed")}
			}
			if err = h.saveRuntimeProbe(ctx, op, id, target, p, "", false); err != nil {
				return "", err
			}
			if p.Code != "" && p.Code != "cli_missing" && firstCode == "" {
				firstCode = p.Code
			}
		}
		h.operationStep(op, "refreshing_daemon")
		if h.DaemonWorkspaceRefresh != nil {
			h.DaemonWorkspaceRefresh.NotifyWorkspacesChanged(uid)
		}
		if firstCode != "" {
			return "", operationFailure(firstCode)
		}
		return "", nil
	})
	writeJSON(w, 202, map[string]string{"operation_id": op, "state": "queued"})
}

func (h *Handler) installBindingRuntime(ctx context.Context, op, bindingID, user, version string, target runtimeTarget, remote computer.SSHRemote) (string, error) {
	h.operationStep(op, "installing_runtime")
	serverURL, ok := runtimeCacheServerURL()
	if !ok {
		return "", operationFailure("runtime_cache_unavailable")
	}
	base := runtimeDownloadBase(serverURL, op, time.Now())
	cmd := cachedRuntimeInstallCommand(target, base, user, version)
	// The root-owned remote lock and process-group timeout survive lost HTTP/SSH
	// clients. A retry cannot overlap an orphaned installer on the same account.
	_, installErr := remote.RunCommandContext(ctx, "sudo -n timeout -k 15s 300s flock -n --close /run/lock/multica-runtime-"+user+" "+cmd)
	h.operationStep(op, "checking_runtime")
	p, probeErr := remote.ProbeRuntime(ctx, user, target.command)
	if probeErr != nil {
		p = computer.RuntimeProbe{State: "check_failed", Code: computer.ErrorCode(probeErr, "version_check_failed")}
	}
	if saveErr := h.saveRuntimeProbe(ctx, op, bindingID, target, p, version, installErr == nil && p.State == "installed"); saveErr != nil {
		return p.Version, saveErr
	}
	if installErr != nil {
		// The follow-up probe records observable assets, but must not attribute
		// an installation failure to the verification step.
		h.operationStep(op, "installing_runtime")
		code := computer.ErrorCode(installErr, "installer_failed")
		if code == "installer_failed" && p.State == "version_failed" {
			code = "version_check_failed"
		}
		return p.Version, operationFailure(code)
	}
	if p.Code != "" {
		return p.Version, operationFailure(p.Code)
	}
	if sub2api.KnownRuntime(target.id) {
		var owner string
		if err := h.DB.QueryRow(ctx, `SELECT user_id::text FROM computer_binding WHERE id=$1`, bindingID).Scan(&owner); err == nil {
			h.operationStep(op, "initializing_model_config")
			if err := h.initializeRuntimeModelConfig(ctx, owner, bindingID, user, target.id, remote); err != nil {
				code := computer.ErrorCode(err, "model_key_unavailable")
				_, _ = h.DB.Exec(ctx, `INSERT INTO computer_runtime_model_config(binding_id,runtime_kind,status,last_test_error_code) VALUES($1,$2,'needs_configuration',$3) ON CONFLICT(binding_id,runtime_kind) DO UPDATE SET status=CASE WHEN computer_runtime_model_config.file_revision='' THEN 'needs_configuration' ELSE computer_runtime_model_config.status END,last_test_error_code=$3`, bindingID, target.id, code)
			}
		}
	}
	return p.Version, nil
}
