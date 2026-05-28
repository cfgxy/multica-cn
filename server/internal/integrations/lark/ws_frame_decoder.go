package lark

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// LarkJSONFrameDecoder decodes the JSON envelopes Lark's
// event-subscription long-conn pushes over each WebSocket message.
//
// The Lark open platform mixes a control surface (heartbeat / ack)
// with the actual event payload in a single channel; this decoder is
// the source of truth for which raw frames the dispatcher should ever
// see. It deliberately distinguishes three outcomes:
//
//   - (msg, true,  nil) — a real `im.message.receive_v1` event that
//     should land in chat_session. The Hub forwards it through the
//     Dispatcher; the connector does not introspect further.
//   - (zero, false, nil) — a recognized control frame (heartbeat,
//     `frame_ack`, etc.) or a non-message event we don't yet handle.
//     The connector drops these silently — they are expected traffic.
//   - (zero, false, err) — malformed JSON or a recognized event we
//     could not parse. The connector logs and drops the single frame,
//     does NOT tear the WS down (one bad frame should not amplify
//     into a reconnect storm).
//
// The Lark wire shape is the documented "p2_im_message_receive_v1"
// envelope; the broad outline is stable but Lark periodically adds
// fields. We extract only what the Dispatcher's InboundMessage needs
// and ignore the rest, which keeps the decoder forwards-compatible.
type LarkJSONFrameDecoder struct{}

// NewLarkJSONFrameDecoder constructs the default Lark JSON decoder.
// The decoder holds no per-installation state, so a single instance
// can serve every supervisor goroutine.
func NewLarkJSONFrameDecoder() *LarkJSONFrameDecoder { return &LarkJSONFrameDecoder{} }

// Decode implements FrameDecoder.
func (d *LarkJSONFrameDecoder) Decode(raw []byte, inst db.LarkInstallation) (InboundMessage, bool, error) {
	if len(raw) == 0 {
		return InboundMessage{}, false, nil
	}
	var env larkEventEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return InboundMessage{}, false, fmt.Errorf("envelope: %w", err)
	}

	// Heartbeats and acks come through with type=="heartbeat" or
	// "frame_ack" depending on the protocol version. Any frame that
	// is not an event_callback is dropped silently — including new
	// kinds Lark might introduce that we have not taught the
	// dispatcher about.
	if env.Type != "" && env.Type != "event_callback" {
		return InboundMessage{}, false, nil
	}

	// Validate the event header. The schema field discriminates
	// payload shape; we only handle the documented v2.0 message-receive
	// event in MVP, and drop everything else with a structured warning
	// (returned to the caller as a non-error "ok=false").
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

	// The body field for text messages carries a stringified JSON
	// object like `{"text":"..."}`; we extract just the text so the
	// dispatcher and the /issue parser see a plain string. Other
	// message types (image, file, post) are not in MVP scope and
	// surface as empty bodies — the dispatcher will still record the
	// chat_message, just with no content.
	switch evt.Message.MessageType {
	case "text":
		msg.Body = extractTextBody(evt.Message.Content)
	}

	// Group-mention discrimination: the WS payload includes a
	// `mentions` array; we set AddressedToBot=true when the
	// installation's bot_open_id appears there. Direct messages
	// (p2p) ignore the field per InboundMessage doc.
	if msg.ChatType == ChatTypeGroup {
		msg.AddressedToBot = containsMention(evt.Message.Mentions, inst.BotOpenID)
	}

	return msg, true, nil
}

// larkEventEnvelope mirrors the outer JSON Lark wraps every push in.
// We keep Event as raw JSON so payload-shape changes for individual
// event types do not force a decoder revision.
type larkEventEnvelope struct {
	Type   string           `json:"type"`
	Header larkEventHeader  `json:"header"`
	Event  json.RawMessage  `json:"event"`
	Schema string           `json:"schema"`
}

type larkEventHeader struct {
	EventID    string `json:"event_id"`
	EventType  string `json:"event_type"`
	CreateTime string `json:"create_time"`
	AppID      string `json:"app_id"`
	TenantKey  string `json:"tenant_key"`
}

// larkMessageReceiveEvent is the documented payload of
// im.message.receive_v1. Field names follow Lark's snake_case JSON.
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
		MessageID   string         `json:"message_id"`
		ChatID      string         `json:"chat_id"`
		ChatType    string         `json:"chat_type"`
		MessageType string         `json:"message_type"`
		Content     string         `json:"content"`
		Mentions    []larkMention  `json:"mentions"`
		CreateTime  string         `json:"create_time"`
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

func extractTextBody(content string) string {
	if content == "" {
		return ""
	}
	var doc struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal([]byte(content), &doc); err != nil {
		// Lark's text content is always a JSON object; if unmarshal
		// fails we leave Body empty rather than dump the raw envelope
		// into chat_session.
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

func containsMention(mentions []larkMention, botOpenID string) bool {
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
