package lark

import (
	"context"
	"strings"
	"testing"
	"time"
)

// 本文件覆盖 RUYI-448：引用消息上下文装配的两处缺陷修复——
// ① 引用 file 父消息时引用块必须携带附件文件名（下载失败时文件名是
//    agent 唯一可得的附件信息，"[File]" 裸占位不可接受）；
// ② 被引用文本消息只是裸飞书文件分享链接时，显式降级注记（服务端凭
//    现有凭据无法取回链接内容，注记阻止 agent 徒劳抓取外链）；
// ③ 近期上下文与引用父消息两次 fetch 并行执行——串行时第三次串行
//    RTT（发言人名字解析）最容易耗尽 2s EnrichTimeout，导致
//    全体发言人回退 "User N" 占位（RUYI-448 截图故障形态）。

// TestEnrichQuotedFileBlockShowsFilename：引用 file 父消息，引用块正文
// 渲染 "[File: <file_name>]"——文件名来自父消息 body.content 的声明。
func TestEnrichQuotedFileBlockShowsFilename(t *testing.T) {
	t.Parallel()
	fake := newEnricherFake()
	fake.byID["om_file_parent"] = []LarkMessage{
		{MessageID: "om_file_parent", MessageType: "file",
			Content:  `{"file_key":"key_doc","file_name":"规格说明.pdf"}`,
			SenderID: "ou_peer", SenderType: "user", CreateTime: "1000"},
	}

	enriched := enrich(t, fake, quotedFileTrigger(), InboundEnricherConfig{})

	want := `<quoted_message message_id="om_file_parent" sender="User 1" type="file">
[File: 规格说明.pdf]
</quoted_message>`
	if !strings.Contains(enriched.Body, want) {
		t.Fatalf("引用块未携带附件文件名：\n%q\nwant contains:\n%q", enriched.Body, want)
	}
}

// TestEnrichQuotedMediaFilenameByType：audio / media（视频）父消息同样
// 按类型渲染带文件名的占位；image 的 content 不声明文件名，保持 [Image]。
func TestEnrichQuotedMediaFilenameByType(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		msgType string
		content string
		want    string
	}{
		{"audio", "audio", `{"file_key":"k","file_name":"会议录音.m4a","duration":1000}`, "[Audio: 会议录音.m4a]"},
		{"video", "media", `{"file_key":"k","image_key":"ik","file_name":"发布演示.mp4"}`, "[Video: 发布演示.mp4]"},
		{"file-unnamed", "file", `{"file_key":"k"}`, "[File]"},
		{"image-no-name", "image", `{"image_key":"ik"}`, "[Image]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fake := newEnricherFake()
			fake.byID["om_media_parent"] = []LarkMessage{
				{MessageID: "om_media_parent", MessageType: tc.msgType,
					Content: tc.content, SenderID: "ou_peer", SenderType: "user", CreateTime: "1000"},
			}
			in := quotedFileTrigger()
			in.ParentID = "om_media_parent"

			enriched := enrich(t, fake, in, InboundEnricherConfig{})

			if !strings.Contains(enriched.Body, tc.want+"\n</quoted_message>") {
				t.Fatalf("msg_type=%s 引用块 = %q, want contains %q", tc.msgType, enriched.Body, tc.want)
			}
		})
	}
}

// TestEnrichQuotedFeishuFileLinkDegrades：被引用的 text 父消息整条只是
// 一个飞书文件分享链接（RUYI-448 截图故障形态：UI 把纯链接消息渲染为文件
// 卡片）——链接原文保留，且附加显式降级注记，告诉 agent 内容无法凭现有
// 凭据取回、应请发送者直接补发附件，而不是徒劳抓取外链。
func TestEnrichQuotedFeishuFileLinkDegrades(t *testing.T) {
	t.Parallel()
	fake := newEnricherFake()
	const link = "https://tenant.example.feishu.cn/file/AbcDefExampleToken123456"
	fake.byID["om_link_parent"] = []LarkMessage{
		textMsg("om_link_parent", "ou_peer", link, "1000"),
	}
	in := quotedFileTrigger()
	in.ParentID = "om_link_parent"

	enriched := enrich(t, fake, in, InboundEnricherConfig{})

	if !strings.Contains(enriched.Body, link) {
		t.Fatalf("引用块丢失链接原文：%q", enriched.Body)
	}
	if !strings.Contains(enriched.Body, "Feishu file/doc/wiki share link") {
		t.Fatalf("裸飞书文件链接引用未附降级注记：%q", enriched.Body)
	}
}

// TestEnrichQuotedWikiDocxLinksDegrade（RUYI-572 决策 2B）：裸 wiki /
// docx 分享链接与裸 /file/ 链接同样触发降级注记——本期 resolver 已能
// 解析这三族，但 capability 缺失或文档级 403 时内容不会到达，注记是
// 固定降级文案。
func TestEnrichQuotedWikiDocxLinksDegrade(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		link string
	}{
		{"wiki", "https://tenant.example.feishu.cn/wiki/wikcnExampleToken123"},
		{"docx", "https://tenant.example.feishu.cn/docx/doxcnExampleToken123"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fake := newEnricherFake()
			fake.byID["om_link_parent"] = []LarkMessage{
				textMsg("om_link_parent", "ou_peer", tc.link, "1000"),
			}
			in := quotedFileTrigger()
			in.ParentID = "om_link_parent"

			enriched := enrich(t, fake, in, InboundEnricherConfig{})

			if !strings.Contains(enriched.Body, tc.link) {
				t.Fatalf("引用块丢失链接原文：%q", enriched.Body)
			}
			if !strings.Contains(enriched.Body, "Feishu file/doc/wiki share link") {
				t.Fatalf("裸 %s 链接引用未附降级注记：%q", tc.name, enriched.Body)
			}
		})
	}
}

// TestEnrichQuotedLinkNoteOnlyForBareFeishuFileLinks：降级注记的触发面
// 收窄——普通文本、非飞书域名、飞书不受支持路径（docs/sheets 等本期
// 不解析的族）、夹在正文中的链接均不触发注记。
func TestEnrichQuotedLinkNoteOnlyForBareFeishuFileLinks(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		text string
	}{
		{"plain-text", "只是普通文本"},
		{"non-feishu-url", "https://example.com/file/abc"},
		{"feishu-unsupported-path", "https://tenant.example.feishu.cn/docs/abc"},
		{"feishu-unsupported-sheets", "https://tenant.example.feishu.cn/sheets/abc"},
		{"link-in-prose", "表格在 https://tenant.example.feishu.cn/file/AbcDefExampleToken 请查收"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fake := newEnricherFake()
			fake.byID["om_parent"] = []LarkMessage{
				textMsg("om_parent", "ou_peer", tc.text, "1000"),
			}
			in := quotedFileTrigger()
			in.ParentID = "om_parent"

			enriched := enrich(t, fake, in, InboundEnricherConfig{})

			if strings.Contains(enriched.Body, "Feishu file-share link") {
				t.Fatalf("文本 %q 不应触发降级注记：%q", tc.text, enriched.Body)
			}
		})
	}
}

// TestEnrichRecentAndQuotedFetchesRunConcurrently：近期上下文（list）与
// 引用父消息（get）两次 fetch 必须并行——串行实现里 list 先行、在
// listEnter 中阻塞等待 get 进入，get 永远不会被调用，Enrich 死锁（3s
// 看门狗把串行实现判红）。并行实现下两者同时进入、同时完成。这是名字
// 解析（第三次串行 RTT）保住 EnrichTimeout 预算的前提。
func TestEnrichRecentAndQuotedFetchesRunConcurrently(t *testing.T) {
	t.Parallel()
	fake := newEnricherFake()
	getEntered := make(chan struct{})
	fake.getEnter = func() { close(getEntered) }
	fake.listEnter = func() { <-getEntered } // 阻塞直至 GetMessage 已进入

	fake.byID["om_parent"] = []LarkMessage{
		textMsg("om_parent", "ou_alice", "父消息", "1000"),
	}
	fake.byChat["oc_g"] = []LarkMessage{
		textMsg("om_trigger", "ou_user", "总结一下", "3000"),
	}
	in := InboundMessage{
		MessageType: "text", MessageID: "om_trigger", ChatID: "oc_g",
		ChatType: ChatTypeGroup, AddressedToBot: true, ParentID: "om_parent",
		SenderOpenID: "ou_user", Body: "总结一下", CreateTime: "3000",
	}

	e := NewInboundEnricher(fake, groupCfg())
	done := make(chan InboundMessage, 1)
	go func() {
		done <- e.Enrich(context.Background(), in, InstallationCredentials{AppID: "a", AppSecret: "s"})
	}()
	select {
	case out := <-done:
		if out.ParentID == "" {
			t.Fatalf("ParentID 丢失：%+v", out)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("list 与 get fetch 串行执行：list 阻塞等待 get 进入时 Enrich 死锁")
	}
}
