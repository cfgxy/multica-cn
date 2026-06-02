package lark

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// LarkJSONFrameDecoder decodes the JSON event payload Lark nests
// inside a long-conn data Frame. The outer binary Frame envelope
// (ws_frame.go) is stripped by the connector; the decoder only sees
// the bytes from Frame.Payload, which Lark formats as the standard
// event-subscription envelope: {schema, header, event}.
//
// Three outcomes:
//
//   - (msg, true,  nil) — `im.message.receive_v1` event. The Hub
//     forwards through the Dispatcher.
//   - (zero, false, nil) — heartbeat-shaped JSON or an event_type we
//     don't yet handle (im.chat.access_event_v1, etc.). The connector
//     drops these silently and still sends a 200 ACK to Lark so the
//     server stops resending.
//   - (zero, false, err) — malformed JSON or schema we couldn't
//     parse. The connector logs + drops the single frame; the WS
//     connection stays up because one bad payload shouldn't amplify
//     into a reconnect storm.
//
// The decoder is stateless and goroutine-safe — a single instance
// serves every supervisor goroutine.
type LarkJSONFrameDecoder struct{}

func NewLarkJSONFrameDecoder() *LarkJSONFrameDecoder { return &LarkJSONFrameDecoder{} }

// Decode implements FrameDecoder.
func (d *LarkJSONFrameDecoder) Decode(payload []byte, inst db.LarkInstallation) (InboundMessage, bool, error) {
	if len(payload) == 0 {
		return InboundMessage{}, false, nil
	}
	var env larkEventEnvelope
	if err := json.Unmarshal(payload, &env); err != nil {
		return InboundMessage{}, false, fmt.Errorf("envelope: %w", err)
	}

	// Lark long-conn data frames are always v2 event envelopes
	// (schema "2.0"). The legacy webhook v1 "type":"event_callback"
	// shape is not used on long-conn — we accept it defensively in
	// case Lark adds a back-compat mode, but the canonical path is
	// schema-driven.
	if env.Type != "" && env.Type != "event_callback" {
		return InboundMessage{}, false, nil
	}

	if env.Header.EventType != "im.message.receive_v1" {
		return InboundMessage{}, false, nil
	}

	if env.Event == nil {
		return InboundMessage{}, false, errors.New("event_callback with empty event payload")
	}
	var evt larkMessageReceiveEvent
	if err := json.Unmarshal(env.Event, &evt); err != nil {
		return InboundMessage{}, false, fmt.Errorf("event: %w", err)
	}

	msg := InboundMessage{
		EventType:    env.Header.EventType,
		EventID:      env.Header.EventID,
		AppID:        env.Header.AppID,
		ChatID:       ChatID(evt.Message.ChatID),
		ChatType:     normalizeChatType(evt.Message.ChatType),
		MessageID:    evt.Message.MessageID,
		SenderOpenID: OpenID(evt.Sender.SenderID.OpenID),
	}

	botUnionID := ""
	if inst.BotUnionID.Valid {
		botUnionID = inst.BotUnionID.String
	}

	switch evt.Message.MessageType {
	case "text":
		msg.Body = resolveMentions(extractTextBody(evt.Message.Content),
			evt.Message.Mentions, inst.BotOpenID, botUnionID)
	}

	if msg.ChatType == ChatTypeGroup {
		msg.AddressedToBot = containsMention(evt.Message.Mentions, inst.BotOpenID, botUnionID)
	}

	return msg, true, nil
}

// larkEventEnvelope mirrors the outer JSON Lark wraps every push in.
type larkEventEnvelope struct {
	Schema string          `json:"schema"`
	Type   string          `json:"type"`
	Header larkEventHeader `json:"header"`
	Event  json.RawMessage `json:"event"`
}

type larkEventHeader struct {
	EventID    string `json:"event_id"`
	EventType  string `json:"event_type"`
	CreateTime string `json:"create_time"`
	AppID      string `json:"app_id"`
	TenantKey  string `json:"tenant_key"`
}

// larkMessageReceiveEvent is the documented payload of
// im.message.receive_v1.
type larkMessageReceiveEvent struct {
	Sender struct {
		SenderID struct {
			OpenID  string `json:"open_id"`
			UnionID string `json:"union_id"`
			UserID  string `json:"user_id"`
		} `json:"sender_id"`
		SenderType string `json:"sender_type"`
		TenantKey  string `json:"tenant_key"`
	} `json:"sender"`
	Message struct {
		MessageID   string        `json:"message_id"`
		ChatID      string        `json:"chat_id"`
		ChatType    string        `json:"chat_type"`
		MessageType string        `json:"message_type"`
		Content     string        `json:"content"`
		Mentions    []larkMention `json:"mentions"`
		CreateTime  string        `json:"create_time"`
	} `json:"message"`
}

type larkMention struct {
	Key string `json:"key"`
	ID  struct {
		OpenID  string `json:"open_id"`
		UnionID string `json:"union_id"`
		UserID  string `json:"user_id"`
	} `json:"id"`
	Name string `json:"name"`
}

// resolveMentions substitutes Lark's `@_user_N` placeholders so the
// agent receives a body that reads naturally and does not require
// resolving the mentions array itself. The bot's OWN mention is
// stripped (the dispatcher already routes the event on
// AddressedToBot — re-emitting `@<bot>` in front of every message
// makes both the chat transcript and any downstream LLM context
// noisier without adding signal). Other participants render as
// `@<displayName>`, falling back to leaving the placeholder alone
// when name is empty (defensive — Lark always populates it in
// practice).
//
// Whitespace cleanup: runs of horizontal whitespace introduced by
// stripping the bot mention are collapsed to a single space, and
// leading/trailing horizontal whitespace per line is trimmed. Line
// breaks in the original message are preserved.
func resolveMentions(text string, mentions []larkMention, botOpenID, botUnionID string) string {
	if text == "" || len(mentions) == 0 {
		return text
	}
	for _, m := range mentions {
		if m.Key == "" {
			continue
		}
		var rep string
		switch {
		case isBotMention(m, botOpenID, botUnionID):
			rep = ""
		case m.Name != "":
			rep = "@" + m.Name
		default:
			continue
		}
		text = strings.ReplaceAll(text, m.Key, rep)
	}
	return tidyMentionWhitespace(text)
}

func isBotMention(m larkMention, botOpenID, botUnionID string) bool {
	if botUnionID != "" && m.ID.UnionID == botUnionID {
		return true
	}
	if botOpenID != "" && m.ID.OpenID == botOpenID {
		return true
	}
	return false
}

// tidyMentionWhitespace collapses runs of spaces/tabs and trims
// horizontal whitespace at the start/end of each line. Newlines are
// preserved so multi-line user messages survive intact.
func tidyMentionWhitespace(s string) string {
	if s == "" {
		return s
	}
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		var b strings.Builder
		b.Grow(len(line))
		prevSpace := false
		for _, r := range line {
			if r == ' ' || r == '\t' {
				if !prevSpace {
					b.WriteByte(' ')
				}
				prevSpace = true
				continue
			}
			prevSpace = false
			b.WriteRune(r)
		}
		lines[i] = strings.TrimSpace(b.String())
	}
	return strings.Join(lines, "\n")
}

func extractTextBody(content string) string {
	if content == "" {
		return ""
	}
	var doc struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal([]byte(content), &doc); err != nil {
		return ""
	}
	return doc.Text
}

func normalizeChatType(t string) ChatType {
	switch strings.ToLower(t) {
	case "p2p":
		return ChatTypeP2P
	case "group":
		return ChatTypeGroup
	default:
		return ChatType(t)
	}
}

// containsMention answers "was THIS bot @-mentioned in this group event".
//
// The bot's stable identifier across WS perspectives is `union_id` —
// see MUL-2671 group-@-mention triage. In a Lark group with several
// Multica bots, each bot's WS receives the event, and Lark fills
// `mentions[].id.open_id` with the per-app form for whichever bot it
// is talking to: bot X's WS sees X's payload-form open_id when bot Y
// was @-ed, and a different payload-form open_id when X itself was
// the target. Only `union_id` is consistent across both WS streams.
//
// Match order:
//
//  1. When we know the bot's `union_id` (captured by GetBotInfo at
//     install time, persisted in lark_installation.bot_union_id),
//     compare against `mentions[].id.union_id`. This is the correct
//     path and is unambiguous in multi-bot deployments.
//
//  2. When `union_id` is unknown — single-bot installs created
//     before migration 112, or contact-scope-restricted operators
//     where /contact/v3/users denied the lookup — fall back to the
//     per-app `open_id` comparison. This is structurally inverted
//     in multi-bot group chats but is fine for the p2p/single-bot
//     case the WS sees most of the time, and avoids hard-failing
//     pre-backfill installations.
//
// Empty inputs short-circuit to false rather than matching every
// mention; that defends against an installation row that somehow
// has both identifiers blank.
func containsMention(mentions []larkMention, botOpenID, botUnionID string) bool {
	if botUnionID != "" {
		for _, m := range mentions {
			if m.ID.UnionID == botUnionID {
				return true
			}
		}
		return false
	}
	if botOpenID == "" {
		return false
	}
	for _, m := range mentions {
		if m.ID.OpenID == botOpenID {
			return true
		}
	}
	return false
}
