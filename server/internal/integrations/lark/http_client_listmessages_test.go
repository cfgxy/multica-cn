package lark

import (
	"context"
	"net/http"
	"testing"
	"time"
)

// TestHTTPClient_ListChatMessages exercises the group-context list path:
// the request carries container_id_type=chat, the chat id, a descending
// sort, the requested page_size, and user_id_type=open_id; items[] come
// back normalized verbatim (ordering is the enricher's job, not the
// transport's).
func TestHTTPClient_ListChatMessages(t *testing.T) {
	fake := newLarkFake(t)
	fake.stubToken("tok", 7200)
	fake.mux.HandleFunc("/open-apis/im/v1/messages", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("want GET, got %s", r.Method)
		}
		q := r.URL.Query()
		if q.Get("container_id_type") != "chat" {
			t.Errorf("container_id_type = %q", q.Get("container_id_type"))
		}
		if q.Get("container_id") != "oc_chat" {
			t.Errorf("container_id = %q", q.Get("container_id"))
		}
		if q.Get("sort_type") != "ByCreateTimeDesc" {
			t.Errorf("sort_type = %q", q.Get("sort_type"))
		}
		if q.Get("page_size") != "20" {
			t.Errorf("page_size = %q", q.Get("page_size"))
		}
		if q.Get("user_id_type") != "open_id" {
			t.Errorf("user_id_type = %q", q.Get("user_id_type"))
		}
		if q.Get("end_time") != "1700000000" {
			t.Errorf("end_time = %q", q.Get("end_time"))
		}
		writeJSON(w, map[string]any{
			"code": 0, "msg": "ok",
			"data": map[string]any{
				"items": []any{
					map[string]any{
						"message_id":  "om_2",
						"msg_type":    "text",
						"create_time": "2000",
						"sender":      map[string]any{"id": "ou_b", "id_type": "open_id", "sender_type": "user"},
						"body":        map[string]any{"content": `{"text":"second"}`},
					},
					map[string]any{
						"message_id":  "om_1",
						"msg_type":    "text",
						"create_time": "1000",
						"sender":      map[string]any{"id": "ou_a", "id_type": "open_id", "sender_type": "user"},
						"body":        map[string]any{"content": `{"text":"first"}`},
					},
				},
			},
		})
	})

	c := newTestClient(fake, time.Now)
	items, err := c.ListChatMessages(context.Background(), testCreds(), ListMessagesParams{ChatID: "oc_chat", PageSize: 20, EndTime: 1700000000})
	if err != nil {
		t.Fatalf("ListChatMessages: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("items = %d, want 2", len(items))
	}
	if items[0].MessageID != "om_2" || items[0].Content != `{"text":"second"}` || items[0].SenderID != "ou_b" {
		t.Errorf("items[0] = %+v", items[0])
	}
	if a := fake.lastAuth(); a != "Bearer tok" {
		t.Errorf("auth header = %q", a)
	}
}

// TestHTTPClient_ListChatMessagesClampsPageSize pins that an over-cap
// page_size is clamped to Lark's 50 limit rather than passed through (a
// raw >50 would earn a 400 from Lark).
func TestHTTPClient_ListChatMessagesClampsPageSize(t *testing.T) {
	fake := newLarkFake(t)
	fake.stubToken("tok", 7200)
	var seenSize string
	var endPresent bool
	fake.mux.HandleFunc("/open-apis/im/v1/messages", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		seenSize = q.Get("page_size")
		endPresent = q.Has("end_time")
		writeJSON(w, map[string]any{"code": 0, "msg": "ok", "data": map[string]any{"items": []any{}}})
	})
	c := newTestClient(fake, time.Now)
	if _, err := c.ListChatMessages(context.Background(), testCreds(), ListMessagesParams{ChatID: "oc_chat", PageSize: 999}); err != nil {
		t.Fatalf("ListChatMessages: %v", err)
	}
	if seenSize != "50" {
		t.Errorf("page_size = %q, want clamped 50", seenSize)
	}
	// With no EndTime set, the end_time param must be omitted entirely.
	if endPresent {
		t.Errorf("end_time should be absent when EndTime=0")
	}
}

// TestHTTPClient_ListChatMessagesThreadScoped pins the #5835 topic path:
// when ThreadID is set the request switches to container_id_type=thread
// with the topic id as container_id, and end_time is omitted even if the
// caller passes one (the thread container rejects it). The returned items'
// thread_id is normalized onto LarkMessage so the enricher can fail-close
// on it.
func TestHTTPClient_ListChatMessagesThreadScoped(t *testing.T) {
	fake := newLarkFake(t)
	fake.stubToken("tok", 7200)
	fake.mux.HandleFunc("/open-apis/im/v1/messages", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("container_id_type") != "thread" {
			t.Errorf("container_id_type = %q, want thread", q.Get("container_id_type"))
		}
		if q.Get("container_id") != "th_topic" {
			t.Errorf("container_id = %q, want th_topic", q.Get("container_id"))
		}
		// The thread container rejects end_time; it must be dropped even
		// though the caller passed EndTime.
		if q.Has("end_time") {
			t.Errorf("end_time must be absent on the thread container, got %q", q.Get("end_time"))
		}
		if q.Get("sort_type") != "ByCreateTimeDesc" {
			t.Errorf("sort_type = %q", q.Get("sort_type"))
		}
		if q.Get("page_size") != "10" {
			t.Errorf("page_size = %q", q.Get("page_size"))
		}
		writeJSON(w, map[string]any{
			"code": 0, "msg": "ok",
			"data": map[string]any{
				"items": []any{
					map[string]any{
						"message_id":  "om_1",
						"msg_type":    "text",
						"create_time": "1000",
						"thread_id":   "th_topic",
						"sender":      map[string]any{"id": "ou_a", "id_type": "open_id", "sender_type": "user"},
						"body":        map[string]any{"content": `{"text":"hi"}`},
					},
				},
			},
		})
	})

	c := newTestClient(fake, time.Now)
	items, err := c.ListChatMessages(context.Background(), testCreds(), ListMessagesParams{ChatID: "oc_chat", ThreadID: "th_topic", PageSize: 10, EndTime: 1700000000})
	if err != nil {
		t.Fatalf("ListChatMessages: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1", len(items))
	}
	if items[0].ThreadID != "th_topic" {
		t.Errorf("normalized ThreadID = %q, want th_topic", items[0].ThreadID)
	}
}

// TestHTTPClient_ListChatMessagesMissingChatID fails fast (no token, no
// network call) when the chat id is empty.
func TestHTTPClient_ListChatMessagesMissingChatID(t *testing.T) {
	fake := newLarkFake(t)
	c := newTestClient(fake, time.Now)
	if _, err := c.ListChatMessages(context.Background(), testCreds(), ListMessagesParams{}); err == nil {
		t.Fatalf("want error for empty chat id")
	}
}

func TestHTTPClient_ListChatMessagesMediaResourceContract(t *testing.T) {
	fake := newLarkFake(t)
	fake.stubToken("tok", 7200)
	fake.mux.HandleFunc("/open-apis/im/v1/messages", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Query().Get("container_id") != "oc_group" ||
			r.URL.Query().Get("container_id_type") != "chat" || r.URL.Query().Get("end_time") != "3" {
			t.Errorf("历史消息请求不符合预期：%s %s", r.Method, r.URL.String())
		}
		writeJSON(w, map[string]any{"code": 0, "data": map[string]any{"items": []any{map[string]any{
			"message_id": "om_original", "msg_type": "file", "create_time": "2000",
			"sender": map[string]any{"id": "ou_sender", "sender_type": "user"},
			"body":   map[string]any{"content": `{"file_key":"key_file","file_name":"notes.txt"}`},
		}}}})
	})
	fake.mux.HandleFunc("/open-apis/im/v1/messages/om_original/resources/key_file", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Query().Get("type") != "file" || r.Header.Get("Authorization") != "Bearer tok" {
			t.Errorf("历史文件下载请求不符合预期：%s %s", r.Method, r.URL.String())
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("document"))
	})

	client := newTestClient(fake, time.Now)
	items, err := client.ListChatMessages(context.Background(), testCreds(), ListMessagesParams{ChatID: "oc_group", EndTime: 3, PageSize: 10})
	if err != nil || len(items) != 1 {
		t.Fatalf("历史文件响应错误：items=%+v err=%v", items, err)
	}
	item := items[0]
	if item.MessageID != "om_original" || item.SenderID != "ou_sender" || item.MessageType != "file" || item.Content != `{"file_key":"key_file","file_name":"notes.txt"}` {
		t.Fatalf("历史文件字段未保真：%+v", item)
	}
	resources := mediaResourcesFromMessage(InboundMessage{MessageID: item.MessageID, MessageType: item.MessageType, Content: item.Content})
	if len(resources) != 1 {
		t.Fatalf("历史文件资源未解析：%+v", resources)
	}
	got, err := client.DownloadMessageResource(context.Background(), testCreds(), DownloadResourceParams{
		MessageID: resources[0].messageID, FileKey: resources[0].key, Type: resources[0].fetchType,
	})
	if err != nil || string(got.Data) != "document" {
		t.Fatalf("原消息资源下载失败：result=%+v err=%v", got, err)
	}
}
