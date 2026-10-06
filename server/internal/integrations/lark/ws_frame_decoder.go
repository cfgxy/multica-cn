package lark

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/multica-ai/multica/server/internal/integrations/channel/engine"
)

// LarkJSONFrameDecoder decodes the JSON event payload Lark nests
// inside a long-conn data Frame. The outer binary Frame envelope
// (ws_frame.go) is stripped by the connector; the decoder only sees
// the bytes from Frame.Payload, which Lark formats as the standard
// event-subscription envelope: {schema, header, event}.
//
// Three outcomes:
//
//   - (msg, true,  nil) — `im.message.receive_v1` event, or a validated
//     `card.action.trigger` button click (RUYI-461). The Hub forwards
//     through the Router.
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
type LarkJSONFrameDecoder struct {
	// commands validates card-button payloads against the shared command
	// registry: only a bare registered command token dispatches, so a
	// tampered value (e.g. "/issue evil title") is refused at the door.
	commands *engine.CommandRegistry
}

func NewLarkJSONFrameDecoder() *LarkJSONFrameDecoder {
	return &LarkJSONFrameDecoder{commands: engine.DefaultCommandRegistry()}
}

// Decode implements FrameDecoder.
func (d *LarkJSONFrameDecoder) Decode(payload []byte, inst Installation) (InboundMessage, bool, error) {
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

	if env.Header.EventType == cardActionEventType {
		return d.decodeCardAction(env)
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
		MessageType:  evt.Message.MessageType,
		Content:      evt.Message.Content,
		CreateTime:   evt.Message.CreateTime,
		// parent_id / root_id are populated by Lark only in reply
		// scenarios. The enricher keys quoted-reply expansion off
		// ParentID (the directly quoted message); RootID is carried for
		// completeness / future thread handling.
		ParentID: evt.Message.ParentID,
		RootID:   evt.Message.RootID,
		// thread_id is present only when the message lives inside a Lark
		// topic (话题). The outbound patcher uses it to decide whether to
		// reply back into that thread; empty means a normal chat message.
		ThreadID: evt.Message.ThreadID,
	}

	botUnionID := ""
	if inst.BotUnionID.Valid {
		botUnionID = inst.BotUnionID.String
	}

	// text + post are flattened synchronously here (no external calls — the
	// decoder must stay fast and dependency-free). merge_forward leaves Body
	// empty: it needs an HTTP round-trip to expand and is handled downstream by
	// the enricher, which keys off MessageType. Standalone media gets a short
	// visible marker while the channel adapter separately downloads and binds
	// the binary as a Multica attachment.
	switch evt.Message.MessageType {
	case "text", "post":
		msg.Body = resolveMentions(flattenContent(evt.Message.MessageType, evt.Message.Content),
			evt.Message.Mentions, inst.BotOpenID, botUnionID)
	case "image", "file", "audio", "media", "video":
		msg.Body = flattenContent(evt.Message.MessageType, evt.Message.Content)
	}

	// Snapshot the user's own text as the command source BEFORE any
	// enrichment runs. The enricher rewrites Body (prepending quoted /
	// forwarded context) but never touches CommandBody, so `/issue …`
	// is still parsed against what the user actually typed.
	msg.CommandBody = msg.Body

	if msg.ChatType == ChatTypeGroup {
		msg.AddressedToBot = containsMention(evt.Message.Mentions, inst.BotOpenID, botUnionID)
	}

	return msg, true, nil
}

// cardActionEventType is the Lark event_type for interactive-card button
// clicks. It arrives on the same long-conn WS as im.message.receive_v1,
// wrapped in the identical v2 envelope — only header.event_type differs.
const cardActionEventType = "card.action.trigger"

// larkCardActionEvent is the payload of card.action.trigger. Only the
// fields the command path needs are modeled: who clicked, which card
// message it belongs to, and the action value the clicked button was
// minted with.
type larkCardActionEvent struct {
	Operator struct {
		OpenID string `json:"open_id"`
	} `json:"operator"`
	Action struct {
		// Value is the button's business payload as OUR renderer wrote it
		// (see helpCard.go): {"v":1,"cmd":"/new","ct":"group"}. Lark
		// round-trips it verbatim; anything else is treated as tampered.
		Value map[string]any `json:"value"`
	} `json:"action"`
	Context struct {
		OpenChatID    string `json:"open_chat_id"`
		OpenMessageID string `json:"open_message_id"`
	} `json:"context"`
}

// cardActionValue is the schema OUR code writes into a card button value.
// v is a schema version so a future revision can evolve the shape without
// misreading old help cards still sitting in chat history.
type cardActionValue struct {
	V   int    `json:"v"`
	Cmd string `json:"cmd"`
	CT  string `json:"ct"`
}

// decodeCardAction turns a card.action.trigger envelope into an
// InboundMessage the shared Router can process. A button click carries no
// message of its own, so the synthesized body IS the minted command:
// Body/CommandBody = cmd, letting "/new" flow through the exact
// ParseControlCommand path a typed "/new" takes (RUYI-461 AC4 — one
// dispatch path, no copied business logic).
//
// Validation is strict and happens here, at the door:
//   - v must be 1 (unknown schema versions are refused, not guessed)
//   - ct must normalize to p2p or group (the event context carries no
//     chat_type, so the card mints its own)
//   - cmd must EXACTLY match a registered command token — no arguments,
//     no case folding. "/issue evil title" or "/ISSUE" leave Command
//     empty; the connector then error-toasts and never emits.
//
// A structurally valid envelope whose value fails the checks still
// returns (msg, true, nil) with CardAction set but Command empty: the
// caller needs the operator/chat context to ACK a meaningful error
// toast, and the empty Command is its signal not to dispatch.
//
// MessageID is header.event_id: Lark redelivers card actions with the
// same event_id, so the Router's message-level dedup absorbs button
// double-clicks and WS redeliveries for free.
func (d *LarkJSONFrameDecoder) decodeCardAction(env larkEventEnvelope) (InboundMessage, bool, error) {
	if env.Event == nil {
		return InboundMessage{}, false, errors.New("card.action.trigger with empty event payload")
	}
	var evt larkCardActionEvent
	if err := json.Unmarshal(env.Event, &evt); err != nil {
		return InboundMessage{}, false, fmt.Errorf("card action: %w", err)
	}

	action := &CardActionEvent{
		OperatorOpenID: evt.Operator.OpenID,
		OpenChatID:     evt.Context.OpenChatID,
		OpenMessageID:  evt.Context.OpenMessageID,
	}
	// Round-trip the free-form value map through JSON so a malformed or
	// unexpectedly-typed value degrades to a validation refusal instead of
	// a decode error the user gets no feedback for.
	raw, err := json.Marshal(evt.Action.Value)
	if err == nil {
		var v cardActionValue
		if jerr := json.Unmarshal(raw, &v); jerr == nil {
			action.Command, action.ChatType = d.validateCardActionValue(v)
		}
	}

	return InboundMessage{
		EventType:      cardActionEventType,
		EventID:        env.Header.EventID,
		AppID:          env.Header.AppID,
		ChatID:         ChatID(action.OpenChatID),
		ChatType:       action.ChatType,
		MessageID:      env.Header.EventID,
		SenderOpenID:   OpenID(action.OperatorOpenID),
		MessageType:    "interactive",
		Body:           action.Command,
		CommandBody:    action.Command,
		AddressedToBot: true,
		CardAction:     action,
	}, true, nil
}

// validateCardActionValue checks a minted button value against the shared
// command registry. It returns the validated command token ("" when
// refused) and the chat type to attribute. Refusal order: v schema
// mismatch, chat type outside {p2p, group}, cmd not an exact registry
// entry.
func (d *LarkJSONFrameDecoder) validateCardActionValue(v cardActionValue) (string, ChatType) {
	if v.V != 1 {
		return "", ""
	}
	ct := normalizeChatType(v.CT)
	if ct != ChatTypeP2P && ct != ChatTypeGroup {
		return "", ""
	}
	if _, ok := d.commands.Lookup(v.Cmd); !ok {
		return "", ""
	}
	return v.Cmd, ct
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
		// ParentID / RootID are only present when the message is a
		// reply / quote. ParentID is the directly quoted message;
		// RootID is the root of the reply tree.
		ParentID string `json:"parent_id"`
		RootID   string `json:"root_id"`
		// ThreadID is present only for messages inside a Lark topic
		// (话题). Lark omits it for plain chat messages, so its presence
		// is the signal that an @-mention happened inside a thread.
		ThreadID string `json:"thread_id"`
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
// Replacement is a single-pass token scan, not naive ReplaceAll. Two
// reasons:
//
//   - Prefix collision: a chat with eleven @-mentions exposes keys
//     `@_user_1` and `@_user_10`; ReplaceAll for `@_user_1` would
//     mangle the substring of `@_user_10`. We sort keys by length
//     DESC and try the longest match at each scan position so the
//     longer placeholder always wins.
//
//   - Whitespace fidelity: when we strip the bot mention we only
//     touch a single space immediately adjacent to it — either the
//     space after the placeholder, or, if there is none, a single
//     trailing space already in the output. Tabs, indentation, code
//     blocks, table pipes, and any other intentional whitespace in
//     the user's message are preserved verbatim.
func resolveMentions(text string, mentions []larkMention, botOpenID, botUnionID string) string {
	if text == "" || len(mentions) == 0 {
		return text
	}
	// Filter empty keys and sort longest first so `@_user_10` is
	// matched before `@_user_1` at any scan position.
	sorted := make([]larkMention, 0, len(mentions))
	for _, m := range mentions {
		if m.Key != "" {
			sorted = append(sorted, m)
		}
	}
	sort.SliceStable(sorted, func(i, j int) bool {
		return len(sorted[i].Key) > len(sorted[j].Key)
	})

	out := make([]byte, 0, len(text))
	i := 0
	for i < len(text) {
		var matched *larkMention
		for idx := range sorted {
			if strings.HasPrefix(text[i:], sorted[idx].Key) {
				matched = &sorted[idx]
				break
			}
		}
		if matched == nil {
			out = append(out, text[i])
			i++
			continue
		}
		end := i + len(matched.Key)
		switch {
		case isBotMention(*matched, botOpenID, botUnionID):
			// Strip: eat one adjacent space (after the placeholder
			// preferred; else backtrack one space we already emitted)
			// so the seam is not left with a double space or a
			// dangling leading space. Tabs / newlines / other chars
			// are untouched.
			if end < len(text) && text[end] == ' ' {
				end++
			} else if n := len(out); n > 0 && out[n-1] == ' ' {
				out = out[:n-1]
			}
		case matched.Name != "":
			out = append(out, '@')
			out = append(out, matched.Name...)
		default:
			// Unknown mention — leave the placeholder intact so the
			// agent at least sees a stable token.
			out = append(out, matched.Key...)
		}
		i = end
	}
	return string(out)
}

// isBotMention identifies whether a payload mention refers to THIS
// bot. Stays in lockstep with containsMention: when union_id is
// known we trust it exclusively (open_id is structurally inverted
// in multi-bot groups — matching on it would re-introduce the
// MUL-2671 routing bug). Only when union_id is missing do we fall
// back to open_id, which is correct in single-bot installs and the
// best we can do in pre-backfill rows.
func isBotMention(m larkMention, botOpenID, botUnionID string) bool {
	if botUnionID != "" {
		return m.ID.UnionID == botUnionID
	}
	if botOpenID == "" {
		return false
	}
	return m.ID.OpenID == botOpenID
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
