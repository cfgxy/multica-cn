package lark

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/integrations/channel/engine"
)

// 本文件覆盖 RUYI-400 范围一：回复（引用）媒体消息时的附件富集——
// 被引用的文件/图片经 QuotedMedia 进入既有媒体解析链路，与触发消息
// 自身媒体、近期上下文媒体互不干扰。

func quotedFileTrigger() InboundMessage {
	return InboundMessage{
		MessageID: "om_child", MessageType: "text", Content: `{"text":"分析这份文档"}`,
		Body: "分析这份文档", ParentID: "om_file_parent", ChatID: "oc_group", ChatType: ChatTypeGroup,
		SenderOpenID: "ou_sender", AddressedToBot: true, CreateTime: "3000",
	}
}

// TestEnrichQuotedFileBecomesAttachment：回复一条历史文件消息并 @Agent，
// 富集器捕获父消息媒体描述符，媒体解析器按父消息 ID 下载并产出附件引用，
// 引用块正文保留文件占位文本。
func TestEnrichQuotedFileBecomesAttachment(t *testing.T) {
	t.Parallel()
	fake := newEnricherFake()
	fake.byID["om_file_parent"] = []LarkMessage{
		{MessageID: "om_file_parent", MessageType: "file",
			Content: `{"file_key":"key_doc","file_name":"规格说明.pdf"}`,
			SenderID: "ou_peer", SenderType: "user", CreateTime: "1000"},
	}

	enriched := enrich(t, fake, quotedFileTrigger(), InboundEnricherConfig{})

	if len(enriched.QuotedMedia) != 1 || enriched.QuotedMedia[0].MessageID != "om_file_parent" {
		t.Fatalf("QuotedMedia = %+v, want 父消息描述符", enriched.QuotedMedia)
	}
	if !strings.Contains(enriched.Body, `<quoted_message message_id="om_file_parent"`) {
		t.Fatalf("引用块缺失：%q", enriched.Body)
	}

	sender := &fakeSender{
		downloadedByKey: map[string]DownloadedResource{
			"key_doc": {Data: []byte("pdf-bytes"), ContentType: "application/pdf", Filename: "规格说明.pdf"},
		},
	}
	storage := &fakeMediaStorage{}
	ledger := &fakeMediaLedger{}
	media := NewFeishuMediaResolver(sender, fakeCreds{secret: "plain"}, storage, ledger, newDiscardLogger(), nil)
	if !media.HasMedia(channelMessageFromLark(enriched)) {
		t.Fatal("引用文件未进入媒体解析器")
	}
	got := media.ResolveMedia(context.Background(), testMediaInstallation(t), engine.ResolvedIdentity{},
		uuidFromString(t, "22222222-2222-2222-2222-222222222222"), uuidFromString(t, "33333333-3333-4333-8333-333333333333"),
		channelMessageFromLark(enriched))
	if len(sender.downloadCalls) != 1 {
		t.Fatalf("下载次数 = %d, want 1：%+v", len(sender.downloadCalls), sender.downloadCalls)
	}
	if call := sender.downloadCalls[0]; call.MessageID != "om_file_parent" || call.FileKey != "key_doc" || call.Type != "file" {
		t.Fatalf("下载参数须绑定父消息 ID：%+v", call)
	}
	if len(got.MediaRefs) != 1 || got.MediaRefs[0].Filename != "规格说明.pdf" {
		t.Fatalf("MediaRefs = %+v", got.MediaRefs)
	}
}

// TestEnrichQuotedImageBecomesAttachment：图片父消息同样进入附件链路。
func TestEnrichQuotedImageBecomesAttachment(t *testing.T) {
	t.Parallel()
	fake := newEnricherFake()
	fake.byID["om_img_parent"] = []LarkMessage{
		{MessageID: "om_img_parent", MessageType: "image", Content: `{"image_key":"key_img"}`,
			SenderID: "ou_peer", SenderType: "user", CreateTime: "1000"},
	}
	in := quotedFileTrigger()
	in.ParentID = "om_img_parent"

	enriched := enrich(t, fake, in, InboundEnricherConfig{})

	if len(enriched.QuotedMedia) != 1 {
		t.Fatalf("QuotedMedia = %+v, want 1 项", enriched.QuotedMedia)
	}
	resolver := NewFeishuMediaResolver(&fakeSender{}, fakeCreds{secret: "plain"}, &fakeMediaStorage{}, &fakeMediaLedger{}, newDiscardLogger(), nil)
	if !resolver.HasMedia(channelMessageFromLark(enriched)) {
		t.Fatal("引用图片未进入媒体解析器")
	}
}

// TestEnrichQuotedTextHasNoMedia：引用纯文本消息不产生媒体描述符，
// 也不触发额外网络调用（GetMessage 一次之外无下载）。
func TestEnrichQuotedTextHasNoMedia(t *testing.T) {
	t.Parallel()
	fake := newEnricherFake()
	fake.byID["om_text_parent"] = []LarkMessage{textMsg("om_text_parent", "ou_peer", "只是普通文本", "1000")}
	in := quotedFileTrigger()
	in.ParentID = "om_text_parent"

	enriched := enrich(t, fake, in, InboundEnricherConfig{})

	if len(enriched.QuotedMedia) != 0 {
		t.Fatalf("QuotedMedia = %+v, want 空", enriched.QuotedMedia)
	}
	resolver := NewFeishuMediaResolver(&fakeSender{}, fakeCreds{secret: "plain"}, &fakeMediaStorage{}, &fakeMediaLedger{}, newDiscardLogger(), nil)
	if resolver.HasMedia(channelMessageFromLark(enriched)) {
		t.Fatal("纯文本引用不应进入媒体解析器")
	}
}

// TestEnrichQuotedMediaUnavailableDegrades：引用不存在/无权读取时降级为
// error 块，不产生媒体描述符——与既有文本引用降级路径一致。
func TestEnrichQuotedMediaUnavailableDegrades(t *testing.T) {
	t.Parallel()
	fake := newEnricherFake()
	fake.errByID["om_gone"] = errors.New("lark: code=230001 msg=no permission")

	in := quotedFileTrigger()
	in.ParentID = "om_gone"
	enriched := enrich(t, fake, in, InboundEnricherConfig{})
	if !strings.Contains(enriched.Body, `type="error"`) {
		t.Fatalf("缺权引用未降级为 error 块：%q", enriched.Body)
	}
	if len(enriched.QuotedMedia) != 0 {
		t.Fatalf("QuotedMedia = %+v, want 空", enriched.QuotedMedia)
	}

	fake2 := newEnricherFake()
	fake2.byID["om_deleted"] = []LarkMessage{
		{MessageID: "om_deleted", MessageType: "file", Deleted: true,
			Content: `{"file_key":"k","file_name":"x.pdf"}`, SenderID: "ou_peer", SenderType: "user"},
	}
	in2 := quotedFileTrigger()
	in2.ParentID = "om_deleted"
	enriched2 := enrich(t, fake2, in2, InboundEnricherConfig{})
	if len(enriched2.QuotedMedia) != 0 {
		t.Fatalf("已删除引用仍产出 QuotedMedia：%+v", enriched2.QuotedMedia)
	}
}

// TestEnrichQuotedMediaDownloadFailure：附件下载失败不阻断入站——
// 不产生 MediaRefs，入站消息照常返回（意图行留给对账器）。
func TestEnrichQuotedMediaDownloadFailure(t *testing.T) {
	t.Parallel()
	fake := newEnricherFake()
	fake.byID["om_file_parent"] = []LarkMessage{
		{MessageID: "om_file_parent", MessageType: "file",
			Content: `{"file_key":"key_doc","file_name":"规格说明.pdf"}`,
			SenderID: "ou_peer", SenderType: "user", CreateTime: "1000"},
	}
	enriched := enrich(t, fake, quotedFileTrigger(), InboundEnricherConfig{})

	sender := &fakeSender{downloadErr: errors.New("lark: http 503")}
	storage := &fakeMediaStorage{}
	ledger := &fakeMediaLedger{}
	media := NewFeishuMediaResolver(sender, fakeCreds{secret: "plain"}, storage, ledger, newDiscardLogger(), nil)
	got := media.ResolveMedia(context.Background(), testMediaInstallation(t), engine.ResolvedIdentity{},
		uuidFromString(t, "22222222-2222-2222-2222-222222222222"), uuidFromString(t, "33333333-3333-4333-8333-333333333333"),
		channelMessageFromLark(enriched))
	if len(got.MediaRefs) != 0 {
		t.Fatalf("下载失败仍产出 MediaRefs：%+v", got.MediaRefs)
	}
	if len(ledger.records) != 1 {
		t.Fatalf("意图行 = %d, want 1（失败留给对账器）", len(ledger.records))
	}
	if len(storage.uploads) != 0 {
		t.Fatalf("下载失败不应上传：%+v", storage.uploads)
	}
}

// TestEnrichQuotedForwardParentNoMediaAttachment：合并转发父消息只渲染
// 转发文本，不触发子消息附件下载（ACK 路径扇出保持有界）。
func TestEnrichQuotedForwardParentNoMediaAttachment(t *testing.T) {
	t.Parallel()
	fake := newEnricherFake()
	fake.byID["om_forward"] = []LarkMessage{
		{MessageID: "om_forward", MessageType: "merge_forward", Content: `"merge_forward"`},
		{MessageID: "om_f_child", MessageType: "file", Content: `{"file_key":"k","file_name":"a.pdf"}`,
			SenderID: "ou_peer", SenderType: "user", CreateTime: "900"},
	}
	in := quotedFileTrigger()
	in.ParentID = "om_forward"

	enriched := enrich(t, fake, in, InboundEnricherConfig{})

	if len(enriched.QuotedMedia) != 0 {
		t.Fatalf("QuotedMedia = %+v, want 空（转发子消息不下载）", enriched.QuotedMedia)
	}
	if !strings.Contains(enriched.Body, "<forwarded_messages") {
		t.Fatalf("转发正文缺失：%q", enriched.Body)
	}
}

// TestEnrichNewChatKeepsQuotedMedia：/new 只屏蔽自动附带的近期上下文；
// 用户显式引用的父消息媒体仍随消息进入附件链路。
func TestEnrichNewChatKeepsQuotedMedia(t *testing.T) {
	t.Parallel()
	fake := newEnricherFake()
	fake.byID["om_file_parent"] = []LarkMessage{
		{MessageID: "om_file_parent", MessageType: "file",
			Content: `{"file_key":"key_doc","file_name":"规格说明.pdf"}`,
			SenderID: "ou_peer", SenderType: "user", CreateTime: "1000"},
	}
	in := quotedFileTrigger()
	in.CommandBody = "/new 分析这份文档"

	enriched := enrich(t, fake, in, InboundEnricherConfig{})

	if len(enriched.QuotedMedia) != 1 {
		t.Fatalf("QuotedMedia = %+v, want 1 项（显式引用不因 /new 丢失）", enriched.QuotedMedia)
	}
}

// TestEnrichQuotedMediaCoexistsWithOwnMedia：触发消息自身带文件、又引用
// 另一条文件消息时，两份媒体各自下载、互不覆盖（对象键含资源消息 ID）。
func TestEnrichQuotedMediaCoexistsWithOwnMedia(t *testing.T) {
	t.Parallel()
	fake := newEnricherFake()
	fake.byID["om_file_parent"] = []LarkMessage{
		{MessageID: "om_file_parent", MessageType: "file",
			Content: `{"file_key":"key_doc","file_name":"规格说明.pdf"}`,
			SenderID: "ou_peer", SenderType: "user", CreateTime: "1000"},
	}
	in := quotedFileTrigger()
	in.MessageType = "file"
	in.Content = `{"file_key":"key_own","file_name":"自己的.xlsx"}`

	enriched := enrich(t, fake, in, InboundEnricherConfig{})

	sender := &fakeSender{
		downloadedByKey: map[string]DownloadedResource{
			"key_doc": {Data: []byte("pdf"), ContentType: "application/pdf", Filename: "规格说明.pdf"},
			"key_own": {Data: []byte("xlsx"), ContentType: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", Filename: "自己的.xlsx"},
		},
	}
	media := NewFeishuMediaResolver(sender, fakeCreds{secret: "plain"}, &fakeMediaStorage{}, &fakeMediaLedger{}, newDiscardLogger(), nil)
	got := media.ResolveMedia(context.Background(), testMediaInstallation(t), engine.ResolvedIdentity{},
		uuidFromString(t, "22222222-2222-2222-2222-222222222222"), uuidFromString(t, "33333333-3333-4333-8333-333333333333"),
		channelMessageFromLark(enriched))
	if len(sender.downloadCalls) != 2 || len(got.MediaRefs) != 2 {
		t.Fatalf("downloads=%d refs=%d, want 2/2", len(sender.downloadCalls), len(got.MediaRefs))
	}
	gotKeys := map[string]bool{}
	for _, call := range sender.downloadCalls {
		gotKeys[call.MessageID+"\x00"+call.FileKey] = true
	}
	if !gotKeys["om_child\x00key_own"] || !gotKeys["om_file_parent\x00key_doc"] {
		t.Fatalf("下载未区分触发与引用资源：%+v", sender.downloadCalls)
	}
}
