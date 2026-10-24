package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// RUYI-874: the three links the leader assigned. Link ① the chat write-back
// (dedup + reconnect visibility), link ② the rich context injection (project
// / messages / skills / images, semantic mentions), link ③ the MCP bridge
// (real data, permission and failure handling). Every test is DB-gated on
// testHandler == nil like the rest of the voice suite.

// dialVoiceSessionChatLinked is dialVoiceSession plus an upgrade query — the
// RUYI-874 chat_session_id link the chat surface passes when opening a voice
// session from a conversation.
func dialVoiceSessionChatLinked(t *testing.T, srv *httptest.Server, agentID, query string) *websocket.Conn {
	t.Helper()
	header := http.Header{}
	header.Set("Cookie", voiceAuthCookie(t, testUserID).String())
	header.Set("X-Workspace-ID", testWorkspaceID)
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/agents/" + agentID + "/voice-session" + query
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err != nil {
		status := "none"
		if resp != nil {
			status = resp.Status
		}
		t.Fatalf("dial gateway: %v (http %s)", err, status)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

// waitProviderFrame polls the stub provider's recorded upstream frames until
// one contains want.
func waitProviderFrame(t *testing.T, provider *stubVoiceProvider, want string) string {
	t.Helper()
	found := ""
	waitFor(t, 5*time.Second, func() bool {
		provider.mu.Lock()
		defer provider.mu.Unlock()
		for _, msg := range provider.clientMsgs {
			if s := string(msg); strings.Contains(s, want) {
				found = s
				return true
			}
		}
		return false
	})
	if found == "" {
		t.Fatalf("provider never received a frame containing %q; recorded: %v", want, provider.clientMsgs)
	}
	return found
}

// waitForTranscriptParked polls until the agent's live session has PARKED a
// transcript entry containing marker in the database. sendDownstream only
// enqueues a frame on the stub's socket; the gateway's provider pump reads
// it on another goroutine, so closing the client right after the write is a
// race the terminal write (and every fact/chat assertion downstream) loses.
// Parking the marker before conn.Close() makes the teardown deterministic —
// the same discipline stage 4 applied to the bridge echo.
func waitForTranscriptParked(t *testing.T, agentID, marker string) {
	t.Helper()
	waitFor(t, 5*time.Second, func() bool {
		var found bool
		return testPool.QueryRow(context.Background(), `
			SELECT transcript::text LIKE '%' || $2 || '%'
			FROM live_session
			WHERE agent_id = $1::uuid AND status = 'active'
		`, agentID, marker).Scan(&found) == nil && found
	})
}

// TestVoiceChatWriteBack_SingleInsertAndReplayNoop pins link ① end to
// end: a chat-linked voice session's merged turns land ONCE as
// message_kind='voice' chat_message rows (reconnect-visible ordinary
// history), with deterministic UUIDv5 ids, broadcast on first insert only;
// the recovery replay inserts zero rows and re-broadcasts nothing.
func TestVoiceChatWriteBack_SingleInsertAndReplayNoop(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	provider := newStubVoiceProvider(t)
	agentID, _ := seedUsableVoiceSetup(t, "Chat WriteBack", `{"model":"stubby-live-1"}`)
	chatID := dbfx.ChatSession(t, agentID)
	srv := voiceGatewayServer(t)

	// Capture the chat broadcast. The Bus has no Unsubscribe, so the
	// listener stays registered for later tests; reads below are scoped
	// to THIS chat session's id, which no other test shares.
	var eventsMu sync.Mutex
	var chatEvents []events.Event
	testHandler.Bus.Subscribe(protocol.EventChatMessage, func(e events.Event) {
		if e.ChatSessionID != chatID {
			return
		}
		eventsMu.Lock()
		chatEvents = append(chatEvents, e)
		eventsMu.Unlock()
	})
	chatEventsFor := func(chatID string) []events.Event {
		eventsMu.Lock()
		defer eventsMu.Unlock()
		out := make([]events.Event, 0, len(chatEvents))
		for _, e := range chatEvents {
			if e.ChatSessionID == chatID {
				out = append(out, e)
			}
		}
		return out
	}

	conn := dialVoiceSessionChatLinked(t, srv, agentID, "?chat_session_id="+chatID)
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, first, err := conn.ReadMessage(); err != nil {
		t.Fatalf("read setupComplete: %v", err)
	} else if !strings.Contains(string(first), "setupComplete") {
		t.Fatalf("expected setupComplete, got %s", first)
	}
	stubConn := provider.gatewayConn(t)
	wantTurns := []struct{ role, content string }{
		{role: "user", content: "用户第一轮"},
		{role: "assistant", content: "助手第一轮"},
		{role: "user", content: "用户第二轮"},
		{role: "assistant", content: "助手第二轮"},
		{role: "user", content: "用户第三轮"},
		{role: "assistant", content: "助手第三轮"},
	}
	for _, turn := range wantTurns {
		transcriptKey := "inputTranscription"
		if turn.role == "assistant" {
			transcriptKey = "outputTranscription"
		}
		provider.sendDownstream(stubConn, map[string]any{
			"serverContent": map[string]any{transcriptKey: map[string]any{"text": turn.content}},
		})
	}
	// Park every turn before the close tears down the relay.
	waitForTranscriptParked(t, agentID, "助手第三轮")
	conn.Close()
	sessionID, _ := waitForVoiceFacts(t, agentID, 1)

	// The link was validated at open: the live session row carries it.
	sessionRow, err := testHandler.Queries.GetLiveSession(context.Background(), parseUUID(sessionID))
	if err != nil {
		t.Fatalf("get live session: %v", err)
	}
	if !sessionRow.ChatSessionID.Valid || sessionRow.ChatSessionID != parseUUID(chatID) {
		t.Fatalf("live session chat link wrong: %+v (want %s)", sessionRow.ChatSessionID, chatID)
	}

	// The terminal write-back runs AFTER the facts layer, so wait for the
	// rows to land before reading them.
	waitFor(t, 5*time.Second, func() bool {
		var n int
		return testPool.QueryRow(context.Background(),
			`SELECT count(*) FROM chat_message
			 WHERE chat_session_id = $1::uuid AND message_kind = 'voice'`, chatID).
			Scan(&n) == nil && n == len(wantTurns)
	})

	// Every alternating turn is stored in a stable chronological order, with
	// deterministic UUIDv5 ids keyed by session|role|turn-index.
	var userMsgID, assistantMsgID string
	if err := testPool.QueryRow(context.Background(), `
		SELECT id::text FROM chat_message
		WHERE chat_session_id = $1::uuid AND role = 'user' AND message_kind = 'voice'
		ORDER BY created_at ASC, id ASC LIMIT 1
	`, chatID).Scan(&userMsgID); err != nil {
		t.Fatalf("user voice row missing: %v", err)
	}
	if err := testPool.QueryRow(context.Background(), `
		SELECT id::text FROM chat_message
		WHERE chat_session_id = $1::uuid AND role = 'assistant' AND message_kind = 'voice'
		ORDER BY created_at ASC, id ASC LIMIT 1
	`, chatID).Scan(&assistantMsgID); err != nil {
		t.Fatalf("assistant voice row missing: %v", err)
	}
	if userMsgID != uuidToString(uuidv5Key(sessionID+"|user|1")) {
		t.Errorf("user row id = %s, want UUIDv5 key %s|user|1", userMsgID, sessionID)
	}
	if assistantMsgID != uuidToString(uuidv5Key(sessionID+"|assistant|2")) {
		t.Errorf("assistant row id = %s, want UUIDv5 key %s|assistant|2", assistantMsgID, sessionID)
	}
	var rowCount int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM chat_message WHERE chat_session_id = $1::uuid AND message_kind = 'voice'`,
		chatID).Scan(&rowCount); err != nil {
		t.Fatalf("count voice rows: %v", err)
	}
	if rowCount != len(wantTurns) {
		t.Errorf("want exactly %d voice rows, got %d", len(wantTurns), rowCount)
	}
	rows, err := testPool.Query(context.Background(), `
		SELECT role, content, created_at FROM chat_message
		WHERE chat_session_id = $1::uuid AND message_kind = 'voice'
		ORDER BY created_at ASC, id ASC
	`, chatID)
	if err != nil {
		t.Fatalf("query ordered voice rows: %v", err)
	}
	var persisted []struct {
		role, content string
		createdAt     time.Time
	}
	for rows.Next() {
		var row struct {
			role, content string
			createdAt     time.Time
		}
		if err := rows.Scan(&row.role, &row.content, &row.createdAt); err != nil {
			rows.Close()
			t.Fatalf("scan ordered voice row: %v", err)
		}
		persisted = append(persisted, row)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Fatalf("iterate ordered voice rows: %v", err)
	}
	rows.Close()
	if len(persisted) != len(wantTurns) {
		t.Fatalf("ordered voice rows = %d, want %d", len(persisted), len(wantTurns))
	}
	for i, want := range wantTurns {
		if persisted[i].role != want.role || persisted[i].content != want.content {
			t.Errorf("ordered voice row %d = %s %q, want %s %q", i, persisted[i].role, persisted[i].content, want.role, want.content)
		}
		if i > 0 && !persisted[i].createdAt.After(persisted[i-1].createdAt) {
			t.Errorf("voice row %d timestamp %s is not after row %d timestamp %s", i, persisted[i].createdAt, i-1, persisted[i-1].createdAt)
		}
	}

	waitFor(t, 5*time.Second, func() bool {
		return len(chatEventsFor(chatID)) == len(wantTurns)
	})

	// First-insert broadcast: one chat:message event per row, in transcript order.
	broadcasts := chatEventsFor(chatID)
	if len(broadcasts) != len(wantTurns) {
		t.Fatalf("want %d chat:message broadcasts, got %d: %+v", len(wantTurns), len(broadcasts), broadcasts)
	}
	for i, e := range broadcasts {
		p, ok := e.Payload.(protocol.ChatMessagePayload)
		if !ok {
			t.Fatalf("chat:message payload type %T", e.Payload)
		}
		if p.ChatSessionID != chatID || p.CreatedAt == "" {
			t.Errorf("broadcast payload wrong: %+v", p)
		}
		if p.Role != wantTurns[i].role || p.Content != wantTurns[i].content {
			t.Errorf("broadcast %d = %s %q, want %s %q", i, p.Role, p.Content, wantTurns[i].role, wantTurns[i].content)
		}
	}

	// Reconnect visibility: the rows are ordinary, listing-visible history
	// (the chat page query is the single source of truth).
	paged, perr := testHandler.Queries.ListChatMessagesPage(context.Background(), db.ListChatMessagesPageParams{
		ChatSessionID: parseUUID(chatID),
		Limit:         24,
	})
	if perr != nil {
		t.Fatalf("list chat messages: %v", perr)
	}
	if len(paged) != len(wantTurns) {
		t.Errorf("chat message page sees %d rows, want %d", len(paged), len(wantTurns))
	}
	for i, msg := range paged {
		want := wantTurns[len(wantTurns)-1-i]
		if msg.Role != want.role || msg.Content != want.content {
			t.Errorf("history page row %d = %s %q, want %s %q", i, msg.Role, msg.Content, want.role, want.content)
		}
	}

	// REPLAY: the recovery entrypoint re-runs the write-back from the
	// persisted transcript — ON CONFLICT collapses it: zero new rows, and
	// created==0 means NO re-broadcast (the dedup no-op).
	if err := testHandler.replayVoiceSessionWriteBack(context.Background(), sessionRow); err != nil {
		t.Fatalf("replay: %v", err)
	}
	var after int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM chat_message WHERE chat_session_id = $1::uuid AND message_kind = 'voice'`,
		chatID).Scan(&after); err != nil {
		t.Fatalf("post-replay count: %v", err)
	}
	if after != len(wantTurns) {
		t.Errorf("replay duplicated rows: %d before, %d after", rowCount, after)
	}
	if got := len(chatEventsFor(chatID)); got != len(wantTurns) {
		t.Errorf("replay re-broadcast: %d events, want still %d", got, len(wantTurns))
	}
	paged, perr = testHandler.Queries.ListChatMessagesPage(context.Background(), db.ListChatMessagesPageParams{
		ChatSessionID: parseUUID(chatID),
		Limit:         24,
	})
	if perr != nil {
		t.Fatalf("list chat messages after replay: %v", perr)
	}
	for i, msg := range paged {
		want := wantTurns[len(wantTurns)-1-i]
		if msg.Role != want.role || msg.Content != want.content {
			t.Errorf("replayed history row %d = %s %q, want %s %q", i, msg.Role, msg.Content, want.role, want.content)
		}
	}
}

// TestVoiceChatWriteBack_NoLinkNoop: a plain voice session (no
// chat_session_id) never touches chat_message — the write-back is a silent
// no-op, not an error.
func TestVoiceChatWriteBack_NoLinkNoop(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	provider := newStubVoiceProvider(t)
	agentID, _ := seedUsableVoiceSetup(t, "NoLink Probe", `{"model":"stubby-live-1"}`)
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
		"serverContent": map[string]any{"inputTranscription": map[string]any{"text": "敲定，无链接会话"}},
	})
	waitForTranscriptParked(t, agentID, "敲定，无链接会话")
	conn.Close()
	sessionID, _ := waitForVoiceFacts(t, agentID, 2)

	var link int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM live_session WHERE id = $1::uuid AND chat_session_id IS NOT NULL`,
		sessionID).Scan(&link); err != nil {
		t.Fatalf("check link: %v", err)
	}
	if link != 0 {
		t.Errorf("plain voice session carries a chat link, want none")
	}
	var msgs int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM chat_message cm
		 JOIN chat_session cs ON cs.id = cm.chat_session_id
		 WHERE cs.agent_id = $1::uuid AND cm.message_kind = 'voice'`, agentID).Scan(&msgs); err != nil {
		t.Fatalf("count agent voice rows: %v", err)
	}
	if msgs != 0 {
		t.Errorf("plain voice session wrote %d chat rows, want 0", msgs)
	}
}

// TestVoiceChatLink_DegradeNotReject pins the link ① permission boundary:
// an invalid or someone-else's chat_session_id NEVER rejects the voice
// session — it degrades to plain voice (the link is a capability, not a
// gate), so the write-back and the context injection stay inside the
// caller's own access.
func TestVoiceChatLink_DegradeNotReject(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	otherUserID := dbfx.User(t, "Not The Caller", "not-the-caller@e.com")
	srv := voiceGatewayServer(t)

	cases := []struct {
		name, link string
	}{
		{"not a uuid", "definitely-not-a-uuid"},
		{"someones else session", ""},      // filled per-subtest: creator != caller
		{"bound to a different agent", ""}, // filled per-subtest
	}
	for i, tc := range cases {
		link := tc.link
		t.Run(tc.name, func(t *testing.T) {
			// One agent per subtest: waitForVoiceFacts reads the agent's
			// single ended session, so a shared agent would break the
			// QueryRow on the second run.
			provider := newStubVoiceProvider(t)
			agentID, _ := seedUsableVoiceSetup(t, "Degrade Probe", `{"model":"stubby-live-1"}`)
			switch i {
			case 1:
				link = dbfx.ChatSession(t, agentID, testutil.Cols{"creator_id": otherUserID})
			case 2:
				otherAgentID := dbfx.Agent(t, "Different Agent", handlerTestRuntimeID(t))
				link = dbfx.ChatSession(t, otherAgentID)
			}
			conn := dialVoiceSessionChatLinked(t, srv, agentID, "?chat_session_id="+link)
			conn.SetReadDeadline(time.Now().Add(5 * time.Second))
			if _, first, err := conn.ReadMessage(); err != nil {
				t.Fatalf("voice session was REJECTED — the link must degrade, not gate: %v", err)
			} else if !strings.Contains(string(first), "setupComplete") {
				t.Fatalf("expected setupComplete, got %s", first)
			}
			stubConn := provider.gatewayConn(t)
			provider.sendDownstream(stubConn, map[string]any{
				"serverContent": map[string]any{"inputTranscription": map[string]any{"text": "敲定"}},
			})
			waitForTranscriptParked(t, agentID, "敲定")
			conn.Close()
			sessionID, _ := waitForVoiceFacts(t, agentID, 2)

			var linkRows int
			if err := testPool.QueryRow(context.Background(),
				`SELECT count(*) FROM live_session WHERE id = $1::uuid AND chat_session_id IS NOT NULL`,
				sessionID).Scan(&linkRows); err != nil {
				t.Fatalf("check link: %v", err)
			}
			if linkRows != 0 {
				t.Errorf("%s: session keeps a chat link it must not have", tc.name)
			}
		})
	}
}

// TestVoiceChatContext_RichInjection pins link ②: when the voice session is
// opened from a chat surface, the model receives exactly what the input box
// carries — the bound project, the recent conversation with agent mentions
// rendered SEMANTICALLY (never raw markdown), the agent's skill names, and
// the image attachments as inlineData parts within the byte budget.
func TestVoiceChatContext_RichInjection(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	provider := newStubVoiceProvider(t)
	agentID, _ := seedUsableVoiceSetup(t, "Rich Context", `{"model":"stubby-live-1"}`)
	chatID := dbfx.ChatSession(t, agentID)
	projectID := dbfx.Project(t, "Voice Anchor Project", testutil.Cols{
		"description": "the bound project",
	})
	dbfx.Exec(t, `UPDATE chat_session SET project_id = $1 WHERE id = $2`, projectID, chatID)
	// One user message with an agent mention (the "@ 不得当纯文本" case) and
	// one assistant reply.
	dbfx.Insert(t, "chat_message", testutil.Cols{
		"chat_session_id": chatID,
		"role":            "user",
		"content":         "[Rich Agent](mention://agent/" + agentID + ") 看一下这个项目",
	})
	dbfx.Insert(t, "chat_message", testutil.Cols{
		"chat_session_id": chatID,
		"role":            "assistant",
		"content":         "收到",
	})
	// The agent's skill surface (names only in the injection).
	skillID := dbfx.Insert(t, "skill", testutil.Cols{
		"workspace_id": testWorkspaceID,
		"name":         "voice-anchor-skill",
		"content":      "skill body",
	})
	dbfx.InsertNoID(t, "agent_skill", testutil.Cols{
		"agent_id": agentID,
		"skill_id": skillID,
	}, "agent_id = $1 AND skill_id = $2", agentID, skillID)
	// Two image attachments, the newest first under the budget. The mock
	// storage serves what the test uploaded — no remote fetch.
	origStorage := testHandler.Storage
	mock := &mockStorage{}
	testHandler.Storage = mock
	t.Cleanup(func() { testHandler.Storage = origStorage })
	img1 := []byte("PNG-DATA-ONE")
	img2 := []byte("PNG-DATA-TWO")
	url1, err := mock.Upload(context.Background(), "voice/img-1.png", img1, "image/png", "img-1.png")
	if err != nil {
		t.Fatalf("upload img1: %v", err)
	}
	url2, err := mock.Upload(context.Background(), "voice/img-2.png", img2, "image/png", "img-2.png")
	if err != nil {
		t.Fatalf("upload img2: %v", err)
	}
	dbfx.Insert(t, "attachment", testutil.Cols{
		"workspace_id":    testWorkspaceID,
		"chat_session_id": chatID,
		"uploader_type":   "member",
		"uploader_id":     testUserID,
		"filename":        "img-1.png",
		"url":             url1,
		"content_type":    "image/png",
		"size_bytes":      int64(len(img1)),
	})
	dbfx.Insert(t, "attachment", testutil.Cols{
		"workspace_id":    testWorkspaceID,
		"chat_session_id": chatID,
		"uploader_type":   "member",
		"uploader_id":     testUserID,
		"filename":        "img-2.png",
		"url":             url2,
		"content_type":    "image/png",
		"size_bytes":      int64(len(img2)),
	})

	srv := voiceGatewayServer(t)
	conn := dialVoiceSessionChatLinked(t, srv, agentID, "?chat_session_id="+chatID)
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, first, err := conn.ReadMessage(); err != nil {
		t.Fatalf("read setupComplete: %v", err)
	} else if !strings.Contains(string(first), "setupComplete") {
		t.Fatalf("expected setupComplete, got %s", first)
	}
	provider.gatewayConn(t)
	turn := waitProviderFrame(t, provider, "clientContent")

	// 1) the bound project, read from the row — never invented.
	if !strings.Contains(turn, "### Chat project") || !strings.Contains(turn, "Voice Anchor Project") || !strings.Contains(turn, "the bound project") {
		t.Errorf("project section missing or wrong: %s", turn)
	}
	// 2) the conversation, oldest first, the mention SEMANTICALLY rewritten
	// to its action reading — the raw markdown must be gone.
	if !strings.Contains(turn, "### Chat conversation so far") {
		t.Errorf("conversation section missing: %s", turn)
	}
	if !strings.Contains(turn, "user mentions agent") {
		t.Errorf("mention not rewritten to its semantic reading: %s", turn)
	}
	if strings.Contains(turn, "mention://agent") {
		t.Errorf("raw markdown mention leaked to the model: %s", turn)
	}
	// 3) the Skill catalog is assembled independently of the chat link and
	// carries the exact identity required by load_skill.
	if !strings.Contains(turn, "### Available agent skills") || !strings.Contains(turn, "voice-anchor-skill") || !strings.Contains(turn, `"source":"workspace"`) {
		t.Errorf("workspace skill catalog missing or incomplete: %s", turn)
	}
	// 4) both images within budget, as inlineData parts.
	if n := strings.Count(turn, "inlineData"); n != 2 {
		t.Errorf("want 2 image parts, got %d: %s", n, turn)
	}
	if !strings.Contains(turn, "image/png") {
		t.Errorf("image mime missing: %s", turn)
	}
	conn.Close()
}

// TestVoiceChatContext_SemanticIssueAndProjectMentions proves the chat
// composer contract for link ②: Issue and project mentions are resolved under
// the live session user's current workspace membership. Valid rows become
// entity context; malformed or foreign references degrade without passing a
// raw link or foreign data to the model. A fresh voice session re-reads the
// conversation so mentions added after the first session also reach setup.
func TestVoiceChatContext_SemanticIssueAndProjectMentions(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID, _ := seedUsableVoiceSetup(t, "Mention Context", `{"model":"stubby-live-1"}`)
	chatID := dbfx.ChatSession(t, agentID)
	issueID := dbfx.Issue(t, "Voice Mention Issue", testutil.Cols{
		"description": "local issue context",
	})
	projectID := dbfx.Project(t, "Voice Mention Project", testutil.Cols{
		"description": "local project context",
	})
	foreignWorkspaceID := dbfx.Workspace(t, "Voice Mention Foreign", "voice-mention-foreign")
	foreignIssueID := dbfx.Issue(t, "Foreign Issue Secret", testutil.Cols{
		"workspace_id": foreignWorkspaceID,
		"description":  "foreign issue description",
	})
	foreignProjectID := dbfx.Project(t, "Foreign Project Secret", testutil.Cols{
		"workspace_id": foreignWorkspaceID,
		"description":  "foreign project description",
	})
	dbfx.Insert(t, "chat_message", testutil.Cols{
		"chat_session_id": chatID,
		"role":            "user",
		"content": "[Issue label](mention://issue/" + issueID + ") " +
			"[Project label](mention://project/" + projectID + ") " +
			"[Foreign issue label](mention://issue/" + foreignIssueID + ") " +
			"[Foreign project label](mention://project/" + foreignProjectID + ") " +
			"[Malformed issue](mention://issue/not-a-uuid) " +
			"malformed mention://issue/not-a-uuid",
	})

	provider := newStubVoiceProvider(t)
	srv := voiceGatewayServer(t)
	conn := dialVoiceSessionChatLinked(t, srv, agentID, "?chat_session_id="+chatID)
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, first, err := conn.ReadMessage(); err != nil {
		t.Fatalf("read setupComplete: %v", err)
	} else if !strings.Contains(string(first), "setupComplete") {
		t.Fatalf("expected setupComplete, got %s", first)
	}
	provider.gatewayConn(t)
	turn := waitProviderFrame(t, provider, "clientContent")
	for _, want := range []string{
		"user references issue", "Voice Mention Issue", "local issue context",
		"user references project", "Voice Mention Project", "local project context",
	} {
		if !strings.Contains(turn, want) {
			t.Errorf("resolved mention context missing %q: %s", want, turn)
		}
	}
	for _, forbidden := range []string{
		"mention://issue", "mention://project", "Foreign Issue Secret",
		"foreign issue description", "Foreign Project Secret", "foreign project description",
	} {
		if strings.Contains(turn, forbidden) {
			t.Errorf("unavailable mention leaked %q: %s", forbidden, turn)
		}
	}
	if got := strings.Count(turn, "unavailable"); got < 4 {
		t.Errorf("want structured degradation for foreign and malformed references, got %d: %s", got, turn)
	}
	conn.Close()

	// The next voice initialization must re-read persisted chat context rather
	// than caching the first setup frame, so a post-session mention is included.
	updatedProjectID := dbfx.Project(t, "Voice Mention Updated Project", testutil.Cols{
		"description": "added before reconnect",
	})
	dbfx.Insert(t, "chat_message", testutil.Cols{
		"chat_session_id": chatID,
		"role":            "user",
		"content":         "[Updated project](mention://project/" + updatedProjectID + ")",
	})
	updatedProvider := newStubVoiceProvider(t)
	updatedConn := dialVoiceSessionChatLinked(t, srv, agentID, "?chat_session_id="+chatID)
	updatedConn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, first, err := updatedConn.ReadMessage(); err != nil {
		t.Fatalf("read reconnect setupComplete: %v", err)
	} else if !strings.Contains(string(first), "setupComplete") {
		t.Fatalf("expected reconnect setupComplete, got %s", first)
	}
	updatedProvider.gatewayConn(t)
	updatedTurn := waitProviderFrame(t, updatedProvider, "clientContent")
	for _, want := range []string{"Voice Mention Updated Project", "added before reconnect"} {
		if !strings.Contains(updatedTurn, want) {
			t.Errorf("reconnect context missing %q: %s", want, updatedTurn)
		}
	}
	updatedConn.Close()
}

// TestVoiceAgentVisibility_ContextAndMCP ensures voice uses the same member
// visibility rule as HTTP ListAgents: private agents do not leak through a
// hand-written mention or MCP enumeration, while owners and public_to targets
// retain access to their visible agents.
func TestVoiceAgentVisibility_ContextAndMCP(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	ownerID := dbfx.User(t, "Voice Private Agent Owner", "voice-private-owner@example.test")
	dbfx.Member(t, testWorkspaceID, ownerID, "member")
	memberID := dbfx.User(t, "Voice Plain Member", "voice-plain-member@example.test")
	dbfx.Member(t, testWorkspaceID, memberID, "member")

	privateAgentID := dbfx.Agent(t, "Voice Private Agent", handlerTestRuntimeID(t), testutil.Cols{
		"owner_id":    ownerID,
		"description": "private agent description",
	})
	allowedAgentID := dbfx.Agent(t, "Voice Allowed Agent", handlerTestRuntimeID(t), testutil.Cols{
		"owner_id":        ownerID,
		"description":     "allowed agent description",
		"visibility":      "workspace",
		"permission_mode": "public_to",
	})
	dbfx.Insert(t, "agent_invocation_target", testutil.Cols{
		"agent_id":    allowedAgentID,
		"target_type": "member",
		"target_id":   memberID,
		"created_by":  ownerID,
	})

	chatAgentID := dbfx.Agent(t, "Voice Visibility Chat", handlerTestRuntimeID(t), testutil.Cols{
		"owner_id":        ownerID,
		"visibility":      "workspace",
		"permission_mode": "public_to",
	})
	dbfx.Insert(t, "agent_invocation_target", testutil.Cols{
		"agent_id":    chatAgentID,
		"target_type": "member",
		"target_id":   memberID,
		"created_by":  ownerID,
	})
	memberChatID := dbfx.ChatSession(t, chatAgentID, testutil.Cols{"creator_id": memberID})
	dbfx.Insert(t, "chat_message", testutil.Cols{
		"chat_session_id": memberChatID,
		"role":            "user",
		"content": "[Private label](mention://agent/" + privateAgentID + ") " +
			"[Allowed label](mention://agent/" + allowedAgentID + ")",
	})
	dbfx.Insert(t, "chat_message", testutil.Cols{
		"chat_session_id": memberChatID,
		"role":            "assistant",
		"content":         "[Private label](mention://agent/" + privateAgentID + ")",
	})
	ownerChatID := dbfx.ChatSession(t, chatAgentID, testutil.Cols{"creator_id": ownerID})
	dbfx.Insert(t, "chat_message", testutil.Cols{
		"chat_session_id": ownerChatID,
		"role":            "user",
		"content":         "[Private label](mention://agent/" + privateAgentID + ")",
	})

	memberSession := db.LiveSession{
		WorkspaceID:   parseUUID(testWorkspaceID),
		UserID:        parseUUID(memberID),
		ChatSessionID: parseUUID(memberChatID),
	}
	memberContext := testHandler.composeVoiceChatContext(ctx, memberSession)
	if memberContext == nil {
		t.Fatal("plain member context = nil, want rendered mention context")
	}
	for _, forbidden := range []string{privateAgentID, "Voice Private Agent", "private agent description"} {
		if strings.Contains(memberContext.Text, forbidden) {
			t.Errorf("plain member context leaked private agent value %q: %s", forbidden, memberContext.Text)
		}
	}
	if !strings.Contains(memberContext.Text, "[unavailable agent reference]") {
		t.Errorf("private mention did not degrade: %s", memberContext.Text)
	}
	if !strings.Contains(memberContext.Text, `user mentions agent "Voice Allowed Agent"`) {
		t.Errorf("allowed mention lost semantic context: %s", memberContext.Text)
	}

	// Revocation after a voice session starts must prevent further rich-context reads.
	dbfx.Exec(t, `DELETE FROM agent_invocation_target WHERE agent_id = $1 AND target_type = 'member' AND target_id = $2`, chatAgentID, memberID)
	if contextAfterRevocation := testHandler.composeVoiceChatContext(ctx, memberSession); contextAfterRevocation != nil {
		t.Errorf("revoked chat agent access retained rich context: %+v", contextAfterRevocation)
	}

	ownerSession := db.LiveSession{
		WorkspaceID:   parseUUID(testWorkspaceID),
		UserID:        parseUUID(ownerID),
		ChatSessionID: parseUUID(ownerChatID),
	}
	ownerContext := testHandler.composeVoiceChatContext(ctx, ownerSession)
	if ownerContext == nil || !strings.Contains(ownerContext.Text, `user mentions agent "Voice Private Agent"`) {
		t.Errorf("agent owner lost private mention semantics: %+v", ownerContext)
	}

}

// TestVoiceChatContext_PlainVoiceFallback: with NO chat link the injection
// stays the existing plain-voice summary section — rich context must not be
// invented for a plain voice session (§3.6 "never invent continuity").
func TestVoiceChatContext_PlainVoiceFallback(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID, _ := seedUsableVoiceSetup(t, "Plain Voice", `{"model":"stubby-live-1"}`)
	srv := voiceGatewayServer(t)

	// Seed the agent's one ended session so the plain section has real
	// content to render.
	p1 := newStubVoiceProvider(t)
	c1 := dialVoiceSession(t, srv, agentID)
	c1.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, _, err := c1.ReadMessage(); err != nil {
		t.Fatalf("read setupComplete: %v", err)
	}
	c1stub := p1.gatewayConn(t)
	p1.sendDownstream(c1stub, map[string]any{
		"serverContent": map[string]any{"inputTranscription": map[string]any{"text": "敲定，用方案一"}},
	})
	waitForTranscriptParked(t, agentID, "敲定，用方案一")
	c1.Close()
	waitForVoiceFacts(t, agentID, 2)

	// Second, unlinked session: the injected turn is the voice summary
	// section, not a chat context.
	p2 := newStubVoiceProvider(t)
	c2 := dialVoiceSession(t, srv, agentID)
	c2.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, _, err := c2.ReadMessage(); err != nil {
		t.Fatalf("read setupComplete: %v", err)
	}
	p2.gatewayConn(t)
	turn := waitProviderFrame(t, p2, "clientContent")
	if !strings.Contains(turn, "### Recent voice session") {
		t.Errorf("plain voice did not fall back to the summary section: %s", turn)
	}
	if strings.Contains(turn, "### Chat project") || strings.Contains(turn, "### Chat conversation so far") {
		t.Errorf("chat context leaked into a plain voice session: %s", turn)
	}
	c2.Close()
}

func TestVoiceIssueContextInjection(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	provider := newStubVoiceProvider(t)
	agentID, _ := seedUsableVoiceSetup(t, "Issue Context", `{"model":"stubby-live-1"}`)
	issueID := dbfx.Issue(t, "Voice Issue Entry Context", testutil.Cols{
		"description": "Context opened from an Issue comment.",
		"status":      "in_progress",
	})
	srv := voiceGatewayServer(t)

	conn := dialVoiceSessionChatLinked(t, srv, agentID, "?issue_id="+issueID)
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, first, err := conn.ReadMessage(); err != nil {
		t.Fatalf("read setupComplete: %v", err)
	} else if !strings.Contains(string(first), "setupComplete") {
		t.Fatalf("expected setupComplete, got %s", first)
	}
	provider.gatewayConn(t)
	contextTurn := waitProviderFrame(t, provider, "### Issue context")
	for _, want := range []string{"Voice Issue Entry Context", "Context opened from an Issue comment.", "in_progress"} {
		if !strings.Contains(contextTurn, want) {
			t.Errorf("Issue context omitted %q: %s", want, contextTurn)
		}
	}
	if !strings.Contains(contextTurn, "### Available agent skills") || !strings.Contains(contextTurn, `"source":"builtin"`) {
		t.Errorf("Issue entry omitted the builtin skill catalog: %s", contextTurn)
	}
	conn.Close()
}
