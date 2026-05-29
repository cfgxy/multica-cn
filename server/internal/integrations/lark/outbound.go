package lark

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/events"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// CardStatus mirrors lark_outbound_card_message.status. Kept as a typed
// alias so callers can't pass arbitrary strings into the status column.
type CardStatus string

const (
	CardStatusPending   CardStatus = "pending"
	CardStatusStreaming CardStatus = "streaming"
	CardStatusFinal     CardStatus = "final"
	CardStatusError     CardStatus = "error"
)

// CardKind enumerates the small set of card variants the patcher
// renders. The Renderer is plug-replaceable so the on-wire card
// template can evolve without touching the patcher's transport / DB
// logic.
type CardKind string

const (
	CardKindThinking CardKind = "thinking"
	CardKindRunning  CardKind = "running"
	CardKindFinal    CardKind = "final"
	CardKindError    CardKind = "error"
)

// CardRender is the rendered card body the Renderer produces. The
// patcher serializes the JSON before handing it to APIClient.
type CardRender struct {
	JSON string
}

// RenderInput is the (typed) snapshot the Renderer sees when building
// or patching a card. Fields are populated as they become available
// during a task lifecycle — IssueNumber is set for `/issue` flows,
// Content is set for completed chat tasks, ErrorMessage for failed.
type RenderInput struct {
	Kind         CardKind
	AgentName    string
	IssueNumber  int32
	IssueID      pgtype.UUID
	TaskID       pgtype.UUID
	Content      string
	ErrorMessage string
}

// Renderer turns a typed RenderInput into the actual Lark card JSON.
// Centralizing this lets us swap card templates (or A/B them) without
// touching event subscription or persistence code.
type Renderer interface {
	Render(in RenderInput) (CardRender, error)
}

// defaultRenderer produces minimal text-only cards that work against
// Lark's generic interactive-card schema. The exact JSON layout will
// be refined when the real product card design lands; this default
// keeps the wiring real (the JSON deserializes against Lark's schema)
// without committing the product to a particular template.
type defaultRenderer struct{}

// NewDefaultRenderer returns the production-default Renderer. Override
// via PatcherConfig.Renderer when a custom template is needed.
func NewDefaultRenderer() Renderer { return &defaultRenderer{} }

func (defaultRenderer) Render(in RenderInput) (CardRender, error) {
	header := "Multica"
	if in.AgentName != "" {
		header = in.AgentName
	}
	var body string
	switch in.Kind {
	case CardKindThinking:
		body = "Thinking…"
	case CardKindRunning:
		body = "Working on it…"
	case CardKindFinal:
		body = in.Content
		if body == "" {
			body = "Done."
		}
	case CardKindError:
		body = "Run failed."
		if in.ErrorMessage != "" {
			body = "Run failed: " + in.ErrorMessage
		}
	default:
		return CardRender{}, fmt.Errorf("unknown card kind %q", in.Kind)
	}
	// update_multi MUST be true on every render: Lark refuses to apply
	// PatchInteractiveCard to a card whose config does not declare it
	// a "shared, updatable" card. Since this renderer drives the
	// thinking → streaming → final/error lifecycle (the card is sent
	// once and patched multiple times), an absent update_multi causes
	// every patch after the first send to silently no-op on the
	// Lark side while the local outbound status row still flips to
	// streaming/final. Keep this on every kind — including thinking
	// and error — because that initial JSON IS the body Lark stores
	// and consults for subsequent patches.
	doc := map[string]any{
		"config": map[string]any{
			"wide_screen_mode": true,
			"update_multi":     true,
		},
		"header": map[string]any{
			"template": "blue",
			"title":    map[string]any{"tag": "plain_text", "content": header},
		},
		"elements": []any{
			map[string]any{
				"tag": "div",
				"text": map[string]any{
					"tag":     "plain_text",
					"content": body,
				},
			},
		},
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		return CardRender{}, err
	}
	return CardRender{JSON: string(raw)}, nil
}

// PatcherQueries is the narrow subset of *db.Queries the Patcher
// needs. Declared as an interface so the patcher is unit-testable
// without a real Postgres connection.
type PatcherQueries interface {
	GetAgentTask(ctx context.Context, id pgtype.UUID) (db.AgentTaskQueue, error)
	GetChatSession(ctx context.Context, id pgtype.UUID) (db.ChatSession, error)
	GetAgent(ctx context.Context, id pgtype.UUID) (db.Agent, error)
	GetLarkInstallation(ctx context.Context, id pgtype.UUID) (db.LarkInstallation, error)
	GetLarkChatSessionBindingBySession(ctx context.Context, chatSessionID pgtype.UUID) (db.LarkChatSessionBinding, error)
	GetLarkOutboundCardByTask(ctx context.Context, taskID pgtype.UUID) (db.LarkOutboundCardMessage, error)
	CreateLarkOutboundCardMessage(ctx context.Context, arg db.CreateLarkOutboundCardMessageParams) (db.LarkOutboundCardMessage, error)
	UpdateLarkOutboundCardStatus(ctx context.Context, arg db.UpdateLarkOutboundCardStatusParams) error
}

// CredentialsResolver decrypts an installation's app_secret for the
// transport layer. *InstallationService satisfies it directly; tests
// substitute a fake.
type CredentialsResolver interface {
	DecryptAppSecret(inst db.LarkInstallation) (string, error)
}

// PatcherConfig tunes the streaming patcher. Defaults via
// withDefaults; tests typically override Renderer / Now / Logger.
type PatcherConfig struct {
	// MinPatchInterval throttles per-card patches so a streaming run
	// doesn't blow past Lark's per-card update rate-limit. Patches
	// issued sooner than this since the last one are dropped (the
	// final/error transition is never dropped — those bypass the
	// throttle).
	MinPatchInterval time.Duration

	Renderer Renderer
	Now      func() time.Time
	Logger   *slog.Logger
}

func (c PatcherConfig) withDefaults() PatcherConfig {
	if c.MinPatchInterval == 0 {
		c.MinPatchInterval = 500 * time.Millisecond
	}
	if c.Renderer == nil {
		c.Renderer = NewDefaultRenderer()
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	if c.Logger == nil {
		c.Logger = slog.Default()
	}
	return c
}

// Patcher reacts to task-lifecycle events on the event bus and keeps
// the Lark interactive card for each chat-driven task in sync. It is
// the §4.5 outbound side of the design: thinking → running/streaming
// → final/error.
//
// Scope:
//
//   - Only tasks whose chat_session has a lark_chat_session_binding
//     produce a card. Tasks born from the web UI or autopilot pass
//     through unchanged.
//
//   - The card binding is per-task (lark_outbound_card_message.task_id),
//     so a chat_session that hosts many runs gets one card per run.
//
//   - Throttling is per-card, in-memory. Multi-replica deployments
//     are de-duplicated at the next layer up: only the replica that
//     also holds the inbound WS lease for the installation will see
//     the run originate locally, and the events bus is per-process.
//     For the SaaS multi-node case, the same Redis fanout used by the
//     UI WS layer makes a future cross-process patcher cheap to add.
type Patcher struct {
	queries     PatcherQueries
	credentials CredentialsResolver
	client      APIClient
	cfg         PatcherConfig

	mu          sync.Mutex
	lastPatched map[string]time.Time // task_id -> last patch wall-clock time
}

// NewPatcher constructs a Patcher bound to its dependencies. The
// patcher does not subscribe to the bus until Register is called.
func NewPatcher(queries PatcherQueries, credentials CredentialsResolver, client APIClient, cfg PatcherConfig) *Patcher {
	cfg = cfg.withDefaults()
	return &Patcher{
		queries:     queries,
		credentials: credentials,
		client:      client,
		cfg:         cfg,
		lastPatched: make(map[string]time.Time),
	}
}

// Register subscribes the patcher to the task-lifecycle events it
// cares about on the supplied bus. Idempotent only if you call it
// against a fresh bus; call sites should invoke it exactly once
// during server boot (after the bus + patcher are constructed and
// before HTTP traffic starts).
//
// We deliberately do NOT subscribe to EventTaskCompleted here.
// TaskService publishes ChatDone (with the assistant message content)
// IMMEDIATELY BEFORE TaskCompleted (which has no content) for every
// chat task. Subscribing to both would patch the final card twice:
// the first patch shows the real reply, the second patch wipes it
// with the "Done." fallback because the TaskCompleted payload's
// `map[string]any` has no "content" key. EventChatDone is the
// canonical "agent finished replying" signal for the Patcher;
// EventTaskCompleted is left to other listeners (web UI, analytics,
// task usage rollup, etc.) where the lack of content doesn't matter.
func (p *Patcher) Register(bus *events.Bus) {
	bus.Subscribe(protocol.EventTaskQueued, p.handleEvent)
	bus.Subscribe(protocol.EventTaskRunning, p.handleEvent)
	bus.Subscribe(protocol.EventTaskFailed, p.handleEvent)
	bus.Subscribe(protocol.EventChatDone, p.handleEvent)
}

func (p *Patcher) handleEvent(e events.Event) {
	// Use a fresh background ctx with a tight timeout: bus delivery is
	// synchronous so a stuck Lark HTTP call would otherwise wedge the
	// whole publish call site.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := p.processEvent(ctx, e); err != nil {
		p.cfg.Logger.Warn("lark patcher: event handling failed",
			"event_type", e.Type,
			"task_id", e.TaskID,
			"chat_session_id", e.ChatSessionID,
			"error", err,
		)
	}
}

func (p *Patcher) processEvent(ctx context.Context, e events.Event) error {
	taskID, chatSessionID, ok := taskAndSessionFromEvent(e)
	if !ok {
		return nil
	}
	if !chatSessionID.Valid {
		// Issue / autopilot tasks have no chat_session.
		return nil
	}

	binding, err := p.queries.GetLarkChatSessionBindingBySession(ctx, chatSessionID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Web-only chat session — not a Lark target.
			return nil
		}
		return fmt.Errorf("lookup chat session binding: %w", err)
	}

	inst, err := p.queries.GetLarkInstallation(ctx, binding.InstallationID)
	if err != nil {
		return fmt.Errorf("load installation: %w", err)
	}
	if InstallationStatus(inst.Status) != InstallationActive {
		// Revoked between trigger and event; nothing to patch.
		return nil
	}
	creds, err := p.installationCredentials(inst)
	if err != nil {
		return err
	}

	agent, agentErr := p.queries.GetAgent(ctx, inst.AgentID)
	agentName := ""
	if agentErr == nil {
		agentName = agent.Name
	}

	switch e.Type {
	case protocol.EventTaskQueued, protocol.EventTaskRunning:
		return p.ensureCard(ctx, creds, binding, taskID, agentName, e.Type)
	case protocol.EventChatDone:
		return p.finalize(ctx, creds, binding, taskID, agentName, e.Payload)
	case protocol.EventTaskFailed:
		return p.fail(ctx, creds, binding, taskID, agentName, e.Payload)
	}
	return nil
}

func (p *Patcher) installationCredentials(inst db.LarkInstallation) (InstallationCredentials, error) {
	if p.credentials == nil {
		return InstallationCredentials{}, errors.New("lark patcher: credentials resolver missing")
	}
	secret, err := p.credentials.DecryptAppSecret(inst)
	if err != nil {
		return InstallationCredentials{}, fmt.Errorf("decrypt app_secret: %w", err)
	}
	creds := InstallationCredentials{
		AppID:     inst.AppID,
		AppSecret: secret,
	}
	if inst.TenantKey.Valid {
		creds.TenantKey = inst.TenantKey.String
	}
	return creds, nil
}

// ensureCard creates the initial "thinking" card on first sight of the
// task, or patches the existing card to its "running" body once the
// daemon claims the task. Both transitions respect the per-card
// throttle.
func (p *Patcher) ensureCard(ctx context.Context, creds InstallationCredentials, binding db.LarkChatSessionBinding, taskID pgtype.UUID, agentName string, eventType string) error {
	card, err := p.queries.GetLarkOutboundCardByTask(ctx, taskID)
	if errors.Is(err, pgx.ErrNoRows) {
		// First sight: render + send the thinking card, persist the binding.
		render, err := p.cfg.Renderer.Render(RenderInput{
			Kind:      CardKindThinking,
			AgentName: agentName,
			TaskID:    taskID,
		})
		if err != nil {
			return fmt.Errorf("render thinking card: %w", err)
		}
		cardMessageID, err := p.client.SendInteractiveCard(ctx, SendCardParams{
			InstallationID: creds,
			ChatID:         ChatID(binding.LarkChatID),
			CardJSON:       render.JSON,
		})
		if err != nil {
			return fmt.Errorf("send thinking card: %w", err)
		}
		if _, err := p.queries.CreateLarkOutboundCardMessage(ctx, db.CreateLarkOutboundCardMessageParams{
			ChatSessionID:     binding.ChatSessionID,
			LarkChatID:        binding.LarkChatID,
			LarkCardMessageID: cardMessageID,
			Status:            string(CardStatusPending),
			TaskID:            pgtype.UUID{Bytes: taskID.Bytes, Valid: true},
		}); err != nil {
			return fmt.Errorf("persist outbound card: %w", err)
		}
		p.markPatched(taskID)
		return nil
	}
	if err != nil {
		return fmt.Errorf("load existing card: %w", err)
	}
	// Existing card. Step it forward to streaming when the task
	// transitions to running; ignore duplicate queued events.
	if eventType != protocol.EventTaskRunning {
		return nil
	}
	if !p.shouldPatch(taskID) {
		return nil
	}
	render, err := p.cfg.Renderer.Render(RenderInput{
		Kind:      CardKindRunning,
		AgentName: agentName,
		TaskID:    taskID,
	})
	if err != nil {
		return fmt.Errorf("render running card: %w", err)
	}
	if err := p.client.PatchInteractiveCard(ctx, PatchCardParams{
		InstallationID:    creds,
		LarkCardMessageID: card.LarkCardMessageID,
		CardJSON:          render.JSON,
	}); err != nil {
		return fmt.Errorf("patch running card: %w", err)
	}
	if err := p.queries.UpdateLarkOutboundCardStatus(ctx, db.UpdateLarkOutboundCardStatusParams{
		ID:     card.ID,
		Status: string(CardStatusStreaming),
	}); err != nil {
		return fmt.Errorf("update card status: %w", err)
	}
	p.markPatched(taskID)
	return nil
}

func (p *Patcher) finalize(ctx context.Context, creds InstallationCredentials, binding db.LarkChatSessionBinding, taskID pgtype.UUID, agentName string, payload any) error {
	card, err := p.loadCardOrSkip(ctx, taskID)
	if err != nil || card == nil {
		return err
	}
	content := chatDoneContent(payload)
	render, err := p.cfg.Renderer.Render(RenderInput{
		Kind:      CardKindFinal,
		AgentName: agentName,
		TaskID:    taskID,
		Content:   content,
	})
	if err != nil {
		return fmt.Errorf("render final card: %w", err)
	}
	// Final transitions bypass the throttle: even if a streaming
	// patch fired 50ms ago, the user must see the final state.
	if err := p.client.PatchInteractiveCard(ctx, PatchCardParams{
		InstallationID:    creds,
		LarkCardMessageID: card.LarkCardMessageID,
		CardJSON:          render.JSON,
	}); err != nil {
		return fmt.Errorf("patch final card: %w", err)
	}
	if err := p.queries.UpdateLarkOutboundCardStatus(ctx, db.UpdateLarkOutboundCardStatusParams{
		ID:     card.ID,
		Status: string(CardStatusFinal),
	}); err != nil {
		return fmt.Errorf("update card final status: %w", err)
	}
	p.markPatched(taskID)
	return nil
}

func (p *Patcher) fail(ctx context.Context, creds InstallationCredentials, binding db.LarkChatSessionBinding, taskID pgtype.UUID, agentName string, payload any) error {
	card, err := p.loadCardOrSkip(ctx, taskID)
	if err != nil || card == nil {
		return err
	}
	render, err := p.cfg.Renderer.Render(RenderInput{
		Kind:         CardKindError,
		AgentName:    agentName,
		TaskID:       taskID,
		ErrorMessage: errorMessageFromPayload(payload),
	})
	if err != nil {
		return fmt.Errorf("render error card: %w", err)
	}
	if err := p.client.PatchInteractiveCard(ctx, PatchCardParams{
		InstallationID:    creds,
		LarkCardMessageID: card.LarkCardMessageID,
		CardJSON:          render.JSON,
	}); err != nil {
		return fmt.Errorf("patch error card: %w", err)
	}
	if err := p.queries.UpdateLarkOutboundCardStatus(ctx, db.UpdateLarkOutboundCardStatusParams{
		ID:     card.ID,
		Status: string(CardStatusError),
	}); err != nil {
		return fmt.Errorf("update card error status: %w", err)
	}
	p.markPatched(taskID)
	return nil
}

func (p *Patcher) loadCardOrSkip(ctx context.Context, taskID pgtype.UUID) (*db.LarkOutboundCardMessage, error) {
	card, err := p.queries.GetLarkOutboundCardByTask(ctx, taskID)
	if err == nil {
		return &card, nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		// No card was ever sent (task didn't originate via Lark);
		// quietly skip rather than spam logs.
		return nil, nil
	}
	return nil, fmt.Errorf("load existing card: %w", err)
}

func (p *Patcher) shouldPatch(taskID pgtype.UUID) bool {
	key := uuidString(taskID)
	p.mu.Lock()
	defer p.mu.Unlock()
	last, ok := p.lastPatched[key]
	if !ok {
		return true
	}
	return p.cfg.Now().Sub(last) >= p.cfg.MinPatchInterval
}

func (p *Patcher) markPatched(taskID pgtype.UUID) {
	key := uuidString(taskID)
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lastPatched[key] = p.cfg.Now()
}

// taskAndSessionFromEvent parses the typed-ish payload broadcastTaskEvent
// publishes — a map[string]any with `task_id` (always) and
// `chat_session_id` (chat tasks only). EventChatDone carries a
// ChatDonePayload struct instead.
func taskAndSessionFromEvent(e events.Event) (taskID, chatSessionID pgtype.UUID, ok bool) {
	if e.TaskID != "" {
		if err := taskID.Scan(e.TaskID); err != nil {
			taskID = pgtype.UUID{}
		}
	}
	if e.ChatSessionID != "" {
		if err := chatSessionID.Scan(e.ChatSessionID); err != nil {
			chatSessionID = pgtype.UUID{}
		}
	}
	switch p := e.Payload.(type) {
	case map[string]any:
		if !taskID.Valid {
			if s, _ := p["task_id"].(string); s != "" {
				_ = taskID.Scan(s)
			}
		}
		if !chatSessionID.Valid {
			if s, _ := p["chat_session_id"].(string); s != "" {
				_ = chatSessionID.Scan(s)
			}
		}
	case protocol.ChatDonePayload:
		if !taskID.Valid {
			_ = taskID.Scan(p.TaskID)
		}
		if !chatSessionID.Valid {
			_ = chatSessionID.Scan(p.ChatSessionID)
		}
	}
	return taskID, chatSessionID, taskID.Valid
}

func chatDoneContent(payload any) string {
	switch p := payload.(type) {
	case protocol.ChatDonePayload:
		return p.Content
	case map[string]any:
		if s, ok := p["content"].(string); ok {
			return s
		}
	}
	return ""
}

func errorMessageFromPayload(payload any) string {
	if m, ok := payload.(map[string]any); ok {
		if s, ok := m["error"].(string); ok {
			return s
		}
		if s, ok := m["error_message"].(string); ok {
			return s
		}
	}
	return ""
}
