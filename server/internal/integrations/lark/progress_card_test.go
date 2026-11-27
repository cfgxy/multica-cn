package lark

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/events"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// errProgressPatchBoom is an ambiguous transport failure: it carries no Lark
// business code, so it must NOT be read as a rate limit.
var errProgressPatchBoom = errors.New("fake: patch transport failure")

// nowMillisString is Lark's epoch-millisecond create_time format, which the
// typing indicator uses to reject stale replays.
func nowMillisString() string {
	return strconv.FormatInt(time.Now().UnixMilli(), 10)
}

// progressClock drives the per-chat throttle deterministically. The progress
// scheduler reads time only through PatcherConfig.Now and waits only through
// PatcherConfig.Wait, so a test can advance the clock without sleeping.
type progressClock struct {
	now time.Time
	// waited records every scheduler wait, so the rate-limit test can prove the
	// final frame waited out the backoff instead of being dropped.
	waited []time.Duration
	// deadlines models per-context budgets: a Wait that would outlive the
	// registered deadline fails with context.DeadlineExceeded, the way the
	// production progressWait fails when the event ctx dies mid-sleep. Nil
	// for tests that do not model budget exhaustion — the default Wait this
	// type shipped with never fails, which is exactly how the ctx-death path
	// stayed untested.
	deadlines map[context.Context]time.Time
}

func (c *progressClock) Now() time.Time { return c.now }

func (c *progressClock) Wait(ctx context.Context, d time.Duration) error {
	if dl, ok := c.deadlines[ctx]; ok && c.now.Add(d).After(dl) {
		return context.DeadlineExceeded
	}
	c.waited = append(c.waited, d)
	c.now = c.now.Add(d)
	return nil
}

func newProgressTestPatcher(t *testing.T) (*Patcher, *fakePatcherQueries, *fakeAPIClient, *progressClock) {
	t.Helper()
	p, q, api := newTestPatcher(t)
	// A channel_task_delivery row only exists for tasks a channel conversation
	// started, and those task rows carry their session. The progress pipeline
	// resolves the card's session identity from that row (events carry none),
	// so the fixture task must carry it too.
	q.task = db.AgentTaskQueue{ChatSessionID: q.binding.ChatSessionID}
	clock := &progressClock{now: time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)}
	p.cfg.Now = clock.Now
	p.cfg.Wait = clock.Wait
	return p, q, api, clock
}

func progressFrame(taskID, sessionID string, seq int, payload protocol.TaskMessagePayload) events.Event {
	payload.TaskID = taskID
	payload.Seq = seq
	return events.Event{
		Type:          protocol.EventTaskMessage,
		TaskID:        taskID,
		ChatSessionID: sessionID,
		Payload:       payload,
	}
}

func decodeCard(t *testing.T, raw string) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatalf("card JSON must deserialize: %v (raw=%s)", err, raw)
	}
	return doc
}

func progressPlainTextRows(t *testing.T, raw string) []string {
	t.Helper()
	doc := decodeCard(t, raw)
	body, _ := doc["body"].(map[string]any)
	elements, _ := body["elements"].([]any)
	rows := make([]string, 0, len(elements))
	for _, element := range elements {
		div, ok := element.(map[string]any)
		if !ok || div["tag"] != "div" {
			continue
		}
		text, _ := div["text"].(map[string]any)
		if content, ok := text["content"].(string); ok {
			rows = append(rows, content)
		}
	}
	return rows
}

// TestProgressCardRenderCarriesUpdateMulti pins the one card-config field a
// patchable card cannot live without: Lark silently no-ops PATCH on a card
// whose config does not declare update_multi.
func TestProgressCardRenderCarriesUpdateMulti(t *testing.T) {
	render, err := NewDefaultProgressRenderer().RenderProgress(ProgressInput{
		AgentName: "TestAgent",
		State:     ProgressStateRunning,
		Entries:   []ProgressEntry{{Kind: ProgressEntryThinking, Text: "reading the repo"}},
	})
	if err != nil {
		t.Fatalf("render running card: %v", err)
	}
	doc := decodeCard(t, render.JSON)
	cfg, ok := doc["config"].(map[string]any)
	if !ok {
		t.Fatalf("card must carry config; doc=%v", doc)
	}
	if cfg["update_multi"] != true {
		t.Fatalf("config.update_multi must be true, got %v", cfg["update_multi"])
	}
	if doc["schema"] != "2.0" {
		t.Fatalf("progress card must use schema 2.0, got %v", doc["schema"])
	}
	header, _ := doc["header"].(map[string]any)
	if header["template"] != "blue" {
		t.Fatalf("running card header must be blue, got %v", header["template"])
	}
}

// TestProgressCardTerminalStatesRetainStatusHeaderAndStructuredSummary verifies
// each terminal PATCH replaces trace detail with safe demand/action/result rows
// while preserving a status-specific header for the settled card.
func TestProgressCardTerminalStatesRetainStatusHeaderAndStructuredSummary(t *testing.T) {
	cases := []struct {
		state    ProgressState
		title    string
		template string
		result   string
	}{
		{ProgressStateCompleted, "TestAgent · 已完成", "green", "结果：任务已完成，完整答复见下一条消息。"},
		{ProgressStateFailed, "TestAgent · 失败", "red", "结果：任务失败，完整错误说明见下一条消息。"},
		{ProgressStateCancelled, "TestAgent · 已取消", "grey", "结果：任务已取消；如需继续，请重新发起。"},
	}
	for _, tc := range cases {
		render, err := NewDefaultProgressRenderer().RenderProgress(ProgressInput{
			AgentName:      "TestAgent",
			State:          tc.state,
			TriggerSummary: "@TestAgent 请检查发布日志并修复问题",
			Entries: []ProgressEntry{
				{Kind: ProgressEntryToolUse, Tool: "Bash", Text: "sensitive command"},
				{Kind: ProgressEntryToolResult, Tool: "Bash", Text: "sensitive result"},
			},
		})
		if err != nil {
			t.Fatalf("render %s card: %v", tc.state, err)
		}
		doc := decodeCard(t, render.JSON)
		header, _ := doc["header"].(map[string]any)
		if got := header["template"]; got != tc.template {
			t.Errorf("state %s: header template=%v, want %s", tc.state, got, tc.template)
		}
		title, _ := header["title"].(map[string]any)
		if got := title["content"]; got != tc.title {
			t.Errorf("state %s: header title=%v, want %s", tc.state, got, tc.title)
		}
		rows := progressPlainTextRows(t, render.JSON)
		if len(rows) != 3 || rows[0] != "需求：请检查发布日志并修复问题" || rows[1] != "执行：调用 Bash 完成处理。" || rows[2] != tc.result {
			t.Errorf("state %s: terminal rows = %q", tc.state, rows)
		}
		if strings.Contains(render.JSON, "sensitive command") || strings.Contains(render.JSON, "sensitive result") {
			t.Errorf("state %s: terminal summary must not retain process detail: %s", tc.state, render.JSON)
		}
	}
}

// TestProgressCardRunningEntriesKeepResultDotsColored pins the mixed running
// card contract: thinking/tool-use/errors remain compact grey plain_text, while
// tool results use compact lark_md so the outcome dot is outside the grey span.
func TestProgressCardRunningEntriesKeepResultDotsColored(t *testing.T) {
	failed := false
	render, err := NewDefaultProgressRenderer().RenderProgress(ProgressInput{
		AgentName: "TestAgent",
		State:     ProgressStateRunning,
		Entries: []ProgressEntry{
			{Kind: ProgressEntryToolUse, Tool: "Read", Text: "https://example.test/log"},
			{Kind: ProgressEntryToolResult, Tool: "Read", Text: "permission denied", Success: &failed},
			{Kind: ProgressEntryError, Text: "network timeout"},
		},
	})
	if err != nil {
		t.Fatalf("render running card: %v", err)
	}
	doc := decodeCard(t, render.JSON)
	body, _ := doc["body"].(map[string]any)
	elements, _ := body["elements"].([]any)
	var sawResult bool
	for _, element := range elements {
		div, ok := element.(map[string]any)
		if !ok || div["tag"] != "div" {
			continue
		}
		text, _ := div["text"].(map[string]any)
		content, _ := text["content"].(string)
		if strings.Contains(content, "工具结果") {
			sawResult = true
			if text["tag"] != "lark_md" || text["text_size"] != "notation" {
				t.Fatalf("tool result must be compact lark_md; got %#v", text)
			}
			if _, hasGlobalGrey := text["text_color"]; hasGlobalGrey {
				t.Fatalf("tool result must not globally grey the status dot; got %#v", text)
			}
			want := "<font color='grey'>工具结果 · </font>🔴<font color='grey'> Read · permission denied</font>"
			if content != want {
				t.Errorf("tool result content = %q, want %q", content, want)
			}
			continue
		}
		if text["tag"] != "plain_text" || text["text_size"] != "notation" || text["text_color"] != "grey" {
			t.Fatalf("non-result entry must remain compact grey plain text; got %#v", text)
		}
	}
	if !sawResult {
		t.Fatal("running card must contain a tool-result row")
	}
}

// TestProgressCardToolResultEscapesDynamicText ensures the one lark_md row
// cannot turn a tool name or summary into Card markup.
func TestProgressCardToolResultEscapesDynamicText(t *testing.T) {
	success := true
	element := renderProgressEntryElement(ProgressEntry{
		Kind: ProgressEntryToolResult, Tool: "Read<unsafe>", Text: "x</font><at id='all'>", Success: &success,
	})
	text, _ := element["text"].(map[string]any)
	content, _ := text["content"].(string)
	if strings.Contains(content, "Read<unsafe>") || strings.Contains(content, "</font><at") {
		t.Fatalf("dynamic result text must not inject lark_md markup: %q", content)
	}
	if !strings.Contains(content, "Read&lt;unsafe&gt;") || !strings.Contains(content, "x&lt;/font&gt;&lt;at id=&#39;all&#39;&gt;") {
		t.Errorf("dynamic result text must be HTML-escaped: %q", content)
	}
}

// TestProgressCardSendsFirstFrameThenPatches walks the happy path: the first
// progress frame creates the card (and its outbound row), later frames patch
// the same message id, and EventChatDone both finalizes the card and leaves the
// separate plain-text answer untouched.
func TestProgressCardSendsFirstFrameThenPatches(t *testing.T) {
	p, q, api, clock := newProgressTestPatcher(t)
	q.task.TriggerSummary = pgtype.Text{String: "@TestAgent 请更新登录流程", Valid: true}
	q.binding.LastMessageID = pgtype.Text{String: "om_trigger", Valid: true}
	taskID := "ee111111-ee11-ee11-ee11-eeeeeeeeeeee"
	sessionID := uuidString(q.binding.ChatSessionID)

	p.handleTaskMessage(progressFrame(taskID, sessionID, 1, protocol.TaskMessagePayload{
		Type: "text", Content: "planning the change",
	}))
	clock.now = clock.now.Add(2 * time.Second)
	p.handleTaskMessage(progressFrame(taskID, sessionID, 2, protocol.TaskMessagePayload{
		Type: "tool_use", Tool: "Read",
	}))

	api.mu.Lock()
	sent, patched := len(api.sent), len(api.patched)
	firstCard := ""
	if sent > 0 {
		firstCard = api.sent[0].CardJSON
	}
	patchTarget := ""
	if patched > 0 {
		patchTarget = api.patched[0].LarkCardMessageID
	}
	api.mu.Unlock()

	if sent != 1 || patched != 1 {
		t.Fatalf("first frame sends, second patches; got sent=%d patched=%d", sent, patched)
	}
	if !strings.Contains(firstCard, "planning the change") {
		t.Errorf("first frame must render the thinking text; got %s", firstCard)
	}
	if patchTarget != "lark_card_msg_1" {
		t.Errorf("patch must target the created card message id, got %q", patchTarget)
	}
	q.mu.Lock()
	created := append([]CreateOutboundCardMessageParams(nil), q.created...)
	q.mu.Unlock()
	if len(created) != 1 || created[0].Status != string(CardStatusStreaming) ||
		created[0].ChannelCardMessageID != "lark_card_msg_1" {
		t.Fatalf("streaming card row must be persisted once; got %+v", created)
	}

	clock.now = clock.now.Add(2 * time.Second)
	p.handleEvent(events.Event{
		Type:          protocol.EventChatDone,
		TaskID:        taskID,
		ChatSessionID: sessionID,
		Payload: protocol.ChatDonePayload{
			TaskID: taskID, ChatSessionID: sessionID, Content: "here is the answer",
		},
	})
	p.progress.waitIdle()

	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.textSent) != 1 || api.textSent[0].Text != "here is the answer" {
		t.Fatalf("the final answer must still be its own plain-text message; textSent=%+v", api.textSent)
	}
	if api.sent[0].ReplyTarget.MessageID != "om_trigger" || api.sent[0].ReplyTarget.InThread {
		t.Errorf("process card must reply to the normal-group trigger; got %+v", api.sent[0].ReplyTarget)
	}
	if api.textSent[0].ReplyTarget.MessageID != "om_trigger" || api.textSent[0].ReplyTarget.InThread {
		t.Errorf("final text must reply to the normal-group trigger; got %+v", api.textSent[0].ReplyTarget)
	}
	if len(api.patched) != 2 {
		t.Fatalf("chat-done must write the terminal card frame; patched=%d", len(api.patched))
	}
	last := api.patched[len(api.patched)-1].CardJSON
	if !strings.Contains(last, "需求：请更新登录流程") || !strings.Contains(last, "任务已完成") || strings.Contains(last, "planning the change") || !strings.Contains(last, "执行：调用 Read") {
		t.Errorf("terminal frame must retain only the structured completion summary; got %s", last)
	}
}

// TestProgressCardFinalizesNoToolRunWithSeparateReply covers a task that only
// emitted ordinary text progress. It must still compact its card and preserve
// the complete final reply as its own Lark text message.
func TestProgressCardFinalizesNoToolRunWithSeparateReply(t *testing.T) {
	p, q, api, _ := newProgressTestPatcher(t)
	taskID := "ee121212-ee12-ee12-ee12-eeeeeeeeeeee"
	sessionID := uuidString(q.binding.ChatSessionID)

	p.handleTaskMessage(progressFrame(taskID, sessionID, 1, protocol.TaskMessagePayload{
		Type: "text", Content: "checking task state",
	}))
	p.handleEvent(events.Event{
		Type:          protocol.EventChatDone,
		TaskID:        taskID,
		ChatSessionID: sessionID,
		Payload: protocol.ChatDonePayload{
			TaskID: taskID, ChatSessionID: sessionID, Content: "完整的最终答复，不应被卡片摘要替代。",
		},
	})
	p.progress.waitIdle()

	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.sent) != 1 || len(api.patched) != 1 {
		t.Fatalf("no-tool task must send one process card and patch that card once; sent=%d patched=%d", len(api.sent), len(api.patched))
	}
	if len(api.textSent) != 1 || api.textSent[0].Text != "完整的最终答复，不应被卡片摘要替代。" {
		t.Fatalf("final text reply must remain complete and separate; textSent=%+v", api.textSent)
	}
	if got := api.patched[0].CardJSON; !strings.Contains(got, "任务已完成") || strings.Contains(got, "checking task state") {
		t.Errorf("terminal card must be a completion summary, not the process trace: %s", got)
	}
}

// TestProgressCardTerminalEventsDistinguishFailureAndCancellation verifies the
// two non-success terminal event paths update the existing process card with
// their actual outcome instead of reporting success or appending a second
// process card.
func TestProgressCardTerminalEventsDistinguishFailureAndCancellation(t *testing.T) {
	cases := []struct {
		name             string
		eventType        string
		payload          any
		summary          string
		wantTextMessages int
		wantExtraCards   int
	}{
		{
			name:             "failed",
			eventType:        protocol.EventTaskFailed,
			payload:          map[string]any{"error": "agent failed"},
			summary:          "任务失败",
			wantTextMessages: 0,
			wantExtraCards:   1, // existing detailed failure reply remains unchanged.
		},
		{
			name:             "cancelled",
			eventType:        protocol.EventTaskCancelled,
			payload:          map[string]any{"status": "cancelled"},
			summary:          "任务已取消",
			wantTextMessages: 0,
			wantExtraCards:   0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, q, api, _ := newProgressTestPatcher(t)
			taskID := "ee131313-ee13-ee13-ee13-eeeeeeeeeeee"
			sessionID := uuidString(q.binding.ChatSessionID)
			toolFailed := true
			p.handleTaskMessage(progressFrame(taskID, sessionID, 1, protocol.TaskMessagePayload{
				Type: "tool_result", Tool: "Bash", Output: "failed command", IsError: &toolFailed,
			}))

			p.handleEvent(events.Event{Type: tc.eventType, TaskID: taskID, ChatSessionID: sessionID, Payload: tc.payload})
			p.progress.waitIdle()

			api.mu.Lock()
			defer api.mu.Unlock()
			if len(api.sent) != 1+tc.wantExtraCards || len(api.textSent) != tc.wantTextMessages {
				t.Fatalf("unexpected terminal delivery; cards=%d text=%d", len(api.sent), len(api.textSent))
			}
			if len(api.patched) != 1 || !strings.Contains(api.patched[0].CardJSON, tc.summary) {
				t.Fatalf("terminal patch must carry %q exactly once; patches=%+v", tc.summary, api.patched)
			}
		})
	}
}

// TestProgressCardTerminalPatchFailureKeepsFinalReply proves a terminal-card
// transport failure cannot suppress or duplicate the already-complete answer.
func TestProgressCardTerminalPatchFailureKeepsFinalReply(t *testing.T) {
	p, q, api, _ := newProgressTestPatcher(t)
	taskID := "ee141414-ee14-ee14-ee14-eeeeeeeeeeee"
	sessionID := uuidString(q.binding.ChatSessionID)
	p.handleTaskMessage(progressFrame(taskID, sessionID, 1, protocol.TaskMessagePayload{
		Type: "text", Content: "working",
	}))
	api.mu.Lock()
	api.patchTransportFailures = progressFinalAttempts
	api.mu.Unlock()

	p.handleEvent(events.Event{
		Type:          protocol.EventChatDone,
		TaskID:        taskID,
		ChatSessionID: sessionID,
		Payload: protocol.ChatDonePayload{
			TaskID: taskID, ChatSessionID: sessionID, Content: "最终正文必须保留。",
		},
	})
	p.progress.waitIdle()

	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.textSent) != 1 || api.textSent[0].Text != "最终正文必须保留。" {
		t.Fatalf("terminal patch failures must not suppress final text: %+v", api.textSent)
	}
	if len(api.sent) != 1 || len(api.patched) != progressFinalAttempts {
		t.Fatalf("must retry only the original card without creating duplicates; cards=%d patches=%d", len(api.sent), len(api.patched))
	}
	for i, outcome := range api.patchOutcomes {
		if outcome == nil {
			t.Fatalf("terminal patch %d unexpectedly succeeded; outcomes=%v", i, api.patchOutcomes)
		}
	}
}

// TestProgressCardsKeepConcurrentTasksAssociated proves two active tasks in
// one chat retain distinct original card IDs through independent finalization;
// neither final summary is routed to the other task's card or reply.
func TestProgressCardsKeepConcurrentTasksAssociated(t *testing.T) {
	p, q, api, clock := newProgressTestPatcher(t)
	sessionID := uuidString(q.binding.ChatSessionID)
	taskA := "ee151515-ee15-ee15-ee15-eeeeeeeeeeee"
	taskB := "ee161616-ee16-ee16-ee16-eeeeeeeeeeee"
	api.mu.Lock()
	api.sendReturns = []string{"lark_card_A", "lark_card_B"}
	api.mu.Unlock()

	p.handleTaskMessage(progressFrame(taskA, sessionID, 1, protocol.TaskMessagePayload{Type: "text", Content: "only task A"}))
	clock.now = clock.now.Add(2 * time.Second)
	p.handleTaskMessage(progressFrame(taskB, sessionID, 1, protocol.TaskMessagePayload{Type: "text", Content: "only task B"}))

	p.handleEvent(events.Event{Type: protocol.EventChatDone, TaskID: taskA, ChatSessionID: sessionID, Payload: protocol.ChatDonePayload{Content: "answer A"}})
	p.progress.waitIdle()
	clock.now = clock.now.Add(2 * time.Second)
	p.handleEvent(events.Event{Type: protocol.EventChatDone, TaskID: taskB, ChatSessionID: sessionID, Payload: protocol.ChatDonePayload{Content: "answer B"}})
	p.progress.waitIdle()

	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.textSent) != 2 || api.textSent[0].Text != "answer A" || api.textSent[1].Text != "answer B" {
		t.Fatalf("each task must retain its own final reply; textSent=%+v", api.textSent)
	}
	if len(api.patched) != 2 || api.patched[0].LarkCardMessageID != "lark_card_A" || api.patched[1].LarkCardMessageID != "lark_card_B" {
		t.Fatalf("terminal patches must target their own original cards; patches=%+v", api.patched)
	}
}

// TestProgressCardThrottlesPerChatAcrossTasks is the scheduling-dimension test:
// two concurrent tasks in ONE chat share one throttle, so the chat's Lark
// write budget does not double with the task count. A per-task schedule would
// let both tasks write inside the same interval.
func TestProgressCardThrottlesPerChatAcrossTasks(t *testing.T) {
	p, q, api, clock := newProgressTestPatcher(t)
	sessionID := uuidString(q.binding.ChatSessionID)
	taskA := "ee222222-ee22-ee22-ee22-eeeeeeeeeeee"
	taskB := "ee333333-ee33-ee33-ee33-eeeeeeeeeeee"

	// Frame from task A creates its card and consumes the chat's write slot.
	p.handleTaskMessage(progressFrame(taskA, sessionID, 1, protocol.TaskMessagePayload{
		Type: "text", Content: "task A step 1",
	}))
	// Same instant: task B must be throttled by A's write, not by its own
	// (empty) history.
	p.handleTaskMessage(progressFrame(taskB, sessionID, 1, protocol.TaskMessagePayload{
		Type: "text", Content: "task B step 1",
	}))
	// Still inside the interval: both tasks stay silent.
	clock.now = clock.now.Add(200 * time.Millisecond)
	for i := range 20 {
		p.handleTaskMessage(progressFrame(taskA, sessionID, i+2, protocol.TaskMessagePayload{
			Type: "text", Content: "more A",
		}))
		p.handleTaskMessage(progressFrame(taskB, sessionID, i+2, protocol.TaskMessagePayload{
			Type: "text", Content: "more B",
		}))
	}

	api.mu.Lock()
	writes := len(api.sent) + len(api.patched)
	api.mu.Unlock()
	if writes != 1 {
		t.Fatalf("per-chat throttle must admit exactly one write inside the interval; got %d", writes)
	}

	// Past the interval one write is admitted again — still one, not one per task.
	clock.now = clock.now.Add(progressPatchInterval + time.Millisecond)
	p.handleTaskMessage(progressFrame(taskA, sessionID, 99, protocol.TaskMessagePayload{
		Type: "text", Content: "A again",
	}))
	p.handleTaskMessage(progressFrame(taskB, sessionID, 99, protocol.TaskMessagePayload{
		Type: "text", Content: "B again",
	}))
	api.mu.Lock()
	defer api.mu.Unlock()
	if got := len(api.sent) + len(api.patched); got != 2 {
		t.Fatalf("one further write per interval per chat; got %d total writes", got)
	}
}

// TestProgressCardFinalFrameLandsAfterPatchFailure covers the interrupted
// stream: a mid-stream patch failure must not cost the terminal frame or the
// answer.
func TestProgressCardFinalFrameLandsAfterPatchFailure(t *testing.T) {
	p, q, api, clock := newProgressTestPatcher(t)
	taskID := "ee444444-ee44-ee44-ee44-eeeeeeeeeeee"
	sessionID := uuidString(q.binding.ChatSessionID)

	p.handleTaskMessage(progressFrame(taskID, sessionID, 1, protocol.TaskMessagePayload{
		Type: "text", Content: "first",
	}))
	api.mu.Lock()
	api.patchErr = errProgressPatchBoom
	api.mu.Unlock()
	clock.now = clock.now.Add(2 * time.Second)
	p.handleTaskMessage(progressFrame(taskID, sessionID, 2, protocol.TaskMessagePayload{
		Type: "text", Content: "second",
	}))
	api.mu.Lock()
	failed := len(api.patched)
	api.patchErr = nil
	api.mu.Unlock()
	if failed != 1 {
		t.Fatalf("mid-stream patch attempt expected; got %d", failed)
	}

	clock.now = clock.now.Add(2 * time.Second)
	p.handleEvent(events.Event{
		Type:          protocol.EventChatDone,
		TaskID:        taskID,
		ChatSessionID: sessionID,
		Payload: protocol.ChatDonePayload{
			TaskID: taskID, ChatSessionID: sessionID, Content: "answer after failure",
		},
	})
	p.progress.waitIdle()

	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.textSent) != 1 || api.textSent[0].Text != "answer after failure" {
		t.Fatalf("answer must land despite the failed patch; textSent=%+v", api.textSent)
	}
	last := api.patched[len(api.patched)-1].CardJSON
	if !strings.Contains(last, "已完成") {
		t.Fatalf("terminal frame must still land after a mid-stream failure; got %s", last)
	}
}

// TestProgressCardFinalFrameSurvivesRateLimit is the backoff test: Lark's
// 230020 puts the chat into backoff, intermediate frames are dropped, and the
// terminal frame waits the backoff out rather than being dropped with them.
func TestProgressCardFinalFrameSurvivesRateLimit(t *testing.T) {
	p, q, api, clock := newProgressTestPatcher(t)
	taskID := "ee555555-ee55-ee55-ee55-eeeeeeeeeeee"
	sessionID := uuidString(q.binding.ChatSessionID)

	p.handleTaskMessage(progressFrame(taskID, sessionID, 1, protocol.TaskMessagePayload{
		Type: "text", Content: "first",
	}))
	api.mu.Lock()
	api.patchErr = &APIError{Op: "patch interactive card", Code: 230020, Msg: "rate limit"}
	api.mu.Unlock()
	clock.now = clock.now.Add(2 * time.Second)
	p.handleTaskMessage(progressFrame(taskID, sessionID, 2, protocol.TaskMessagePayload{
		Type: "text", Content: "second",
	}))

	// Inside the backoff window every intermediate frame is dropped.
	api.mu.Lock()
	beforeDrops := len(api.patched)
	api.mu.Unlock()
	clock.now = clock.now.Add(2 * time.Second)
	p.handleTaskMessage(progressFrame(taskID, sessionID, 3, protocol.TaskMessagePayload{
		Type: "text", Content: "third",
	}))
	api.mu.Lock()
	afterDrops := len(api.patched)
	api.patchErr = nil
	api.mu.Unlock()
	if afterDrops != beforeDrops {
		t.Fatalf("rate-limit backoff must drop intermediate frames; patched went %d -> %d", beforeDrops, afterDrops)
	}

	p.handleEvent(events.Event{
		Type:          protocol.EventChatDone,
		TaskID:        taskID,
		ChatSessionID: sessionID,
		Payload: protocol.ChatDonePayload{
			TaskID: taskID, ChatSessionID: sessionID, Content: "answer under rate limit",
		},
	})
	p.progress.waitIdle()

	if len(clock.waited) == 0 {
		t.Fatalf("terminal frame must wait out the backoff instead of dropping")
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.textSent) != 1 {
		t.Fatalf("answer must land despite the rate limit; textSent=%+v", api.textSent)
	}
	last := api.patched[len(api.patched)-1].CardJSON
	if !strings.Contains(last, "已完成") {
		t.Fatalf("terminal frame must land after backoff; got %s", last)
	}
}

// TestProgressCardFinalFrameLandsUnderBackoffDebtAndSharedBudget reproduces
// the injected-QA shape neither final-frame test above covers: a process
// frame armed the 10s rate-limit backoff, and by the time chat:done arrives
// the shared event budget is nearly spent — the reply send consumed it, which
// is why QA measured POST /complete at 10.0s. The clock here honors a ctx
// deadline the way production progressWait does, so a wait that outlives the
// shared budget fails instead of silently succeeding. The terminal frame must
// still land: its retries wait out the full re-armed backoff (a probe fired
// early always lands inside the penalty window) on a budget that is not the
// event's.
func TestProgressCardFinalFrameLandsUnderBackoffDebtAndSharedBudget(t *testing.T) {
	p, q, api, clock := newProgressTestPatcher(t)
	taskID := "ee777777-ee77-ee77-ee77-eeeeeeeeeeee"
	sessionID := uuidString(q.binding.ChatSessionID)

	p.handleTaskMessage(progressFrame(taskID, sessionID, 1, protocol.TaskMessagePayload{
		Type: "text", Content: "first",
	}))
	// The second frame trips the rate limit and arms the backoff debt, and
	// the terminal frame's first attempt trips again (re-arming it) — the
	// state the QA injection left behind on the card's schedule.
	api.mu.Lock()
	api.patchRateLimitFailures = 2
	api.mu.Unlock()
	clock.now = clock.now.Add(2 * time.Second)
	p.handleTaskMessage(progressFrame(taskID, sessionID, 2, protocol.TaskMessagePayload{
		Type: "text", Content: "second",
	}))

	// chat:done arrives 2s later, with 4s of event budget left and 8s of
	// backoff debt outstanding.
	clock.now = clock.now.Add(2 * time.Second)
	shared, done := context.WithCancel(context.Background())
	defer done()
	clock.deadlines = map[context.Context]time.Time{shared: clock.now.Add(4 * time.Second)}
	_ = p.processEvent(shared, events.Event{
		Type:          protocol.EventChatDone,
		TaskID:        taskID,
		ChatSessionID: sessionID,
		Payload: protocol.ChatDonePayload{
			TaskID: taskID, ChatSessionID: sessionID, Content: "answer under debt",
		},
	})
	p.progress.waitIdle()

	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.textSent) != 1 || api.textSent[0].Text != "answer under debt" {
		t.Fatalf("answer must land regardless of the card's backoff debt; textSent=%+v", api.textSent)
	}
	if len(api.patched) < 2 {
		t.Fatalf("terminal frame must be attempted after the debt wait; patched=%d", len(api.patched))
	}
	last := api.patched[len(api.patched)-1].CardJSON
	if !strings.Contains(last, "已完成") {
		t.Fatalf("terminal frame must land despite backoff debt plus a spent shared budget; got %s", last)
	}
	if len(clock.waited) != 2 || clock.waited[0] != 8*time.Second || clock.waited[1] != progressRateLimitBackoff {
		t.Fatalf("terminal retries must wait out the full re-armed backoff; got waits %v", clock.waited)
	}
}

// TestProgressCardFinalFrameRetriesTransportFailure pins the failure-class
// rule for the terminal frame: the final write is a full-card PATCH
// replacement keyed by the message id, so it is idempotent and an ambiguous
// transport failure — no Lark code, not a rate limit — must consume the same
// retry budget as a rate limit instead of abandoning the card after one
// attempt. Retrying must also not invent a backoff: the wait between
// attempts stays the chat's ordinary write interval.
func TestProgressCardFinalFrameRetriesTransportFailure(t *testing.T) {
	p, q, api, clock := newProgressTestPatcher(t)
	taskID := "ee999999-ee99-ee99-ee99-eeeeeeeeeeee"
	sessionID := uuidString(q.binding.ChatSessionID)

	p.handleTaskMessage(progressFrame(taskID, sessionID, 1, protocol.TaskMessagePayload{
		Type: "text", Content: "first",
	}))
	// The next two patch attempts answer with an ambiguous transport
	// failure: the mid-stream frame, then the terminal frame's first
	// attempt. The terminal retry must land on the second attempt.
	api.mu.Lock()
	api.patchTransportFailures = 2
	api.mu.Unlock()
	clock.now = clock.now.Add(2 * time.Second)
	p.handleTaskMessage(progressFrame(taskID, sessionID, 2, protocol.TaskMessagePayload{
		Type: "text", Content: "second",
	}))

	clock.now = clock.now.Add(2 * time.Second)
	p.handleEvent(events.Event{
		Type:          protocol.EventChatDone,
		TaskID:        taskID,
		ChatSessionID: sessionID,
		Payload: protocol.ChatDonePayload{
			TaskID: taskID, ChatSessionID: sessionID, Content: "answer after transport failure",
		},
	})
	p.progress.waitIdle()

	api.mu.Lock()
	defer api.mu.Unlock()
	outcomes := append([]error(nil), api.patchOutcomes...)
	if len(api.textSent) != 1 || api.textSent[0].Text != "answer after transport failure" {
		t.Fatalf("answer must land regardless of patch failures; textSent=%+v", api.textSent)
	}
	if len(outcomes) != 3 || outcomes[2] != nil {
		t.Fatalf("terminal frame must be retried past a transport failure and land; outcomes=%v", outcomes)
	}
	last := api.patched[len(api.patched)-1].CardJSON
	if !strings.Contains(last, "已完成") {
		t.Fatalf("the landing retry must carry the completed card; got %s", last)
	}
	if len(clock.waited) != 1 || clock.waited[0] != progressPatchInterval {
		t.Fatalf("transport-failure retry must wait only the write interval, not the rate-limit backoff; waited=%v", clock.waited)
	}
}

// gatedSendClient parks the first frame's POST mid-flight: the embedded fake
// has already recorded the send, the message id has not come back, and the
// push path still holds the chat's schedule lock — the exact intermediate
// state a chat:done can race against in production.
type gatedSendClient struct {
	*fakeAPIClient
	gate     chan struct{} // closed by the test to let the parked POST return
	inFlight chan struct{} // closed by the client once the POST is parked
}

func (g *gatedSendClient) SendInteractiveCard(ctx context.Context, p SendCardParams) (string, error) {
	id, err := g.fakeAPIClient.SendInteractiveCard(ctx, p)
	close(g.inFlight)
	<-g.gate
	return id, err
}

// TestProgressCardFinalFrameLandsWhenFirstFrameStillInFlight pins the
// send-vs-finalize race: a chat:done processed while the first frame's POST
// is still in flight reads cardMessageID before send() has written it. The
// judgment is made without the chat's write slot, so no lock-ordered double
// can pin its ordering — progressFinalizeAfterCardRead is the seam that
// makes the read observable. With the early exit in place the test fails
// deterministically: finalize has already given up (and released the card)
// by the time the POST returns, so the terminal frame never lands.
func TestProgressCardFinalFrameLandsWhenFirstFrameStillInFlight(t *testing.T) {
	p, q, api, _ := newProgressTestPatcher(t)
	taskID := "eeaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	sessionID := uuidString(q.binding.ChatSessionID)

	gate := make(chan struct{})
	inFlight := make(chan struct{})
	p.client = &gatedSendClient{fakeAPIClient: api, gate: gate, inFlight: inFlight}
	// A real row id, so markTerminal's status flip is observable: the stuck-
	// streaming DB row was half of the race's damage, the frozen card the
	// other half.
	q.mu.Lock()
	q.createReturn = OutboundCardMessage{ID: uuidFromString(t, "eeeebbbb-eeee-eeee-eeee-eeeeeeeeeeee")}
	q.mu.Unlock()

	frameDone := make(chan struct{})
	go func() {
		defer close(frameDone)
		p.handleTaskMessage(progressFrame(taskID, sessionID, 1, protocol.TaskMessagePayload{
			Type: "text", Content: "first",
		}))
	}()
	<-inFlight // first POST recorded and parked; the schedule lock is held

	readDone := make(chan struct{})
	progressFinalizeAfterCardRead = func() { close(readDone) }
	defer func() { progressFinalizeAfterCardRead = nil }()

	p.handleEvent(events.Event{
		Type:          protocol.EventChatDone,
		TaskID:        taskID,
		ChatSessionID: sessionID,
		Payload: protocol.ChatDonePayload{
			TaskID: taskID, ChatSessionID: sessionID, Content: "answer during first frame",
		},
	})
	<-readDone // finalize has judged hasCard on the intermediate state

	close(gate) // the first POST returns; send() writes cardMessageID
	<-frameDone
	p.progress.waitIdle()

	api.mu.Lock()
	defer api.mu.Unlock()
	outcomes := append([]error(nil), api.patchOutcomes...)
	if len(api.textSent) != 1 || api.textSent[0].Text != "answer during first frame" {
		t.Fatalf("answer must land regardless of the race; textSent=%+v", api.textSent)
	}
	if len(api.sent) != 1 {
		t.Fatalf("exactly one card send expected; sent=%d", len(api.sent))
	}
	if len(outcomes) != 1 || outcomes[0] != nil {
		t.Fatalf("terminal frame must land after the in-flight first frame; patch outcomes=%v", outcomes)
	}
	last := api.patched[len(api.patched)-1].CardJSON
	if !strings.Contains(last, "已完成") {
		t.Fatalf("terminal frame must carry the completed state; got %s", last)
	}
	q.mu.Lock()
	status := append([]UpdateOutboundCardStatusParams(nil), q.statusUpdates...)
	q.mu.Unlock()
	if len(status) != 1 || status[0].Status != string(CardStatusFinal) {
		t.Fatalf("the card row must leave streaming for final; statusUpdates=%+v", status)
	}
}

// TestProgressCardWindowTruncates pins the bounded entry window (cc-connect
// core/progress_compact.go:301, maxEntries 10): older entries fall off and the
// card says so instead of growing without limit.
func TestProgressCardWindowTruncates(t *testing.T) {
	p, q, api, clock := newProgressTestPatcher(t)
	taskID := "ee666666-ee66-ee66-ee66-eeeeeeeeeeee"
	sessionID := uuidString(q.binding.ChatSessionID)

	for i := range progressMaxEntries + 3 {
		p.handleTaskMessage(progressFrame(taskID, sessionID, i+1, protocol.TaskMessagePayload{
			Type: "tool_use", Tool: "Tool" + string(rune('A'+i)),
		}))
		clock.now = clock.now.Add(progressPatchInterval + time.Millisecond)
	}

	api.mu.Lock()
	defer api.mu.Unlock()
	last := api.patched[len(api.patched)-1].CardJSON
	if strings.Contains(last, "ToolA") {
		t.Errorf("oldest entry must fall out of the window; got %s", last)
	}
	if !strings.Contains(last, "仅显示最近更新") {
		t.Errorf("truncated card must carry the truncation note; got %s", last)
	}
}

// TestProgressCardCredentialsNeverRenderedRaw pins the security constraint of
// the derived tool text: titles and previews may only carry the payload's
// Input/Output channels, and the card re-scrubs them with redact.Text before
// rendering. Even a payload that skipped the daemon's and the ingest's
// redaction (deployment-order skew) cannot leak a secret-shaped token into
// the chat — it renders in its redacted form instead.
func TestProgressCardCredentialsNeverRenderedRaw(t *testing.T) {
	p, q, api, clock := newProgressTestPatcher(t)
	taskID := "ee777777-ee77-ee77-ee77-eeeeeeeeeeee"
	sessionID := uuidString(q.binding.ChatSessionID)
	secret := "supersecretvalue"

	p.handleTaskMessage(progressFrame(taskID, sessionID, 1, protocol.TaskMessagePayload{
		Type: "tool_use", Tool: "Bash",
		Input: map[string]any{"command": "curl -H 'Authorization: Bearer " + secret + "'"},
	}))
	clock.now = clock.now.Add(2 * time.Second)
	p.handleTaskMessage(progressFrame(taskID, sessionID, 2, protocol.TaskMessagePayload{
		Type: "tool_result", Tool: "Bash",
		Output: "token=" + secret,
	}))

	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.sent) != 1 || len(api.patched) != 1 {
		t.Fatalf("expected one send and one patch; sent=%d patched=%d", len(api.sent), len(api.patched))
	}
	card := api.patched[0].CardJSON
	if strings.Contains(card, secret) {
		t.Fatalf("a secret-shaped token must never render raw; got %s", card)
	}
	if !strings.Contains(card, "Bearer [REDACTED]") {
		t.Errorf("the tool title must show the command in its redacted form; got %s", card)
	}
	if !strings.Contains(card, "[REDACTED CREDENTIAL]") {
		t.Errorf("the result preview must show the output in its redacted form; got %s", card)
	}
	if !strings.Contains(card, "Bash") {
		t.Errorf("the tool name is the part that is safe to show; got %s", card)
	}
}

// TestProgressDemandRedactsTriggerSummary covers the user-controlled summary
// path, which does not pass through tool payload sanitization. A terminal card
// must never echo a credential typed in the triggering Lark message.
func TestProgressDemandRedactsTriggerSummary(t *testing.T) {
	secret := "ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmn"
	render, err := NewDefaultProgressRenderer().RenderProgress(ProgressInput{
		AgentName:      "TestAgent",
		State:          ProgressStateCompleted,
		TriggerSummary: "@TestAgent 请检查令牌 " + secret,
	})
	if err != nil {
		t.Fatalf("render terminal card: %v", err)
	}
	if strings.Contains(render.JSON, secret) {
		t.Fatalf("trigger summary credential must not render raw: %s", render.JSON)
	}
	if !strings.Contains(render.JSON, "需求：") || !strings.Contains(render.JSON, "[REDACTED") {
		t.Errorf("terminal demand must retain a redacted summary: %s", render.JSON)
	}
}

// TestProgressCardRedactsFallbackInputDemand verifies the older/imported-task
// path too: when TriggerSummary is empty, the first immutable input is loaded
// and then redacted before the terminal PATCH renders it.
func TestProgressCardRedactsFallbackInputDemand(t *testing.T) {
	p, q, api, _ := newProgressTestPatcher(t)
	taskID := uuidFromString(t, "ee787878-ee78-ee78-ee78-eeeeeeeeeeee")
	secret := "sk-proj-abc123def456ghi789jkl012mno345"
	q.task.ChatInputTaskID = taskID
	q.taskChannelIngested = true
	q.inputMessages = []db.ChatMessage{{Content: "@TestAgent 使用密钥 " + secret + " 检查配置"}}

	p.handleTaskMessage(progressFrame(uuidString(taskID), uuidString(q.binding.ChatSessionID), 1, protocol.TaskMessagePayload{
		Type: "text", Content: "checking configuration",
	}))
	p.handleEvent(events.Event{
		Type: protocol.EventChatDone, TaskID: uuidString(taskID), ChatSessionID: uuidString(q.binding.ChatSessionID),
		Payload: protocol.ChatDonePayload{TaskID: uuidString(taskID), Content: "done"},
	})
	p.progress.waitIdle()

	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.patched) != 1 {
		t.Fatalf("expected one terminal patch, got %d", len(api.patched))
	}
	card := api.patched[0].CardJSON
	if strings.Contains(card, secret) {
		t.Fatalf("fallback input credential must not render raw: %s", card)
	}
	if !strings.Contains(card, "需求：") || !strings.Contains(card, "[REDACTED") {
		t.Errorf("fallback demand must retain a redacted summary: %s", card)
	}
}

// TestProgressCardClearsTypingBadgeOnFirstFrame settles the badge-vs-card
// ordering: once the card is on screen carrying "进行中", the Typing reaction is
// a second "processing" signal and comes off.
func TestProgressCardClearsTypingBadgeOnFirstFrame(t *testing.T) {
	p, q, api, _ := newProgressTestPatcher(t)
	taskID := "ee888888-ee88-ee88-ee88-eeeeeeeeeeee"
	sessionID := uuidString(q.binding.ChatSessionID)

	typing := NewTypingIndicatorManager(api, fakeCredentials{secret: "shh"}, q, newDiscardLogger())
	typing.Add(t.Context(), q.installation, q.binding.ChatSessionID, "om_trigger_msg", nowMillisString())
	p.SetTypingIndicatorManager(typing)

	p.handleTaskMessage(progressFrame(taskID, sessionID, 1, protocol.TaskMessagePayload{
		Type: "text", Content: "starting",
	}))

	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.deletedReactions) != 1 {
		t.Fatalf("the first card frame must clear the Typing badge; deleted=%+v", api.deletedReactions)
	}
}

// productionTaskMessageFrame builds the exact event shape handler.publishTask
// emits for EventTaskMessage (the CreateTaskMessages loop in
// internal/handler/daemon.go): a TaskID scope hint, NO ChatSessionID scope
// hint, and a protocol.TaskMessagePayload value payload — the payload type has
// no chat_session_id field at all. Fixtures that stuff a session into the
// event prove nothing about production, which is how the first delivery
// shipped a card pipeline that was unreachable outside tests.
func productionTaskMessageFrame(taskID string, seq int, payload protocol.TaskMessagePayload) events.Event {
	return events.Event{
		Type:        protocol.EventTaskMessage,
		WorkspaceID: "00000000-0000-0000-0000-000000000001",
		ActorType:   "system",
		ActorID:     "",
		TaskID:      taskID,
		Payload:     payload,
	}
}

// TestProgressCardHandlesProductionEventShape is the seam test for the shape
// above: with no session identity on the event, the card must still be created
// and patched, and the persisted row plus the Typing-badge clear must be keyed
// by the session resolved from the task's delivery path.
func TestProgressCardHandlesProductionEventShape(t *testing.T) {
	p, q, api, clock := newProgressTestPatcher(t)
	taskID := "ee666666-ee66-ee66-ee66-eeeeeeeeeeee"
	sessionID := uuidString(q.binding.ChatSessionID)

	p.handleTaskMessage(productionTaskMessageFrame(taskID, 1, protocol.TaskMessagePayload{
		Type: "text", Content: "planning the change",
	}))
	clock.now = clock.now.Add(2 * time.Second)
	p.handleTaskMessage(productionTaskMessageFrame(taskID, 2, protocol.TaskMessagePayload{
		Type: "tool_use", Tool: "Read",
	}))

	api.mu.Lock()
	sent, patched := len(api.sent), len(api.patched)
	api.mu.Unlock()
	if sent != 1 || patched != 1 {
		t.Fatalf("production-shape frames must create then patch the card; sent=%d patched=%d", sent, patched)
	}

	q.mu.Lock()
	created := append([]CreateOutboundCardMessageParams(nil), q.created...)
	q.mu.Unlock()
	if len(created) != 1 || uuidString(created[0].ChatSessionID) != sessionID {
		t.Fatalf("card row must be keyed by the task-path session; got %+v", created)
	}
}

// TestProgressEntryFromPayloadDerivesToolText pins the payload→entry mapping
// for the two new text layers: a tool_use entry carries the title derived
// from the (sanitized) input, a tool_result entry the preview derived from
// the (truncated) output, and both degrade to the bare tool row when the
// payload carries nothing derivable. IsError mapping is unaffected.
func TestProgressEntryFromPayloadDerivesToolText(t *testing.T) {
	errFlag := true
	entry, ok := progressEntryFromPayload(protocol.TaskMessagePayload{
		Type:  "tool_use",
		Tool:  "Bash",
		Input: map[string]any{"command": "git status"},
	})
	if !ok || entry.Tool != "Bash" || entry.Text != "git status" {
		t.Fatalf("tool_use must derive its title from the input; got %+v ok=%v", entry, ok)
	}

	entry, ok = progressEntryFromPayload(protocol.TaskMessagePayload{Type: "tool_use", Tool: "Read"})
	if !ok || entry.Text != "" {
		t.Fatalf("tool_use without input must degrade to the bare row; got %+v", entry)
	}

	entry, ok = progressEntryFromPayload(protocol.TaskMessagePayload{
		Type: "tool_result", Tool: "Bash", Output: "clean tree",
	})
	if !ok || entry.Text != "clean tree" {
		t.Fatalf("tool_result must derive its preview from the output; got %+v", entry)
	}

	entry, ok = progressEntryFromPayload(protocol.TaskMessagePayload{
		Type: "tool_result", Tool: "Bash", Output: "boom", IsError: &errFlag,
	})
	if !ok || entry.Text != "boom" || entry.Success == nil || *entry.Success {
		t.Fatalf("failed tool_result must keep the error flag beside its preview; got %+v", entry)
	}

	entry, ok = progressEntryFromPayload(protocol.TaskMessagePayload{Type: "tool_result", Tool: "Read"})
	if !ok || entry.Text != "" {
		t.Fatalf("tool_result without output must degrade to the bare row; got %+v", entry)
	}
}

// TestProgressCardToolResultDotFollowsSuccess verifies all three outcome dots
// use lark_md without a global colour filter, while their descriptions remain
// inside the grey font span.
func TestProgressCardToolResultDotFollowsSuccess(t *testing.T) {
	failed := false
	ok := true
	render, err := NewDefaultProgressRenderer().RenderProgress(ProgressInput{
		AgentName: "TestAgent",
		State:     ProgressStateRunning,
		Entries: []ProgressEntry{
			{Kind: ProgressEntryToolUse, Tool: "Bash", Text: "git status"},
			{Kind: ProgressEntryToolResult, Tool: "Bash", Text: "clean tree", Success: &ok},
			{Kind: ProgressEntryToolResult, Tool: "Bash", Text: "boom", Success: &failed},
			{Kind: ProgressEntryToolUse, Tool: "Read"},
			{Kind: ProgressEntryToolResult, Tool: "Read"},
		},
	})
	if err != nil {
		t.Fatalf("render progress card: %v", err)
	}
	rows := progressPlainTextRows(t, render.JSON)
	if len(rows) != 5 {
		t.Fatalf("expected five tool rows, got %+v", rows)
	}
	if rows[0] != "工具调用 · Bash · git status" || rows[3] != "工具调用 · Read" {
		t.Errorf("tool-use rows = %q, want compact plain text rows", rows)
	}
	wantResultRows := []string{
		"<font color='grey'>工具结果 · </font>🟢<font color='grey'> Bash · clean tree</font>",
		"<font color='grey'>工具结果 · </font>🔴<font color='grey'> Bash · boom</font>",
		"<font color='grey'>工具结果 · </font>⚪<font color='grey'> Read</font>",
	}
	for i, want := range wantResultRows {
		got := rows[[]int{1, 2, 4}[i]]
		if got != want {
			t.Errorf("tool-result row %d = %q, want %q", i, got, want)
		}
	}
}

// TestProgressCardCarriesToolTitleAndResultSummary walks the streaming path
// end to end: task messages that carry Input/Output land on the card as a
// tool title and an unwrapped result preview.
func TestProgressCardCarriesToolTitleAndResultSummary(t *testing.T) {
	p, q, api, clock := newProgressTestPatcher(t)
	taskID := "eecccccc-cccc-cccc-cccc-cccccccccccc"
	sessionID := uuidString(q.binding.ChatSessionID)

	p.handleTaskMessage(progressFrame(taskID, sessionID, 1, protocol.TaskMessagePayload{
		Type: "tool_use", Tool: "Bash",
		Input: map[string]any{"command": "git status"},
	}))
	clock.now = clock.now.Add(2 * time.Second)
	p.handleTaskMessage(progressFrame(taskID, sessionID, 2, protocol.TaskMessagePayload{
		Type: "tool_result", Tool: "Bash",
		Output: `"clean\ntree"`,
	}))

	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.patched) != 1 {
		t.Fatalf("the result frame must patch the card; patched=%d", len(api.patched))
	}
	last := api.patched[0].CardJSON
	if !strings.Contains(last, "工具调用 · Bash · git status") {
		t.Errorf("card must carry the compact tool title; got %s", last)
	}
	rows := progressPlainTextRows(t, last)
	if len(rows) != 2 || rows[1] != "<font color='grey'>工具结果 · </font>⚪<font color='grey'> Bash · clean tree</font>" {
		t.Errorf("card must carry the compact unwrapped, colour-safe result preview; rows=%q", rows)
	}
}
