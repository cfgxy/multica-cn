package lark

import (
	"encoding/json"
	"testing"
)

// cardActionEnvelope builds a card.action.trigger WS frame payload exactly
// as Lark wraps it: the standard v2 event envelope, click context in
// event.context, and OUR minted value inside event.action.value.
func cardActionEnvelope(eventID, openID, chatID, msgID string, value map[string]any) []byte {
	env := map[string]any{
		"header": map[string]any{
			"event_id":   eventID,
			"event_type": cardActionEventType,
			"app_id":     "cli_app_x",
		},
		"event": map[string]any{
			"operator": map[string]any{"open_id": openID},
			"action":   map[string]any{"value": value},
			"context": map[string]any{
				"open_chat_id":    chatID,
				"open_message_id": msgID,
			},
		},
	}
	raw, err := json.Marshal(env)
	if err != nil {
		panic(err)
	}
	return raw
}

func decodeCardActionForTest(t *testing.T, value map[string]any) (InboundMessage, bool, error) {
	t.Helper()
	d := NewLarkJSONFrameDecoder()
	return d.Decode(cardActionEnvelope("evt-9", "ou_clicker", "oc_chat", "om_card", value), Installation{BotOpenID: "ou_bot"})
}

func mintedValue(v int, cmd, ct string) map[string]any {
	return map[string]any{"v": v, "cmd": cmd, "ct": ct}
}

func TestDecoderCardActionValidNewClick(t *testing.T) {
	t.Parallel()
	msg, ok, err := decodeCardActionForTest(t, mintedValue(1, "/new", "group"))
	if err != nil || !ok {
		t.Fatalf("Decode ok=%v err=%v", ok, err)
	}
	if msg.CardAction == nil {
		t.Fatal("CardAction should be non-nil")
	}
	if msg.CardAction.Command != "/new" {
		t.Errorf("Command = %q, want /new", msg.CardAction.Command)
	}
	if msg.CardAction.ChatType != ChatTypeGroup {
		t.Errorf("ChatType = %q, want group", msg.CardAction.ChatType)
	}
	if msg.CardAction.OperatorOpenID != "ou_clicker" {
		t.Errorf("OperatorOpenID = %q", msg.CardAction.OperatorOpenID)
	}
	if msg.CardAction.OpenChatID != "oc_chat" {
		t.Errorf("OpenChatID = %q", msg.CardAction.OpenChatID)
	}
	if msg.CardAction.OpenMessageID != "om_card" {
		t.Errorf("OpenMessageID = %q", msg.CardAction.OpenMessageID)
	}
	// The command text must ride the standard body fields so the shared
	// Router sees the identical shape a typed "/new" produces (AC4).
	if msg.Body != "/new" || msg.CommandBody != "/new" {
		t.Errorf("Body/CommandBody = %q/%q, want /new", msg.Body, msg.CommandBody)
	}
	if msg.ChatID != "oc_chat" || msg.ChatType != ChatTypeGroup {
		t.Errorf("Chat = %s/%s", msg.ChatID, msg.ChatType)
	}
	if msg.SenderOpenID != "ou_clicker" {
		t.Errorf("SenderOpenID = %q", msg.SenderOpenID)
	}
	if !msg.AddressedToBot {
		t.Error("card clicks must always be AddressedToBot")
	}
	// MessageID = event_id: Lark redelivers with the same event_id, so the
	// Router's dedup absorbs double-clicks for free.
	if msg.MessageID != "evt-9" || msg.EventID != "evt-9" {
		t.Errorf("MessageID/EventID = %q/%q, want evt-9", msg.MessageID, msg.EventID)
	}
	if msg.EventType != cardActionEventType {
		t.Errorf("EventType = %q", msg.EventType)
	}
}

func TestDecoderCardActionValidHelpClickP2P(t *testing.T) {
	t.Parallel()
	msg, ok, err := decodeCardActionForTest(t, mintedValue(1, "/help", "p2p"))
	if err != nil || !ok {
		t.Fatalf("Decode ok=%v err=%v", ok, err)
	}
	if msg.CardAction.Command != "/help" {
		t.Errorf("Command = %q, want /help", msg.CardAction.Command)
	}
	if msg.ChatType != ChatTypeP2P {
		t.Errorf("ChatType = %q, want p2p", msg.ChatType)
	}
}

// TestDecoderCardActionRefusals covers every validation gate: each refusal
// must keep the envelope deliverable (ok=true, CardAction non-nil — the
// connector needs operator/chat context to ACK an error toast) while
// leaving Command empty so nothing dispatches.
func TestDecoderCardActionRefusals(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		value map[string]any
	}{
		{"unregistered stop", mintedValue(1, "/stop", "group")},
		{"arguments appended", mintedValue(1, "/issue evil title", "group")},
		{"case folded", mintedValue(1, "/NEW", "group")},
		{"wrong schema version", mintedValue(2, "/new", "group")},
		{"bad chat type", mintedValue(1, "/new", "tenant")},
		{"empty chat type", mintedValue(1, "/new", "")},
		{"empty command", mintedValue(1, "", "group")},
		{"missing value", nil},
		{"wrong types", map[string]any{"v": "one", "cmd": 7, "ct": true}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			msg, ok, err := decodeCardActionForTest(t, tc.value)
			if err != nil || !ok {
				t.Fatalf("Decode ok=%v err=%v — refusal must still deliver the envelope", ok, err)
			}
			if msg.CardAction == nil {
				t.Fatal("CardAction must stay non-nil so the connector can toast")
			}
			if msg.CardAction.Command != "" {
				t.Errorf("Command = %q, want empty (refused)", msg.CardAction.Command)
			}
			if msg.Body != "" || msg.CommandBody != "" {
				t.Errorf("Body/CommandBody = %q/%q, want empty", msg.Body, msg.CommandBody)
			}
		})
	}
}

func TestDecoderCardActionMalformedEventIsError(t *testing.T) {
	t.Parallel()
	d := NewLarkJSONFrameDecoder()
	raw := []byte(`{
		"header":{"event_id":"evt-9","event_type":"card.action.trigger","app_id":"a"},
		"event":{"operator":"not-an-object"}
	}`)
	_, ok, err := d.Decode(raw, Installation{})
	if ok || err == nil {
		t.Fatalf("ok=%v err=%v, want ok=false with error", ok, err)
	}
}

func TestDecoderCardActionEmptyEventIsError(t *testing.T) {
	t.Parallel()
	d := NewLarkJSONFrameDecoder()
	raw := []byte(`{"header":{"event_id":"evt-9","event_type":"card.action.trigger","app_id":"a"}}`)
	_, ok, err := d.Decode(raw, Installation{})
	if ok || err == nil {
		t.Fatalf("ok=%v err=%v, want ok=false with error", ok, err)
	}
}
