package lark

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"
)

// waitForCardWrites polls conn until at least n frames have been written
// and returns the snapshot, so assertions never race the Run goroutine.
func waitForCardWrites(t *testing.T, conn *fakeWSConn, n int) [][]byte {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		writes := conn.snapshot()
		if len(writes) >= n {
			return writes
		}
		select {
		case <-deadline:
			t.Fatalf("only %d outbound frames in 2s, want >=%d", len(writes), n)
		case <-time.After(5 * time.Millisecond):
		}
	}
}

// decodeCardAck unmarshals one outbound ACK frame's payload as the SDK
// Response shape and returns its code plus the decoded card-response body.
// json.Unmarshal base64-decodes the Data []byte field automatically, so
// data is exactly what Lark's client would receive.
func decodeCardAck(t *testing.T, raw []byte) (int, map[string]any) {
	t.Helper()
	f, err := UnmarshalFrame(raw)
	if err != nil {
		t.Fatalf("unmarshal ack frame: %v", err)
	}
	var resp larkWSResponse
	if err := json.Unmarshal(f.Payload, &resp); err != nil {
		t.Fatalf("ack payload json: %v", err)
	}
	var data map[string]any
	if len(resp.Data) > 0 {
		if err := json.Unmarshal(resp.Data, &data); err != nil {
			t.Fatalf("ack card data json: %v", err)
		}
	}
	return resp.Code, data
}

func toastOf(t *testing.T, data map[string]any) map[string]any {
	t.Helper()
	toast, ok := data["toast"].(map[string]any)
	if !ok {
		t.Fatalf("ack data has no toast object: %v", data)
	}
	return toast
}

// cardActionDecoder returns a FrameDecoder that answers every payload with
// the supplied card action, mirroring what LarkJSONFrameDecoder produces.
func cardActionDecoder(action *CardActionEvent) FrameDecoder {
	return FrameDecoderFunc(func([]byte, Installation) (InboundMessage, bool, error) {
		return InboundMessage{
			EventType:      cardActionEventType,
			EventID:        "evt-card",
			MessageID:      "evt-card",
			ChatID:         "oc_chat",
			ChatType:       ChatTypeGroup,
			SenderOpenID:   "ou_clicker",
			AddressedToBot: true,
			CardAction:     action,
		}, true, nil
	})
}

func TestWSConnectorCardActionAcceptedToastsAndEmits(t *testing.T) {
	t.Parallel()
	conn := newFakeWSConn()
	c := quietConnector(t, conn, cardActionDecoder(&CardActionEvent{
		OperatorOpenID: "ou_clicker", OpenChatID: "oc_chat",
		ChatType: ChatTypeGroup, Command: "/new",
	}), time.Hour)

	var mu sync.Mutex
	var emitted []InboundMessage
	emit := func(_ context.Context, msg InboundMessage) (DispatchResult, error) {
		mu.Lock()
		emitted = append(emitted, msg)
		mu.Unlock()
		return DispatchResult{Outcome: OutcomeChatStarted}, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx, Installation{AppID: "test_app"}, emit) }()

	pushDataFrame(conn, []byte("payload"), "m1")

	waitForCardWrites(t, conn, 1)

	mu.Lock()
	n := len(emitted)
	var cmd string
	if n > 0 {
		cmd = emitted[0].CardAction.Command
	}
	mu.Unlock()
	if n != 1 {
		t.Fatalf("emit calls = %d, want 1", n)
	}
	if cmd != "/new" {
		t.Errorf("emitted command = %q, want /new", cmd)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after cancel")
	}

	code, data := decodeCardAck(t, conn.snapshot()[0])
	if code != 200 {
		t.Errorf("ack code = %d, want 200", code)
	}
	toast := toastOf(t, data)
	if toast["type"] != "success" {
		t.Errorf("toast type = %v, want success", toast["type"])
	}
}

func TestWSConnectorCardActionRefusedToastsErrorWithoutEmit(t *testing.T) {
	t.Parallel()
	conn := newFakeWSConn()
	c := quietConnector(t, conn, cardActionDecoder(&CardActionEvent{
		OperatorOpenID: "ou_clicker", OpenChatID: "oc_chat",
		ChatType: ChatTypeGroup, Command: "", // tampered value refused upstream
	}), time.Hour)

	emit := func(context.Context, InboundMessage) (DispatchResult, error) {
		t.Errorf("emit must not be called for a refused card action")
		return DispatchResult{}, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx, Installation{AppID: "test_app"}, emit) }()

	pushDataFrame(conn, []byte("payload"), "m1")
	waitForCardWrites(t, conn, 1)

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after cancel")
	}

	code, data := decodeCardAck(t, conn.snapshot()[0])
	if code != 200 {
		t.Errorf("ack code = %d, want 200 (refusal is terminal, not retryable)", code)
	}
	toast := toastOf(t, data)
	if toast["type"] != "error" {
		t.Errorf("toast type = %v, want error", toast["type"])
	}
	if content, _ := toast["content"].(string); !contains(content, "/help") {
		t.Errorf("toast content = %q, want guidance to /help", content)
	}
}

func TestWSConnectorCardActionEmitErrorNacksAndReturns(t *testing.T) {
	t.Parallel()
	conn := newFakeWSConn()
	c := quietConnector(t, conn, cardActionDecoder(&CardActionEvent{
		OperatorOpenID: "ou_clicker", OpenChatID: "oc_chat",
		ChatType: ChatTypeGroup, Command: "/help",
	}), time.Hour)

	emit := func(context.Context, InboundMessage) (DispatchResult, error) {
		return DispatchResult{}, context.DeadlineExceeded // infra failure
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx, Installation{AppID: "test_app"}, emit) }()

	pushDataFrame(conn, []byte("payload"), "m1")

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Run must surface the emit error so the Hub reconnects")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after emit error")
	}

	// NACK (code 500) tells Lark to retry; dedup on event_id makes the
	// retry a no-op if the dispatch actually landed.
	writes := waitForCardWrites(t, conn, 1)
	code, _ := decodeCardAck(t, writes[0])
	if code != 500 {
		t.Errorf("nack code = %d, want 500", code)
	}
}

func TestCardActionToastJSONShape(t *testing.T) {
	t.Parallel()
	var doc struct {
		Toast struct {
			Type    string `json:"type"`
			Content string `json:"content"`
		} `json:"toast"`
	}
	if err := json.Unmarshal(CardActionToastJSON("success", "ok"), &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if doc.Toast.Type != "success" || doc.Toast.Content != "ok" {
		t.Errorf("toast = %+v", doc.Toast)
	}
}
