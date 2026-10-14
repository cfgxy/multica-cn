package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/multica-ai/multica/server/internal/computer"
)

type bindingDetail struct {
	DaemonCheckedAt *time.Time `json:"daemon_checked_at"`
	computerBinding
	ComputerName string     `json:"computer_name"`
	DaemonID     string     `json:"daemon_id"`
	AccountState string     `json:"account_state"`
	DaemonState  string     `json:"daemon_state"`
	CheckedAt    *time.Time `json:"checked_at"`
	ArchivedAt   *time.Time `json:"archived_at"`
	LastSeenAt   *time.Time `json:"last_seen_at"`
}

func (h *Handler) ComputerBindingDetail(w http.ResponseWriter, r *http.Request) {
	h.bindingDetail(w, r, false)
}
func (h *Handler) AdminBindingDetail(w http.ResponseWriter, r *http.Request) {
	h.bindingDetail(w, r, true)
}
func (h *Handler) bindingDetail(w http.ResponseWriter, r *http.Request, admin bool) {
	uid, id, ok := h.bindingAccess(w, r, admin)
	if !ok {
		return
	}
	var d bindingDetail
	var op bindingOperationSummary
	err := h.DB.QueryRow(r.Context(), `SELECT b.id::text,b.computer_id::text,COALESCE(b.workspace_id::text,''),b.username,
 CASE WHEN b.state='running' AND EXISTS(SELECT 1 FROM computer_operation o WHERE o.binding_id=b.id AND o.state IN ('queued','running') AND o.deadline_at<now()) THEN 'interrupted' ELSE b.state END,b.last_error,b.verified,
 COALESCE(c.name,b.computer_id::text),COALESCE(w.name,''),CASE WHEN b.workspace_id IS NULL THEN 'none' WHEN w.id IS NOT NULL THEN 'accessible' ELSE 'unavailable' END,b.account_state,b.checked_at,b.archived_at,
 (SELECT max(last_seen_at) FROM agent_runtime WHERE daemon_id=b.id::text AND owner_id=b.user_id AND workspace_id=b.workspace_id),
 b.daemon_state,b.daemon_checked_at,
 EXISTS(SELECT 1 FROM computer_operation o WHERE o.binding_id=b.id AND o.state IN ('queued','running') AND o.deadline_at>=now()),
 COALESCE(op.id::text,''),COALESCE(op.kind,''),COALESCE(op.effective_state,''),COALESCE(op.error_code,''),op.finished_at
 FROM computer_binding b LEFT JOIN computer c ON c.id=b.computer_id LEFT JOIN workspace w ON w.id=b.workspace_id AND ($3::boolean OR EXISTS(SELECT 1 FROM member m WHERE m.workspace_id=w.id AND m.user_id=$2))
 LEFT JOIN LATERAL (SELECT id,kind,finished_at,CASE WHEN state IN ('queued','running') AND deadline_at<now() THEN 'interrupted' ELSE state END AS effective_state,error_code FROM computer_operation WHERE binding_id=b.id ORDER BY created_at DESC,id DESC LIMIT 1) op ON true
 WHERE b.id=$1`, id, uid, admin).Scan(&d.ID, &d.ComputerID, &d.WorkspaceID, &d.Username, &d.State, &d.LastError, &d.Verified, &d.ComputerName, &d.WorkspaceName, &d.WorkspaceAccess, &d.AccountState, &d.CheckedAt, &d.ArchivedAt, &d.LastSeenAt, &d.DaemonState, &d.DaemonCheckedAt, &d.OperationBusy, &op.ID, &op.Kind, &op.State, &op.ErrorCode, &op.FinishedAt)
	if err != nil {
		writeError(w, 500, "Cannot read Linux User details")
		return
	}
	if op.ID != "" {
		d.LatestOperation = &op
	}
	d.DaemonID = d.ID
	writeJSON(w, 200, d)
}

// Lifecycle actions do not require membership in a workspace that may have
// been deleted. Verified account ownership remains mandatory.
func (h *Handler) ComputerBindingLifecycle(w http.ResponseWriter, r *http.Request) {
	uid, id, ok := h.bindingAccess(w, r, false)
	if !ok {
		return
	}
	var in struct {
		Action          string `json:"action"`
		Password        string `json:"password"`
		ConfirmUsername string `json:"confirm_username"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
	dec.DisallowUnknownFields()
	if dec.Decode(&in) != nil || (in.Action != "remove" && in.Action != "delete_user" && in.Action != "archive") {
		writeError(w, 400, "Invalid lifecycle action")
		return
	}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, 500, "Cannot start lifecycle action")
		return
	}
	defer tx.Rollback(r.Context())
	var b computerBinding
	var verified, active, archived bool
	var account string
	err = tx.QueryRow(r.Context(), `SELECT id::text,computer_id::text,COALESCE(workspace_id::text,''),username,state,verified,account_state,EXISTS(SELECT 1 FROM computer_operation WHERE binding_id=$1 AND state IN ('queued','running')),archived_at IS NOT NULL FROM computer_binding WHERE id=$1 AND user_id=$2 FOR UPDATE`, id, uid).Scan(&b.ID, &b.ComputerID, &b.WorkspaceID, &b.Username, &b.State, &verified, &account, &active, &archived)
	if err != nil {
		writeError(w, 500, "Cannot read binding")
		return
	}
	if archived {
		writeError(w, 409, "Re-provision an archived binding before changing its remote account")
		return
	}
	if active || b.State == "running" {
		operationStartError(w, errOperationBusy)
		return
	}
	if in.ConfirmUsername != b.Username {
		writeError(w, 400, "Type the Linux username to confirm this action")
		return
	}
	if in.Action == "archive" {
		if b.State != "removed" {
			writeError(w, 409, "Remove the managed daemon before archiving this binding")
			return
		}
		_, err = tx.Exec(r.Context(), `UPDATE computer_binding SET archived_at=now(),updated_at=now() WHERE id=$1`, id)
		if err == nil {
			_, err = tx.Exec(r.Context(), `INSERT INTO computer_audit(user_id,binding_id,action,outcome) VALUES($1,$2,'archive','succeeded')`, uid, id)
		}
		if err == nil {
			err = tx.Commit(r.Context())
		}
		if err != nil {
			writeError(w, 500, "Cannot archive binding")
			return
		}
		writeJSON(w, 200, map[string]bool{"saved": true})
		return
	}
	if !verified {
		writeError(w, 409, "Account ownership has not been verified")
		return
	}
	if in.Action == "delete_user" && b.State != "removed" {
		writeError(w, 409, "Remove the managed daemon before deleting the Linux user")
		return
	}
	if len(in.Password) < 1 || len(in.Password) > 4096 {
		writeError(w, 400, "Linux password is required")
		return
	}
	var m computer.Machine
	err = tx.QueryRow(r.Context(), `SELECT id::text,host,port,ssh_user FROM computer WHERE id=$1 FOR SHARE`, b.ComputerID).Scan(&m.ID, &m.Host, &m.Port, &m.SSHUser)
	if err != nil {
		writeError(w, 404, "Computer unavailable")
		return
	}
	key, dir := os.Getenv("MULTICA_COMPUTER_SSH_KEY"), os.Getenv("MULTICA_COMPUTER_STATE_DIR")
	if key == "" || dir == "" {
		writeError(w, 503, "Computer provisioning is not configured")
		return
	}
	if computer.ValidateLinuxUsername(b.Username) != nil || b.Username == m.SSHUser || b.Username == "root" {
		writeError(w, 403, "Protected Linux account")
		return
	}
	op, err := insertComputerOperation(r.Context(), tx, uid, id, in.Action, "", "")
	if err != nil {
		operationStartError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		writeError(w, 500, "Cannot save operation")
		return
	}
	go h.runRemoteOperation(op, func(ctx context.Context) (string, error) {
		remote := computer.SSHRemote{Host: m.Host, Port: m.Port, User: m.SSHUser, KeyPath: key, DaemonID: id, Timeout: 90 * time.Second, Context: ctx}
		store := computer.FileStore{Dir: dir}
		err := store.WithKey(m.ID, b.Username, func(a computer.Attempt) error {
			_, allowed, err := a.Reserve(5)
			if err != nil {
				return err
			}
			if !allowed {
				return operationFailure("password_locked")
			}
			exists, err := remote.UserExists(b.Username)
			if err != nil {
				_ = a.Refund()
				return err
			}
			if exists {
				match, err := remote.PasswordMatches(b.Username, in.Password)
				if err != nil {
					_ = a.Refund()
					return err
				}
				if !match {
					return operationFailure("password_mismatch")
				}
				if err = a.Reset(); err != nil {
					return err
				}
			} else {
				_ = a.Refund()
			}
			if in.Action == "delete_user" {
				h.operationStep(op, "deleting_account")
				// userdel without --force rejects accounts with live processes. No task
				// credentials or running process is killed to make deletion succeed.
				if exists {
					if err = remote.DeleteUser(b.Username); err != nil {
						return operationFailure("account_delete_failed")
					}
				}
				err = h.withRunningOperation(ctx, op, func(tx pgx.Tx) error {
					_, err := tx.Exec(ctx, `UPDATE computer_binding SET account_state='missing',checked_at=now(),updated_at=now() WHERE id=$1`, id)
					if err == nil {
						_, err = tx.Exec(ctx, `DELETE FROM computer_runtime_model_config WHERE binding_id=$1`, id)
					}
					return err
				})
				return err
			}
			h.operationStep(op, "removing_daemon")
			if err = remote.RemoveDaemon(b.Username); err != nil {
				return err
			}
			err = h.withRunningOperation(ctx, op, func(tx pgx.Tx) error {
				_, err := tx.Exec(ctx, `UPDATE computer_binding SET state='removed',daemon_state='stopped',daemon_checked_at=now(),last_error='',updated_at=now() WHERE id=$1`, id)
				return err
			})
			return err
		})
		return "", err
	})
	writeJSON(w, 202, map[string]string{"operation_id": op, "state": "queued"})
}
