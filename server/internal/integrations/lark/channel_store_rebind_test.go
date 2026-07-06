package lark

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Rebind regression fixtures. Namespaced away from the scope test's ids so a
// shared test database never cross-contaminates. channel_* has no foreign keys,
// so these rows need no parent records; the test cleans up by deterministic key
// before and after (a killed prior run must not leave colliding rows behind).
const (
	rbWS        = "5c09e100-0000-4000-8000-000000000001"
	rbWS2       = "5c09e100-0000-4000-8000-000000000002"
	rbAgentA    = "5c09e100-0000-4000-8000-00000000000a"
	rbAgentB    = "5c09e100-0000-4000-8000-00000000000b"
	rbInstaller = "5c09e100-0000-4000-8000-000000000005"
	rbUser      = "5c09e100-0000-4000-8000-000000000006"
	rbChatSess  = "5c09e100-0000-4000-8000-000000000007"

	rbAppSame       = "cli_rb_same"
	rbAppDiff       = "cli_rb_diff"
	rbAppActive     = "cli_rb_active"
	rbAppWsFence    = "cli_rb_wsfence"
	rbAppReactivate = "cli_rb_reactivate"
	rbAppMove       = "cli_rb_move"
)

// TestChannelStore_RemoveRevokedInstallationByAppID guards the WHERE clause of
// DeleteChannelInstallationByAppID: it must delete ONLY a revoked row that
// belongs to a DIFFERENT agent in the SAME workspace. The same agent's own
// revoked row, any active row, and rows in another workspace must survive.
func TestChannelStore_RemoveRevokedInstallationByAppID(t *testing.T) {
	pool := channelScopeTestDB(t)
	ctx := context.Background()
	store := NewChannelStore(db.New(pool))

	apps := []string{rbAppSame, rbAppDiff, rbAppActive, rbAppWsFence, rbAppReactivate, rbAppMove}
	clean := func() {
		_, _ = pool.Exec(ctx, `DELETE FROM channel_installation WHERE config->>'app_id' = ANY($1)`, apps)
		_, _ = pool.Exec(ctx, `DELETE FROM channel_user_binding WHERE multica_user_id = $1`, rbUser)
		_, _ = pool.Exec(ctx, `DELETE FROM channel_chat_session_binding WHERE chat_session_id = $1`, rbChatSess)
	}
	clean()
	t.Cleanup(clean)

	// insert an installation and return its id.
	insert := func(app, ws, agent, status string) pgtype.UUID {
		var id string
		if err := pool.QueryRow(ctx, `
INSERT INTO channel_installation (workspace_id, agent_id, channel_type, config, installer_user_id, status)
VALUES ($1, $2, 'feishu', jsonb_build_object('app_id', $3::text), $4, $5)
RETURNING id
`, ws, agent, app, rbInstaller, status).Scan(&id); err != nil {
			t.Fatalf("insert installation app=%s status=%s: %v", app, status, err)
		}
		return util.MustParseUUID(id)
	}
	exists := func(id pgtype.UUID) bool {
		_, err := store.GetLarkInstallation(ctx, id)
		if err == nil {
			return true
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return false
		}
		t.Fatalf("GetLarkInstallation: %v", err)
		return false
	}

	wsUUID := util.MustParseUUID(rbWS)
	agentAUUID := util.MustParseUUID(rbAgentA)
	agentBUUID := util.MustParseUUID(rbAgentB)

	t.Run("same agent revoked row is preserved", func(t *testing.T) {
		clean()
		id := insert(rbAppSame, rbWS, rbAgentA, "revoked")
		if err := store.RemoveRevokedInstallationByAppID(ctx, wsUUID, agentAUUID, rbAppSame); err != nil {
			t.Fatalf("RemoveRevokedInstallationByAppID: %v", err)
		}
		if !exists(id) {
			t.Fatal("same agent's own revoked row was deleted; it must be reactivated in place by the upsert, not orphaned")
		}
	})

	t.Run("different agent revoked row is deleted", func(t *testing.T) {
		clean()
		id := insert(rbAppDiff, rbWS, rbAgentA, "revoked")
		if err := store.RemoveRevokedInstallationByAppID(ctx, wsUUID, agentBUUID, rbAppDiff); err != nil {
			t.Fatalf("RemoveRevokedInstallationByAppID: %v", err)
		}
		if exists(id) {
			t.Fatal("a different agent's revoked row was not deleted; it would keep blocking the app_id unique slot")
		}
	})

	t.Run("active row is never deleted", func(t *testing.T) {
		clean()
		id := insert(rbAppActive, rbWS, rbAgentA, "active")
		if err := store.RemoveRevokedInstallationByAppID(ctx, wsUUID, agentBUUID, rbAppActive); err != nil {
			t.Fatalf("RemoveRevokedInstallationByAppID: %v", err)
		}
		if !exists(id) {
			t.Fatal("an active installation was deleted through the revoked-cleanup path")
		}
	})

	t.Run("other workspace revoked row is preserved", func(t *testing.T) {
		clean()
		id := insert(rbAppWsFence, rbWS2, rbAgentA, "revoked")
		if err := store.RemoveRevokedInstallationByAppID(ctx, wsUUID, agentBUUID, rbAppWsFence); err != nil {
			t.Fatalf("RemoveRevokedInstallationByAppID: %v", err)
		}
		if !exists(id) {
			t.Fatal("a revoked row in another workspace was deleted; the delete must stay workspace-scoped")
		}
	})
}

// TestChannelStore_ReinstallReactivationSemantics exercises the full
// finishSuccess ordering (cleanup-then-upsert) against a real database and
// pins the product behavior the fix protects:
//
//   - SAME agent reconnect: the revoked row is reactivated in place, keeping its
//     installation_id and every member/chat binding hanging off it.
//   - DIFFERENT agent rebind: a fresh installation_id is created and the old
//     agent's revoked row is removed so it no longer blocks the app_id slot.
func TestChannelStore_ReinstallReactivationSemantics(t *testing.T) {
	pool := channelScopeTestDB(t)
	ctx := context.Background()
	store := NewChannelStore(db.New(pool))

	apps := []string{rbAppReactivate, rbAppMove}
	clean := func() {
		_, _ = pool.Exec(ctx, `DELETE FROM channel_installation WHERE config->>'app_id' = ANY($1)`, apps)
		_, _ = pool.Exec(ctx, `DELETE FROM channel_user_binding WHERE multica_user_id = $1`, rbUser)
		_, _ = pool.Exec(ctx, `DELETE FROM channel_chat_session_binding WHERE chat_session_id = $1`, rbChatSess)
	}
	clean()
	t.Cleanup(clean)

	insertRevoked := func(app, agent string) pgtype.UUID {
		var id string
		if err := pool.QueryRow(ctx, `
INSERT INTO channel_installation (workspace_id, agent_id, channel_type, config, installer_user_id, status)
VALUES ($1, $2, 'feishu', jsonb_build_object('app_id', $3::text), $4, 'revoked')
RETURNING id
`, rbWS, agent, app, rbInstaller).Scan(&id); err != nil {
			t.Fatalf("insert revoked installation: %v", err)
		}
		return util.MustParseUUID(id)
	}
	// Attach a member binding + chat-session binding to an installation, the way
	// a real workspace accumulates them while the bot is connected.
	attachBindings := func(installID pgtype.UUID) {
		if _, err := pool.Exec(ctx, `
INSERT INTO channel_user_binding (workspace_id, multica_user_id, installation_id, channel_type, channel_user_id)
VALUES ($1, $2, $3, 'feishu', 'ou_rb_user')
`, rbWS, rbUser, installID); err != nil {
			t.Fatalf("insert user binding: %v", err)
		}
		if _, err := pool.Exec(ctx, `
INSERT INTO channel_chat_session_binding (chat_session_id, installation_id, channel_type, channel_chat_id, chat_type)
VALUES ($1, $2, 'feishu', 'oc_rb_chat', 'p2p')
`, rbChatSess, installID); err != nil {
			t.Fatalf("insert chat-session binding: %v", err)
		}
	}
	countBindingsOn := func(installID pgtype.UUID) (users, chats int) {
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM channel_user_binding WHERE installation_id = $1`, installID).Scan(&users); err != nil {
			t.Fatalf("count user bindings: %v", err)
		}
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM channel_chat_session_binding WHERE installation_id = $1`, installID).Scan(&chats); err != nil {
			t.Fatalf("count chat bindings: %v", err)
		}
		return
	}

	upsert := func(agent, app string) Installation {
		inst, err := store.UpsertLarkInstallation(ctx, UpsertInstallationParams{
			WorkspaceID:        util.MustParseUUID(rbWS),
			AgentID:            util.MustParseUUID(agent),
			AppID:              app,
			AppSecretEncrypted: []byte{1, 2, 3},
			BotOpenID:          "ou_rb_bot",
			InstallerUserID:    util.MustParseUUID(rbInstaller),
			Region:             "feishu",
		})
		if err != nil {
			t.Fatalf("UpsertLarkInstallation: %v", err)
		}
		return inst
	}

	t.Run("same agent reconnect keeps installation_id and bindings", func(t *testing.T) {
		clean()
		oldID := insertRevoked(rbAppReactivate, rbAgentA)
		attachBindings(oldID)

		// finishSuccess order: cleanup for the current agent (a no-op for the
		// same agent), then upsert.
		if err := store.RemoveRevokedInstallationByAppID(ctx, util.MustParseUUID(rbWS), util.MustParseUUID(rbAgentA), rbAppReactivate); err != nil {
			t.Fatalf("cleanup: %v", err)
		}
		inst := upsert(rbAgentA, rbAppReactivate)

		if inst.ID != oldID {
			t.Fatalf("same agent reconnect changed installation_id: got %v, want %v (in-place reactivation lost)", inst.ID, oldID)
		}
		if inst.Status != "active" {
			t.Fatalf("reactivated installation status=%q, want active", inst.Status)
		}
		if users, chats := countBindingsOn(oldID); users != 1 || chats != 1 {
			t.Fatalf("bindings not preserved on reconnect: users=%d chats=%d, want 1/1", users, chats)
		}
	})

	t.Run("different agent rebind gets a fresh installation_id", func(t *testing.T) {
		clean()
		oldID := insertRevoked(rbAppMove, rbAgentA)
		attachBindings(oldID)

		if err := store.RemoveRevokedInstallationByAppID(ctx, util.MustParseUUID(rbWS), util.MustParseUUID(rbAgentB), rbAppMove); err != nil {
			t.Fatalf("cleanup: %v", err)
		}
		inst := upsert(rbAgentB, rbAppMove)

		if inst.ID == oldID {
			t.Fatal("different agent rebind reused the old installation_id; the blocking revoked row was not cleared")
		}
		if inst.Status != "active" {
			t.Fatalf("new installation status=%q, want active", inst.Status)
		}
		// The old revoked row is gone (its unique app_id slot is freed for B).
		if _, err := store.GetLarkInstallation(ctx, oldID); !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("old agent's revoked row still present after rebind: err=%v", err)
		}
	})
}
