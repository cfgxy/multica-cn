package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/multica-ai/multica/server/internal/computer"
	"github.com/multica-ai/multica/server/pkg/agent"
)

// Explicit instance configuration takes precedence, including an explicitly
// empty value. Legacy Computer operators bootstrap admins only when unset.
// Workspace roles never grant instance-level privileges.
func isInstanceAdmin(uid string) bool {
	if uid == "" {
		return false
	}
	ids, configured := os.LookupEnv("MULTICA_INSTANCE_ADMIN_IDS")
	if !configured {
		ids = os.Getenv("MULTICA_COMPUTER_OPERATOR_IDS")
	}
	for _, id := range strings.Split(ids, ",") {
		if strings.TrimSpace(id) == uid {
			return true
		}
	}
	return false
}
func requireInstanceAdmin(w http.ResponseWriter, r *http.Request) (string, bool) {
	uid, ok := requireUserID(w, r)
	if !ok {
		return "", false
	}
	w.Header().Set("Cache-Control", "no-store")
	if !isInstanceAdmin(uid) {
		writeError(w, 403, "Instance administrator access required")
		return "", false
	}
	return uid, true
}
func (h *Handler) InstanceAccess(w http.ResponseWriter, r *http.Request) {
	uid, ok := requireUserID(w, r)
	if !ok {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, map[string]bool{"admin": isInstanceAdmin(uid)})
}

type adminComputer struct {
	computer.Machine
	Enabled bool `json:"enabled"`
	// Registration and last-check metadata for the admin list. Secrets never
	// appear here: only who registered it and how the last probe went.
	CreatedBy     string     `json:"created_by,omitempty"`
	CreatedByName string     `json:"created_by_name,omitempty"`
	CreatedAt     *time.Time `json:"created_at,omitempty"`
	CheckedAt     *time.Time `json:"checked_at,omitempty"`
	CheckOK       *bool      `json:"check_ok,omitempty"`
	CheckDetail   string     `json:"check_detail,omitempty"`
	Bindings      int        `json:"bindings"`
}

const adminComputerColumns = `SELECT c.id::text,c.name,c.host,c.port,c.ssh_user,c.enabled,
 c.created_by::text,COALESCE(u.name,c.created_by::text),c.created_at,
 c.checked_at,c.check_ok,c.check_detail,
 (SELECT count(*) FROM computer_binding b WHERE b.computer_id=c.id AND b.archived_at IS NULL)
 FROM computer c LEFT JOIN "user" u ON u.id=c.created_by`

func scanAdminComputer(row pgx.Row) (adminComputer, error) {
	var m adminComputer
	err := row.Scan(&m.ID, &m.Name, &m.Host, &m.Port, &m.SSHUser, &m.Enabled,
		&m.CreatedBy, &m.CreatedByName, &m.CreatedAt,
		&m.CheckedAt, &m.CheckOK, &m.CheckDetail, &m.Bindings)
	return m, err
}

func (h *Handler) AdminComputers(w http.ResponseWriter, r *http.Request) {
	uid, ok := requireInstanceAdmin(w, r)
	if !ok {
		return
	}
	if r.Method == http.MethodPost {
		var in computer.Machine
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in) != nil {
			writeError(w, 400, "Invalid Computer")
			return
		}
		if err := in.Validate(); err != nil {
			writeError(w, 400, err.Error())
			return
		}
		// Do not take a registration on trust: a Computer that fails the check
		// is never stored, so the registry cannot hold unusable machines.
		probe, probeErr := probeMachine(in)
		if probeErr != nil {
			writeError(w, 503, probeErr.Error())
			return
		}
		if !probe.OK {
			writeJSON(w, 422, map[string]any{"error": "Computer did not pass the connection check", "code": "computer_check_failed", "probe": probe})
			return
		}
		tx, err := h.TxStarter.Begin(r.Context())
		if err != nil {
			writeError(w, 500, "Cannot register Computer")
			return
		}
		defer tx.Rollback(r.Context())
		var id string
		err = tx.QueryRow(r.Context(), `INSERT INTO computer(name,host,port,ssh_user,created_by,checked_at,check_ok) VALUES($1,$2,$3,$4,$5,now(),true) RETURNING id::text`, in.Name, in.Host, in.Port, in.SSHUser, uid).Scan(&id)
		if err == nil {
			_, err = tx.Exec(r.Context(), `INSERT INTO computer_audit(user_id,computer_id,action,outcome) VALUES($1,$2,'register','success')`, uid, id)
		}
		if err == nil {
			err = tx.Commit(r.Context())
		}
		if err != nil {
			writeError(w, 500, "Cannot register Computer")
			return
		}
		writeJSON(w, 201, map[string]any{"id": id, "probe": probe})
		return
	}
	rows, err := h.DB.Query(r.Context(), adminComputerColumns+` ORDER BY c.name,c.id`)
	if err != nil {
		writeError(w, 500, "Cannot list Computers")
		return
	}
	defer rows.Close()
	list := []adminComputer{}
	for rows.Next() {
		m, err := scanAdminComputer(rows)
		if err != nil {
			writeError(w, 500, "Cannot read Computer")
			return
		}
		list = append(list, m)
	}
	if rows.Err() != nil {
		writeError(w, 500, "Cannot list Computers")
		return
	}
	writeJSON(w, 200, list)
}
func (h *Handler) UpdateAdminComputer(w http.ResponseWriter, r *http.Request) {
	uid, ok := requireInstanceAdmin(w, r)
	if !ok {
		return
	}
	id, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "Computer ID")
	if !ok {
		return
	}
	var in struct {
		Name    *string `json:"name"`
		Host    *string `json:"host"`
		Port    *int    `json:"port"`
		SSHUser *string `json:"ssh_user"`
		Enabled *bool   `json:"enabled"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	dec.DisallowUnknownFields()
	if dec.Decode(&in) != nil {
		writeError(w, 400, "Invalid Computer update")
		return
	}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, 500, "Cannot update Computer")
		return
	}
	defer tx.Rollback(r.Context())
	var m adminComputer
	err = tx.QueryRow(r.Context(), `SELECT name,host,port,ssh_user,enabled FROM computer WHERE id=$1 FOR UPDATE`, id).Scan(&m.Name, &m.Host, &m.Port, &m.SSHUser, &m.Enabled)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "Computer not found")
		return
	}
	if err != nil {
		writeError(w, 500, "Cannot read Computer")
		return
	}
	old := m
	if in.Name != nil {
		m.Name = *in.Name
	}
	if in.Host != nil {
		m.Host = *in.Host
	}
	if in.Port != nil {
		m.Port = *in.Port
	}
	if in.SSHUser != nil {
		m.SSHUser = *in.SSHUser
	}
	if in.Enabled != nil {
		m.Enabled = *in.Enabled
	}
	if err = m.Machine.Validate(); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	// Existing bindings identify OS accounts on a specific machine. Never
	// silently repoint them at a different host or privileged SSH identity.
	if old.Host != m.Host || old.Port != m.Port || old.SSHUser != m.SSHUser {
		var bound bool
		err = tx.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM computer_binding WHERE computer_id=$1)`, id).Scan(&bound)
		if err != nil {
			writeError(w, 500, "Cannot check bindings")
			return
		}
		if bound {
			writeError(w, 409, "Connection cannot change after account binding; register a new Computer")
			return
		}
	}
	_, err = tx.Exec(r.Context(), `UPDATE computer SET name=$2,host=$3,port=$4,ssh_user=$5,enabled=$6 WHERE id=$1`, id, m.Name, m.Host, m.Port, m.SSHUser, m.Enabled)
	action := "update"
	if in.Enabled != nil && old.Enabled != m.Enabled {
		if m.Enabled {
			action = "enable"
		} else {
			action = "disable"
		}
	}
	if err == nil {
		_, err = tx.Exec(r.Context(), `INSERT INTO computer_audit(user_id,computer_id,action,outcome) VALUES($1,$2,$3,'success')`, uid, id, action)
	}
	if err == nil {
		err = tx.Commit(r.Context())
	}
	if err != nil {
		writeError(w, 500, "Cannot update Computer")
		return
	}
	writeJSON(w, 200, map[string]bool{"saved": true})
}

// probeMachine is a variable so tests can supply a verdict without a real
// machine. Production always uses sshProbe.
var probeMachine = sshProbe

// sshProbe runs the read-only connectivity probe. Only the SSH key is
// required: a probe installs nothing, so the provisioning artifact and state
// directory are not needed yet.
func sshProbe(m computer.Machine) (computer.ProbeResult, error) {
	keyPath := os.Getenv("MULTICA_COMPUTER_SSH_KEY")
	if keyPath == "" {
		return computer.ProbeResult{}, errors.New("Computer provisioning is not configured")
	}
	remote := computer.SSHRemote{Host: m.Host, Port: m.Port, User: m.SSHUser, KeyPath: keyPath, Timeout: 45 * time.Second}
	return remote.Probe()
}

// CheckAdminComputerDraft probes connection details that are not saved yet so
// an admin sees the verdict before registering a Computer.
func (h *Handler) CheckAdminComputerDraft(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireInstanceAdmin(w, r); !ok {
		return
	}
	var in computer.Machine
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in) != nil {
		writeError(w, 400, "Invalid Computer")
		return
	}
	if err := in.Validate(); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	res, err := probeMachine(in)
	if err != nil {
		writeError(w, 503, err.Error())
		return
	}
	writeJSON(w, 200, res)
}

// CheckAdminComputer probes a registered Computer and records the verdict.
func (h *Handler) CheckAdminComputer(w http.ResponseWriter, r *http.Request) {
	uid, ok := requireInstanceAdmin(w, r)
	if !ok {
		return
	}
	id, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "Computer ID")
	if !ok {
		return
	}
	var m computer.Machine
	err := h.DB.QueryRow(r.Context(), `SELECT host,port,ssh_user FROM computer WHERE id=$1`, id).Scan(&m.Host, &m.Port, &m.SSHUser)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "Computer not found")
		return
	}
	if err != nil {
		writeError(w, 500, "Cannot read Computer")
		return
	}
	res, err := probeMachine(m)
	if err != nil {
		writeError(w, 503, err.Error())
		return
	}
	outcome := "failure"
	if res.OK {
		outcome = "success"
	}
	if _, err = h.DB.Exec(r.Context(), `UPDATE computer SET checked_at=now(),check_ok=$2,check_detail=$3 WHERE id=$1`, id, res.OK, failedCheckSummary(res)); err != nil {
		writeError(w, 500, "Cannot record check")
		return
	}
	// A recorded probe is an admin action against a specific Computer.
	_, _ = h.DB.Exec(r.Context(), `INSERT INTO computer_audit(user_id,computer_id,action,outcome) VALUES($1,$2,'check',$3)`, uid, id, outcome)
	writeJSON(w, 200, res)
}

// AdminSshPubKey returns the public key that the backend uses to log into
// managed Computers. Admins need it to add the key to a machine's
// authorized_keys before registering it.
func (h *Handler) AdminSshPubKey(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireInstanceAdmin(w, r); !ok {
		return
	}
	keyPath := os.Getenv("MULTICA_COMPUTER_SSH_KEY")
	if keyPath == "" {
		writeError(w, 503, "Computer provisioning is not configured")
		return
	}
	// Try <keyPath>.pub first; fall back to deriving the public key from the
	// private key so that different key-file layouts both work.
	pubPath := keyPath + ".pub"
	data, err := os.ReadFile(pubPath)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			writeError(w, 503, "SSH public key file cannot be read")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		data, err = exec.CommandContext(ctx, "ssh-keygen", "-y", "-P", "", "-f", keyPath).Output()
		if err != nil {
			writeError(w, 503, "SSH public key could not be derived")
			return
		}
	}
	writeJSON(w, 200, map[string]string{"pubkey": strings.TrimSpace(string(data))})
}

// failedCheckSummary keeps the stored detail short and only about failures.
func failedCheckSummary(res computer.ProbeResult) string {
	parts := []string{}
	for _, c := range res.Checks {
		if !c.OK {
			part := c.Name
			if c.Detail != "" {
				part += ": " + c.Detail
			}
			parts = append(parts, part)
		}
	}
	summary := strings.Join(parts, "; ")
	if len(summary) > 500 {
		summary = summary[:500]
	}
	return summary
}

// DeleteAdminComputer removes a Computer that has no account bound to it.
// Audit rows are deliberately kept: computer_id has no foreign key, so the
// history of a removed Computer stays readable.
func (h *Handler) DeleteAdminComputer(w http.ResponseWriter, r *http.Request) {
	uid, ok := requireInstanceAdmin(w, r)
	if !ok {
		return
	}
	id, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "Computer ID")
	if !ok {
		return
	}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, 500, "Cannot delete Computer")
		return
	}
	defer tx.Rollback(r.Context())
	var name string
	err = tx.QueryRow(r.Context(), `SELECT name FROM computer WHERE id=$1 FOR UPDATE`, id).Scan(&name)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "Computer not found")
		return
	}
	if err != nil {
		writeError(w, 500, "Cannot read Computer")
		return
	}
	// A binding names a real OS account on that machine. Deleting the Computer
	// would orphan it and leave the account installed with no way back.
	var bound int
	if err = tx.QueryRow(r.Context(), `SELECT count(*) FROM computer_binding WHERE computer_id=$1 AND archived_at IS NULL`, id).Scan(&bound); err != nil {
		writeError(w, 500, "Cannot check bindings")
		return
	}
	if bound > 0 {
		writeError(w, 409, "Computer still has bound accounts; remove them before deleting")
		return
	}
	if _, err = tx.Exec(r.Context(), `DELETE FROM computer WHERE id=$1`, id); err == nil {
		_, err = tx.Exec(r.Context(), `INSERT INTO computer_audit(user_id,computer_id,action,outcome) VALUES($1,$2,'delete','success')`, uid, id)
	}
	if err == nil {
		err = tx.Commit(r.Context())
	}
	if err != nil {
		writeError(w, 500, "Cannot delete Computer")
		return
	}
	writeJSON(w, 200, map[string]bool{"deleted": true})
}

func (h *Handler) AdminComputerBindings(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireInstanceAdmin(w, r); !ok {
		return
	}
	rows, err := h.DB.Query(r.Context(), `SELECT b.id::text,b.computer_id::text,COALESCE(b.workspace_id::text,''),b.username,b.state,b.last_error,b.user_id::text,COALESCE(u.name,b.user_id::text) FROM computer_binding b LEFT JOIN "user" u ON u.id=b.user_id ORDER BY b.updated_at DESC,b.id LIMIT 500`)
	if err != nil {
		writeError(w, 500, "Cannot list bindings")
		return
	}
	defer rows.Close()
	type binding struct {
		computerBinding
		UserID   string `json:"user_id"`
		UserName string `json:"user_name"`
	}
	list := []binding{}
	for rows.Next() {
		var b binding
		if rows.Scan(&b.ID, &b.ComputerID, &b.WorkspaceID, &b.Username, &b.State, &b.LastError, &b.UserID, &b.UserName) != nil {
			writeError(w, 500, "Cannot read binding")
			return
		}
		list = append(list, b)
	}
	if rows.Err() != nil {
		writeError(w, 500, "Cannot list bindings")
		return
	}
	writeJSON(w, 200, list)
}

// AdminCheckLinuxUser checks whether a bound OS account still exists.
func (h *Handler) AdminCheckLinuxUser(w http.ResponseWriter, r *http.Request) {
	h.checkLinuxUser(w, r, true)
}
func (h *Handler) CheckLinuxUser(w http.ResponseWriter, r *http.Request) {
	h.checkLinuxUser(w, r, false)
}
func (h *Handler) checkLinuxUser(w http.ResponseWriter, r *http.Request, admin bool) {
	uid, bindingID, ok := h.bindingAccess(w, r, admin)
	if !ok {
		return
	}
	id := parseUUID(bindingID)
	var m computer.Machine
	var linuxUser string
	err := h.DB.QueryRow(r.Context(), `SELECT c.id::text,c.host,c.port,c.ssh_user,b.username FROM computer_binding b JOIN computer c ON c.id=b.computer_id WHERE b.id=$1`, id).Scan(&m.ID, &m.Host, &m.Port, &m.SSHUser, &linuxUser)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "Linux User binding not found")
		return
	}
	if err != nil {
		writeError(w, 500, "Cannot read Linux User binding")
		return
	}
	if err := computer.ValidateLinuxUsername(linuxUser); err != nil {
		writeError(w, 500, "Invalid stored Linux username")
		return
	}
	keyPath := os.Getenv("MULTICA_COMPUTER_SSH_KEY")
	if keyPath == "" {
		writeError(w, 503, "Computer provisioning is not configured")
		return
	}
	remote := computer.SSHRemote{Host: m.Host, Port: m.Port, User: m.SSHUser, KeyPath: keyPath, Timeout: 30 * time.Second}
	out, checkErr := remote.RunCommandContext(r.Context(), "if getent passwd "+computer.ShellQuote(linuxUser)+" >/dev/null; then printf present; else printf missing; fi; printf '\\n'; systemctl is-active "+computer.ShellQuote("multica-daemon@"+linuxUser+".service")+" || true")
	if checkErr != nil {
		code := computer.ErrorCode(checkErr, "account_unavailable")
		writeJSON(w, 502, map[string]string{"code": code, "error": computer.ErrorSummary(code)})
		return
	}
	parts := strings.Fields(out)
	if len(parts) == 0 || (parts[0] != "present" && parts[0] != "missing") {
		writeError(w, 502, "Invalid account check response")
		return
	}
	present := parts[0] == "present"
	daemonState := "unknown"
	if len(parts) > 1 {
		switch parts[1] {
		case "active":
			daemonState = "running"
		case "inactive":
			daemonState = "stopped"
		case "failed":
			daemonState = "failed"
		}
	}
	accountState := "missing"
	if present {
		accountState = "present"
	}
	_, err = h.DB.Exec(r.Context(), `UPDATE computer_binding SET account_state=$2,checked_at=now(),daemon_state=$3,daemon_checked_at=now() WHERE id=$1`, id, accountState, daemonState)
	if err != nil {
		writeError(w, 500, "Cannot save account check")
		return
	}
	_, _ = h.DB.Exec(r.Context(), `INSERT INTO computer_audit(user_id,computer_id,binding_id,action,outcome) VALUES($1,$2,$3,'linux_user_check',$4)`, uid, m.ID, id, map[bool]string{true: "success", false: "failure"}[present])
	writeJSON(w, 200, map[string]bool{"present": present})
}

// runtimeVersionRE accepts only the characters npm/semver versions use.
// It prevents shell-special characters from reaching the install command.
var runtimeVersionRE = regexp.MustCompile(`^[0-9A-Za-z.\-+]+$`)

func (h *Handler) ownedRuntimeBinding(w http.ResponseWriter, r *http.Request, uid string) (computer.Machine, string, string, bool) {
	id, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "Binding ID")
	if !ok {
		return computer.Machine{}, "", "", false
	}
	var m computer.Machine
	var linuxUser, state string
	var verified bool
	err := h.DB.QueryRow(r.Context(), `SELECT c.id::text,c.host,c.port,c.ssh_user,b.username,b.state,b.verified FROM computer_binding b JOIN computer c ON c.id=b.computer_id WHERE b.id=$1 AND b.user_id=$2 AND b.archived_at IS NULL`, id, uid).Scan(&m.ID, &m.Host, &m.Port, &m.SSHUser, &linuxUser, &state, &verified)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "Linux User binding not found")
		return computer.Machine{}, "", "", false
	}
	if err != nil {
		writeError(w, 500, "Cannot read Linux User binding")
		return computer.Machine{}, "", "", false
	}
	if !verified || (state != "ready" && state != "failed" && state != "pending") {
		writeError(w, 409, "Linux User is not ready")
		return computer.Machine{}, "", "", false
	}
	if err := computer.ValidateLinuxUsername(linuxUser); err != nil {
		writeError(w, 500, "Invalid stored Linux username")
		return computer.Machine{}, "", "", false
	}
	return m, linuxUser, uuidToString(id), true
}

// ComputerBindingRuntimeInstall installs into the binding owner's Linux home.
func (h *Handler) ComputerBindingRuntimeInstall(w http.ResponseWriter, r *http.Request) {
	uid, ok := requireUserID(w, r)
	if !ok {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	m, linuxUser, bindingID, ok := h.ownedRuntimeBinding(w, r, uid)
	if !ok {
		return
	}
	var in struct {
		RuntimeID string `json:"runtime_id"`
		Version   string `json:"version"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	dec.DisallowUnknownFields()
	if dec.Decode(&in) != nil {
		writeError(w, 400, "Invalid request body")
		return
	}
	rt, exists := agent.BuiltinRuntimeByID(in.RuntimeID)
	var installCmd string
	supportsVersion := true
	if exists {
		installCmd = rt.InstallCommand
		supportsVersion = !rt.LatestOnly
	} else {
		pf, pfExists := agent.ProtocolFamilyInstallByID(in.RuntimeID)
		if !pfExists {
			writeError(w, 400, fmt.Sprintf("Unknown runtime %q", in.RuntimeID))
			return
		}
		installCmd = pf.InstallCommand
		supportsVersion = !pf.LatestOnly
	}
	if installCmd == "" {
		writeError(w, 422, fmt.Sprintf("Runtime %q does not support installation", in.RuntimeID))
		return
	}
	if in.Version == "" {
		in.Version = "latest"
	}
	if !runtimeVersionRE.MatchString(in.Version) {
		writeError(w, 400, "Invalid version: only alphanumeric, dot, hyphen, and plus are allowed")
		return
	}
	if !supportsVersion {
		if in.Version != "" && in.Version != "latest" {
			writeError(w, 422, "This runtime only supports latest; omit version or use latest")
			return
		}
		in.Version = "latest"
	}

	keyPath := os.Getenv("MULTICA_COMPUTER_SSH_KEY")
	if keyPath == "" {
		writeError(w, 503, "Computer provisioning is not configured")
		return
	}

	if _, ok := runtimeCacheServerURL(); !ok {
		writeError(w, 503, "Runtime cache server URL is not configured")
		return
	}
	operationID, err := h.beginBindingOperation(r.Context(), uid, bindingID, "runtime_install", in.RuntimeID, in.Version)
	if err != nil {
		operationStartError(w, err)
		return
	}
	var target runtimeTarget
	for _, candidate := range runtimeTargets() {
		if candidate.id == in.RuntimeID {
			target = candidate
			break
		}
	}
	remote := computer.SSHRemote{Host: m.Host, Port: m.Port, User: m.SSHUser, KeyPath: keyPath, Timeout: 6 * time.Minute}
	go h.runRemoteOperation(operationID, func(ctx context.Context) (string, error) {
		actual, err := h.installBindingRuntime(ctx, operationID, bindingID, linuxUser, in.Version, target, remote)
		if err == nil && h.DaemonWorkspaceRefresh != nil {
			h.operationStep(operationID, "refreshing_daemon")
			h.DaemonWorkspaceRefresh.NotifyWorkspacesChanged(uid)
		}
		return actual, err
	})
	h.runtimeInstallReceipt(w, r, operationID)
}

func (h *Handler) AdminComputerAudit(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireInstanceAdmin(w, r); !ok {
		return
	}
	rows, err := h.DB.Query(r.Context(), `SELECT a.id::text,a.user_id::text,COALESCE(u.name,a.user_id::text),COALESCE(a.computer_id::text,b.computer_id::text,''),COALESCE(a.binding_id::text,''),a.action,a.outcome,a.created_at FROM computer_audit a LEFT JOIN computer_binding b ON b.id=a.binding_id LEFT JOIN "user" u ON u.id=a.user_id ORDER BY a.created_at DESC,a.id LIMIT 200`)
	if err != nil {
		writeError(w, 500, "Cannot list audit")
		return
	}
	defer rows.Close()
	type entry struct {
		ID         string    `json:"id"`
		UserID     string    `json:"user_id"`
		UserName   string    `json:"user_name"`
		ComputerID string    `json:"computer_id"`
		BindingID  string    `json:"binding_id"`
		Action     string    `json:"action"`
		Outcome    string    `json:"outcome"`
		CreatedAt  time.Time `json:"created_at"`
	}
	list := []entry{}
	for rows.Next() {
		var e entry
		if rows.Scan(&e.ID, &e.UserID, &e.UserName, &e.ComputerID, &e.BindingID, &e.Action, &e.Outcome, &e.CreatedAt) != nil {
			writeError(w, 500, "Cannot read audit")
			return
		}
		list = append(list, e)
	}
	if rows.Err() != nil {
		writeError(w, 500, "Cannot list audit")
		return
	}
	writeJSON(w, 200, list)
}
