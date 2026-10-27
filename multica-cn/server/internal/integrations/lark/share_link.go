package lark

import (
	"net/url"
	"regexp"
	"strings"
)

// Feishu share-link scanning (RUYI-572): a bare cloud-file/doc/wiki share
// URL reaches the bot as plain text inside msg_type=text / post bodies —
// no message_id+file_key pair, so the media_resources pipeline never sees
// it. The scanner turns such text into typed share links the media
// resolver can fetch through the Drive/Docs OpenAPI.
//
// Security posture: the parsed token is only ever sent as the path/query
// parameter of a fixed OpenAPI endpoint over the existing httpAPIClient
// (pinned base URL + tenant_access_token). The share page itself is never
// fetched (it 302s to a login page), so there is no SSRF surface.

// shareLinkFamily enumerates the link families this build can resolve.
// Other Feishu path families (docs/sheets/base/mindnotes/…) are out of
// scope (ADR 005 §8 决策 1B): the scanner deliberately does not match
// them, so they stay silent exactly as before this capability existed.
type shareLinkFamily string

const (
	shareLinkFile shareLinkFamily = "file" // /file/<token> → drive download
	shareLinkDocx shareLinkFamily = "docx" // /docx/<token> → raw_content
	shareLinkWiki shareLinkFamily = "wiki" // /wiki/<token> → get_node routing
)

// shareLink is one parsed, validated share link.
type shareLink struct {
	Family shareLinkFamily
	Token  string
}

// shareLinkTokenPattern is the token charset: Feishu share tokens are
// opaque alphanumeric segments. Anything else (path escapes, encodings,
// over-long junk) is treated as not-a-share-link, per ADR 005 §2.
const shareLinkTokenMaxLen = 64

func validShareLinkToken(s string) bool {
	if s == "" || len(s) > shareLinkTokenMaxLen {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') {
			return false
		}
	}
	return true
}

// isFeishuShareHost reports whether host is a Feishu/Lark public share
// host (apex or any subdomain, case-insensitive) — the host check shared
// by the scanner and the RUYI-448 bare-link note.
func isFeishuShareHost(host string) bool {
	host = strings.ToLower(host)
	return host == "feishu.cn" || strings.HasSuffix(host, ".feishu.cn") ||
		host == "larksuite.com" || strings.HasSuffix(host, ".larksuite.com")
}

// parseShareLink parses one URL string into a share link. The token comes
// only from the URL path; query and fragment are dropped. Anything that
// is not an http(s) URL on a Feishu share host with exactly
// /<family>/<token> as its path reads as not-a-share-link — log-only
// silence at the call sites, per the degrade matrix.
func parseShareLink(raw string) (shareLink, bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return shareLink{}, false
	}
	if !isFeishuShareHost(u.Hostname()) {
		return shareLink{}, false
	}
	segs := strings.Split(u.Path, "/")
	// Expect exactly ["", "<family>", "<token>"].
	if len(segs) != 3 || segs[1] == "" || segs[2] == "" {
		return shareLink{}, false
	}
	var family shareLinkFamily
	switch shareLinkFamily(segs[1]) {
	case shareLinkFile, shareLinkDocx, shareLinkWiki:
		family = shareLinkFamily(segs[1])
	default:
		return shareLink{}, false
	}
	if !validShareLinkToken(segs[2]) {
		return shareLink{}, false
	}
	return shareLink{Family: family, Token: segs[2]}, true
}

// shareLinkScanPattern finds candidate share URLs inside free text. The
// token class stops at any non-alphanumeric byte, so CJK punctuation and
// closing brackets never join the token; candidates that carry extra path
// segments are rejected afterwards by parseShareLink's exact-path check.
var shareLinkScanPattern = regexp.MustCompile(
	`https?://(?:[A-Za-z0-9-]+\.)*(?:feishu\.cn|larksuite\.com)/(?:file|docx|wiki)/[A-Za-z0-9]{1,64}`)

// shareLinksFromText scans free text (a message body, a post's flattened
// runs, a quoted parent's text) and returns the deduped share links in
// order of first appearance.
func shareLinksFromText(text string) []shareLink {
	if text == "" {
		return nil
	}
	matches := shareLinkScanPattern.FindAllString(text, -1)
	if len(matches) == 0 {
		return nil
	}
	var out []shareLink
	seen := make(map[string]bool, len(matches))
	for _, m := range matches {
		link, ok := parseShareLink(m)
		if !ok {
			continue
		}
		key := string(link.Family) + "\x00" + link.Token
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, link)
	}
	return out
}

// shareLinksFromMessage extracts share links from one message's raw
// content. Only text and post bodies carry them: the flattening reuses
// the enricher's own content decoder, so a post link span's href survives
// as "(href)" and is scanned like any other URL. Every other msg_type is
// link-free by construction.
func shareLinksFromMessage(lm InboundMessage) []shareLink {
	switch lm.MessageType {
	case "text", "post":
	default:
		return nil
	}
	if lm.Content == "" {
		return nil
	}
	return shareLinksFromText(flattenContent(lm.MessageType, lm.Content))
}

// isBareFeishuPathLink reports whether the whole (trimmed) string is one
// bare URL on a Feishu share host whose path starts with prefix.
func isBareFeishuPathLink(s, prefix string) bool {
	s = strings.TrimSpace(s)
	if s == "" || strings.ContainsAny(s, " \n\t\r") {
		return false
	}
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return false
	}
	if !isFeishuShareHost(u.Host) {
		return false
	}
	return strings.HasPrefix(u.Path, prefix)
}

// isBareFeishuShareLink extends the RUYI-448 bare-link predicate to every
// family this build resolves (ADR 005 §8 决策 2B): one bare /file/,
// /docx/ or /wiki/ URL and nothing else.
func isBareFeishuShareLink(s string) bool {
	return isBareFeishuPathLink(s, "/file/") ||
		isBareFeishuPathLink(s, "/docx/") ||
		isBareFeishuPathLink(s, "/wiki/")
}

// isBareFeishuFileLink keeps its exact RUYI-448 semantics (bare /file/
// URL only) — the note call sites and their tests pin this behavior.
func isBareFeishuFileLink(s string) bool {
	return isBareFeishuPathLink(s, "/file/")
}
