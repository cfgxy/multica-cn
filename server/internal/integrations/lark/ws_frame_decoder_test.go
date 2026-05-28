package lark

import (
	"testing"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestLarkJSONFrameDecoderTextMessageInP2P(t *testing.T) {
	t.Parallel()
	raw := []byte(`{
		"type":"event_callback",
		"header":{
			"event_id":"evt-1",
			"event_type":"im.message.receive_v1",
			"app_id":"cli_app_x"
		},
		"event":{
			"sender":{
				"sender_id":{"open_id":"ou_user"},
				"sender_type":"user"
			},
			"message":{
				"message_id":"om_1",
				"chat_id":"oc_1",
				"chat_type":"p2p",
				"message_type":"text",
				"content":"{\"text\":\"hello\"}"
			}
		}
	}`)

	d := NewLarkJSONFrameDecoder()
	msg, ok, err := d.Decode(raw, db.LarkInstallation{BotOpenID: "ou_bot"})
	if err != nil || !ok {
		t.Fatalf("Decode ok=%v err=%v", ok, err)
	}
	if msg.EventID != "evt-1" {
		t.Errorf("EventID = %q", msg.EventID)
	}
	if msg.AppID != "cli_app_x" {
		t.Errorf("AppID = %q", msg.AppID)
	}
	if msg.ChatType != ChatTypeP2P {
		t.Errorf("ChatType = %q", msg.ChatType)
	}
	if msg.MessageID != "om_1" {
		t.Errorf("MessageID = %q", msg.MessageID)
	}
	if msg.SenderOpenID != "ou_user" {
		t.Errorf("SenderOpenID = %q", msg.SenderOpenID)
	}
	if msg.Body != "hello" {
		t.Errorf("Body = %q", msg.Body)
	}
	if msg.AddressedToBot {
		t.Errorf("P2P AddressedToBot should not be true")
	}
}

func TestLarkJSONFrameDecoderGroupMentionDiscrimination(t *testing.T) {
	t.Parallel()
	mkRaw := func(mentionOpenID string) []byte {
		return []byte(`{
			"type":"event_callback",
			"header":{"event_id":"e","event_type":"im.message.receive_v1","app_id":"a"},
			"event":{
				"sender":{"sender_id":{"open_id":"ou_user"}},
				"message":{
					"message_id":"m","chat_id":"c","chat_type":"group",
					"message_type":"text","content":"{\"text\":\"hi\"}",
					"mentions":[{"id":{"open_id":"` + mentionOpenID + `"}}]
				}
			}
		}`)
	}
	d := NewLarkJSONFrameDecoder()

	t.Run("mentions bot", func(t *testing.T) {
		msg, ok, err := d.Decode(mkRaw("ou_bot"), db.LarkInstallation{BotOpenID: "ou_bot"})
		if err != nil || !ok {
			t.Fatalf("ok=%v err=%v", ok, err)
		}
		if msg.ChatType != ChatTypeGroup {
			t.Errorf("ChatType = %q", msg.ChatType)
		}
		if !msg.AddressedToBot {
			t.Error("AddressedToBot = false; expected true")
		}
	})

	t.Run("mentions other user", func(t *testing.T) {
		msg, ok, err := d.Decode(mkRaw("ou_other"), db.LarkInstallation{BotOpenID: "ou_bot"})
		if err != nil || !ok {
			t.Fatalf("ok=%v err=%v", ok, err)
		}
		if msg.AddressedToBot {
			t.Error("AddressedToBot = true; expected false")
		}
	})
}

func TestLarkJSONFrameDecoderDropsHeartbeat(t *testing.T) {
	t.Parallel()
	d := NewLarkJSONFrameDecoder()
	cases := [][]byte{
		[]byte(`{"type":"heartbeat"}`),
		[]byte(`{"type":"frame_ack","data":{"id":"1"}}`),
		[]byte(`{"type":"event_callback","header":{"event_type":"im.message.unknown_kind"}}`),
	}
	for _, raw := range cases {
		msg, ok, err := d.Decode(raw, db.LarkInstallation{})
		if err != nil || ok {
			t.Errorf("Decode(%q) ok=%v err=%v; expected (false, nil)", raw, ok, err)
		}
		if msg.EventID != "" {
			t.Errorf("expected zero-value InboundMessage on drop, got %+v", msg)
		}
	}
}

func TestLarkJSONFrameDecoderEmptyRaw(t *testing.T) {
	t.Parallel()
	msg, ok, err := NewLarkJSONFrameDecoder().Decode(nil, db.LarkInstallation{})
	if ok || err != nil {
		t.Fatalf("expected (zero, false, nil) for empty raw; got ok=%v err=%v msg=%+v", ok, err, msg)
	}
}

func TestLarkJSONFrameDecoderMalformedReturnsError(t *testing.T) {
	t.Parallel()
	_, ok, err := NewLarkJSONFrameDecoder().Decode([]byte("not-json"), db.LarkInstallation{})
	if err == nil {
		t.Fatal("expected error on malformed envelope")
	}
	if ok {
		t.Error("ok should be false on decode failure")
	}
}

func TestLarkJSONFrameDecoderMessageContentEmptyOnInvalidContentJSON(t *testing.T) {
	t.Parallel()
	raw := []byte(`{
		"type":"event_callback",
		"header":{"event_id":"e","event_type":"im.message.receive_v1","app_id":"a"},
		"event":{
			"sender":{"sender_id":{"open_id":"ou_user"}},
			"message":{"message_id":"m","chat_id":"c","chat_type":"p2p","message_type":"text","content":"not-json"}
		}
	}`)
	msg, ok, err := NewLarkJSONFrameDecoder().Decode(raw, db.LarkInstallation{})
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if msg.Body != "" {
		t.Errorf("Body = %q; expected empty on unparseable content", msg.Body)
	}
}

func TestLarkJSONFrameDecoderNonTextMessageHasEmptyBody(t *testing.T) {
	t.Parallel()
	raw := []byte(`{
		"type":"event_callback",
		"header":{"event_id":"e","event_type":"im.message.receive_v1","app_id":"a"},
		"event":{
			"sender":{"sender_id":{"open_id":"ou_user"}},
			"message":{"message_id":"m","chat_id":"c","chat_type":"p2p","message_type":"image","content":"{\"image_key\":\"img1\"}"}
		}
	}`)
	msg, ok, err := NewLarkJSONFrameDecoder().Decode(raw, db.LarkInstallation{})
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if msg.Body != "" {
		t.Errorf("Body = %q; non-text messages should have empty body in MVP", msg.Body)
	}
	if msg.MessageID == "" {
		t.Error("MessageID should still be populated for non-text events")
	}
}
