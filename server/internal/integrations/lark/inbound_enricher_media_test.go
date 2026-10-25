package lark

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"github.com/multica-ai/multica/server/internal/integrations/channel/engine"
)

func TestEnrichRecentMediaUsesOriginalMessageID(t *testing.T) {
	fake := newEnricherFake()
	fake.byChat["oc_group"] = []LarkMessage{
		textMsg("om_trigger", "ou_sender", "看看文件", "3000"),
		{MessageID: "om_file", MessageType: "file", Content: `{"file_key":"key_document","file_name":"notes.txt"}`, SenderID: "ou_sender", SenderType: "user", CreateTime: "2000"},
		{MessageID: "om_photo", MessageType: "image", Content: `{"image_key":"key_photo"}`, SenderID: "ou_sender", SenderType: "user", CreateTime: "1000"},
	}
	trigger := InboundMessage{
		MessageID: "om_trigger", MessageType: "text", Content: `{"text":"看看文件"}`,
		ChatID: "oc_group", ChatType: ChatTypeGroup, SenderOpenID: "ou_sender",
		AddressedToBot: true, Body: "看看文件", CreateTime: "3000",
	}

	enriched := enrich(t, fake, trigger, groupCfg())
	sender := &fakeSender{
		downloadedByKey: map[string]DownloadedResource{
			"key_document": {Data: []byte("document"), ContentType: "text/plain", Filename: "notes.txt"},
			"key_photo":    {Data: []byte("photo"), ContentType: "image/png", Filename: "photo.png"},
		},
	}
	storage := &fakeMediaStorage{}
	ledger := &fakeMediaLedger{}
	media := NewFeishuMediaResolver(sender, fakeCreds{secret: "plain"}, storage, ledger, newDiscardLogger(), nil)
	if !media.HasMedia(channelMessageFromLark(enriched)) {
		t.Fatal("历史文件未进入媒体解析器")
	}

	got := media.ResolveMedia(context.Background(), testMediaInstallation(t), engine.ResolvedIdentity{},
		uuidFromString(t, "22222222-2222-2222-2222-222222222222"), uuidFromString(t, "33333333-3333-4333-8333-333333333333"), channelMessageFromLark(enriched))
	if len(sender.downloadCalls) != 2 || len(storage.uploads) != 2 || len(ledger.records) != 2 || len(got.MediaRefs) != 2 {
		t.Fatalf("downloads=%+v uploads=%d intents=%d refs=%+v", sender.downloadCalls, len(storage.uploads), len(ledger.records), got.MediaRefs)
	}
	want := map[string]DownloadResourceParams{
		"key_document": {MessageID: "om_file", FileKey: "key_document", Type: "file"},
		"key_photo":    {MessageID: "om_photo", FileKey: "key_photo", Type: "image"},
	}
	for _, call := range sender.downloadCalls {
		if call != want[call.FileKey] {
			t.Errorf("下载须绑定历史原消息 ID：got %+v, want %+v", call, want[call.FileKey])
		}
	}
	if got.MediaRefs[0].Filename != "photo.png" || got.MediaRefs[1].Filename != "notes.txt" {
		t.Errorf("附件文件名异常：%+v", got.MediaRefs)
	}
}

func TestEnrichMergeForwardImageBecomesAttachment(t *testing.T) {
	fake := newEnricherFake()
	fake.byID["om_forward"] = []LarkMessage{
		{MessageID: "om_forward", MessageType: "merge_forward", Content: `{"content":"Merged and Forwarded Message"}`},
		{MessageID: "om_image", MessageType: "image", Content: `{"image_key":"key_forward_image"}`,
			SenderID: "ou_sender", SenderType: "user", CreateTime: "1000"},
		{MessageID: "om_post", MessageType: "post", Content: `{"content":[[{"tag":"img","image_key":"key_post_image"},{"tag":"media","file_key":"key_post_video"}]]}`,
			SenderID: "ou_sender", SenderType: "user", CreateTime: "2000"},
	}
	trigger := InboundMessage{
		MessageID: "om_forward", MessageType: "merge_forward", ChatID: "oc_group",
		ChatType: ChatTypeGroup, AddressedToBot: true,
	}

	enriched := enrich(t, fake, trigger, groupCfg())
	if len(enriched.ForwardMedia) != 2 || enriched.ForwardMedia[0].MessageID != "om_image" || enriched.ForwardMedia[1].MessageID != "om_post" {
		t.Fatalf("ForwardMedia = %+v, want forwarded child image descriptor", enriched.ForwardMedia)
	}

	sender := &fakeSender{downloadedByKey: map[string]DownloadedResource{
		"key_forward_image": {Data: []byte("image"), ContentType: "image/png", Filename: "forwarded.png"},
		"key_post_image":    {Data: []byte("post image"), ContentType: "image/png", Filename: "forwarded-post.png"},
		"key_post_video":    {Data: []byte("video"), ContentType: "video/mp4", Filename: "forwarded.mp4"},
	}}
	storage := &fakeMediaStorage{}
	ledger := &fakeMediaLedger{}
	media := NewFeishuMediaResolver(sender, fakeCreds{secret: "plain"}, storage, ledger, newDiscardLogger(), nil)
	message := channelMessageFromLark(enriched)
	if !media.HasMedia(message) {
		t.Fatal("转发子消息图片未进入媒体解析器")
	}

	got := media.ResolveMedia(context.Background(), testMediaInstallation(t), engine.ResolvedIdentity{},
		uuidFromString(t, "22222222-2222-2222-2222-222222222222"), uuidFromString(t, "33333333-3333-4333-8333-333333333333"), message)
	wantDownloads := map[string]DownloadResourceParams{
		"key_forward_image": {MessageID: "om_image", FileKey: "key_forward_image", Type: "image"},
		"key_post_image":    {MessageID: "om_post", FileKey: "key_post_image", Type: "image"},
	}
	if len(sender.downloadCalls) != len(wantDownloads) {
		t.Fatalf("forwarded image download = %+v", sender.downloadCalls)
	}
	for _, call := range sender.downloadCalls {
		if call != wantDownloads[call.FileKey] {
			t.Errorf("forwarded image download = %+v, want %+v", call, wantDownloads[call.FileKey])
		}
	}
	if len(got.MediaRefs) != 2 || got.MediaRefs[0].Filename != "forwarded.png" || got.MediaRefs[1].Filename != "forwarded-post.png" {
		t.Fatalf("MediaRefs = %+v, want both forwarded image attachments", got.MediaRefs)
	}
}

func TestFeishuMediaResolver_DeduplicatesAcrossRecentAndForwardMedia(t *testing.T) {
	sender := &fakeSender{downloaded: DownloadedResource{Data: []byte("image"), ContentType: "image/png"}}
	storage := &fakeMediaStorage{}
	ledger := &fakeMediaLedger{}
	resolver := NewFeishuMediaResolver(sender, fakeCreds{secret: "plain"}, storage, ledger, newDiscardLogger(), nil)
	media := EnrichedMediaMessage{MessageID: "om_image", MessageType: "image", Content: `{"image_key":"same_key"}`}
	lm := InboundMessage{
		MessageID:    "om_forward",
		MessageType:  "merge_forward",
		RecentMedia:  []EnrichedMediaMessage{media},
		ForwardMedia: []EnrichedMediaMessage{media},
	}

	got := resolver.ResolveMedia(context.Background(), testMediaInstallation(t), engine.ResolvedIdentity{},
		uuidFromString(t, "22222222-2222-2222-2222-222222222222"), uuidFromString(t, "33333333-3333-4333-8333-333333333333"), channelMessageFromLark(lm))
	if len(sender.downloadCalls) != 1 || len(storage.uploads) != 1 || len(ledger.records) != 1 || len(got.MediaRefs) != 1 {
		t.Fatalf("downloads=%d uploads=%d intents=%d refs=%d, want 1 each", len(sender.downloadCalls), len(storage.uploads), len(ledger.records), len(got.MediaRefs))
	}
}

func TestFeishuMediaResolver_CapsTotalMediaResources(t *testing.T) {
	sender := &fakeSender{downloaded: DownloadedResource{Data: []byte("image"), ContentType: "image/png"}}
	storage := &fakeMediaStorage{}
	ledger := &fakeMediaLedger{}
	resolver := NewFeishuMediaResolver(sender, fakeCreds{secret: "plain"}, storage, ledger, newDiscardLogger(), nil)
	lm := InboundMessage{MessageID: "om_forward", MessageType: "merge_forward"}
	for i := 0; i < maxMediaResourcesPerMessage+1; i++ {
		lm.ForwardMedia = append(lm.ForwardMedia, EnrichedMediaMessage{
			MessageID: "om_image_" + strconv.Itoa(i), MessageType: "image",
			Content: `{"image_key":"key_` + strconv.Itoa(i) + `"}`,
		})
	}

	got := resolver.ResolveMedia(context.Background(), testMediaInstallation(t), engine.ResolvedIdentity{},
		uuidFromString(t, "22222222-2222-2222-2222-222222222222"), uuidFromString(t, "33333333-3333-4333-8333-333333333333"), channelMessageFromLark(lm))
	if len(sender.downloadCalls) != maxMediaResourcesPerMessage || len(storage.uploads) != maxMediaResourcesPerMessage ||
		len(ledger.records) != maxMediaResourcesPerMessage || len(got.MediaRefs) != maxMediaResourcesPerMessage {
		t.Fatalf("downloads=%d uploads=%d intents=%d refs=%d, want capped at %d", len(sender.downloadCalls), len(storage.uploads), len(ledger.records), len(got.MediaRefs), maxMediaResourcesPerMessage)
	}
}

func TestEnrichRecentMediaIsolation(t *testing.T) {
	tests := []struct {
		name   string
		change func(*LarkMessage, *InboundMessage)
		err    error
		want   bool
	}{
		{name: "同发送者同话题", want: true},
		{name: "其他发送者", change: func(item *LarkMessage, _ *InboundMessage) { item.SenderID = "ou_other" }},
		{name: "其他话题", change: func(item *LarkMessage, _ *InboundMessage) { item.ThreadID = "th_b" }},
		{name: "话题字段缺失", change: func(item *LarkMessage, _ *InboundMessage) { item.ThreadID = "" }},
		{name: "非话题触发排除话题文件", change: func(_ *LarkMessage, trigger *InboundMessage) { trigger.ThreadID = "" }},
		{name: "触发后发出的文件", change: func(item *LarkMessage, _ *InboundMessage) { item.CreateTime = "4000" }},
		{name: "无法核对文件时间", change: func(item *LarkMessage, _ *InboundMessage) { item.CreateTime = "" }},
		{name: "无法核对触发时间", change: func(_ *LarkMessage, trigger *InboundMessage) { trigger.CreateTime = "" }},
		{name: "应用发送的文件", change: func(item *LarkMessage, _ *InboundMessage) { item.SenderType = "app" }},
		{name: "无可解析媒体键", change: func(item *LarkMessage, _ *InboundMessage) { item.Content = `{}` }},
		{name: "未单独提及机器人", change: func(_ *LarkMessage, trigger *InboundMessage) { trigger.AddressedToBot = false }},
		{name: "开启新聊天", change: func(_ *LarkMessage, trigger *InboundMessage) { trigger.CommandBody = "/new" }},
		{name: "预取失败", err: errors.New("history unavailable")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fake := newEnricherFake()
			item := LarkMessage{MessageID: "om_file", MessageType: "file", Content: `{"file_key":"key_file"}`,
				SenderID: "ou_sender", SenderType: "user", CreateTime: "2000", ThreadID: "th_a"}
			trigger := InboundMessage{MessageID: "om_trigger", MessageType: "text", ChatID: "oc_group",
				ChatType: ChatTypeGroup, SenderOpenID: "ou_sender", Body: "读取文件",
				AddressedToBot: true, ThreadID: "th_a", CreateTime: "3000"}
			if tc.change != nil {
				tc.change(&item, &trigger)
			}
			fake.byChat["oc_group"] = []LarkMessage{item}
			if tc.err != nil {
				fake.errByChat["oc_group"] = tc.err
			}
			enriched := enrich(t, fake, trigger, groupCfg())
			resolver := NewFeishuMediaResolver(&fakeSender{}, fakeCreds{secret: "plain"}, &fakeMediaStorage{}, &fakeMediaLedger{}, newDiscardLogger(), nil)
			if got := resolver.HasMedia(channelMessageFromLark(enriched)); got != tc.want {
				t.Errorf("HasMedia = %t, want %t; recent=%+v; list=%v; body=%q", got, tc.want, enriched.RecentMedia, fake.listParams, enriched.Body)
			}
		})
	}
}
