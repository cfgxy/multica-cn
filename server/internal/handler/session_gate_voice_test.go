package handler

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// RUYI-425 stage 4: the text-side inheritance contract (design §3.5) and the
// §3.6 hard principles at the brief boundary — the summary is a projection,
// fact events are authoritative, and a brief never invents voice content.

// TestRenderVoiceSessionSection pins the §3.6-3 wording at the brief
// boundary: the summary is presented as a projection, the fact events carry
// the authority, and each stays renderable without the other.
func TestRenderVoiceSessionSection(t *testing.T) {
	t.Parallel()

	ended := pgtype.Timestamptz{Time: time.Date(2026, 10, 5, 9, 0, 0, 0, time.UTC), Valid: true}
	full := renderVoiceSessionSection(
		db.LiveSession{
			Summary: "Voice session live_session:s1 (2 turns), 2 fact event(s):\n- [state_change] session ended (normal)",
			EndedAt: ended,
		},
		[]db.AgentFactEvent{
			{EventID: "live_session:s1#turn:1#user_decision", Kind: "user_decision", EvidenceRef: "live_session:s1#turn:1"},
			{EventID: "live_session:s1#end#state_change", Kind: "state_change", EvidenceRef: "live_session:s1#turn:0"},
		},
	)
	for _, want := range []string{
		"### Recent voice session",
		"The summary is a projection",
		"trust the facts and cite the event id",
		"Voice session live_session:s1 (2 turns)",
		"`live_session:s1#turn:1#user_decision` [user_decision] evidence=live_session:s1#turn:1",
	} {
		if !strings.Contains(full, want) {
			t.Errorf("section missing %q:\n%s", want, full)
		}
	}

	// Facts survive without a summary (write-back landed, projection lost) —
	// the authoritative side is always renderable on its own.
	factsOnly := renderVoiceSessionSection(db.LiveSession{EndedAt: ended}, nil)
	if strings.Contains(factsOnly, "Voice session") {
		t.Errorf("no summary expected:\n%s", factsOnly)
	}
	if !strings.Contains(factsOnly, "### Recent voice session") {
		t.Errorf("empty summary must not suppress the section:\n%s", factsOnly)
	}
}

// TestClaimBrief_VoiceSectionEndToEnd proves the whole chain on a real
// database: a closed live session with facts and summary flows into the
// assembled brief the gate attaches to a compacting claim.
func TestClaimBrief_VoiceSectionEndToEnd(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	_, agentID, runtimeID, daemonID, _ := seedGatedFollowUp(t, ctx, "brief voice section end to end")

	seedEndedVoiceSession(t, agentID, "Voice session live_session:seeded (2 turns), 1 fact event(s):\n- [user_decision] user decision: 决定用 Postgres")
	seedVoiceFact(t, agentID, "live_session:seeded#turn:1#user_decision", "user_decision", "live_session:seeded#turn:1")

	brief := claimBriefForTest(t, runtimeID, daemonID)
	for _, want := range []string{
		"### Recent voice session",
		"决定用 Postgres",
		"`live_session:seeded#turn:1#user_decision`",
		"trust the facts and cite the event id",
	} {
		if !strings.Contains(brief, want) {
			t.Errorf("brief missing %q.\nbrief:\n%s", want, brief)
		}
	}
}

// TestClaimBrief_NoVoiceSessionNoVoiceContent is the §3.6 negative test: a
// text-only agent's brief contains no voice section at all — nothing is
// invented to fill it.
func TestClaimBrief_NoVoiceSessionNoVoiceContent(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	issueID, agentID, runtimeID, daemonID, _ := seedGatedFollowUp(t, ctx, "brief no voice session")
	if agentID == "" || issueID == "" {
		t.Fatal("seed failed")
	}

	brief := claimBriefForTest(t, runtimeID, daemonID)
	if strings.Contains(brief, "voice") {
		t.Errorf("brief invented voice content with no live session:\n%s", brief)
	}
}

// TestClaimBrief_VoiceProjectionDefersToFacts is the §3.6-3 authority test:
// when the stored projection and the fact events disagree, the brief still
// renders both but stamps the facts as authoritative — the projection is
// never promoted to truth by the presentation layer.
func TestClaimBrief_VoiceProjectionDefersToFacts(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	_, agentID, runtimeID, daemonID, _ := seedGatedFollowUp(t, ctx, "brief voice projection defers to facts")

	// A stale/wrong projection (as if rebuilt from an older fact set) and a
	// contradicting authoritative event.
	seedEndedVoiceSession(t, agentID, "Voice session live_session:stale (1 turns): no structured facts extracted.")
	seedVoiceFact(t, agentID, "live_session:stale#turn:2#user_decision", "user_decision", "live_session:stale#turn:2")

	brief := claimBriefForTest(t, runtimeID, daemonID)
	if !strings.Contains(brief, "no structured facts extracted") {
		t.Errorf("projection text missing (it stays visible, demoted):\n%s", brief)
	}
	if !strings.Contains(brief, "`live_session:stale#turn:2#user_decision`") {
		t.Errorf("authoritative event missing:\n%s", brief)
	}
	if !strings.Contains(brief, "trust the facts and cite the event id") {
		t.Errorf("authority statement missing:\n%s", brief)
	}
}

// seedEndedVoiceSession inserts one ended live_session row for the agent with
// the given summary projection.
func seedEndedVoiceSession(t *testing.T, agentID, summary string) string {
	t.Helper()
	var sessionID string
	dbfx.QueryRow(t, `
		INSERT INTO live_session (workspace_id, agent_id, runtime_instance_id, user_id, status, ended_at, summary)
		SELECT a.workspace_id, a.id, gen_random_uuid(), a.workspace_id, 'ended', now() - interval '5 minutes', $2
		FROM agent a WHERE a.id = $1::uuid
		RETURNING id::text
	`, agentID, summary).Scan(&sessionID)
	return sessionID
}

// seedVoiceFact inserts one agent_fact_event row tied to the agent's latest
// ended live session.
func seedVoiceFact(t *testing.T, agentID, eventID, kind, evidence string) {
	t.Helper()
	dbfx.Exec(t, `
		INSERT INTO agent_fact_event (workspace_id, agent_id, live_session_id, event_id, seq, source_runtime, kind, payload, evidence_ref)
		SELECT a.workspace_id, a.id, s.id, $2, 0, 'gemini_live', $3, '{"text":"决定用 Postgres"}'::jsonb, $4
		FROM agent a
		JOIN live_session s ON s.agent_id = a.id AND s.status = 'ended'
		WHERE a.id = $1::uuid
		ORDER BY s.ended_at DESC
		LIMIT 1
	`, agentID, eventID, kind, evidence)
}
