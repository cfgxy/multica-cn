package lark

import (
	"context"
	"errors"
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
	media := NewFeishuMediaResolver(sender, fakeCreds{secret: "plain"}, storage, ledger, newDiscardLogger())
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
			resolver := NewFeishuMediaResolver(&fakeSender{}, fakeCreds{secret: "plain"}, &fakeMediaStorage{}, &fakeMediaLedger{}, newDiscardLogger())
			if got := resolver.HasMedia(channelMessageFromLark(enriched)); got != tc.want {
				t.Errorf("HasMedia = %t, want %t; recent=%+v; list=%v; body=%q", got, tc.want, enriched.RecentMedia, fake.listParams, enriched.Body)
			}
		})
	}
}
