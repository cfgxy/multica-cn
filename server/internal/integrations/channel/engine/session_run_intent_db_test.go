package engine

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/multica-ai/multica/server/internal/integrations/channel"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func countRunIntentRowsFor(t *testing.T, pool *pgxpool.Pool, sessionID pgtype.UUID) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*) FROM channel_chat_run_intent WHERE chat_session_id = $1`, sessionID).Scan(&n); err != nil {
		t.Fatalf("count run intent rows: %v", err)
	}
	return n
}

func TestAppendUserMessageRecordsRunIntentAtomically(t *testing.T) {
	pool := sessionPersistenceTestDB(t)
	fixture := seedSessionPersistenceFixture(t, pool)
	ctx := context.Background()
	session := NewChatSession(db.New(pool), pool, channel.Type("lark"), SessionTitles{})

	trigger := &RunIntentTrigger{InitiatorUserID: fixture.userID, InstallationID: fixture.installationID}
	res, err := session.AppendUserMessage(ctx, AppendInput{
		SessionID: fixture.sessionID, Sender: fixture.userID,
		InstallationID: fixture.installationID, Body: "hello", MessageID: "m-run-1",
		RecordRunIntent: trigger,
	})
	if err != nil {
		t.Fatalf("append with run intent: %v", err)
	}

	var state string
	var initiator, rowBinding pgtype.UUID
	var forceFresh bool
	var routeRevision int64
	var fireDelta float64
	if err := pool.QueryRow(ctx, `
		SELECT state, initiator_user_id, force_fresh, binding_id, route_revision,
		       EXTRACT(EPOCH FROM fire_at - now())
		FROM channel_chat_run_intent
		WHERE chat_session_id = $1 AND context_revision = $2`,
		fixture.sessionID, res.ContextRevision,
	).Scan(&state, &initiator, &forceFresh, &rowBinding, &routeRevision, &fireDelta); err != nil {
		t.Fatalf("load run intent row: %v", err)
	}
	if state != "pending" || initiator != fixture.userID || forceFresh {
		t.Fatalf("intent state/initiator/forceFresh = %s/%v/%t, want pending/user/false", state, initiator, forceFresh)
	}
	if rowBinding != res.BindingID || routeRevision != res.RouteRevision {
		t.Fatalf("intent binding/route = %v/%d, want append result %v/%d", rowBinding, routeRevision, res.BindingID, res.RouteRevision)
	}
	// The durable fire deadline sits two batch windows out: the in-memory
	// debounce flushes first, the reconciler only compensates a lost flush.
	if fireDelta < 5 || fireDelta > 8 {
		t.Fatalf("fire_at delta = %.1fs, want ~6s (2x batch window)", fireDelta)
	}

	// A burst inside the same window collapses into the SAME row with the
	// deadline pushed out (MUL-2645 latest-sender-wins semantics).
	if _, err := session.AppendUserMessage(ctx, AppendInput{
		SessionID: fixture.sessionID, Sender: fixture.userID,
		InstallationID: fixture.installationID, Body: "again, one more thing", MessageID: "m-run-2",
		RecordRunIntent: trigger,
	}); err != nil {
		t.Fatalf("burst append: %v", err)
	}
	if rows := countRunIntentRowsFor(t, pool, fixture.sessionID); rows != 1 {
		t.Fatalf("burst produced %d intent rows, want 1 (window collapse)", rows)
	}
}

func TestAppendUserMessageWithoutTriggerWritesNoIntent(t *testing.T) {
	pool := sessionPersistenceTestDB(t)
	fixture := seedSessionPersistenceFixture(t, pool)
	session := NewChatSession(db.New(pool), pool, channel.Type("lark"), SessionTitles{})

	if _, err := session.AppendUserMessage(context.Background(), AppendInput{
		SessionID: fixture.sessionID, Sender: fixture.userID,
		InstallationID: fixture.installationID, Body: "no trigger", MessageID: "m-noop",
	}); err != nil {
		t.Fatalf("append without trigger: %v", err)
	}
	if n := countRunIntentRowsFor(t, pool, fixture.sessionID); n != 0 {
		t.Fatalf("untriggered append produced %d intent rows, want 0", n)
	}
}

func TestAppendUserMessageArmsOrphansAndSkipsMissingInitiators(t *testing.T) {
	pool := sessionPersistenceTestDB(t)
	fixture := seedSessionPersistenceFixture(t, pool)
	ctx := context.Background()
	session := NewChatSession(db.New(pool), pool, channel.Type("lark"), SessionTitles{})

	// Generation 1 receives input but its window is never armed (no trigger —
	// the pre-ledger or crash shape), so no intent row exists for it yet.
	if _, err := session.AppendUserMessage(ctx, AppendInput{
		SessionID: fixture.sessionID, Sender: fixture.userID,
		InstallationID: fixture.installationID, Body: "orphan input", MessageID: "m-orphan-1",
	}); err != nil {
		t.Fatalf("append orphan input: %v", err)
	}
	if n := countRunIntentRowsFor(t, pool, fixture.sessionID); n != 0 {
		t.Fatalf("untriggered append produced %d intent rows, want 0", n)
	}

	// Corrupt the orphan's initiator snapshot — recovery must fail closed.
	if _, err := pool.Exec(ctx, `
		UPDATE channel_chat_context_generation SET initiator_user_id = NULL
		WHERE chat_session_id = $1 AND revision = 1`, fixture.sessionID); err != nil {
		t.Fatalf("wipe orphan initiator: %v", err)
	}

	// The next ordinary message force-freshes into generation 2 and arms the
	// generations that still have unowned input — but only those carrying an
	// initiator snapshot.
	res, err := session.AppendUserMessage(ctx, AppendInput{
		SessionID: fixture.sessionID, Sender: fixture.userID,
		InstallationID: fixture.installationID, Body: "fresh start please", MessageID: "m-orphan-2",
		ForceFresh: true, RecordRunIntent: &RunIntentTrigger{
			InitiatorUserID: fixture.userID, InstallationID: fixture.installationID,
		},
	})
	if err != nil {
		t.Fatalf("append fresh message: %v", err)
	}
	if res.ContextRevision != 2 {
		t.Fatalf("context revision = %d, want 2 after ForceFresh", res.ContextRevision)
	}

	var rev1Rows, rev2Rows int
	var rev2Fresh, rev2InitiatorMatch bool
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM channel_chat_run_intent
		WHERE chat_session_id = $1 AND context_revision = 1`, fixture.sessionID).Scan(&rev1Rows); err != nil {
		t.Fatalf("count generation-1 rows: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT count(*), BOOL_OR(force_fresh), BOOL_OR(initiator_user_id = $2)
		FROM channel_chat_run_intent
		WHERE chat_session_id = $1 AND context_revision = 2`,
		fixture.sessionID, fixture.userID).Scan(&rev2Rows, &rev2Fresh, &rev2InitiatorMatch); err != nil {
		t.Fatalf("count generation-2 rows: %v", err)
	}
	if rev1Rows != 0 {
		t.Fatalf("generation 1 produced %d intent rows, want 0 (NULL initiator fails closed)", rev1Rows)
	}
	if rev2Rows != 1 || !rev2Fresh || !rev2InitiatorMatch {
		t.Fatalf("generation 2 rows/fresh/initiator = %d/%t/%t, want 1/true/true", rev2Rows, rev2Fresh, rev2InitiatorMatch)
	}
}
