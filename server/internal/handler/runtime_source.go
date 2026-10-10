package handler

import "context"

// RuntimeExecutionSource identifies the managed OS account that executes a CLI.
// It deliberately contains no authentication material.
type RuntimeExecutionSource struct {
	BindingID string `json:"binding_id"`
	LinuxUser string `json:"linux_user"`
	Host      string `json:"host"`
	Preferred bool   `json:"preferred"`
}

func (h *Handler) enrichRuntimeSources(ctx context.Context, workspaceID string, runtimes []AgentRuntimeResponse) error {
	if len(runtimes) == 0 {
		return nil
	}
	ids := make([]string, len(runtimes))
	for i := range runtimes {
		ids[i] = runtimes[i].ID
	}
	rows, err := h.DB.Query(ctx, `SELECT rt.id::text,b.id::text,b.username,c.host,
        COALESCE(b.workspace_id=rt.workspace_id AND b.archived_at IS NULL,false)
        FROM agent_runtime rt
        JOIN computer_binding b ON b.id::text=rt.daemon_id AND b.user_id=rt.owner_id
        JOIN computer c ON c.id=b.computer_id
        WHERE rt.workspace_id=$1 AND rt.id::text=ANY($2::text[])`, workspaceID, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	sources := make(map[string]*RuntimeExecutionSource)
	for rows.Next() {
		var id string
		var source RuntimeExecutionSource
		if err := rows.Scan(&id, &source.BindingID, &source.LinuxUser, &source.Host, &source.Preferred); err != nil {
			return err
		}
		sources[id] = &source
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for i := range runtimes {
		runtimes[i].ExecutionSource = sources[runtimes[i].ID]
	}
	return nil
}
