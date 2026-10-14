package handler

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/proxy"

	"github.com/gorilla/websocket"
)

// ---------------------------------------------------------------------------
// Voice provider egress proxy (RUYI-692 D1, Owner 2026-10-11 09:30 纠偏 §4).
//
// The gateway dials Gemini Live through a dedicated per-connection proxy:
// MULTICA_GEMINI_PROXY_URL names the proxy endpoint — `socks5://` is the
// preferred form, `http://`/`https://` (CONNECT) are accepted too. The URL may
// carry user:pass credentials; it lives only in the server environment and is
// never logged, persisted, or echoed (the same discipline as the credential
// store — with nothing to encrypt because nothing is stored).
//
// Scope is the connection, not the process: the dialer is instantiated per
// provider dial with Proxy pointing at exactly this URL, so a configured proxy
// affects only the Gemini Live hop. Environment proxy variables
// (HTTPS_PROXY & co.) are deliberately NOT inherited — a fresh dialer with an
// explicit Proxy (or none) cannot silently widen the egress policy the
// operator configured; doing nothing keeps the direct dial.
// ---------------------------------------------------------------------------

// voiceProviderDialTimeout bounds one provider dial (handshake included).
const voiceProviderDialTimeout = 10 * time.Second

// voiceProviderProxyURL parses the configured proxy endpoint. ok is false when
// no proxy is configured or the configured value is unusable — the latter logs
// once per dial attempt (dials are rare; a persistently bad value shows up as
// unreachable badges and degraded entries, which is the honest state).
func (h *Handler) voiceProviderProxyURL() (*url.URL, bool) {
	raw := strings.TrimSpace(h.VoiceProviderProxyURL)
	if raw == "" {
		return nil, false
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		slog.Error("voice provider proxy URL is unusable; refusing to dial around the configured egress policy",
			"scheme", parsedSchemeOf(parsed), "error", err)
		return nil, false
	}
	switch strings.ToLower(parsed.Scheme) {
	case "socks5", "socks5h", "http", "https":
		return parsed, true
	default:
		slog.Error("voice provider proxy URL scheme unsupported (want socks5:// or http(s)://); refusing to dial around the configured egress policy",
			"scheme", parsed.Scheme)
		return nil, false
	}
}

func parsedSchemeOf(u *url.URL) string {
	if u == nil {
		return ""
	}
	return u.Scheme
}

// voiceProviderDialer builds the per-connection dialer for one provider hop.
// A zero-Proxy dialer means "no proxy at all": gorilla's DefaultDialer would
// inherit ProxyFromEnvironment, which this gateway must never do (纠偏 §4:
// 不做整机全局代理, and the env-var inheritance with it).
//
// A configured-but-unusable proxy fails closed: the dialer is refused with an
// error rather than falling back to direct, because a silent direct dial
// would quietly defeat exactly the egress policy the operator configured.
func (h *Handler) voiceProviderDialer() (*websocket.Dialer, error) {
	dialer := &websocket.Dialer{
		HandshakeTimeout: voiceProviderDialTimeout,
	}
	proxyURL, ok := h.voiceProviderProxyURL()
	if !ok {
		if strings.TrimSpace(h.VoiceProviderProxyURL) == "" {
			return dialer, nil
		}
		return nil, errVoiceProxyUnusable
	}
	dialer.Proxy = http.ProxyURL(proxyURL)
	return dialer, nil
}

// errVoiceProxyUnusable marks a configured-but-unusable proxy value; the
// gateway fails closed on it instead of silently dialing around the
// operator's egress policy.
var errVoiceProxyUnusable = errorString("MULTICA_GEMINI_PROXY_URL is set but unusable (want socks5:// or http(s)://)")

// voiceProbeHTTPClient resolves the HTTP client for the models.list
// credential probe. Precedence: an injected test client wins; otherwise a
// configured proxy shapes the transport so the probe answers the same
// question the session dial will ("can THIS server reach Google through THIS
// proxy"); otherwise the plain default. An unusable configured proxy yields a
// failing transport — the probe reports unreachable rather than measuring an
// egress path the operator turned off.
func (h *Handler) voiceProbeHTTPClient() *http.Client {
	if h.VoiceProbeHTTPClient != nil {
		return h.VoiceProbeHTTPClient
	}
	proxyURL, ok := h.voiceProviderProxyURL()
	if !ok {
		if strings.TrimSpace(h.VoiceProviderProxyURL) != "" {
			return &http.Client{Timeout: voiceProbeTimeout, Transport: failingRoundTripper{}}
		}
		return &http.Client{Timeout: voiceProbeTimeout}
	}
	return &http.Client{Timeout: voiceProbeTimeout, Transport: voiceProxyTransport(proxyURL)}
}

// failingRoundTripper fails every request: the transport-shaped answer to a
// configured-but-unusable proxy, so callers see the honest "unreachable"
// instead of a direct-dial result the policy forbids.
type failingRoundTripper struct{}

func (failingRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errVoiceProxyUnusable
}

// voiceProxyTransport builds an HTTP transport that routes through the given
// proxy endpoint. HTTP(S) proxies use the standard Proxy hook; SOCKS5 dials
// the tunnel directly (x/net/proxy), because net/http's Proxy hook does not
// speak SOCKS5.
func voiceProxyTransport(proxyURL *url.URL) http.RoundTripper {
	switch strings.ToLower(proxyURL.Scheme) {
	case "socks5", "socks5h":
		dialer, err := proxy.FromURL(proxyURL, proxy.Direct)
		if err != nil {
			// Unreachable for values voiceProviderProxyURL accepted; fall
			// back to a plain transport rather than crash the probe path.
			slog.Error("voice proxy dialer build failed", "error", err)
			return http.DefaultTransport
		}
		contextDialer, ok := dialer.(proxy.ContextDialer)
		if !ok {
			return http.DefaultTransport
		}
		return &http.Transport{
			DialContext: contextDialer.DialContext,
		}
	default:
		// http/https CONNECT proxy.
		return &http.Transport{
			Proxy: http.ProxyURL(proxyURL),
			DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, network, address)
			},
		}
	}
}
