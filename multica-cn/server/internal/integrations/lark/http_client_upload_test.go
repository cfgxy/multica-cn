package lark

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// What Lark accepts on the two upload endpoints is a multipart body with
// specific field names, and there is no way to find out from inside the product
// that we got one of them wrong: the upload simply comes back refused and the
// user is told their file did not send. So the request shape is pinned here,
// against a server that reads the body the way Lark does.

// uploadObservation is one parsed multipart request.
type uploadObservation struct {
	Path   string
	Auth   string
	Fields map[string]string
	// FileField is the multipart field name the bytes arrived under, and
	// FilePart the filename on that part.
	FileField string
	FilePart  string
	Data      []byte
}

// stubUpload installs one of the upload endpoints, parses the multipart body
// and hands the parsed form back to the test. respData is the `data` object of
// the reply (image_key / file_key).
func stubUpload(f *larkFakeServer, path string, respData map[string]any, observe func(uploadObservation)) {
	f.mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			f.t.Errorf("upload %s: want POST, got %s", path, r.Method)
		}
		mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || mediaType != "multipart/form-data" || params["boundary"] == "" {
			f.t.Fatalf("upload %s: want multipart/form-data with a boundary, got %q (err=%v)",
				path, r.Header.Get("Content-Type"), err)
		}
		obs := uploadObservation{Path: r.URL.Path, Auth: r.Header.Get("Authorization"), Fields: map[string]string{}}
		mr := multipart.NewReader(r.Body, params["boundary"])
		for {
			part, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				f.t.Fatalf("upload %s: read part: %v", path, err)
			}
			body, err := io.ReadAll(part)
			if err != nil {
				f.t.Fatalf("upload %s: read part body: %v", path, err)
			}
			if part.FileName() != "" {
				obs.FileField = part.FormName()
				obs.FilePart = part.FileName()
				obs.Data = body
				continue
			}
			obs.Fields[part.FormName()] = string(body)
		}
		if observe != nil {
			observe(obs)
		}
		writeJSON(w, map[string]any{"code": 0, "msg": "ok", "data": respData})
	})
}

func TestHTTPClient_UploadImage_SendsMessageTypedMultipart(t *testing.T) {
	fake := newLarkFake(t)
	fake.stubToken("tok_live", 7200)
	var obs uploadObservation
	stubUpload(fake, "/open-apis/im/v1/images", map[string]any{"image_key": "img_v2_abc"}, func(o uploadObservation) {
		obs = o
	})
	c := newTestClient(fake, time.Now)

	key, err := c.UploadImage(context.Background(), UploadImageParams{
		InstallationID: testCreds(),
		Filename:       "chart.png",
		Data:           []byte("PNGBYTES"),
	})
	if err != nil {
		t.Fatalf("UploadImage: %v", err)
	}
	if key != "img_v2_abc" {
		t.Errorf("image_key = %q, want img_v2_abc", key)
	}
	// image_type=message is what makes the key usable in a chat message; an
	// avatar key is refused by the send endpoint.
	if obs.Fields["image_type"] != "message" {
		t.Errorf("image_type = %q, want message", obs.Fields["image_type"])
	}
	// The field name is Lark's, not ours: "image" here, "file" on the other
	// endpoint. A wrong name reads as a missing file.
	if obs.FileField != "image" {
		t.Errorf("file field = %q, want image", obs.FileField)
	}
	if obs.FilePart != "chart.png" {
		t.Errorf("part filename = %q, want chart.png", obs.FilePart)
	}
	if string(obs.Data) != "PNGBYTES" {
		t.Errorf("uploaded bytes = %q, want PNGBYTES", obs.Data)
	}
	if obs.Auth != "Bearer tok_live" {
		t.Errorf("Authorization = %q, want Bearer tok_live", obs.Auth)
	}
}

func TestHTTPClient_UploadFile_CarriesTypeAndRecipientVisibleName(t *testing.T) {
	fake := newLarkFake(t)
	fake.stubToken("tok_live", 7200)
	var obs uploadObservation
	stubUpload(fake, "/open-apis/im/v1/files", map[string]any{"file_key": "file_v3_xyz"}, func(o uploadObservation) {
		obs = o
	})
	c := newTestClient(fake, time.Now)

	key, err := c.UploadFile(context.Background(), UploadFileParams{
		InstallationID: testCreds(),
		FileType:       "pdf",
		Filename:       "report.pdf",
		Data:           []byte("%PDF-1.7"),
	})
	if err != nil {
		t.Fatalf("UploadFile: %v", err)
	}
	if key != "file_v3_xyz" {
		t.Errorf("file_key = %q, want file_v3_xyz", key)
	}
	if obs.Fields["file_type"] != "pdf" {
		t.Errorf("file_type = %q, want pdf", obs.Fields["file_type"])
	}
	// file_name is not decoration: it is what the recipient sees on the card
	// and what their download is named.
	if obs.Fields["file_name"] != "report.pdf" {
		t.Errorf("file_name = %q, want report.pdf", obs.Fields["file_name"])
	}
	if obs.FileField != "file" {
		t.Errorf("file field = %q, want file", obs.FileField)
	}
	if string(obs.Data) != "%PDF-1.7" {
		t.Errorf("uploaded bytes = %q, want the PDF header", obs.Data)
	}
}

// An unset file_type still has to be a member of Lark's enum, or the upload is
// refused for a reason that has nothing to do with the file.
func TestHTTPClient_UploadFile_DefaultsFileTypeToStream(t *testing.T) {
	fake := newLarkFake(t)
	fake.stubToken("tok_live", 7200)
	var obs uploadObservation
	stubUpload(fake, "/open-apis/im/v1/files", map[string]any{"file_key": "file_1"}, func(o uploadObservation) {
		obs = o
	})
	c := newTestClient(fake, time.Now)

	if _, err := c.UploadFile(context.Background(), UploadFileParams{
		InstallationID: testCreds(),
		Data:           []byte("zipbytes"),
	}); err != nil {
		t.Fatalf("UploadFile: %v", err)
	}
	if obs.Fields["file_type"] != feishuFileTypeStream {
		t.Errorf("file_type = %q, want %q", obs.Fields["file_type"], feishuFileTypeStream)
	}
	if obs.Fields["file_name"] == "" {
		t.Error("file_name must never be empty: it is the name the recipient downloads")
	}
}

// The multipart body is buffered rather than streamed precisely so the
// refresh-and-retry path can replay it. A streamed body would be consumed by
// the first attempt and the replay would upload zero bytes — which Lark answers
// with a business error that looks nothing like a token problem.
func TestHTTPClient_UploadImage_TokenRejectedMidUpload_ReplaysTheWholeBody(t *testing.T) {
	fake := newLarkFake(t)
	stubRotatingToken(fake, "tok_dead", "tok_fresh")
	var (
		attempts atomic.Int32
		seen     []uploadObservation
	)
	// The first upload gets Lark's expired-token shape: HTTP 400 with the code
	// in the body. The second gets a key.
	fake.mux.HandleFunc("/open-apis/im/v1/images", func(w http.ResponseWriter, r *http.Request) {
		n := attempts.Add(1)
		seen = append(seen, parseUpload(t, r))
		if n == 1 {
			writeLarkStatusError(w, http.StatusBadRequest, codeTenantTokenInvalid, invalidTokenMsg)
			return
		}
		writeJSON(w, map[string]any{"code": 0, "msg": "ok", "data": map[string]any{"image_key": "img_after_refresh"}})
	})
	c := newTestClient(fake, time.Now)

	key, err := c.UploadImage(context.Background(), UploadImageParams{
		InstallationID: testCreds(),
		Filename:       "chart.png",
		Data:           []byte("PNGBYTES"),
	})
	if err != nil {
		t.Fatalf("UploadImage after token refresh: %v", err)
	}
	if key != "img_after_refresh" {
		t.Errorf("image_key = %q, want img_after_refresh", key)
	}
	if got := attempts.Load(); got != 2 {
		t.Fatalf("upload attempts = %d, want 2 (original + one replay)", got)
	}
	if len(seen) != 2 {
		t.Fatalf("observed %d uploads, want 2", len(seen))
	}
	if seen[0].Auth != "Bearer tok_dead" || seen[1].Auth != "Bearer tok_fresh" {
		t.Errorf("replay must carry the refreshed token; got %q then %q", seen[0].Auth, seen[1].Auth)
	}
	if string(seen[1].Data) != "PNGBYTES" || seen[1].Fields["image_type"] != "message" {
		t.Errorf("replay body incomplete: data=%q fields=%v", seen[1].Data, seen[1].Fields)
	}
	// Two mints, not one: the rejected token was dropped from the cache rather
	// than replayed on every later upload.
	if got := fake.tokenN.Load(); got != 2 {
		t.Errorf("token mints = %d, want 2 (the rejected one was not dropped)", got)
	}
}

// parseUpload is stubUpload's body reader, reused by handlers that answer
// differently per attempt.
func parseUpload(t *testing.T, r *http.Request) uploadObservation {
	t.Helper()
	_, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil {
		t.Fatalf("parse content type: %v", err)
	}
	obs := uploadObservation{Path: r.URL.Path, Auth: r.Header.Get("Authorization"), Fields: map[string]string{}}
	mr := multipart.NewReader(r.Body, params["boundary"])
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("read part: %v", err)
		}
		body, err := io.ReadAll(part)
		if err != nil {
			t.Fatalf("read part body: %v", err)
		}
		if part.FileName() != "" {
			obs.FileField, obs.FilePart, obs.Data = part.FormName(), part.FileName(), body
			continue
		}
		obs.Fields[part.FormName()] = string(body)
	}
	return obs
}

// The send endpoints take a key, never bytes, and the key lives in a
// JSON-encoded content envelope whose field name differs per msg_type. A
// file_key under "image_key" is refused.
func TestHTTPClient_SendImageMessage_PutsTheKeyInTheContentEnvelope(t *testing.T) {
	fake := newLarkFake(t)
	fake.stubToken("tok_live", 7200)
	var body map[string]string
	fake.stubSend(map[string]any{
		"code": 0, "msg": "ok", "data": map[string]any{"message_id": "om_img"},
	}, func(r *http.Request, b map[string]string) {
		body = b
		if got := r.URL.Query().Get("receive_id_type"); got != "chat_id" {
			t.Errorf("receive_id_type = %q, want chat_id", got)
		}
	})
	c := newTestClient(fake, time.Now)

	id, err := c.SendImageMessage(context.Background(), SendImageParams{
		InstallationID: testCreds(),
		ChatID:         "oc_chat",
		ImageKey:       "img_v2_abc",
	})
	if err != nil {
		t.Fatalf("SendImageMessage: %v", err)
	}
	if id != "om_img" {
		t.Errorf("message_id = %q, want om_img", id)
	}
	if body["msg_type"] != "image" {
		t.Errorf("msg_type = %q, want image", body["msg_type"])
	}
	if body["receive_id"] != "oc_chat" {
		t.Errorf("receive_id = %q, want oc_chat", body["receive_id"])
	}
	var content map[string]string
	if err := json.Unmarshal([]byte(body["content"]), &content); err != nil {
		t.Fatalf("content is not JSON: %v (raw=%q)", err, body["content"])
	}
	if content["image_key"] != "img_v2_abc" {
		t.Errorf("content = %v, want image_key=img_v2_abc", content)
	}
}

// A file addressed at a message goes to the reply endpoint with
// reply_in_thread, which is how words and their attachment stay in one 话题.
func TestHTTPClient_SendFileMessage_ThreadsIntoTheReplyEndpoint(t *testing.T) {
	fake := newLarkFake(t)
	fake.stubToken("tok_live", 7200)
	var (
		gotID   string
		gotBody map[string]any
	)
	fake.stubReply(map[string]any{
		"code": 0, "msg": "ok", "data": map[string]any{"message_id": "om_file"},
	}, func(r *http.Request, id string, b map[string]any) {
		gotID, gotBody = id, b
	})
	c := newTestClient(fake, time.Now)

	id, err := c.SendFileMessage(context.Background(), SendFileParams{
		InstallationID: testCreds(),
		ChatID:         "oc_chat",
		FileKey:        "file_v3_xyz",
		ReplyTarget:    ReplyTarget{MessageID: "om_parent", InThread: true},
	})
	if err != nil {
		t.Fatalf("SendFileMessage: %v", err)
	}
	if id != "om_file" {
		t.Errorf("message_id = %q, want om_file", id)
	}
	if gotID != "om_parent" {
		t.Errorf("reply target = %q, want om_parent", gotID)
	}
	if gotBody["reply_in_thread"] != true {
		t.Errorf("reply_in_thread = %v, want true", gotBody["reply_in_thread"])
	}
	if gotBody["msg_type"] != "file" {
		t.Errorf("msg_type = %v, want file", gotBody["msg_type"])
	}
	raw, _ := gotBody["content"].(string)
	var content map[string]string
	if err := json.Unmarshal([]byte(raw), &content); err != nil {
		t.Fatalf("content is not JSON: %v (raw=%q)", err, raw)
	}
	if content["file_key"] != "file_v3_xyz" {
		t.Errorf("content = %v, want file_key=file_v3_xyz", content)
	}
}

// A business rejection has to surface as *APIError, because that is what the
// delivery path reads to decide the file definitely did not arrive — and to say
// so to the user rather than the ambiguous "I got no verdict" wording.
func TestHTTPClient_SendFileMessage_BusinessRejectionIsAnAPIError(t *testing.T) {
	fake := newLarkFake(t)
	fake.stubToken("tok_live", 7200)
	fake.stubSend(map[string]any{"code": 234001, "msg": "bot is not in the chat"}, nil)
	c := newTestClient(fake, time.Now)

	_, err := c.SendFileMessage(context.Background(), SendFileParams{
		InstallationID: testCreds(),
		ChatID:         "oc_chat",
		FileKey:        "file_1",
	})
	if err == nil {
		t.Fatal("a non-zero code must be an error")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("want *APIError, got %T: %v", err, err)
	}
	if apiErr.Code != 234001 || !strings.Contains(apiErr.Msg, "not in the chat") {
		t.Errorf("APIError = %+v, want code 234001 with Lark's message", apiErr)
	}
}
