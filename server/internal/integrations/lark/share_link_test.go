package lark

import (
	"strings"
	"testing"
)

func TestParseShareLink(t *testing.T) {
	tests := []struct {
		name   string
		raw    string
		want   shareLink
		wantOK bool
	}{
		{
			name:   "file link on apex domain",
			raw:    "https://feishu.cn/file/ABCdef123",
			want:   shareLink{Family: shareLinkFile, Token: "ABCdef123"},
			wantOK: true,
		},
		{
			name:   "file link on subdomain",
			raw:    "https://xxx.feishu.cn/file/token123",
			want:   shareLink{Family: shareLinkFile, Token: "token123"},
			wantOK: true,
		},
		{
			name:   "wiki link on larksuite",
			raw:    "https://example.larksuite.com/wiki/wikcnKQ1k3p",
			want:   shareLink{Family: shareLinkWiki, Token: "wikcnKQ1k3p"},
			wantOK: true,
		},
		{
			name:   "docx link",
			raw:    "https://xxx.feishu.cn/docx/doxcn123456",
			want:   shareLink{Family: shareLinkDocx, Token: "doxcn123456"},
			wantOK: true,
		},
		{
			name:   "host case is insensitive",
			raw:    "https://Xxx.Feishu.CN/file/tok123",
			want:   shareLink{Family: shareLinkFile, Token: "tok123"},
			wantOK: true,
		},
		{
			name:   "query and fragment are dropped",
			raw:    "https://xxx.feishu.cn/file/tok123?from=space&x=1#frag",
			want:   shareLink{Family: shareLinkFile, Token: "tok123"},
			wantOK: true,
		},
		{
			name:   "port is tolerated",
			raw:    "https://xxx.feishu.cn:443/file/tok123",
			want:   shareLink{Family: shareLinkFile, Token: "tok123"},
			wantOK: true,
		},
		{
			name:   "wrong host suffix (feishu.cn.evil.com)",
			raw:    "https://feishu.cn.evil.com/file/tok123",
			wantOK: false,
		},
		{
			name:   "non-feishu host",
			raw:    "https://example.com/file/tok123",
			wantOK: false,
		},
		{
			name:   "bare domain lookalike",
			raw:    "https://notfeishu.cn/file/tok123",
			wantOK: false,
		},
		{
			name:   "unsupported family sheets",
			raw:    "https://xxx.feishu.cn/sheets/tok123",
			wantOK: false,
		},
		{
			name:   "unsupported family base",
			raw:    "https://xxx.feishu.cn/base/tok123",
			wantOK: false,
		},
		{
			name:   "unsupported family docs (legacy doc export is out of scope)",
			raw:    "https://xxx.feishu.cn/docs/tok123",
			wantOK: false,
		},
		{
			name:   "unsupported family mindnotes",
			raw:    "https://xxx.feishu.cn/mindnotes/tok123",
			wantOK: false,
		},
		{
			name:   "empty token",
			raw:    "https://xxx.feishu.cn/file/",
			wantOK: false,
		},
		{
			name:   "token with non-alnum characters",
			raw:    "https://xxx.feishu.cn/file/tok123/../../etc",
			wantOK: false,
		},
		{
			name:   "token beyond 64 chars",
			raw:    "https://xxx.feishu.cn/file/" + strings.Repeat("a", 65),
			wantOK: false,
		},
		{
			name:   "token at 64 chars is accepted",
			raw:    "https://xxx.feishu.cn/file/" + strings.Repeat("a", 64),
			want:   shareLink{Family: shareLinkFile, Token: strings.Repeat("a", 64)},
			wantOK: true,
		},
		{
			name:   "extra path segment after token",
			raw:    "https://xxx.feishu.cn/file/tok123/extra",
			wantOK: false,
		},
		{
			name:   "non-http scheme",
			raw:    "ftp://xxx.feishu.cn/file/tok123",
			wantOK: false,
		},
		{
			name:   "no scheme",
			raw:    "xxx.feishu.cn/file/tok123",
			wantOK: false,
		},
		{
			name:   "empty input",
			raw:    "",
			wantOK: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseShareLink(tt.raw)
			if ok != tt.wantOK {
				t.Fatalf("parseShareLink(%q) ok = %v, want %v (got %+v)", tt.raw, ok, tt.wantOK, got)
			}
			if !tt.wantOK {
				return
			}
			if got != tt.want {
				t.Fatalf("parseShareLink(%q) = %+v, want %+v", tt.raw, got, tt.want)
			}
		})
	}
}

func TestShareLinksFromText(t *testing.T) {
	t.Run("finds multiple distinct links", func(t *testing.T) {
		text := "看下这个 https://xxx.feishu.cn/file/Abc123 和 https://xxx.feishu.cn/docx/doxcn9 以及 https://yyy.larksuite.com/wiki/wikcn1 谢谢"
		links := shareLinksFromText(text)
		if len(links) != 3 {
			t.Fatalf("got %d links (%+v), want 3", len(links), links)
		}
		if links[0].Family != shareLinkFile || links[0].Token != "Abc123" {
			t.Errorf("links[0] = %+v", links[0])
		}
		if links[1].Family != shareLinkDocx || links[1].Token != "doxcn9" {
			t.Errorf("links[1] = %+v", links[1])
		}
		if links[2].Family != shareLinkWiki || links[2].Token != "wikcn1" {
			t.Errorf("links[2] = %+v", links[2])
		}
	})
	t.Run("dedupes repeated links", func(t *testing.T) {
		text := "https://xxx.feishu.cn/file/tok1 again https://xxx.feishu.cn/file/tok1"
		links := shareLinksFromText(text)
		if len(links) != 1 || links[0].Token != "tok1" {
			t.Fatalf("got %+v, want single tok1", links)
		}
	})
	t.Run("trailing CJK punctuation does not join the token", func(t *testing.T) {
		links := shareLinksFromText("文件在 https://xxx.feishu.cn/file/tok1，请查收。（第二个 https://xxx.feishu.cn/file/tok2。）")
		if len(links) != 2 || links[0].Token != "tok1" || links[1].Token != "tok2" {
			t.Fatalf("got %+v", links)
		}
	})
	t.Run("wrapped in parentheses", func(t *testing.T) {
		links := shareLinksFromText("(https://xxx.feishu.cn/file/tok9)")
		if len(links) != 1 || links[0].Token != "tok9" {
			t.Fatalf("got %+v", links)
		}
	})
	t.Run("plain text and non-feishu URLs yield nothing", func(t *testing.T) {
		text := "see https://example.com/file/tok1 and just words, no links"
		if links := shareLinksFromText(text); len(links) != 0 {
			t.Fatalf("got %+v, want none", links)
		}
	})
	t.Run("unsupported families are ignored", func(t *testing.T) {
		if links := shareLinksFromText("https://xxx.feishu.cn/sheets/tok1"); len(links) != 0 {
			t.Fatalf("got %+v, want none", links)
		}
	})
	t.Run("empty text", func(t *testing.T) {
		if links := shareLinksFromText(""); len(links) != 0 {
			t.Fatalf("got %+v, want none", links)
		}
	})
}

func TestShareLinksFromMessage(t *testing.T) {
	t.Run("text message", func(t *testing.T) {
		lm := InboundMessage{MessageType: "text", Content: `{"text":"https://xxx.feishu.cn/file/tok1 请看"}`}
		links := shareLinksFromMessage(lm)
		if len(links) != 1 || links[0].Token != "tok1" {
			t.Fatalf("got %+v", links)
		}
	})
	t.Run("post message with href span", func(t *testing.T) {
		lm := InboundMessage{MessageType: "post", Content: `{"title":"t","content":[[{"tag":"text","text":"看这个 "},{"tag":"a","text":"链接","href":"https://xxx.feishu.cn/wiki/wikcn7"}]]}`}
		links := shareLinksFromMessage(lm)
		if len(links) != 1 || links[0].Family != shareLinkWiki || links[0].Token != "wikcn7" {
			t.Fatalf("got %+v", links)
		}
	})
	t.Run("image message has no links", func(t *testing.T) {
		lm := InboundMessage{MessageType: "image", Content: `{"image_key":"img_v2_x"}`}
		if links := shareLinksFromMessage(lm); len(links) != 0 {
			t.Fatalf("got %+v", links)
		}
	})
	t.Run("invalid content json", func(t *testing.T) {
		lm := InboundMessage{MessageType: "text", Content: `not-json`}
		if links := shareLinksFromMessage(lm); len(links) != 0 {
			t.Fatalf("got %+v", links)
		}
	})
	t.Run("empty content", func(t *testing.T) {
		if links := shareLinksFromMessage(InboundMessage{MessageType: "text"}); len(links) != 0 {
			t.Fatalf("got %+v", links)
		}
	})
}

func TestIsBareFeishuShareLink(t *testing.T) {
	tests := []struct {
		text string
		want bool
	}{
		{"https://xxx.feishu.cn/file/tok1", true},
		{"  https://xxx.feishu.cn/file/tok1  ", true},
		{"https://xxx.feishu.cn/docx/doxcn1", true},
		{"https://xxx.larksuite.com/wiki/wikcn1", true},
		{"https://xxx.feishu.cn/sheets/tok1", false},
		{"看这个 https://xxx.feishu.cn/file/tok1", false},
		{"https://xxx.feishu.cn/file/tok1\nsecond line", false},
		{"", false},
		{"https://example.com/file/tok1", false},
	}
	for _, tt := range tests {
		if got := isBareFeishuShareLink(tt.text); got != tt.want {
			t.Errorf("isBareFeishuShareLink(%q) = %v, want %v", tt.text, got, tt.want)
		}
	}
}

// The RUYI-448 note predicate must keep its exact original semantics after
// the host/family check was generalized: a bare https(s) Feishu /file/ URL
// and nothing else.
func TestIsBareFeishuFileLinkUnchanged(t *testing.T) {
	tests := []struct {
		text string
		want bool
	}{
		{"https://xxx.feishu.cn/file/tok1", true},
		{"http://feishu.cn/file/tok1", true},
		{"https://xxx.feishu.cn/file/tok1?x=1", true},
		{"https://xxx.feishu.cn/docx/doxcn1", false},
		{"https://xxx.feishu.cn/wiki/wikcn1", false},
		{"https://xxx.feishu.cn/file/tok1 x", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := isBareFeishuFileLink(tt.text); got != tt.want {
			t.Errorf("isBareFeishuFileLink(%q) = %v, want %v", tt.text, got, tt.want)
		}
	}
}
