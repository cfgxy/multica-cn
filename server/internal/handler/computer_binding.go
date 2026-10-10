package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/computer"
	"github.com/multica-ai/multica/server/internal/util/secretbox"
)

type bindingOperationSummary struct {
	FinishedAt *time.Time `json:"finished_at"`
	ID         string     `json:"id"`
	Kind       string     `json:"kind"`
	State      string     `json:"state"`
	ErrorCode  string     `json:"error_code"`
}

type computerBinding struct {
	WorkspaceName   string                   `json:"workspace_name"`
	WorkspaceAccess string                   `json:"workspace_access"`
	LatestOperation *bindingOperationSummary `json:"latest_operation"`
	HealthPort      int                      `json:"-"`
	ID              string                   `json:"id"`
	ComputerID      string                   `json:"computer_id"`
	WorkspaceID     string                   `json:"workspace_id"`
	Username        string                   `json:"username"`
	State           string                   `json:"state"`
	LastError       string                   `json:"last_error"`
	Verified        bool                     `json:"verified"`
	AccountState    string                   `json:"account_state"`
	OperationBusy   bool                     `json:"operation_busy"`
}

func (h *Handler) ComputerBindings(w http.ResponseWriter, r *http.Request) {
	uid, ok := requireUserID(w, r)
	if !ok {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodGet {
		rows, err := h.DB.Query(r.Context(), `SELECT b.id::text,b.computer_id::text,COALESCE(b.workspace_id::text,''),b.username,
 CASE WHEN b.state='running' AND EXISTS(SELECT 1 FROM computer_operation o WHERE o.binding_id=b.id AND o.state IN ('queued','running') AND o.deadline_at<now()) THEN 'interrupted' ELSE b.state END,
 b.last_error,b.verified,b.account_state,
 EXISTS(SELECT 1 FROM computer_operation o WHERE o.binding_id=b.id AND o.state IN ('queued','running') AND o.deadline_at>=now()),
 COALESCE(w.name,''),CASE WHEN b.workspace_id IS NULL THEN 'none' WHEN w.id IS NOT NULL THEN 'accessible' ELSE 'unavailable' END,
 COALESCE(op.id::text,''),COALESCE(op.kind,''),COALESCE(op.effective_state,''),COALESCE(op.error_code,''),op.finished_at
 FROM computer_binding b
 LEFT JOIN workspace w ON w.id=b.workspace_id AND EXISTS(SELECT 1 FROM member m WHERE m.workspace_id=w.id AND m.user_id=$1)
 LEFT JOIN LATERAL (SELECT id,kind,finished_at,CASE WHEN state IN ('queued','running') AND deadline_at<now() THEN 'interrupted' ELSE state END AS effective_state,error_code FROM computer_operation WHERE binding_id=b.id ORDER BY created_at DESC,id DESC LIMIT 1) op ON true
 WHERE b.user_id=$1 AND b.archived_at IS NULL ORDER BY b.updated_at DESC`, uid)
		if err != nil {
			writeError(w, 500, "Cannot read your Computers")
			return
		}
		defer rows.Close()
		list := []computerBinding{}
		for rows.Next() {
			var b computerBinding
			var op bindingOperationSummary
			if rows.Scan(&b.ID, &b.ComputerID, &b.WorkspaceID, &b.Username, &b.State, &b.LastError, &b.Verified, &b.AccountState, &b.OperationBusy, &b.WorkspaceName, &b.WorkspaceAccess, &op.ID, &op.Kind, &op.State, &op.ErrorCode, &op.FinishedAt) != nil {
				writeError(w, 500, "Cannot read binding")
				return
			}
			if op.ID != "" {
				b.LatestOperation = &op
			}
			list = append(list, b)
		}
		if rows.Err() != nil {
			writeError(w, 500, "Cannot read bindings")
			return
		}
		writeJSON(w, 200, list)
		return
	}
	var in struct {
		ComputerID  string `json:"computer_id"`
		WorkspaceID string `json:"workspace_id"`
		Username    string `json:"username"`
		Password    string `json:"password"`
		Action      string `json:"action"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&in) != nil {
		writeError(w, 400, "Invalid operation")
		return
	}
	if _, ok := parseUUIDOrBadRequest(w, in.ComputerID, "computer_id"); !ok {
		return
	}
	if _, ok := parseUUIDOrBadRequest(w, in.WorkspaceID, "workspace_id"); !ok {
		return
	}
	if in.Action != "provision" && in.Action != "create_account" && in.Action != "sync" && in.Action != "upgrade" && in.Action != "remove" {
		writeError(w, 400, "Invalid operation")
		return
	}
	if _, err := computer.Decide(computer.Input{Username: in.Username}); err != nil {
		writeError(w, 400, "Invalid Linux username")
		return
	}
	if len(in.Password) < 1 || len(in.Password) > 4096 {
		writeError(w, 400, "Linux password is required")
		return
	}
	if _, ok := h.requireWorkspaceMember(w, r, in.WorkspaceID, "Workspace membership required"); !ok {
		return
	}
	settings := computer.Settings{}
	if in.Action == "provision" || in.Action == "sync" {
		key, err := secretbox.LoadKey("MULTICA_COMPUTER_SECRET_KEY")
		if err != nil {
			writeError(w, 503, "Computer provisioning is not configured")
			return
		}
		box, err := secretbox.New(key)
		if err != nil {
			writeError(w, 503, "Computer provisioning is not configured")
			return
		}
		var cipher []byte
		if h.DB.QueryRow(r.Context(), `SELECT ciphertext FROM computer_credential WHERE user_id=$1`, uid).Scan(&cipher) != nil {
			writeError(w, 400, "Save your credentials first")
			return
		}
		plain, err := box.Open(cipher)
		if err != nil {
			writeError(w, 500, "Cannot decrypt settings")
			return
		}
		var saved struct {
			Owner    string            `json:"owner"`
			Settings computer.Settings `json:"settings"`
		}
		if json.Unmarshal(plain, &saved) != nil || saved.Owner != uid {
			writeError(w, 500, "Invalid settings owner")
			return
		}
		settings = saved.Settings
		pat, err := h.Queries.GetPersonalAccessTokenByHash(r.Context(), auth.HashToken(settings.MulticaPAT))
		if err != nil || uuidToString(pat.UserID) != uid {
			writeError(w, 400, "Multica token must be valid and belong to you")
			return
		}
	}

	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, 500, "Cannot start operation")
		return
	}
	defer tx.Rollback(r.Context())
	var m computer.Machine
	err = tx.QueryRow(r.Context(), `SELECT id::text,name,host,port,ssh_user FROM computer WHERE id=$1 AND (enabled OR $2='remove') FOR SHARE`, in.ComputerID, in.Action).Scan(&m.ID, &m.Name, &m.Host, &m.Port, &m.SSHUser)
	if err != nil {
		writeError(w, 404, "Computer unavailable")
		return
	}
	serverURL := os.Getenv("MULTICA_COMPUTER_SERVER_URL")
	keyPath := os.Getenv("MULTICA_COMPUTER_SSH_KEY")
	binary := os.Getenv("MULTICA_COMPUTER_CLI_PATH")
	lockDir := os.Getenv("MULTICA_COMPUTER_STATE_DIR")
	if keyPath == "" || lockDir == "" || ((in.Action == "provision" || in.Action == "sync" || in.Action == "upgrade") && (serverURL == "" || binary == "")) {
		writeError(w, 503, "Computer provisioning is not configured")
		return
	}
	if in.Action != "provision" && in.Action != "create_account" {
		var owned bool
		if h.DB.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM computer_binding WHERE computer_id=$1 AND username=$2 AND user_id=$3 AND (workspace_id=$4 OR $5='remove') AND verified)`, m.ID, in.Username, uid, in.WorkspaceID, in.Action).Scan(&owned) != nil || !owned {
			writeError(w, 404, "Create or reuse this account first")
			return
		}
	}
	// The unique machine/account record is also the ownership boundary. Failed
	// attempts never let a second caller race the first caller's remote job.
	var b computerBinding
	err = tx.QueryRow(r.Context(), claimComputerBindingSQL, m.ID, uid, in.WorkspaceID, in.Username, in.Action).Scan(&b.ID, &b.ComputerID, &b.WorkspaceID, &b.Username, &b.State, &b.LastError, &b.HealthPort)
	if errors.Is(err, pgx.ErrNoRows) {
		code, message, classifyErr := classifyComputerBindingConflict(r.Context(), tx, m.ID, in.Username, uid, in.WorkspaceID)
		if classifyErr != nil {
			writeError(w, 500, "Cannot check Linux User availability")
			return
		}
		writeErrorCode(w, 409, code, message)
		return
	}
	if err != nil {
		writeError(w, 500, "Cannot start operation")
		return
	}
	operationID, err := insertComputerOperation(r.Context(), tx, uid, b.ID, in.Action, "", "")
	if err != nil {
		operationStartError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		writeError(w, 500, "Cannot start operation")
		return
	}
	remote := computer.SSHRemote{Host: m.Host, Port: m.Port, User: m.SSHUser, KeyPath: keyPath, BinaryPath: binary, DaemonID: b.ID, HealthPort: b.HealthPort, WorkspaceID: b.WorkspaceID, Timeout: 90 * time.Second}
	req := computer.Request{ComputerID: m.ID, ServerURL: serverURL, Username: in.Username, Password: in.Password, GitName: settings.GitName, GitEmail: settings.GitEmail, GitLabURL: settings.GitLabURL, GitLabToken: settings.GitLabToken, GitSSHKey: settings.GitSSHKey, GitKnownHosts: settings.GitKnownHosts, ModelEnv: settings.ModelEnv, MulticaPAT: settings.MulticaPAT, FailureLimit: 5, AccountOnly: in.Action == "create_account", PreserveFiles: in.Action == "upgrade"}
	action := in.Action
	go h.runRemoteOperation(operationID, func(ctx context.Context) (string, error) {
		remote.Context = ctx
		req.Step = func(step string) { h.operationStep(operationID, step) }
		return "", h.runComputerOperation(ctx, operationID, uid, b, remote, &computer.FileStore{Dir: lockDir}, req, action)
	})
	writeJSON(w, 202, b)
}

func classifyComputerBindingConflict(ctx context.Context, tx pgx.Tx, computerID, username, uid, workspaceID string) (string, string, error) {
	var ownerID, boundWorkspaceID, state string
	var verified, busy, expired bool
	err := tx.QueryRow(ctx, `SELECT user_id::text,COALESCE(workspace_id::text,''),state,verified,EXISTS(SELECT 1 FROM computer_operation o WHERE o.binding_id=computer_binding.id AND o.state IN ('queued','running') AND o.deadline_at>=now()),EXISTS(SELECT 1 FROM computer_operation o WHERE o.binding_id=computer_binding.id AND o.state IN ('queued','running') AND o.deadline_at<now()) FROM computer_binding WHERE computer_id=$1 AND username=$2`, computerID, username).Scan(&ownerID, &boundWorkspaceID, &state, &verified, &busy, &expired)
	if errors.Is(err, pgx.ErrNoRows) {
		return "binding_conflict", "Linux User availability changed; retry.", nil
	}
	if err != nil {
		return "", "", err
	}
	if ownerID != uid && verified {
		return "username_unavailable", "This Linux username is unavailable on this Computer.", nil
	}
	if expired {
		if ownerID == uid {
			return "operation_recovery_required", "Review and acknowledge the interrupted operation in My environments before retrying.", nil
		}
		return "binding_conflict", "Linux User availability changed; retry.", nil
	}
	if busy || state == "running" {
		return "operation_busy", "Another operation is active for this Linux user.", nil
	}
	if !verified {
		return "binding_conflict", "Linux User availability changed; retry.", nil
	}
	if ownerID == uid && boundWorkspaceID != workspaceID && state != "removed" && state != "detached" {
		return "binding_workspace_conflict", "Remove this Linux User's managed daemon from its current workspace before reconnecting it.", nil
	}
	return "binding_conflict", "Linux User availability changed; retry.", nil
}

func (h *Handler) runComputerOperation(ctx context.Context, operationID, uid string, b computerBinding, remote computer.SSHRemote, store *computer.FileStore, req computer.Request, action string) (operationErr error) {
	state := "failed"
	defer func() {
		if recover() != nil {
			logOperationPanic(operationID)
			operationErr = operationFailure("interrupted")
		}
		message := ""
		if operationErr != nil {
			code := computer.ErrorCode(operationErr, "remote_step_failed")
			var coded *computerOperationError
			if errors.As(operationErr, &coded) {
				code = coded.code
			}
			message = computer.ErrorSummary(code)
		}
		finish, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		err := h.withRunningOperation(finish, operationID, func(tx pgx.Tx) error {
			tag, err := tx.Exec(finish, `UPDATE computer_binding SET state=$2,last_error=$3,updated_at=now(),daemon_state=CASE WHEN $2='ready' THEN 'running' WHEN $2='removed' THEN 'stopped' ELSE daemon_state END,daemon_checked_at=CASE WHEN $2 IN ('ready','removed') THEN now() ELSE daemon_checked_at END WHERE id=$1 AND state='running'`, b.ID, state, message)
			if err == nil && tag.RowsAffected() != 1 {
				return errOperationSuperseded
			}
			return err
		})
		if err != nil {
			if !errors.Is(err, errOperationSuperseded) {
				logOperationDBError("Cannot finish binding operation", operationID, err)
			}
			if operationErr == nil {
				operationErr = err
			}
		}
	}()

	if action == "remove" {
		err := store.WithKey(b.ComputerID, b.Username, func(a computer.Attempt) error {
			_, allowed, err := a.Reserve(5)
			if err != nil {
				return err
			}
			if !allowed {
				return errors.New("locked")
			}
			ok, err := remote.PasswordMatches(b.Username, req.Password)
			if err != nil {
				_ = a.Refund()
				return err
			}
			if !ok {
				return errors.New("password mismatch")
			}
			if err = a.Reset(); err != nil {
				return err
			}
			return remote.RemoveDaemon(b.Username)
		})
		if err == nil {
			state = "removed"
		}
		return err
	}
	if action == "upgrade" {
		req.AfterAuthenticate = func() error {
			if req.Step != nil {
				req.Step("writing_configuration")
			}
			return h.ensureComputerDaemonCredentials(ctx, uid, b, remote, req.ServerURL)
		}
	}
	result, err := computer.Apply(remote, store, req)
	if err != nil {
		return err
	}
	if result.Action == computer.ActionLocked {
		return operationFailure("password_locked")
	}
	if result.Action == computer.ActionRename {
		return operationFailure("password_mismatch")
	}
	if err := h.withRunningOperation(ctx, operationID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE computer_binding SET verified=true,account_state='present',checked_at=now() WHERE id=$1`, b.ID)
		return err
	}); err != nil {
		return err
	}

	if req.AccountOnly {
		state = "pending"
		return nil
	}
	if req.Step != nil {
		req.Step("waiting_registration")
	}
	// A live registration by this exact user/workspace/daemon is the success gate.
	deadline := time.NewTimer(90 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		var online bool
		err := h.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM agent_runtime WHERE daemon_id=$1 AND owner_id=$2 AND workspace_id=$3 AND status='online' AND last_seen_at>now()-interval '30 seconds')`, b.ID, uid, b.WorkspaceID).Scan(&online)
		if err == nil && online {
			state = "ready"
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return operationFailure("daemon_offline")
		case <-ticker.C:
		}
	}
}

// A failed, unverified attempt is not ownership of an OS account. It may be
// retried by another human, who must still pass PAM. Verified ownership never
// transfers, including after failure or uninstall. Running jobs never overlap.
const claimComputerBindingSQL = `
INSERT INTO computer_binding(computer_id,user_id,workspace_id,username,state,health_port)
VALUES($1,$2,$3,$4,'running',CASE
  WHEN EXISTS (SELECT 1 FROM computer_binding WHERE computer_id=$1 AND username=$4)
  THEN (SELECT health_port FROM computer_binding WHERE computer_id=$1 AND username=$4)
  ELSE nextval('computer_health_port_seq') END)
ON CONFLICT(computer_id,username) DO UPDATE SET
 user_id=EXCLUDED.user_id,
 workspace_id=CASE
  WHEN NOT computer_binding.verified
    OR ($5 IN ('provision','create_account') AND computer_binding.state IN ('removed','detached'))
  THEN $3 ELSE computer_binding.workspace_id END,
 health_port=CASE WHEN computer_binding.state='failed'
  THEN EXCLUDED.health_port ELSE computer_binding.health_port END,
 state='running',last_error='',archived_at=NULL,updated_at=now()
WHERE (
 (computer_binding.user_id=$2 AND
  (computer_binding.workspace_id=$3 OR $5='remove' OR computer_binding.state IN ('removed','detached')))
 OR (NOT computer_binding.verified AND computer_binding.state='failed' AND $5 IN ('provision','create_account'))
) AND (computer_binding.state<>'running' OR computer_binding.updated_at<now()-interval '20 minutes')
  AND NOT ($5='create_account' AND computer_binding.verified AND computer_binding.state='ready')
  AND NOT EXISTS (SELECT 1 FROM computer_operation o WHERE o.binding_id=computer_binding.id AND o.state IN ('queued','running'))
RETURNING id::text,computer_id::text,COALESCE(workspace_id::text,''),username,state,last_error,health_port
`
