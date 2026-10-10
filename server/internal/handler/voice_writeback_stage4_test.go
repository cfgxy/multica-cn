package handler

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// RUYI-425 stage 4: the write-back chain (design §3.5/§3.6). Providers stay
// stubbed in-process; only fake key values are used. These tests pin the
// facts-layer contract end to end: extraction on session close (including the
// abnormal-disconnect path), evidence back-references, database-side replay
// idempotency, and the summary projection's rebuildability.

// waitForVoiceFacts polls until the write-back for the agent's latest session
// has landed in full — the facts and the summary projection that closes the
// same attempt — returning (session_id, fact_count). The write-back inserts
// the facts before the summary with no transaction spanning the two, so a
// facts-only wait can observe the attempt mid-flight (RUYI-593: the replay
// test raced the not-yet-written summary and read an empty baseline).
func waitForVoiceFacts(t *testing.T, agentID string, minFacts int) (string, int) {
	t.Helper()
	var sessionID string
	var count int
	waitFor(t, 5*time.Second, func() bool {
		err := testPool.QueryRow(context.Background(), `
			SELECT s.id::text, (SELECT count(*) FROM agent_fact_event f WHERE f.live_session_id = s.id)
			FROM live_session s WHERE s.agent_id = $1 AND s.status = 'ended' AND s.summary <> ''
		`, agentID).Scan(&sessionID, &count)
		return err == nil && count >= minFacts
	})
	return sessionID, count
}

// TestVoiceWriteBack_FactsSummaryOnDisconnect runs a relay until the client
// disconnects mid-session — the abnormal teardown path — and pins what the
// write-back must produce: decision/tool/lifecycle facts with evidence
// back-references, and the summary projection rebuilt from them. Nothing the
// relay received may be lost (§3.5 失败语义).
func TestVoiceWriteBack_FactsSummaryOnDisconnect(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	provider := newStubVoiceProvider(t)
	agentID, _ := seedUsableVoiceSetup(t, "WriteBack Probe", `{"model":"stubby-live-1"}`)
	srv := voiceGatewayServer(t)

	conn := dialVoiceSession(t, srv, agentID)
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, first, err := conn.ReadMessage(); err != nil {
		t.Fatalf("read setupComplete: %v", err)
	} else if !strings.Contains(string(first), "setupComplete") {
		t.Fatalf("expected setupComplete, got %s", first)
	}

	stubConn := provider.gatewayConn(t)
	// Downstream: a user turn with an explicit decision, an assistant reply,
	// a provider tool call, and a resumption handle.
	provider.sendDownstream(stubConn, map[string]any{
		"serverContent": map[string]any{"inputTranscription": map[string]any{"text": "我们决定用方案二上线"}},
	})
	provider.sendDownstream(stubConn, map[string]any{
		"serverContent": map[string]any{"outputTranscription": map[string]any{"text": "好的"}},
	})
	provider.sendDownstream(stubConn, map[string]any{
		"toolCall": map[string]any{"functionCalls": []any{map[string]any{
			"id": "call-1", "name": "search", "args": map[string]any{"q": "rum"},
		}}},
	})
	provider.sendDownstream(stubConn, map[string]any{
		"sessionResumptionUpdate": map[string]any{"newHandle": "wb-handle-9"},
	})
	// Upstream: the client returns the tool result. Wait until the gateway
	// has relayed it upstream — the stub recording it proves the client pump
	// parsed it — before disconnecting, since a close drops unread data.
	if err := conn.WriteMessage(websocket.TextMessage, []byte(
		`{"toolResponse":{"functionResponses":[{"id":"call-1","name":"search","response":{"hits":2}}]}}`)); err != nil {
		t.Fatalf("send toolResponse: %v", err)
	}
	waitFor(t, 3*time.Second, func() bool {
		provider.mu.Lock()
		defer provider.mu.Unlock()
		for _, msg := range provider.clientMsgs {
			if strings.Contains(string(msg), "toolResponse") {
				return true
			}
		}
		return false
	})

	// Abrupt client disconnect — no close frame. The deferred terminal write
	// must still persist the transcript and run the write-back.
	conn.Close()
	sessionID, count := waitForVoiceFacts(t, agentID, 5)

	// Fact-level contract: one user_decision, tool_call + tool_result, the
	// resumption-handle and end state changes — every row scoped to this
	// session and evidence-referenced.
	rows, err := testPool.Query(context.Background(), `
		SELECT event_id, kind, payload::text, evidence_ref, source_runtime
		FROM agent_fact_event WHERE live_session_id = $1::uuid ORDER BY seq
	`, sessionID)
	if err != nil {
		t.Fatalf("query facts: %v", err)
	}
	defer rows.Close()
	type factRow struct {
		eventID, kind, payload, evidenceRef, sourceRuntime string
	}
	var facts []factRow
	for rows.Next() {
		var f factRow
		if err := rows.Scan(&f.eventID, &f.kind, &f.payload, &f.evidenceRef, &f.sourceRuntime); err != nil {
			t.Fatalf("scan fact: %v", err)
		}
		facts = append(facts, f)
	}
	if count != len(facts) || len(facts) < 5 {
		t.Fatalf("want >=5 facts, got %d", len(facts))
	}

	byID := map[string]factRow{}
	kinds := map[string]int{}
	for _, f := range facts {
		byID[f.eventID] = f
		kinds[f.kind]++
		if !strings.HasPrefix(f.eventID, "live_session:"+sessionID+"#") {
			t.Errorf("event_id %q not namespaced to the session", f.eventID)
		}
		if f.sourceRuntime != "gemini_live" {
			t.Errorf("source_runtime = %q", f.sourceRuntime)
		}
	}
	if kinds["user_decision"] != 1 || kinds["tool_call"] != 1 || kinds["tool_result"] != 1 || kinds["state_change"] != 2 {
		t.Fatalf("kind mix wrong: %v (facts %+v)", kinds, facts)
	}

	decision := byID["live_session:"+sessionID+"#turn:1#user_decision"]
	var decisionPayload map[string]any
	if err := json.Unmarshal([]byte(decision.payload), &decisionPayload); err != nil {
		t.Fatalf("decode decision payload: %v (%s)", err, decision.payload)
	}
	if text, _ := decisionPayload["text"].(string); !strings.Contains(text, "决定用方案二上线") {
		t.Errorf("decision payload missing the turn text: %s", decision.payload)
	}
	if decision.evidenceRef != "live_session:"+sessionID+"#turn:1" {
		t.Errorf("decision evidence_ref = %q", decision.evidenceRef)
	}
	call := byID["live_session:"+sessionID+"#call:0:search#tool_call"]
	var callPayload map[string]any
	if err := json.Unmarshal([]byte(call.payload), &callPayload); err != nil || callPayload["name"] != "search" {
		t.Errorf("tool_call fact wrong: %+v (%v)", call, err)
	}
	if args, _ := callPayload["args"].(map[string]any); args == nil || args["q"] != "rum" {
		t.Errorf("tool_call args wrong: %v", callPayload["args"])
	}
	if resp := byID["live_session:"+sessionID+"#resp:0:search#tool_result"]; resp.eventID == "" {
		t.Errorf("tool_result fact missing")
	}
	end := byID["live_session:"+sessionID+"#end#state_change"]
	var endPayload map[string]any
	if err := json.Unmarshal([]byte(end.payload), &endPayload); err != nil || endPayload["turn_count"] != float64(2) {
		t.Errorf("end payload wrong: %s (%v)", end.payload, err)
	}
	if handle := byID["live_session:"+sessionID+"#handle#state_change"]; handle.eventID == "" {
		t.Errorf("resumption-handle fact missing")
	}

	// The summary projection: template text rebuilt from these facts, stored
	// next to the session.
	var summary string
	if err := testPool.QueryRow(context.Background(),
		`SELECT summary FROM live_session WHERE id = $1::uuid`, sessionID).Scan(&summary); err != nil || summary == "" {
		t.Fatalf("summary not persisted: %v %q", err, summary)
	}
	if !strings.Contains(summary, "5 fact event(s)") || !strings.Contains(summary, "[user_decision] user decision: 我们决定用方案二上线") {
		t.Errorf("summary not rebuilt from facts:\n%s", summary)
	}
	// The transcript stays a projection and never doubles as a fact: the
	// assistant narration is in neither the facts nor inventable content.
	for _, f := range facts {
		if f.kind != "user_decision" && strings.Contains(f.payload, "好的") {
			t.Errorf("assistant narration leaked into a %s payload: %s", f.kind, f.payload)
		}
	}
}

// TestVoiceWriteBack_ReplayProducesNoDuplicates pins the §3.6-5 idempotency
// rule: re-running the write-back for an already-written session (the
// at-least-once retry / recovery path) collapses on the (workspace_id,
// event_id) unique index — zero duplicate rows, summary unchanged.
func TestVoiceWriteBack_ReplayProducesNoDuplicates(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	provider := newStubVoiceProvider(t)
	agentID, _ := seedUsableVoiceSetup(t, "Replay Probe", `{"model":"stubby-live-1"}`)
	srv := voiceGatewayServer(t)

	conn := dialVoiceSession(t, srv, agentID)
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, first, err := conn.ReadMessage(); err != nil {
		t.Fatalf("read setupComplete: %v", err)
	} else if !strings.Contains(string(first), "setupComplete") {
		t.Fatalf("expected setupComplete, got %s", first)
	}
	stubConn := provider.gatewayConn(t)
	provider.sendDownstream(stubConn, map[string]any{
		"serverContent": map[string]any{"inputTranscription": map[string]any{"text": "敲定，用方案一"}},
	})
	conn.Close()
	sessionID, count := waitForVoiceFacts(t, agentID, 2)

	var summaryBefore string
	if err := testPool.QueryRow(context.Background(),
		`SELECT summary FROM live_session WHERE id = $1::uuid`, sessionID).Scan(&summaryBefore); err != nil {
		t.Fatalf("read summary: %v", err)
	}
	if summaryBefore == "" {
		t.Fatalf("empty summary baseline — the wait did not cover the summary projection")
	}

	// Replay through the recovery entrypoint, driven by the persisted row.
	sessionRow, err := testHandler.Queries.GetLiveSession(context.Background(), parseUUID(sessionID))
	if err != nil {
		t.Fatalf("get live session: %v", err)
	}
	if err := testHandler.replayVoiceSessionWriteBack(context.Background(), sessionRow); err != nil {
		t.Fatalf("replay: %v", err)
	}

	var countAfter int
	var summaryAfter string
	if err := testPool.QueryRow(context.Background(), `
		SELECT (SELECT count(*) FROM agent_fact_event WHERE live_session_id = $1::uuid), summary
		FROM live_session WHERE id = $1::uuid
	`, sessionID).Scan(&countAfter, &summaryAfter); err != nil {
		t.Fatalf("post-replay read: %v", err)
	}
	if countAfter != count {
		t.Fatalf("replay produced duplicates: %d rows before, %d after", count, countAfter)
	}
	if summaryAfter != summaryBefore {
		t.Fatalf("replay changed the summary:\nbefore: %s\nafter:  %s", summaryBefore, summaryAfter)
	}
}
