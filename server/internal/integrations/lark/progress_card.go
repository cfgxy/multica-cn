package lark

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// The progress card is the process view of a run: one updatable card that
// carries what the agent is doing, patched forward as the run proceeds, and
// stopped at a terminal frame. It deliberately does NOT carry the answer —
// the answer stays the separate ordinary message sendChatReply posts, and the
// card's own footer says so. That separation is what distinguishes this from
// the card lifecycle removed earlier (see Patcher's doc comment): the reply is
// still a plain IM message, and the card is an extra, visibly-stopped artifact
// beside it.
const (
	// progressPatchInterval is the floor between two Lark writes for ONE chat.
	// Scheduling is per-chat rather than per-task on purpose: the write quota
	// Lark enforces is the app's, so N concurrent tasks in one chat must share
	// one budget instead of each claiming its own.
	progressPatchInterval = 1500 * time.Millisecond

	// progressMaxEntries bounds the rendered window. A long run produces
	// hundreds of frames; the card shows the most recent ones and says so.
	progressMaxEntries = 10

	// progressEntryMaxRunes caps one entry's text so a single huge thinking
	// block cannot push the card past Lark's payload limit.
	progressEntryMaxRunes = 160

	// progressRateLimitBackoff is how long a chat stays silent after Lark
	// answered a write with a rate-limit code. Intermediate frames inside the
	// window are dropped; a terminal frame waits instead (bounded below).
	progressRateLimitBackoff = 10 * time.Second

	// progressFinalMaxWait bounds how long the terminal frame may wait for the
	// interval or a backoff to elapse. The final frame must LAND, so after the
	// bounded wait it is attempted regardless.
	progressFinalMaxWait = 5 * time.Second

	// progressFinalAttempts is how many times the terminal frame is retried
	// when Lark keeps answering with a rate limit.
	progressFinalAttempts = 3

	// progressScheduleIdleTTL / progressMaxSchedules bound the per-chat
	// schedule map so a server that has seen many chats does not hold one
	// entry per chat forever.
	progressScheduleIdleTTL = 10 * time.Minute
	progressMaxSchedules    = 1024

	// larkRateLimitCode is Lark's "too many requests" business code. It is
	// deliberately absent from threadReplyUnsupportedCodes (a rate limit says
	// nothing about the target), and is the one code this scheduler backs off on.
	larkRateLimitCode = 230020
)

// ProgressState is the lifecycle state the card header and footer render.
type ProgressState string

const (
	ProgressStateRunning   ProgressState = "running"
	ProgressStateCompleted ProgressState = "completed"
	ProgressStateFailed    ProgressState = "failed"
	ProgressStateCancelled ProgressState = "cancelled"
)

// ProgressEntryKind classifies one line of the process view.
type ProgressEntryKind string

const (
	ProgressEntryThinking   ProgressEntryKind = "thinking"
	ProgressEntryToolUse    ProgressEntryKind = "tool_use"
	ProgressEntryToolResult ProgressEntryKind = "tool_result"
	ProgressEntryError      ProgressEntryKind = "error"
)

// ProgressEntry is one rendered line. Tool arguments and tool output are
// deliberately absent: they are exactly where a token, a cookie or raw PII
// shows up (a shell command with an Authorization header, an API response
// body), and the card is posted into a chat the agent does not control. The
// tool NAME and a success dot carry the progress signal without the payload.
type ProgressEntry struct {
	Kind ProgressEntryKind
	Text string
	Tool string
	// Success is three-valued like protocol.TaskMessagePayload.IsError: nil
	// means the runtime reported nothing, which renders as a neutral dot rather
	// than a false "succeeded".
	Success *bool
}

// ProgressInput is the snapshot a ProgressRenderer turns into card JSON.
type ProgressInput struct {
	AgentName string
	State     ProgressState
	Entries   []ProgressEntry
	Truncated bool
}

// ProgressRenderer builds the progress card body. Separate from Renderer
// because the two produce different card generations: Renderer still drives
// the schema-1.0 error card, this one the schema-2.0 process card.
type ProgressRenderer interface {
	RenderProgress(in ProgressInput) (CardRender, error)
}

type defaultProgressRenderer struct{}

// NewDefaultProgressRenderer returns the production progress-card renderer.
func NewDefaultProgressRenderer() ProgressRenderer { return &defaultProgressRenderer{} }

// progressStateMeta is the per-state header suffix, header colour and footer.
// The footer is load-bearing product text, not decoration: it tells the reader
// the card has stopped and where the actual answer is, which is what keeps a
// stopped process card from reading as a truncated reply.
func progressStateMeta(state ProgressState) (titleSuffix, template, footer string) {
	switch state {
	case ProgressStateCompleted:
		return "已完成", "green", "本过程卡片已停止更新，完整答复见下一条消息。"
	case ProgressStateFailed:
		return "失败", "red", "本过程卡片已停止更新（失败），完整错误说明见下一条消息。"
	case ProgressStateCancelled:
		return "已取消", "grey", "本过程卡片已停止更新（已取消），本次运行没有产生答复。"
	default:
		return "进行中", "blue", ""
	}
}

func progressKindLabel(kind ProgressEntryKind) (label, color string) {
	switch kind {
	case ProgressEntryToolUse:
		return "工具调用", "blue"
	case ProgressEntryToolResult:
		return "工具结果", "green"
	case ProgressEntryError:
		return "错误", "red"
	default:
		return "思考", "grey"
	}
}

// progressResultDot renders the three-valued tool outcome.
func progressResultDot(success *bool) string {
	switch {
	case success == nil:
		return "⚪"
	case *success:
		return "🟢"
	default:
		return "🔴"
	}
}

func renderProgressEntryElement(e ProgressEntry) map[string]any {
	if e.Kind == ProgressEntryThinking {
		return map[string]any{
			"tag": "div",
			"text": map[string]any{
				"tag":        "plain_text",
				"content":    "💭 " + e.Text,
				"text_size":  "notation",
				"text_color": "grey",
			},
		}
	}
	label, color := progressKindLabel(e.Kind)
	var body string
	switch e.Kind {
	case ProgressEntryToolUse:
		body = "**" + e.Tool + "**"
	case ProgressEntryToolResult:
		body = progressResultDot(e.Success) + " **" + e.Tool + "**"
	default:
		body = e.Text
	}
	return map[string]any{
		"tag":     "markdown",
		"content": fmt.Sprintf("<text_tag color='%s'>%s</text_tag> %s", color, label, body),
	}
}

func (defaultProgressRenderer) RenderProgress(in ProgressInput) (CardRender, error) {
	name := in.AgentName
	if name == "" {
		name = "Multica"
	}
	suffix, template, footer := progressStateMeta(in.State)

	elements := make([]any, 0, len(in.Entries)*2+4)
	if in.Truncated {
		elements = append(elements, map[string]any{
			"tag": "div",
			"text": map[string]any{
				"tag":        "plain_text",
				"content":    "仅显示最近更新。",
				"text_size":  "notation",
				"text_color": "grey",
			},
		}, map[string]any{"tag": "hr"})
	}
	for i, e := range in.Entries {
		if i > 0 {
			elements = append(elements, map[string]any{"tag": "hr"})
		}
		elements = append(elements, renderProgressEntryElement(e))
	}
	if footer != "" {
		elements = append(elements, map[string]any{"tag": "hr"}, map[string]any{
			"tag": "div",
			"text": map[string]any{
				"tag":        "plain_text",
				"content":    footer,
				"text_size":  "notation",
				"text_color": "grey",
			},
		})
	}

	// update_multi MUST be present: Lark silently no-ops a PATCH against a card
	// whose config does not declare it updatable, which would leave the card
	// frozen on its first frame while every local write looked successful.
	doc := map[string]any{
		"schema": "2.0",
		"config": map[string]any{
			"wide_screen_mode": true,
			"update_multi":     true,
		},
		"header": map[string]any{
			"title":    map[string]any{"tag": "plain_text", "content": name + " · " + suffix},
			"template": template,
		},
		"body": map[string]any{"elements": elements},
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		return CardRender{}, err
	}
	return CardRender{JSON: string(raw)}, nil
}

// progressSchedule is the per-chat write budget. Every field is guarded by mu,
// which is also held across the Lark call so an in-flight write blocks the next
// one instead of racing it.
type progressSchedule struct {
	mu          sync.Mutex
	lastWrite   time.Time
	backoffTill time.Time

	// refs / idleSince let the owning map evict a chat nobody is streaming to.
	refs      int
	idleSince time.Time
}

// waitFor is how long the caller must wait before writing, bounded by
// progressFinalMaxWait. Intermediate frames treat a non-zero result as "drop";
// the terminal frame waits it out.
func (s *progressSchedule) waitFor(now time.Time) time.Duration {
	wait := time.Duration(0)
	if d := s.lastWrite.Add(progressPatchInterval).Sub(now); d > wait {
		wait = d
	}
	if d := s.backoffTill.Sub(now); d > wait {
		wait = d
	}
	if wait > progressFinalMaxWait {
		wait = progressFinalMaxWait
	}
	if wait < 0 {
		wait = 0
	}
	return wait
}

// progressCard is one task's accumulated process view plus the identity of the
// Lark message carrying it. It holds no decrypted credential: the installation
// is re-resolved per frame, so a secret rotation between two frames is picked
// up and no plaintext secret lives in this map.
type progressCard struct {
	mu sync.Mutex

	chatKey  string
	schedule *progressSchedule

	agentName string
	entries   []ProgressEntry
	truncated bool

	cardMessageID string
	rowID         pgtype.UUID
	finalized     bool
}

// appendEntry adds one line, coalescing consecutive text frames. Text frames
// are incremental deltas of one assistant turn (the same assumption telegram's
// outbound makes when it accumulates them), so giving each delta its own window
// slot would evict real tool activity to render one sentence letter by letter.
func (c *progressCard) appendEntry(e ProgressEntry) {
	if e.Kind == ProgressEntryThinking && len(c.entries) > 0 {
		if last := &c.entries[len(c.entries)-1]; last.Kind == ProgressEntryThinking {
			last.Text = clampProgressText(last.Text + e.Text)
			return
		}
	}
	c.entries = append(c.entries, e)
	if len(c.entries) > progressMaxEntries {
		c.entries = append([]ProgressEntry(nil), c.entries[len(c.entries)-progressMaxEntries:]...)
		c.truncated = true
	}
}

func (c *progressCard) snapshot(state ProgressState) ProgressInput {
	return ProgressInput{
		AgentName: c.agentName,
		State:     state,
		Entries:   append([]ProgressEntry(nil), c.entries...),
		Truncated: c.truncated,
	}
}

func clampProgressText(s string) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= progressEntryMaxRunes {
		return s
	}
	return string(r[:progressEntryMaxRunes]) + "…"
}

// progressCards owns the live cards and the per-chat schedules. Lock order is
// always progressCards.mu → schedule.mu → card.mu, never the inverse.
type progressCards struct {
	p *Patcher

	mu        sync.Mutex
	cards     map[string]*progressCard     // key: task id string
	schedules map[string]*progressSchedule // key: real Lark chat id
}

func newProgressCards(p *Patcher) *progressCards {
	return &progressCards{
		p:         p,
		cards:     make(map[string]*progressCard),
		schedules: make(map[string]*progressSchedule),
	}
}

// acquire returns the task's card, creating it (and retaining its chat
// schedule) on first use. A nil result means the chat budget is exhausted.
func (pc *progressCards) acquire(taskKey, chatKey string) *progressCard {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	if card, ok := pc.cards[taskKey]; ok {
		return card
	}
	sched, ok := pc.schedules[chatKey]
	if !ok {
		if len(pc.schedules) >= progressMaxSchedules {
			pc.sweepIdleLocked()
		}
		if len(pc.schedules) >= progressMaxSchedules {
			return nil
		}
		sched = &progressSchedule{}
		pc.schedules[chatKey] = sched
	}
	sched.refs++
	card := &progressCard{chatKey: chatKey, schedule: sched}
	pc.cards[taskKey] = card
	return card
}

func (pc *progressCards) lookup(taskKey string) *progressCard {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	return pc.cards[taskKey]
}

// release drops the task's card and un-refs its chat schedule.
func (pc *progressCards) release(taskKey string) {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	card, ok := pc.cards[taskKey]
	if !ok {
		return
	}
	delete(pc.cards, taskKey)
	if sched := pc.schedules[card.chatKey]; sched != nil {
		sched.refs--
		if sched.refs <= 0 {
			sched.refs = 0
			sched.idleSince = pc.p.cfg.Now()
		}
	}
}

func (pc *progressCards) sweepIdleLocked() {
	cutoff := pc.p.cfg.Now().Add(-progressScheduleIdleTTL)
	for key, sched := range pc.schedules {
		if sched.refs == 0 && !sched.idleSince.IsZero() && sched.idleSince.Before(cutoff) {
			delete(pc.schedules, key)
		}
	}
}

// progressEntryFromPayload maps one task message onto a rendered line, or
// reports false for frames the process view does not show.
func progressEntryFromPayload(payload protocol.TaskMessagePayload) (ProgressEntry, bool) {
	switch payload.Type {
	case "text":
		if payload.Content == "" {
			return ProgressEntry{}, false
		}
		return ProgressEntry{Kind: ProgressEntryThinking, Text: clampProgressText(payload.Content)}, true
	case "tool_use":
		if payload.Tool == "" {
			return ProgressEntry{}, false
		}
		return ProgressEntry{Kind: ProgressEntryToolUse, Tool: payload.Tool}, true
	case "tool_result":
		if payload.Tool == "" {
			return ProgressEntry{}, false
		}
		e := ProgressEntry{Kind: ProgressEntryToolResult, Tool: payload.Tool}
		if payload.IsError != nil {
			ok := !*payload.IsError
			e.Success = &ok
		}
		return e, true
	case "error":
		return ProgressEntry{Kind: ProgressEntryError, Text: clampProgressText(payload.Content)}, true
	default:
		return ProgressEntry{}, false
	}
}

// handleTaskMessage is the streaming entry point: one task message becomes one
// process-view line and, when the chat's write budget allows, one Lark write.
func (p *Patcher) handleTaskMessage(e events.Event) {
	payload, ok := e.Payload.(protocol.TaskMessagePayload)
	if !ok {
		return
	}
	entry, ok := progressEntryFromPayload(payload)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := p.progress.push(ctx, e, entry); err != nil {
		p.cfg.Logger.Warn("lark progress card: frame failed",
			"task_id", e.TaskID, "chat_session_id", e.ChatSessionID, "error", err)
	}
}

func (pc *progressCards) push(ctx context.Context, e events.Event, entry ProgressEntry) error {
	// Production TaskMessage events carry no session identity (publishTask
	// stamps only the TaskID hint; TaskMessagePayload has no chat_session_id),
	// so the session comes from the task's delivery path below, not from the
	// event. taskAndSessionFromEvent still supplies the fallbacks it always
	// did; only the identity SOURCE changes here.
	taskID, _, ok := taskAndSessionFromEvent(e)
	if !ok {
		return nil
	}
	target, err := pc.p.resolveOutboundTarget(ctx, taskID)
	if err != nil || target == nil {
		return err
	}
	chatSessionID := target.chatSessionID
	if !chatSessionID.Valid {
		// A delivery snapshot only exists for tasks a channel conversation
		// started, and those tasks own a session; an invalid one means the
		// data invariant is broken. Refuse to guess a target for the card.
		return nil
	}

	taskKey := uuidString(taskID)
	chatKey := string(outboundChatID(target.binding))
	card := pc.acquire(taskKey, chatKey)
	if card == nil {
		return nil
	}

	card.mu.Lock()
	if card.finalized {
		card.mu.Unlock()
		return nil
	}
	card.agentName = target.agentName
	card.appendEntry(entry)
	card.mu.Unlock()

	// TryLock-and-drop: an intermediate frame that cannot have the chat's write
	// slot is not worth queueing, because the frame behind it already carries a
	// newer snapshot of the same card.
	if !card.schedule.mu.TryLock() {
		return nil
	}
	defer card.schedule.mu.Unlock()
	if card.schedule.waitFor(pc.p.cfg.Now()) > 0 {
		return nil
	}
	return pc.write(ctx, card, target, chatSessionID, ProgressStateRunning)
}

// finalize writes the card's last frame and drops its state. It waits out a
// pending interval or backoff (bounded) rather than dropping, because this is
// the frame that tells the reader the card has stopped. A task with no card —
// a short run that produced no progress frame, or a non-Lark task — is a no-op.
func (pc *progressCards) finalize(ctx context.Context, taskID, chatSessionID pgtype.UUID, state ProgressState) error {
	taskKey := uuidString(taskID)
	card := pc.lookup(taskKey)
	if card == nil {
		return nil
	}
	defer pc.release(taskKey)

	target, err := pc.p.resolveOutboundTarget(ctx, taskID)
	if err != nil || target == nil {
		return err
	}

	card.mu.Lock()
	if card.finalized {
		card.mu.Unlock()
		return nil
	}
	card.finalized = true
	hasCard := card.cardMessageID != ""
	card.mu.Unlock()
	if !hasCard {
		// Nothing was ever sent (every frame was throttled or the first send
		// failed); a terminal-only card would show a stopped process view with
		// no process in it.
		return nil
	}

	card.schedule.mu.Lock()
	defer card.schedule.mu.Unlock()
	for attempt := 0; attempt < progressFinalAttempts; attempt++ {
		if wait := card.schedule.waitFor(pc.p.cfg.Now()); wait > 0 {
			if werr := pc.p.cfg.Wait(ctx, wait); werr != nil {
				break
			}
		}
		err = pc.write(ctx, card, target, chatSessionID, state)
		if err == nil || !isLarkRateLimited(err) {
			break
		}
	}
	return err
}

// write renders the current snapshot and pushes it to Lark. The caller holds
// the chat schedule lock, so exactly one write per chat is in flight.
func (pc *progressCards) write(ctx context.Context, card *progressCard, target *outboundTarget, chatSessionID pgtype.UUID, state ProgressState) error {
	card.mu.Lock()
	in := card.snapshot(state)
	messageID := card.cardMessageID
	card.mu.Unlock()

	render, err := pc.p.cfg.ProgressRenderer.RenderProgress(in)
	if err != nil {
		return fmt.Errorf("render progress card: %w", err)
	}

	now := pc.p.cfg.Now()
	if messageID == "" {
		err = pc.send(ctx, card, target, chatSessionID, render)
	} else {
		err = pc.p.client.PatchInteractiveCard(ctx, PatchCardParams{
			InstallationID:    target.creds,
			LarkCardMessageID: messageID,
			CardJSON:          render.JSON,
		})
	}

	// The attempt consumed the chat's slot whether or not Lark accepted it;
	// retrying immediately after a rejection is how a rate limit becomes a
	// rate-limit storm.
	card.schedule.lastWrite = now
	switch {
	case err == nil:
		card.schedule.backoffTill = time.Time{}
	case isLarkRateLimited(err):
		card.schedule.backoffTill = now.Add(progressRateLimitBackoff)
	}
	if err != nil {
		return err
	}

	if state != ProgressStateRunning {
		pc.markTerminal(ctx, card, state)
	}
	return nil
}

// send creates the card message, records the outbound row, and takes the
// Typing badge off. The badge and the card are both "processing" signals: once
// the card is on screen carrying its running header the badge is redundant, so it comes off
// at the first frame rather than waiting for the reply. processEvent still
// clears it before every reply, which stays the safety net for a run whose
// card never landed. ("进行中" is the running card's own header suffix.)
func (pc *progressCards) send(ctx context.Context, card *progressCard, target *outboundTarget, chatSessionID pgtype.UUID, render CardRender) error {
	var messageID string
	err := sendWithThreadFallback(pc.p.cfg.Logger, "send progress card", threadReplyTarget(target.binding), func(t ReplyTarget) error {
		id, serr := pc.p.client.SendInteractiveCard(ctx, SendCardParams{
			InstallationID: target.creds,
			ChatID:         outboundChatID(target.binding),
			CardJSON:       render.JSON,
			ReplyTarget:    t,
		})
		messageID = id
		return serr
	})
	if err != nil {
		return err
	}

	card.mu.Lock()
	card.cardMessageID = messageID
	card.mu.Unlock()

	row, rerr := pc.p.queries.CreateLarkOutboundCardMessage(ctx, CreateOutboundCardMessageParams{
		ChatSessionID:        chatSessionID,
		ChannelChatID:        string(outboundChatID(target.binding)),
		ChannelCardMessageID: messageID,
		Status:               string(CardStatusStreaming),
		TaskID:               target.taskID,
	})
	if rerr != nil {
		// The card is already on screen; losing its row costs bookkeeping, not
		// the process view, so the frame is not failed for it.
		pc.p.cfg.Logger.Warn("lark progress card: persist outbound row failed",
			"task_id", uuidString(target.taskID), "error", rerr)
	} else {
		card.mu.Lock()
		card.rowID = row.ID
		card.mu.Unlock()
	}

	if pc.p.typingIndicator != nil {
		pc.p.typingIndicator.Clear(ctx, chatSessionID)
	}
	return nil
}

func (pc *progressCards) markTerminal(ctx context.Context, card *progressCard, state ProgressState) {
	card.mu.Lock()
	rowID := card.rowID
	card.mu.Unlock()
	if !rowID.Valid {
		return
	}
	status := CardStatusFinal
	if state == ProgressStateFailed {
		status = CardStatusError
	}
	if err := pc.p.queries.UpdateLarkOutboundCardStatus(ctx, UpdateOutboundCardStatusParams{
		ID:     rowID,
		Status: string(status),
	}); err != nil {
		pc.p.cfg.Logger.Warn("lark progress card: status update failed",
			"card_row_id", uuidString(rowID), "error", err)
	}
}

// isLarkRateLimited reports whether Lark answered with its rate-limit verdict.
// Read through larkErrorCode so both envelope shapes are covered; an error with
// no Lark code (transport failure, timeout, a proxy's HTML 502) yields 0 and is
// NOT treated as a rate limit — backing off on an ambiguous failure would delay
// the final frame for a reason Lark never gave.
func isLarkRateLimited(err error) bool {
	if larkErrorCode(err) == larkRateLimitCode {
		return true
	}
	var statusErr *larkAPIStatusError
	return errors.As(err, &statusErr) && statusErr.StatusCode == 429
}

// progressWait is the production PatcherConfig.Wait: a cancellable sleep.
func progressWait(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
