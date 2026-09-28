package lark

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/events"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// ---- fakes for the attachment path ----

func (f *fakePatcherQueries) ListAttachmentsByChatMessage(ctx context.Context, arg db.ListAttachmentsByChatMessageParams) ([]db.Attachment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.attachmentLookups++
	return f.attachments, f.attachmentsErr
}

func (f *fakePatcherQueries) lookupCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.attachmentLookups
}

func (f *fakeAPIClient) UploadImage(ctx context.Context, p UploadImageParams) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.imageUploads = append(f.imageUploads, p)
	if f.uploadImageErr != nil {
		return "", f.uploadImageErr
	}
	return f.imageKeyReturn, nil
}

func (f *fakeAPIClient) UploadFile(ctx context.Context, p UploadFileParams) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fileUploads = append(f.fileUploads, p)
	if f.uploadFileErr != nil {
		return "", f.uploadFileErr
	}
	return f.fileKeyReturn, nil
}

func (f *fakeAPIClient) SendImageMessage(ctx context.Context, p SendImageParams) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.imageSent = append(f.imageSent, p)
	if f.threadReplyErr != nil && p.ReplyTarget.IsSet() {
		return "", f.threadReplyErr
	}
	if f.sendImageErr != nil {
		return "", f.sendImageErr
	}
	return "lark_image_msg_1", nil
}

func (f *fakeAPIClient) SendFileMessage(ctx context.Context, p SendFileParams) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fileSent = append(f.fileSent, p)
	if f.threadReplyErr != nil && p.ReplyTarget.IsSet() {
		return "", f.threadReplyErr
	}
	// sendFileHook lets a test give consecutive sends different outcomes, which
	// is what a mixed-outcome reply needs.
	if f.sendFileHook != nil {
		return "", f.sendFileHook()
	}
	if f.sendFileErr != nil {
		return "", f.sendFileErr
	}
	return "lark_file_msg_1", nil
}

func (f *fakeAPIClient) snapshot() (images []SendImageParams, files []SendFileParams, texts []SendTextParams) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]SendImageParams(nil), f.imageSent...),
		append([]SendFileParams(nil), f.fileSent...),
		append([]SendTextParams(nil), f.textSent...)
}

// fakeObjectStore is the object-storage seam: a URL maps to bytes, or to
// an error when the deployment cannot read what the attachment row points at.
type fakeObjectStore struct {
	objects map[string][]byte
	readErr error
	// unknownURL makes KeyFromURL return "" — the object belongs to some
	// other deployment's storage.
	unknownURL bool
}

func (s *fakeObjectStore) KeyFromURL(rawURL string) string {
	if s.unknownURL {
		return ""
	}
	return rawURL
}

func (s *fakeObjectStore) GetReader(ctx context.Context, key string) (io.ReadCloser, error) {
	if s.readErr != nil {
		return nil, s.readErr
	}
	data, ok := s.objects[key]
	if !ok {
		return nil, errors.New("fake object store: no such key")
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

// fakeMetrics records the bounded reason labels so a test can assert what an
// operator would see, not merely that the delivery returned.
type fakeMetrics struct {
	mu          sync.Mutex
	delivered   int
	dropped     []string
	unconfirmed []string
	sheds       int
}

func (m *fakeMetrics) RecordAttachmentDelivered() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.delivered++
}

func (m *fakeMetrics) RecordAttachmentDropped(reason string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.dropped = append(m.dropped, reason)
}

func (m *fakeMetrics) RecordAttachmentUnconfirmed(reason string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.unconfirmed = append(m.unconfirmed, reason)
}

func (m *fakeMetrics) RecordAttachmentDeliveryShed() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sheds++
}

func (m *fakeMetrics) read() (delivered int, dropped, unconfirmed []string, sheds int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.delivered, append([]string(nil), m.dropped...), append([]string(nil), m.unconfirmed...), m.sheds
}

const (
	testAttachmentMessageID = "dddddddd-dddd-dddd-dddd-dddddddddddd"
	testWorkspaceID         = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
)

// newAttachmentPatcher builds a Patcher wired for file delivery, with the
// spawn seam collapsed so the delivery runs inline and the test needs no
// synchronization beyond handleEvent returning.
func newAttachmentPatcher(t *testing.T, store mediaObjectStore, m Metrics) (*Patcher, *fakePatcherQueries, *fakeAPIClient) {
	t.Helper()
	p, q, api := newTestPatcherWith(t, WithAttachments(store), WithOutboundMetrics(m))
	p.spawn = func(f func()) { f() }
	return p, q, api
}

// chatDoneEvent is the event a finished chat turn publishes, with the
// assistant message id every attachment on the turn is bound to.
func chatDoneEvent(t *testing.T, q *fakePatcherQueries, content string) events.Event {
	t.Helper()
	taskID := uuidFromString(t, "ee555555-ee55-ee55-ee55-eeeeeeeeeeee")
	q.task = db.AgentTaskQueue{ChatInputTaskID: taskID}
	q.taskChannelIngested = true
	return events.Event{
		Type:          protocol.EventChatDone,
		TaskID:        uuidString(taskID),
		ChatSessionID: uuidString(q.binding.ChatSessionID),
		WorkspaceID:   testWorkspaceID,
		Payload: protocol.ChatDonePayload{
			TaskID:        uuidString(taskID),
			ChatSessionID: uuidString(q.binding.ChatSessionID),
			MessageID:     testAttachmentMessageID,
			Content:       content,
		},
	}
}

func attachmentRow(t *testing.T, filename, contentType string, size int64) db.Attachment {
	t.Helper()
	return db.Attachment{
		ID:            uuidFromString(t, "f0f0f0f0-f0f0-f0f0-f0f0-f0f0f0f0f0f0"),
		Filename:      filename,
		Url:           "https://objects.example/" + filename,
		ContentType:   contentType,
		SizeBytes:     size,
		ChatMessageID: uuidFromString(t, testAttachmentMessageID),
	}
}

// TestDeliverAttachments_ImageFollowsTheReply is the whole point of the
// feature: the words go out first as their own message, then the image the
// agent produced goes out as a second one, uploaded through the image
// endpoint and sent by the image_key it returned.
func TestDeliverAttachments_ImageFollowsTheReply(t *testing.T) {
	store := &fakeObjectStore{objects: map[string][]byte{
		"https://objects.example/chart.png": []byte("PNGDATA"),
	}}
	m := &fakeMetrics{}
	p, q, api := newAttachmentPatcher(t, store, m)
	api.imageKeyReturn = "img_v3_key"
	q.attachments = []db.Attachment{attachmentRow(t, "chart.png", "image/png", 7)}

	p.handleEvent(chatDoneEvent(t, q, "here is the chart"))

	images, files, texts := api.snapshot()
	if len(texts) != 1 || texts[0].Text != "here is the chart" {
		t.Fatalf("reply text = %+v, want the agent's words sent once", texts)
	}
	if len(api.imageUploads) != 1 {
		t.Fatalf("image uploads = %d, want 1", len(api.imageUploads))
	}
	if got := string(api.imageUploads[0].Data); got != "PNGDATA" {
		t.Errorf("uploaded bytes = %q, want the object's bytes", got)
	}
	if api.imageUploads[0].Filename != "chart.png" {
		t.Errorf("uploaded filename = %q, want chart.png", api.imageUploads[0].Filename)
	}
	if len(files) != 0 {
		t.Errorf("file sends = %+v, want an image to travel as an image", files)
	}
	if len(images) != 1 || images[0].ImageKey != "img_v3_key" {
		t.Fatalf("image sends = %+v, want one send carrying the uploaded key", images)
	}
	if images[0].ChatID != "oc_test_chat" {
		t.Errorf("image chat id = %q, want the bound chat", images[0].ChatID)
	}
	delivered, dropped, unconfirmed, _ := m.read()
	if delivered != 1 || len(dropped) != 0 || len(unconfirmed) != 0 {
		t.Errorf("metrics = delivered %d dropped %v unconfirmed %v, want one clean delivery",
			delivered, dropped, unconfirmed)
	}
}

// TestDeliverAttachments_FileOnlyReply covers the turn whose substance IS the
// file: the agent wrote nothing, so no text message is owed, and the file
// must still reach the chat. The pre-existing empty-content early return in
// sendChatReply is exactly what used to swallow this.
func TestDeliverAttachments_FileOnlyReply(t *testing.T) {
	store := &fakeObjectStore{objects: map[string][]byte{
		"https://objects.example/report.pdf": []byte("%PDF-1.7"),
	}}
	m := &fakeMetrics{}
	p, q, api := newAttachmentPatcher(t, store, m)
	api.fileKeyReturn = "file_v3_key"
	q.attachments = []db.Attachment{attachmentRow(t, "report.pdf", "application/pdf", 8)}

	p.handleEvent(chatDoneEvent(t, q, ""))

	_, files, texts := api.snapshot()
	if len(texts) != 0 {
		t.Errorf("text sends = %+v, want silence when the agent wrote nothing", texts)
	}
	if len(api.fileUploads) != 1 || api.fileUploads[0].FileType != "pdf" {
		t.Fatalf("file uploads = %+v, want one upload declared as pdf", api.fileUploads)
	}
	if len(files) != 1 || files[0].FileKey != "file_v3_key" {
		t.Fatalf("file sends = %+v, want one send carrying the uploaded key", files)
	}
	if delivered, _, _, _ := m.read(); delivered != 1 {
		t.Errorf("delivered = %d, want 1", delivered)
	}
}

// TestDeliverAttachments_NoObjectStoreConfigured is the negative case the
// two-layer file policy rests on: a deployment with no object storage wires
// no WithAttachments, so nothing about files happens — not even the lookup.
// The capability declaration is decided by the same condition at the router,
// so a delivery path that ran here would mean the agent was told files work
// on a deployment that cannot send one.
func TestDeliverAttachments_NoObjectStoreConfigured(t *testing.T) {
	p, q, api := newTestPatcher(t)
	p.spawn = func(f func()) { f() }
	q.attachments = []db.Attachment{attachmentRow(t, "report.pdf", "application/pdf", 8)}

	p.handleEvent(chatDoneEvent(t, q, "no files here"))

	if n := q.lookupCount(); n != 0 {
		t.Errorf("attachment lookups = %d, want 0 when no object storage is wired", n)
	}
	images, files, texts := api.snapshot()
	if len(texts) != 1 {
		t.Errorf("text sends = %d, want the reply itself to be unaffected", len(texts))
	}
	if len(images) != 0 || len(files) != 0 || len(api.imageUploads) != 0 || len(api.fileUploads) != 0 {
		t.Errorf("file traffic = %d images / %d files / %d+%d uploads, want none",
			len(images), len(files), len(api.imageUploads), len(api.fileUploads))
	}
}

// TestDeliverAttachments_RefusedSendTellsTheUser — Lark answered and refused.
// Nothing arrived and nothing can have, so the user is told in the definite
// wording and the drop is counted under a reason an operator can act on.
func TestDeliverAttachments_RefusedSendTellsTheUser(t *testing.T) {
	store := &fakeObjectStore{objects: map[string][]byte{
		"https://objects.example/report.pdf": []byte("%PDF-1.7"),
	}}
	m := &fakeMetrics{}
	p, q, api := newAttachmentPatcher(t, store, m)
	api.fileKeyReturn = "file_v3_key"
	api.sendFileErr = &APIError{Op: "send file message", Code: 234001, Msg: "refused"}
	q.attachments = []db.Attachment{attachmentRow(t, "report.pdf", "application/pdf", 8)}

	p.handleEvent(chatDoneEvent(t, q, "attached"))

	_, _, texts := api.snapshot()
	if len(texts) != 2 || texts[1].Text != mediaSendFailedText {
		t.Fatalf("texts = %+v, want the reply plus the definite failure notice", texts)
	}
	_, dropped, unconfirmed, _ := m.read()
	if len(dropped) != 1 || dropped[0] != dropReasonSendRefused {
		t.Errorf("dropped = %v, want one %q", dropped, dropReasonSendRefused)
	}
	if len(unconfirmed) != 0 {
		t.Errorf("unconfirmed = %v, want a refusal to stay a definite drop", unconfirmed)
	}
}

// TestDeliverAttachments_TransportFailureIsUnconfirmed — the request never
// came back with a verdict, so the file may already be on the user's screen.
// Saying "failed" there is its own harm, and resending is worse: a duplicate
// cannot be taken back.
func TestDeliverAttachments_TransportFailureIsUnconfirmed(t *testing.T) {
	store := &fakeObjectStore{objects: map[string][]byte{
		"https://objects.example/report.pdf": []byte("%PDF-1.7"),
	}}
	m := &fakeMetrics{}
	p, q, api := newAttachmentPatcher(t, store, m)
	api.fileKeyReturn = "file_v3_key"
	api.sendFileErr = errors.New("dial tcp: connection reset")
	q.attachments = []db.Attachment{attachmentRow(t, "report.pdf", "application/pdf", 8)}

	p.handleEvent(chatDoneEvent(t, q, "attached"))

	_, _, texts := api.snapshot()
	if len(texts) != 2 || texts[1].Text != mediaSendUnknownText {
		t.Fatalf("texts = %+v, want the reply plus the unconfirmed notice", texts)
	}
	_, dropped, unconfirmed, _ := m.read()
	if len(unconfirmed) != 1 || unconfirmed[0] != unconfirmedReasonSendUnconfirmed {
		t.Errorf("unconfirmed = %v, want one %q", unconfirmed, unconfirmedReasonSendUnconfirmed)
	}
	if len(dropped) != 0 {
		t.Errorf("dropped = %v, want an unverified send out of the drop rate", dropped)
	}
}

// TestDeliverAttachments_UploadRefusedIsADefiniteFailure — a failed upload
// never minted a key, so no message was ever addressed to the chat.
func TestDeliverAttachments_UploadRefusedIsADefiniteFailure(t *testing.T) {
	store := &fakeObjectStore{objects: map[string][]byte{
		"https://objects.example/chart.png": []byte("PNGDATA"),
	}}
	m := &fakeMetrics{}
	p, q, api := newAttachmentPatcher(t, store, m)
	api.uploadImageErr = errors.New("dial tcp: connection reset")
	q.attachments = []db.Attachment{attachmentRow(t, "chart.png", "image/png", 7)}

	p.handleEvent(chatDoneEvent(t, q, "attached"))

	images, _, texts := api.snapshot()
	if len(images) != 0 {
		t.Errorf("image sends = %+v, want no send without a key", images)
	}
	if len(texts) != 2 || texts[1].Text != mediaSendFailedText {
		t.Fatalf("texts = %+v, want the reply plus the definite failure notice", texts)
	}
	if _, dropped, _, _ := m.read(); len(dropped) != 1 || dropped[0] != dropReasonUploadRefused {
		t.Errorf("dropped = %v, want one %q", dropped, dropReasonUploadRefused)
	}
}

// TestDeliverAttachments_LookupFailureTellsTheUser — we cannot even tell
// whether this answer carried a file. Silence leaves the user waiting for
// something that was never attempted.
func TestDeliverAttachments_LookupFailureTellsTheUser(t *testing.T) {
	m := &fakeMetrics{}
	p, q, api := newAttachmentPatcher(t, &fakeObjectStore{}, m)
	q.attachmentsErr = errors.New("db: connection refused")

	p.handleEvent(chatDoneEvent(t, q, "attached"))

	_, _, texts := api.snapshot()
	if len(texts) != 2 || texts[1].Text != mediaLookupFailedText {
		t.Fatalf("texts = %+v, want the reply plus the lookup-failed notice", texts)
	}
	if _, dropped, _, _ := m.read(); len(dropped) != 1 || dropped[0] != dropReasonLookupFailed {
		t.Errorf("dropped = %v, want one %q", dropped, dropReasonLookupFailed)
	}
}

// TestDeliverAttachments_UnreadableObjectIsNotSent — the row points at
// something this deployment's storage does not hold.
func TestDeliverAttachments_UnreadableObjectIsNotSent(t *testing.T) {
	m := &fakeMetrics{}
	p, q, api := newAttachmentPatcher(t, &fakeObjectStore{unknownURL: true}, m)
	q.attachments = []db.Attachment{attachmentRow(t, "report.pdf", "application/pdf", 8)}

	p.handleEvent(chatDoneEvent(t, q, "attached"))

	if len(api.fileUploads) != 0 {
		t.Errorf("file uploads = %+v, want none for an object we cannot read", api.fileUploads)
	}
	if _, dropped, _, _ := m.read(); len(dropped) != 1 || dropped[0] != dropReasonObjectUnreadable {
		t.Errorf("dropped = %v, want one %q", dropped, dropReasonObjectUnreadable)
	}
}

// TestDeliverAttachments_ThreadedReplyKeepsTheFileInTheTopic — a reply that
// threads into a 话题 must not drop its file into the main group chat.
func TestDeliverAttachments_ThreadedReplyKeepsTheFileInTheTopic(t *testing.T) {
	store := &fakeObjectStore{objects: map[string][]byte{
		"https://objects.example/chart.png": []byte("PNGDATA"),
	}}
	p, q, api := newAttachmentPatcher(t, store, &fakeMetrics{})
	api.imageKeyReturn = "img_v3_key"
	q.binding.LastMessageID = pgtype.Text{String: "om_trigger", Valid: true}
	q.binding.LastThreadID = pgtype.Text{String: "omt_topic", Valid: true}
	q.attachments = []db.Attachment{attachmentRow(t, "chart.png", "image/png", 7)}

	p.handleEvent(chatDoneEvent(t, q, "in the topic"))

	images, _, _ := api.snapshot()
	if len(images) != 1 {
		t.Fatalf("image sends = %d, want 1", len(images))
	}
	if !images[0].ReplyTarget.IsSet() || !images[0].ReplyTarget.InThread {
		t.Errorf("image reply target = %+v, want the topic's trigger message", images[0].ReplyTarget)
	}
}

// TestDeliverAttachments_ThreadUnsupportedFallsBackToChatLevel — the same
// classified fallback the text reply uses. A topic that cannot take a reply
// must not cost the user the file.
func TestDeliverAttachments_ThreadUnsupportedFallsBackToChatLevel(t *testing.T) {
	store := &fakeObjectStore{objects: map[string][]byte{
		"https://objects.example/chart.png": []byte("PNGDATA"),
	}}
	m := &fakeMetrics{}
	p, q, api := newAttachmentPatcher(t, store, m)
	api.imageKeyReturn = "img_v3_key"
	api.threadReplyErr = errThreadReplyClassified
	q.binding.LastMessageID = pgtype.Text{String: "om_trigger", Valid: true}
	q.binding.LastThreadID = pgtype.Text{String: "omt_topic", Valid: true}
	q.attachments = []db.Attachment{attachmentRow(t, "chart.png", "image/png", 7)}

	p.handleEvent(chatDoneEvent(t, q, "in the topic"))

	images, _, _ := api.snapshot()
	if len(images) != 2 {
		t.Fatalf("image sends = %d, want the threaded attempt plus the chat-level retry", len(images))
	}
	if images[1].ReplyTarget.IsSet() {
		t.Errorf("retry reply target = %+v, want a chat-level send", images[1].ReplyTarget)
	}
	if delivered, dropped, _, _ := m.read(); delivered != 1 || len(dropped) != 0 {
		t.Errorf("metrics = delivered %d dropped %v, want the fallback counted as delivered", delivered, dropped)
	}
}

// TestDeliverAttachments_OversizeFileNeverLeavesStorage — the recorded size
// is checked before a byte is fetched; 30 MiB is Feishu's file ceiling.
func TestDeliverAttachments_OversizeFileNeverLeavesStorage(t *testing.T) {
	m := &fakeMetrics{}
	p, q, api := newAttachmentPatcher(t, &fakeObjectStore{}, m)
	q.attachments = []db.Attachment{attachmentRow(t, "huge.bin", "application/octet-stream", maxOutboundFileBytes+1)}

	p.handleEvent(chatDoneEvent(t, q, "attached"))

	if len(api.fileUploads) != 0 {
		t.Errorf("file uploads = %+v, want an oversize file refused locally", api.fileUploads)
	}
	if _, dropped, _, _ := m.read(); len(dropped) != 1 || dropped[0] != dropReasonTooLarge {
		t.Errorf("dropped = %v, want one %q", dropped, dropReasonTooLarge)
	}
}

// TestDeliverAttachments_MixedOutcomesSayBothThingsOnce — one refused file
// and one unverified file produce one notice each, not one per file and not
// the definite wording for the unverified one.
func TestDeliverAttachments_MixedOutcomesSayBothThingsOnce(t *testing.T) {
	store := &fakeObjectStore{objects: map[string][]byte{
		"https://objects.example/a.pdf": []byte("%PDF-a"),
		"https://objects.example/b.pdf": []byte("%PDF-b"),
	}}
	m := &fakeMetrics{}
	p, q, api := newAttachmentPatcher(t, store, m)
	api.fileKeyReturn = "file_v3_key"
	calls := 0
	api.sendFileErr = nil
	q.attachments = []db.Attachment{
		attachmentRow(t, "a.pdf", "application/pdf", 6),
		attachmentRow(t, "b.pdf", "application/pdf", 6),
	}
	// One refusal, one ambiguous transport failure, in that order.
	api.sendFileHook = func() error {
		calls++
		if calls == 1 {
			return &APIError{Op: "send file message", Code: 234001, Msg: "refused"}
		}
		return errors.New("dial tcp: connection reset")
	}

	p.handleEvent(chatDoneEvent(t, q, "two files"))

	_, _, texts := api.snapshot()
	if len(texts) != 2 {
		t.Fatalf("texts = %+v, want the reply plus exactly one notice message", texts)
	}
	notice := texts[1].Text
	if !strings.Contains(notice, mediaSendFailedText) || !strings.Contains(notice, mediaSendUnknownText) {
		t.Errorf("notice = %q, want both wordings once each", notice)
	}
	_, dropped, unconfirmed, _ := m.read()
	if len(dropped) != 1 || len(unconfirmed) != 1 {
		t.Errorf("metrics = dropped %v unconfirmed %v, want one of each", dropped, unconfirmed)
	}
}

// ---- kind / file_type / name decisions ----

func TestFeishuMediaKindPrefersContentTypeAndDemotesOversizeImages(t *testing.T) {
	cases := []struct {
		name        string
		contentType string
		filename    string
		size        int
		wantImage   bool
	}{
		{"png under the image ceiling", "image/png", "chart.png", 1 << 10, true},
		{"png over the image ceiling travels as a file", "image/png", "chart.png", maxOutboundImageBytes + 1, false},
		{"octet-stream falls back to the extension", "application/octet-stream", "chart.png", 1 << 10, true},
		{"pdf is a file", "application/pdf", "report.pdf", 1 << 10, false},
		{"unknown bytes with no extension are a file", "", "blob", 1 << 10, false},
		{"content type parameters do not confuse it", "image/png; charset=binary", "chart", 1 << 10, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := feishuSendsAsImage(tc.contentType, tc.filename, tc.size); got != tc.wantImage {
				t.Errorf("feishuSendsAsImage(%q, %q, %d) = %v, want %v",
					tc.contentType, tc.filename, tc.size, got, tc.wantImage)
			}
		})
	}
}

func TestFeishuFileType(t *testing.T) {
	cases := map[string]string{
		"report.pdf":   "pdf",
		"notes.doc":    "doc",
		"notes.docx":   "doc",
		"sheet.xlsx":   "xls",
		"deck.pptx":    "ppt",
		"clip.mp4":     "mp4",
		"voice.opus":   "opus",
		"archive.zip":  "stream",
		"noextension":  "stream",
		"REPORT.PDF":   "pdf",
		"weird.tar.gz": "stream",
	}
	for name, want := range cases {
		if got := feishuFileType(name); got != want {
			t.Errorf("feishuFileType(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestOutboundAttachmentName(t *testing.T) {
	if got := outboundAttachmentName("", "image/png"); got != "attachment.png" {
		t.Errorf("empty filename = %q, want attachment.png", got)
	}
	if got := outboundAttachmentName("../../etc/passwd", "text/plain"); got != "passwd" {
		t.Errorf("path traversal = %q, want a single segment", got)
	}
	if got := outboundAttachmentName("report.pdf", "application/pdf"); got != "report.pdf" {
		t.Errorf("plain name = %q, want it unchanged", got)
	}
}
