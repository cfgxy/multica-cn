package handler

import (
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/computer"
	"github.com/multica-ai/multica/server/internal/util/secretbox"
	"net/http"
)

// Computer endpoints are mounted behind human authentication. Operator rights
// are deployment-scoped, deliberately independent of workspace admin roles.
func (h *Handler) ComputerSettings(w http.ResponseWriter, r *http.Request) {
	uid, ok := requireUserID(w, r)
	if !ok {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
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
	operator := isInstanceAdmin(uid)
	switch r.Method {
	case http.MethodGet:
		var encrypted []byte
		err := h.DB.QueryRow(r.Context(), "SELECT ciphertext FROM computer_credential WHERE user_id=$1", uid).Scan(&encrypted)
		settings := computer.Settings{}
		if err == nil {
			plain, err := box.Open(encrypted)
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
		} else if !errors.Is(err, pgx.ErrNoRows) {
			writeError(w, 500, "Cannot read settings")
			return
		}
		writeJSON(w, 200, map[string]any{"operator": operator, "settings": settings})
	case http.MethodPut:
		var settings computer.Settings
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 65536)).Decode(&settings) != nil {
			writeError(w, 400, "Invalid settings")
			return
		}
		if err := settings.Validate(); err != nil {
			writeError(w, 400, err.Error())
			return
		}
		if settings.MulticaPAT != "" {
			pat, err := h.Queries.GetPersonalAccessTokenByHash(r.Context(), auth.HashToken(settings.MulticaPAT))
			if err != nil || uuidToString(pat.UserID) != uid {
				writeError(w, 400, "Multica token must be valid and belong to you")
				return
			}
		}
		plain, _ := json.Marshal(map[string]any{"owner": uid, "settings": settings})
		encrypted, err := box.Seal(plain)
		if err != nil {
			writeError(w, 500, "Cannot encrypt settings")
			return
		}
		_, err = h.DB.Exec(r.Context(), `INSERT INTO computer_credential(user_id,ciphertext) VALUES($1,$2) ON CONFLICT(user_id) DO UPDATE SET ciphertext=EXCLUDED.ciphertext,updated_at=now()`, uid, encrypted)
		if err != nil {
			writeError(w, 500, "Cannot save settings")
			return
		}
		writeJSON(w, 200, map[string]bool{"saved": true})
	}
}

func (h *Handler) Computers(w http.ResponseWriter, r *http.Request) {
	uid, ok := requireUserID(w, r)
	if !ok {
		return
	}
	operator := isInstanceAdmin(uid)
	if r.Method == http.MethodPost {
		h.AdminComputers(w, r)
		return
	}
	rows, err := h.DB.Query(r.Context(), `SELECT c.id::text,c.name,c.host,c.port,c.ssh_user,c.enabled FROM computer c WHERE c.enabled OR EXISTS(SELECT 1 FROM computer_binding b WHERE b.computer_id=c.id AND b.user_id=$1 AND b.verified) ORDER BY c.name`, uid)
	if err != nil {
		writeError(w, 500, "Cannot list Computers")
		return
	}
	defer rows.Close()
	list := []adminComputer{}
	for rows.Next() {
		var m adminComputer
		if rows.Scan(&m.ID, &m.Name, &m.Host, &m.Port, &m.SSHUser, &m.Enabled) != nil {
			writeError(w, 500, "Cannot read Computer")
			return
		}
		list = append(list, m)
	}
	if rows.Err() != nil {
		writeError(w, 500, "Cannot list Computers")
		return
	}
	if !operator {
		// A human only needs the stable id and display name to choose a
		// Computer. Keep bastion host, port, and SSH operator details private.
		for i := range list {
			list[i].Host, list[i].Port, list[i].SSHUser = "", 0, ""
		}
	}
	writeJSON(w, 200, list)
}
