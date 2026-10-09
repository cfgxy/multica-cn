package lark

import (
	"context"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/integrations/channel"
	"github.com/multica-ai/multica/server/internal/integrations/channel/engine"
)

// Share-link media ingest (RUYI-572): /file/ /docx/ /wiki/ links found in
// message text resolve into uploaded attachments, through the same
// ledger→upload→MediaRef chain as native attachments, degrading the same
// way on failure.

const (
	linkFileURL = "https://example.feishu.cn/file/AbCd1234"
	linkDocxURL = "https://example.feishu.cn/docx/WxyZ9876"
	linkWikiURL = "https://example.feishu.cn/wiki/WikTok123"
)

func linkTextTrigger(url string) InboundMessage {
	return InboundMessage{
		MessageID:   "om_link",
		MessageType: "text",
		Body:        "see " + url,
		Content:     `{"text":"see ` + url + `"}`,
		ChatID:      "oc_links",
	}
}

func resolveLinks(t *testing.T, sender *fakeSender, lm InboundMessage) channelMessageSlice {
	t.Helper()
	storage := &fakeMediaStorage{}
	resolver := NewFeishuMediaResolver(sender, fakeCreds{secret: "plain"}, storage, &fakeMediaLedger{}, newDiscardLogger(), nil)
	msg := resolver.ResolveMedia(context.Background(), testMediaInstallation(t), engine.ResolvedIdentity{},
		uuidFromString(t, "22222222-2222-2222-2222-222222222222"), uuidFromString(t, "33333333-3333-4333-8333-333333333333"),
		channelMessageFromLark(lm))
	return channelMessageSlice{msg: msg, storage: storage}
}

// channelMessageSlice bundles the resolved message with the storage fake so
// link tests can assert both in one helper call.
type channelMessageSlice struct {
	msg     channel.InboundMessage
	storage *fakeMediaStorage
}

func TestFeishuMediaResolver_FileLinkBecomesMediaRef(t *testing.T) {
	sender := &fakeSender{driveFiles: map[string]DownloadedResource{
		"AbCd1234": {Data: []byte("ZIPDATA"), ContentType: "application/zip", Filename: "report.zip", SizeBytes: 7},
	}}
	got := resolveLinks(t, sender, linkTextTrigger(linkFileURL))

	if len(sender.driveCalls) != 1 || sender.driveCalls[0] != "AbCd1234" {
		t.Fatalf("drive calls = %v, want exactly [AbCd1234]", sender.driveCalls)
	}
	if len(got.msg.MediaRefs) != 1 {
		t.Fatalf("refs = %+v, want exactly one", got.msg.MediaRefs)
	}
	ref := got.msg.MediaRefs[0]
	if ref.Filename != "report.zip" || ref.MimeType != "application/zip" || ref.SizeBytes != 7 {
		t.Fatalf("ref metadata wrong: %+v", ref)
	}
	if ref.StorageKey == "" || ref.StorageURL == "" {
		t.Fatalf("ref storage pointers missing: %+v", ref)
	}
	if len(got.storage.uploads) != 1 || string(got.storage.uploads[0].data) != "ZIPDATA" {
		t.Fatalf("uploads = %+v, want one ZIPDATA upload", got.storage.uploads)
	}
}

func TestFeishuMediaResolver_DocxLinkUploadsTextArtifact(t *testing.T) {
	sender := &fakeSender{docxContents: map[string]string{"WxyZ9876": "hello 文档正文"}}
	got := resolveLinks(t, sender, linkTextTrigger(linkDocxURL))

	if len(sender.docxCalls) != 1 || sender.docxCalls[0] != "WxyZ9876" {
		t.Fatalf("docx calls = %v, want exactly [WxyZ9876]", sender.docxCalls)
	}
	if len(got.msg.MediaRefs) != 1 {
		t.Fatalf("refs = %+v, want exactly one", got.msg.MediaRefs)
	}
	ref := got.msg.MediaRefs[0]
	if ref.Filename != "feishu-docx-WxyZ9876.txt" {
		t.Fatalf("filename = %q, want feishu-docx-WxyZ9876.txt", ref.Filename)
	}
	if ref.MimeType != "text/plain; charset=utf-8" {
		t.Fatalf("mime = %q, want text/plain", ref.MimeType)
	}
	if len(got.storage.uploads) != 1 || string(got.storage.uploads[0].data) != "hello 文档正文" {
		t.Fatalf("uploads = %+v, want one docx-body upload", got.storage.uploads)
	}
}

func TestFeishuMediaResolver_WikiRoutesToDocx(t *testing.T) {
	sender := &fakeSender{
		wikiNodes:    map[string]WikiNode{"WikTok123": {Title: "设计说明", ObjType: "docx", ObjToken: "DocInner1"}},
		docxContents: map[string]string{"DocInner1": "wiki body"},
	}
	got := resolveLinks(t, sender, linkTextTrigger(linkWikiURL))

	if len(sender.wikiCalls) != 1 || len(sender.docxCalls) != 1 || sender.docxCalls[0] != "DocInner1" {
		t.Fatalf("wiki=%v docx=%v, want get_node then raw_content on DocInner1", sender.wikiCalls, sender.docxCalls)
	}
	ref := got.msg.MediaRefs[0]
	if ref.Filename != "设计说明.txt" {
		t.Fatalf("filename = %q, want 设计说明.txt (wiki title)", ref.Filename)
	}
	if len(got.msg.MediaRefs) != 1 || len(got.storage.uploads) != 1 {
		t.Fatalf("refs=%d uploads=%d, want 1/1", len(got.msg.MediaRefs), len(got.storage.uploads))
	}
}

func TestFeishuMediaResolver_WikiRoutesToFile(t *testing.T) {
	sender := &fakeSender{
		wikiNodes: map[string]WikiNode{"WikTok123": {Title: "素材包", ObjType: "file", ObjToken: "F1leToken"}},
		driveFiles: map[string]DownloadedResource{
			"F1leToken": {Data: []byte("BIN"), ContentType: "application/zip", Filename: "assets.zip", SizeBytes: 3},
		},
	}
	got := resolveLinks(t, sender, linkTextTrigger(linkWikiURL))

	if len(sender.driveCalls) != 1 || sender.driveCalls[0] != "F1leToken" {
		t.Fatalf("drive calls = %v, want exactly [F1leToken] via obj_token", sender.driveCalls)
	}
	ref := got.msg.MediaRefs[0]
	if ref.Filename != "assets.zip" {
		t.Fatalf("filename = %q, want assets.zip", ref.Filename)
	}
}

func TestFeishuMediaResolver_WikiUnsupportedObjTypeSkips(t *testing.T) {
	sender := &fakeSender{
		wikiNodes: map[string]WikiNode{"WikTok123": {Title: "表格", ObjType: "sheet", ObjToken: "Sh33t"}},
	}
	got := resolveLinks(t, sender, linkTextTrigger(linkWikiURL))

	if len(got.msg.MediaRefs) != 0 || len(got.storage.uploads) != 0 {
		t.Fatalf("refs=%d uploads=%d, want 0/0 for unsupported obj_type", len(got.msg.MediaRefs), len(got.storage.uploads))
	}
	if len(sender.driveCalls) != 0 || len(sender.docxCalls) != 0 {
		t.Fatalf("no fetch may follow an unsupported obj_type, got drive=%v docx=%v", sender.driveCalls, sender.docxCalls)
	}
}

func TestFeishuMediaResolver_LinkDeniedSendsCapabilityHint(t *testing.T) {
	cases := []struct {
		name        string
		sender      *fakeSender
		url         string
		wantInCard  string
	}{
		{
			name: "file link denies drive_file_links",
			sender: &fakeSender{driveErrByKey: map[string]error{"AbCd1234": hintDeniedErr()}},
			url:        linkFileURL,
			wantInCard: "drive:drive:readonly",
		},
		{
			name: "wiki link denies wiki_doc_links",
			sender: &fakeSender{wikiErrByKey: map[string]error{"WikTok123": hintDeniedErr()}},
			url:        linkWikiURL,
			wantInCard: "wiki:wiki:readonly",
		},
		{
			name: "docx link denies wiki_doc_links",
			sender: &fakeSender{docxErrByKey: map[string]error{"WxyZ9876": hintDeniedErr()}},
			url:        linkDocxURL,
			wantInCard: "docx:document:readonly",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cardSender := &fakeSender{
				driveFiles:   tc.sender.driveFiles,
				driveErrByKey: tc.sender.driveErrByKey,
				wikiNodes:    tc.sender.wikiNodes,
				wikiErrByKey: tc.sender.wikiErrByKey,
				docxContents: tc.sender.docxContents,
				docxErrByKey: tc.sender.docxErrByKey,
			}
			storage := &fakeMediaStorage{}
			resolver := NewFeishuMediaResolver(cardSender, fakeCreds{secret: "plain"}, storage, &fakeMediaLedger{}, newDiscardLogger(), newHintSenderForTest(cardSender))
			lm := linkTextTrigger(tc.url)
			lm.ChatID = "oc_hint"
			resolver.ResolveMedia(context.Background(), testMediaInstallation(t), engine.ResolvedIdentity{},
				uuidFromString(t, "22222222-2222-2222-2222-222222222222"), uuidFromString(t, "33333333-3333-4333-8333-333333333333"),
				channelMessageFromLark(lm))

			if len(cardSender.cardSends) != 1 {
				t.Fatalf("denied link must send one hint card, got %d", len(cardSender.cardSends))
			}
			if !strings.Contains(cardSender.cardSends[0].CardJSON, tc.wantInCard) {
				t.Fatalf("card must carry %q scopes: %s", tc.wantInCard, cardSender.cardSends[0].CardJSON)
			}
			if len(storage.uploads) != 0 {
				t.Fatalf("no upload may follow a denied fetch, got %+v", storage.uploads)
			}
		})
	}
}

func TestFeishuMediaResolver_LinksDedupedAcrossTriggerAndQuoted(t *testing.T) {
	sender := &fakeSender{driveFiles: map[string]DownloadedResource{
		"AbCd1234": {Data: []byte("ZIPDATA"), ContentType: "application/zip", Filename: "report.zip"},
	}}
	lm := linkTextTrigger(linkFileURL)
	lm.QuotedMedia = []EnrichedMediaMessage{{
		MessageID:   "om_quoted",
		MessageType: "text",
		Content:     `{"text":"replying to ` + linkFileURL + `"}`,
	}}
	got := resolveLinks(t, sender, lm)

	if len(sender.driveCalls) != 1 {
		t.Fatalf("drive calls = %v, want exactly one for the same link in trigger+quoted", sender.driveCalls)
	}
	if len(got.msg.MediaRefs) != 1 {
		t.Fatalf("refs = %d, want exactly one", len(got.msg.MediaRefs))
	}
}

func TestFeishuMediaResolver_NoShareLinkClientDegradesSilently(t *testing.T) {
	storage := &fakeMediaStorage{}
	resolver := NewFeishuMediaResolver(&bareSenderStub{}, fakeCreds{secret: "plain"}, storage, &fakeMediaLedger{}, newDiscardLogger(), nil)
	lm := linkTextTrigger(linkFileURL)

	if !resolver.HasMedia(channelMessageFromLark(lm)) {
		t.Fatal("HasMedia must be true for a share-link message even without a share-link client")
	}
	got := resolver.ResolveMedia(context.Background(), testMediaInstallation(t), engine.ResolvedIdentity{},
		uuidFromString(t, "22222222-2222-2222-2222-222222222222"), uuidFromString(t, "33333333-3333-4333-8333-333333333333"),
		channelMessageFromLark(lm))
	if len(got.MediaRefs) != 0 || len(storage.uploads) != 0 {
		t.Fatalf("no client: refs=%d uploads=%d, want 0/0 (log-only degrade)", len(got.MediaRefs), len(storage.uploads))
	}
}

func TestHasMediaShareLinkText(t *testing.T) {
	resolver := NewFeishuMediaResolver(&fakeSender{}, fakeCreds{secret: "plain"}, &fakeMediaStorage{}, &fakeMediaLedger{}, newDiscardLogger(), nil)
	cases := []struct {
		name string
		lm   InboundMessage
		want bool
	}{
		{"file link", InboundMessage{MessageID: "om_1", MessageType: "text",
			Body: "看 " + linkFileURL, Content: `{"text":"看 ` + linkFileURL + `"}`}, true},
		{"wiki link", InboundMessage{MessageID: "om_2", MessageType: "text",
			Body: linkWikiURL, Content: `{"text":"` + linkWikiURL + `"}`}, true},
		{"external link", InboundMessage{MessageID: "om_3", MessageType: "text",
			Body: "https://example.com/file/x", Content: `{"text":"https://example.com/file/x"}`}, false},
		{"feishu non-share path", InboundMessage{MessageID: "om_4", MessageType: "text",
			Body: "https://example.feishu.cn/docs/abc", Content: `{"text":"https://example.feishu.cn/docs/abc"}`}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolver.HasMedia(channelMessageFromLark(tc.lm)); got != tc.want {
				t.Fatalf("HasMedia = %v, want %v", got, tc.want)
			}
		})
	}
}
