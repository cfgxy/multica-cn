package handler

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"net/http"
	"os"
	"time"

	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/computer"
)

func (h *Handler) ReadComputerCredentials(w http.ResponseWriter, r *http.Request) {
	h.computerCredentials(w, r, false)
}
func (h *Handler) WriteComputerCredentials(w http.ResponseWriter, r *http.Request) {
	h.computerCredentials(w, r, true)
}

// Credential transfer is owner-only, authenticated against the Linux account,
// and serialized with all other account operations. Secrets are returned only
// to the requesting human, never stored in operation or audit payloads.
func (h *Handler) computerCredentials(w http.ResponseWriter, r *http.Request, writing bool) {
	w.Header().Set("Cache-Control", "no-store")
	uid, id, ok := h.bindingAccess(w, r, false)
	if !ok {
		return
	}
	var in struct {
		Password string            `json:"password"`
		Settings computer.Settings `json:"settings"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 131072)).Decode(&in) != nil || in.Password == "" || len(in.Password) > 4096 {
		writeError(w, 400, "Linux password is required")
		return
	}
	if writing {
		if err := in.Settings.Validate(); err != nil {
			writeError(w, 400, err.Error())
			return
		}
		if in.Settings.MulticaPAT != "" {
			pat, err := h.Queries.GetPersonalAccessTokenByHash(r.Context(), auth.HashToken(in.Settings.MulticaPAT))
			if err != nil || uuidToString(pat.UserID) != uid {
				writeError(w, 400, "Multica token must be valid and belong to you")
				return
			}
		}
	}
	var m computer.Machine
	var user, workspace string
	var port int
	err := h.DB.QueryRow(r.Context(), `SELECT c.id::text,c.host,c.port,c.ssh_user,b.username,COALESCE(b.workspace_id::text,''),b.health_port FROM computer_binding b JOIN computer c ON c.id=b.computer_id WHERE b.id=$1 AND b.user_id=$2 AND b.verified AND b.archived_at IS NULL`, id, uid).Scan(&m.ID, &m.Host, &m.Port, &m.SSHUser, &user, &workspace, &port)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 409, "Verify this Linux account before transferring credentials")
		return
	}
	if err != nil {
		writeError(w, 500, "Cannot read Linux User binding")
		return
	}
	key, locks := os.Getenv("MULTICA_COMPUTER_SSH_KEY"), os.Getenv("MULTICA_COMPUTER_STATE_DIR")
	if key == "" || locks == "" || (writing && os.Getenv("MULTICA_COMPUTER_SERVER_URL") == "") {
		writeError(w, 503, "Computer provisioning is not configured")
		return
	}
	kind := "credentials_read"
	if writing {
		kind = "credentials_write"
	}
	op, err := h.beginBindingOperation(r.Context(), uid, id, kind, "", "")
	if err != nil {
		operationStartError(w, err)
		return
	}
	var settings computer.Settings
	var transferErr error
	completed := false
	h.runRemoteOperation(op, func(ctx context.Context) (string, error) {
		ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
		defer cancel()
		remote := computer.SSHRemote{Host: m.Host, Port: m.Port, User: m.SSHUser, KeyPath: key, DaemonID: id, WorkspaceID: workspace, HealthPort: port, Context: ctx, Timeout: 30 * time.Second}
		transferErr = computer.WithPassword(remote, &computer.FileStore{Dir: locks}, m.ID, user, in.Password, func() error {
			var err error
			h.operationStep(op, "reading_credentials")
			settings, err = remote.ReadSettings(user)
			if err != nil {
				return err
			}
			if !writing {
				return nil
			}
			settings = computer.MergeSettings(settings, in.Settings)
			if err = settings.Validate(); err != nil {
				return operationFailure("credential_transfer_failed")
			}
			files, err := computer.RenderFiles(os.Getenv("MULTICA_COMPUTER_SERVER_URL"), settings.GitName, settings.GitEmail, settings.GitLabURL, settings.GitLabToken, settings.ModelEnv, settings.MulticaPAT)
			if err != nil {
				return operationFailure("credential_transfer_failed")
			}
			files.GitSSHKey, files.GitKnownHosts = settings.GitSSHKey, settings.GitKnownHosts
			if settings.GitSSHKey != "" {
				files.Gitconfig += "[core]\n\tsshCommand = \"ssh -i ~/.config/multica-provision/git.key -o IdentitiesOnly=yes -o StrictHostKeyChecking=yes -o UserKnownHostsFile=~/.config/multica-provision/known_hosts\"\n"
			}
			h.operationStep(op, "writing_configuration")
			return remote.WriteFiles(user, files)
		})
		completed = true
		return "", transferErr
	})
	if !completed || transferErr != nil {
		code := computer.ErrorCode(transferErr, "credential_transfer_failed")
		var coded *computerOperationError
		if errors.As(transferErr, &coded) {
			code = coded.code
		}
		if !completed {
			code = "interrupted"
		}
		status := http.StatusBadGateway
		switch code {
		case "password_mismatch":
			status = http.StatusForbidden
		case "password_locked":
			status = http.StatusTooManyRequests
		case "account_unavailable", "account_missing", "credential_transfer_failed":
			status = http.StatusUnprocessableEntity
		case "ssh_timeout":
			status = http.StatusGatewayTimeout
		}
		writeJSON(w, status, map[string]string{"code": code, "error": computer.ErrorSummary(code)})
		return
	}
	if writing {
		writeJSON(w, 200, map[string]bool{"saved": true})
		return
	}
	writeJSON(w, 200, map[string]any{"operator": false, "settings": settings})
}
