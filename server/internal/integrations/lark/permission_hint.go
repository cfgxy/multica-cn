package lark

// Runtime permission-denied → in-chat authorization hint card (RUYI-400
// scope extension). When a REAL call on the inbound pipeline (enrich fetches,
// contact lookup, media download) fails with a Lark permission business
// code, the matching capability's 补授权 hint card goes into the same chat:
// what is missing (catalog-sourced copy + scopes, never hardcoded), and the
// same dev-console deep link the web permission panel renders
// (`/app/{app_id}/auth`, app-level public identifiers only).
//
// Delivery contract:
//   - Deduped per (installation, chat, capability): one card per missing
//     episode, never a per-message pile-up. The marker is held while the
//     capability stays missing.
//   - Recovery re-arms: the first successful call for that capability
//     releases the marker (and, grants being in place, failures stop — so
//     cards stop too). A later revocation notifies afresh.
//   - Send failures degrade silently: one warn log, the marker is released
//     so a later message retries the hint once, and the inbound pipeline is
//     never blocked or retried (the send runs detached, off the ACK budget).
//   - No new persistence: the dedup state is in-memory and bounded — a
//     restart re-arms at most one card per chat+capability, which is the
//     desired "fresh reminder after restart" behavior.

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	// permissionHintSendTimeout bounds one detached card send. Independent
	// of the enricher's EnrichTimeout on purpose: the hint fires after the
	// failed call and must never eat into the WS ACK budget.
	permissionHintSendTimeout = 10 * time.Second

	// permissionHintMaxTrackedKeys bounds the dedup map (chats with a
	// missing capability, per app). Eviction is arbitrary — the bound only
	// exists so a pathological tenant cannot grow the map forever.
	permissionHintMaxTrackedKeys = 1024
)

type permissionHintKey struct {
	appID      string
	chatID     ChatID
	capability CapabilityID
}

// permissionHintDeduper holds the "card already sent for this episode"
// markers. claim/release are atomic so concurrent enrich goroutines cannot
// double-send.
type permissionHintDeduper struct {
	mu   sync.Mutex
	sent map[permissionHintKey]struct{}
}

func (d *permissionHintDeduper) claim(key permissionHintKey) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.sent == nil {
		d.sent = make(map[permissionHintKey]struct{})
	}
	if _, held := d.sent[key]; held {
		return false
	}
	if len(d.sent) >= permissionHintMaxTrackedKeys {
		for stale := range d.sent {
			delete(d.sent, stale)
			break
		}
	}
	d.sent[key] = struct{}{}
	return true
}

func (d *permissionHintDeduper) release(key permissionHintKey) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.sent, key)
}

// PermissionHintSender observes runtime permission failures and posts the
// in-chat authorization hint card. Nil-safe: a nil *PermissionHintSender
// no-ops, so call sites on optional wiring paths need no nil checks.
type PermissionHintSender struct {
	client APIClient
	dedup  permissionHintDeduper
	logger *slog.Logger
	// goSend runs a send task; production defaults to a goroutine. Tests
	// override it to run inline for determinism.
	goSend func(func())
}

// NewPermissionHintSender wires the hint sender onto an API client.
func NewPermissionHintSender(client APIClient, logger *slog.Logger) *PermissionHintSender {
	if logger == nil {
		logger = slog.Default()
	}
	return &PermissionHintSender{
		client: client,
		logger: logger,
		goSend: func(f func()) { go f() },
	}
}

// ObserveDenied records a failed call. Only Lark permission-class failures
// produce a card (transport errors, rate limits and deleted-message codes
// say nothing about scopes). The send is asynchronous and detached: the
// caller's context (the WS ACK budget) is deliberately not inherited.
func (s *PermissionHintSender) ObserveDenied(ctx context.Context, creds InstallationCredentials, chatID ChatID, capability CapabilityID, err error) {
	if s == nil || err == nil || chatID == "" || creds.AppID == "" {
		return
	}
	if !isRuntimePermissionDenied(err) {
		return
	}
	key := permissionHintKey{appID: creds.AppID, chatID: chatID, capability: capability}
	if !s.dedup.claim(key) {
		return
	}
	s.goSend(func() {
		if !s.sendHintCard(ctx, creds, key) {
			s.dedup.release(key)
		}
	})
}

// ObserveSuccess records a succeeded call for the capability: recovery.
// The dedup marker is released so a future revocation notifies afresh
// (and, with the grant in place, no failures occur — cards stop).
func (s *PermissionHintSender) ObserveSuccess(appID string, chatID ChatID, capability CapabilityID) {
	if s == nil || appID == "" || chatID == "" {
		return
	}
	s.dedup.release(permissionHintKey{appID: appID, chatID: chatID, capability: capability})
}

// sendHintCard renders and posts the card. Returns false when nothing was
// sent (build error or send failure) — the caller releases the dedup
// marker so a later message can retry the hint.
func (s *PermissionHintSender) sendHintCard(ctx context.Context, creds InstallationCredentials, key permissionHintKey) bool {
	sendCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), permissionHintSendTimeout)
	defer cancel()
	cardJSON, err := permissionHintCardJSON(creds, key.capability)
	if err != nil {
		s.logger.Warn("lark permission hint: build card failed",
			"capability", string(key.capability), "err", err)
		return false
	}
	if _, err := s.client.SendInteractiveCard(sendCtx, SendCardParams{
		InstallationID: creds,
		ChatID:         key.chatID,
		CardJSON:       cardJSON,
	}); err != nil {
		// Silent degradation by contract: one sanitized warn, no retry,
		// never an error message into the chat.
		s.logger.Warn("lark permission hint: card send failed; degrading silently",
			"capability", string(key.capability), "chat_id", string(key.chatID), "err", err)
		return false
	}
	s.logger.Info("lark permission hint: authorization card sent",
		"capability", string(key.capability), "chat_id", string(key.chatID))
	return true
}

// isRuntimePermissionDenied reports whether err is a Lark permission-class
// failure. Exact business codes first (the probe's set, including the
// canonical 99991672), then the shared enricher classifier's code/text
// heuristics as the fallback.
func isRuntimePermissionDenied(err error) bool {
	if err == nil {
		return false
	}
	if code, _, ok := larkErrorCodeMsg(err); ok {
		if _, denied := probePermissionCodes[code]; denied {
			return true
		}
	}
	return classifyRecentContextFetchError(err).category == recentContextFailurePermissionDenied
}

// capabilityHintCopy is the in-chat copy for one capability. Mirrors the
// web panel's i18n labels (lark-tab.tsx) in server-rendered Chinese — the
// card, like the binding prompt card, is zh-only.
func capabilityHintCopy(id CapabilityID) (label, impact string) {
	switch id {
	case CapabilityReceiveMessages:
		return "接收消息", "机器人将收不到 @ 消息与私聊"
	case CapabilitySendMessages:
		return "发送消息", "机器人将无法回复任何消息"
	case CapabilityReadHistory:
		return "读取消息历史", "引用回复、合并转发与群聊上下文将不可用"
	case CapabilityMediaResources:
		return "下载消息附件", "图片、文件等附件将无法读取"
	case CapabilityContactLookup:
		return "解析成员名称", "群聊中发言人将显示为 User N"
	}
	return string(id), "相关功能将不可用"
}

// permissionHintCardJSON renders the interactive card. Scopes come from
// the capability catalog (the single source of truth) and the deep link
// carries only the app-level public app_id — no secrets, no tenant data.
func permissionHintCardJSON(creds InstallationCredentials, capability CapabilityID) (string, error) {
	if _, ok := CapabilitySpecByID(capability); !ok {
		return "", fmt.Errorf("lark permission hint: unknown capability %q", capability)
	}
	label, impact := capabilityHintCopy(capability)
	scopes := ScopesForCapability(capability)
	var scopeLines strings.Builder
	for _, scope := range scopes {
		scopeLines.WriteString("- `" + scope + "`\n")
	}
	consoleURL := creds.Region.OpenPlatformBaseURL() + "/app/" + url.PathEscape(creds.AppID) + "/auth"
	content := fmt.Sprintf("检测到机器人缺少「%s」能力，%s。\n\n"+
		"请管理员在飞书开发者后台为应用补授权：建议开通以下权限并发布版本后生效：\n%s",
		label, impact, scopeLines.String())
	doc := map[string]any{
		"config": map[string]any{"wide_screen_mode": true},
		"header": map[string]any{
			"template": "orange",
			"title":    map[string]any{"tag": "plain_text", "content": "Multica 权限不足提示"},
		},
		"elements": []any{
			map[string]any{
				"tag":  "div",
				"text": map[string]any{"tag": "lark_md", "content": content},
			},
			map[string]any{
				"tag": "action",
				"actions": []any{
					map[string]any{
						"tag":  "button",
						"text": map[string]any{"tag": "plain_text", "content": "前往授权"},
						"type": "primary",
						"url":  consoleURL,
					},
				},
			},
		},
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		return "", fmt.Errorf("lark permission hint: marshal card: %w", err)
	}
	return string(raw), nil
}
