package handler

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/multica-ai/multica/server/internal/testutil"
)

// RUYI-692 D1: per-connection provider proxy, probe re-check (dial-failure
// badge, manual re-probe, timed refresh), and the projection layering
// (initial clientContent turn after setupComplete, reserved `tools` key,
// goAway consumption). Providers are always in-process stubs; no real Gemini
// endpoint is contacted and no real credential value appears.

// ---------------------------------------------------------------------------
// Proxy dialer selection (pure unit).
// ---------------------------------------------------------------------------

func TestVoiceProviderDialerProxySelection(t *testing.T) {
	t.Run("no config means direct dial and no environment inheritance", func(t *testing.T) {
		t.Setenv("HTTPS_PROXY", "http://127.0.0.1:9")
		t.Setenv("https_proxy", "http://127.0.0.1:9")
		h := &Handler{}
		dialer, err := h.voiceProviderDialer()
		if err != nil || dialer == nil {
			t.Fatalf("dialer = %v (%v), want a direct dialer", dialer, err)
		}
		if dialer.Proxy != nil {
			t.Fatalf("dialer.Proxy = non-nil, want nil (env vars must NOT be inherited)")
		}
	})

	t.Run("socks5 proxy rides the per-connection dialer", func(t *testing.T) {
		h := &Handler{VoiceProviderProxyURL: "socks5://127.0.0.1:21081"}
		dialer, err := h.voiceProviderDialer()
		if err != nil {
			t.Fatalf("dialer error: %v", err)
		}
		if dialer.Proxy == nil {
			t.Fatal("dialer.Proxy is nil, want the configured socks5 proxy")
		}
		got, err := dialer.Proxy(&http.Request{URL: mustParseURL(t, "wss://generativelanguage.googleapis.com/ws")})
		if err != nil || got.Host != "127.0.0.1:21081" {
			t.Fatalf("dialer.Proxy resolved %v (%v), want 127.0.0.1:21081", got, err)
		}
	})

	t.Run("http connect proxy accepted as the compatible form", func(t *testing.T) {
		for _, raw := range []string{"http://127.0.0.1:21081", "https://127.0.0.1:21081", "socks5h://127.0.0.1:21081"} {
			h := &Handler{VoiceProviderProxyURL: raw}
			if _, ok := h.voiceProviderProxyURL(); !ok {
				t.Fatalf("proxy %q rejected, want accepted", raw)
			}
		}
	})

	t.Run("unusable configured proxy fails closed, never silently dials direct", func(t *testing.T) {
		for _, raw := range []string{"ftp://127.0.0.1:21", "not-a-url", "http://", "127.0.0.1:21081"} {
			h := &Handler{VoiceProviderProxyURL: raw}
			if _, ok := h.voiceProviderProxyURL(); ok {
				t.Fatalf("proxy %q accepted, want rejected", raw)
			}
			dialer, err := h.voiceProviderDialer()
			if err == nil {
				t.Fatalf("proxy %q produced a dialer, want fail-closed error", raw)
			}
			if dialer != nil {
				t.Fatalf("proxy %q leaked a dialer past the configured egress policy", raw)
			}
		}
	})

	t.Run("probe client honours proxy and injected-client precedence", func(t *testing.T) {
		injected := &http.Client{Timeout: time.Second}
		h := &Handler{VoiceProbeHTTPClient: injected, VoiceProviderProxyURL: "http://127.0.0.1:21081"}
		if h.voiceProbeHTTPClient() != injected {
			t.Fatal("injected probe client must win over the proxy-shaped one")
		}
		h.VoiceProbeHTTPClient = nil
		client := h.voiceProbeHTTPClient()
		tr, ok := client.Transport.(*http.Transport)
		if !ok || tr.Proxy == nil {
			t.Fatal("proxy-configured probe client must carry a proxy transport")
		}
		req, _ := http.NewRequest(http.MethodGet, "https://generativelanguage.googleapis.com/v1beta/models", nil)
		got, err := tr.Proxy(req)
		if err != nil || got.Host != "127.0.0.1:21081" {
			t.Fatalf("probe transport proxy = %v (%v), want 127.0.0.1:21081", got, err)
		}
	})
}

// ---------------------------------------------------------------------------
// Dial failure records the connectivity badge (start 拨号失败即时置态).
// ---------------------------------------------------------------------------

func TestVoiceGateway_DialFailureRecordsProbeBadge(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	t.Run("connection refused records unreachable", func(t *testing.T) {
		credentialTestBox(t)
		stubProbeTarget(t, http.StatusOK)
		agentID, instanceID := seedUsableVoiceSetup(t, "Dial Fail Unreachable", "")

		dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		deadURL := dead.URL
		dead.Close() // close first: the address now refuses connections
		previous := testHandler.VoiceProviderWSBaseURL
		testHandler.VoiceProviderWSBaseURL = "ws" + strings.TrimPrefix(deadURL, "http")
		t.Cleanup(func() { testHandler.VoiceProviderWSBaseURL = previous })

		w := startVoiceSession(t, agentID)
		if w.Code != http.StatusBadGateway {
			t.Fatalf("start = %d %s, want 502 VOICE_PROVIDER_UNREACHABLE", w.Code, w.Body.String())
		}
		assertProbeBadge(t, instanceID, "unreachable")
	})

	t.Run("provider 401 at handshake records invalid", func(t *testing.T) {
		credentialTestBox(t)
		stubProbeTarget(t, http.StatusOK)
		agentID, instanceID := seedUsableVoiceSetup(t, "Dial Fail Invalid", "")

		rejector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		}))
		defer rejector.Close()
		previous := testHandler.VoiceProviderWSBaseURL
		testHandler.VoiceProviderWSBaseURL = "ws" + strings.TrimPrefix(rejector.URL, "http")
		t.Cleanup(func() { testHandler.VoiceProviderWSBaseURL = previous })

		w := startVoiceSession(t, agentID)
		if w.Code != http.StatusBadGateway {
			t.Fatalf("start = %d %s, want 502 VOICE_PROVIDER_UNREACHABLE", w.Code, w.Body.String())
		}
		assertProbeBadge(t, instanceID, "invalid")
	})
}

func assertProbeBadge(t *testing.T, instanceID, want string) {
	t.Helper()
	var status string
	waitFor(t, 5*time.Second, func() bool {
		err := testPool.QueryRow(context.Background(),
			`SELECT metadata->'credential_probe'->>'status' FROM agent_runtime WHERE id = $1`, instanceID).Scan(&status)
		return err == nil && status == want
	})
}

func mustParseURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse %q: %v", raw, err)
	}
	return parsed
}

// ---------------------------------------------------------------------------
// Manual server-side re-probe replaces the RUYI-626 client report.
// ---------------------------------------------------------------------------

func TestRunCredentialProbe_ServerSideManualProbe(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	credentialTestBox(t)
	// seedUsableVoiceSetup's save-time probe rides its own 200 stub; force
	// the degraded badge directly, then prove the manual re-probe heals it.
	_, instanceID := seedUsableVoiceSetup(t, "Manual Re-probe", "")
	setProbeBadge(t, instanceID, "invalid", time.Minute)
	assertProbeBadge(t, instanceID, "invalid")

	// The operator (or the entry UI) re-runs the probe against a healthy
	// target; the legacy client-report body must be ignored, not obeyed.
	probeHits := 0
	probeSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		probeHits++
		w.WriteHeader(http.StatusOK)
	}))
	defer probeSrv.Close()
	previous := testHandler.VoiceProbeBaseURL
	testHandler.VoiceProbeBaseURL = probeSrv.URL
	t.Cleanup(func() { testHandler.VoiceProbeBaseURL = previous })

	w := httptest.NewRecorder()
	req := newRequest(http.MethodPost, "/api/runtimes/"+instanceID+"/credential-probe", map[string]any{"status": "ok"})
	req = withURLParams(req, "runtimeId", instanceID)
	testHandler.RunCredentialProbe(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("manual probe = %d %s, want 200", w.Code, w.Body.String())
	}
	var resp struct {
		Probe CredentialProbeResult `json:"probe"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode manual probe response: %v", err)
	}
	if resp.Probe.Status != "ok" {
		t.Fatalf("manual probe status = %q, want ok", resp.Probe.Status)
	}
	if probeHits != 1 {
		t.Fatalf("probe target hit %d times, want exactly 1 server-run probe", probeHits)
	}
	assertProbeBadge(t, instanceID, "ok")
}

// ---------------------------------------------------------------------------
// Timed re-probe: degraded badges self-heal, fresh-ok badges are left alone,
// stale-ok badges refresh, non-voice instances are never touched.
// ---------------------------------------------------------------------------

func setProbeBadge(t *testing.T, instanceID, status string, age time.Duration) {
	t.Helper()
	checkedAt := time.Now().UTC().Add(-age).Format(time.RFC3339)
	badge, err := json.Marshal(map[string]string{"status": status, "checked_at": checkedAt})
	if err != nil {
		t.Fatalf("marshal badge: %v", err)
	}
	if _, err := testPool.Exec(context.Background(),
		`UPDATE agent_runtime SET metadata = jsonb_set(COALESCE(metadata,'{}'::jsonb), '{credential_probe}', $1::jsonb) WHERE id = $2`,
		string(badge), instanceID); err != nil {
		t.Fatalf("set probe badge: %v", err)
	}
}

func TestVoiceProbeRefresh_HealsDegradedAndStaleBadges(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	// One box for the whole test: every credential below is sealed under it,
	// so the refresh sweep can decrypt them all. (seedUsableVoiceSetup would
	// install a fresh box per instance and strand the earlier ciphertexts.)
	credentialTestBox(t)

	probeHits := 0
	probeSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		probeHits++
		w.WriteHeader(http.StatusOK)
	}))
	defer probeSrv.Close()
	previous := testHandler.VoiceProbeBaseURL
	testHandler.VoiceProbeBaseURL = probeSrv.URL
	t.Cleanup(func() { testHandler.VoiceProbeBaseURL = previous })

	seedVoiceInstance := func(name, status string, age time.Duration) string {
		t.Helper()
		instanceID := insertOnlineVoiceInstanceFixture(t, name, "")
		if w := putRuntimeCredential(t, instanceID, "api_key", voiceTestAPIKey); w.Code != http.StatusOK {
			t.Fatalf("seed credential: %d %s", w.Code, w.Body.String())
		}
		setProbeBadge(t, instanceID, status, age)
		return instanceID
	}
	degraded := seedVoiceInstance("Refresh Degraded", "unreachable", time.Minute)
	staleOK := seedVoiceInstance("Refresh Stale", "ok", 2*time.Hour)
	freshOK := seedVoiceInstance("Refresh Fresh", "ok", time.Minute)

	// A non-voice instance carrying a credential must never be probed.
	nonVoiceID := dbfx.Runtime(t, "Refresh Non-Voice", testutil.Cols{
		"provider":            "claude_code",
		"registration_source": "manual",
		"credential_ref":      testutil.Raw("'ref-does-not-resolve:api_key'"),
	})

	hitsBefore := probeHits
	testHandler.RefreshVoiceProbeBadges(context.Background())

	if got := probeBadgeStatus(t, degraded); got != "ok" {
		t.Fatalf("degraded badge = %q, want healed to ok", got)
	}
	if got := probeBadgeStatus(t, staleOK); got != "ok" {
		t.Fatalf("stale-ok badge = %q, want refreshed ok", got)
	}
	if got := probeBadgeStatus(t, freshOK); got != "ok" {
		t.Fatalf("fresh-ok badge = %q, want untouched ok", got)
	}
	// degraded + stale-ok re-probed; fresh-ok skipped.
	if probeHits != hitsBefore+2 {
		t.Fatalf("probe hits = %d, want exactly %d new probes", probeHits, hitsBefore+2)
	}
	var nonVoiceBadge *string
	if err := testPool.QueryRow(context.Background(),
		`SELECT metadata->'credential_probe'->>'status' FROM agent_runtime WHERE id = $1`, nonVoiceID).Scan(&nonVoiceBadge); err != nil {
		t.Fatalf("read non-voice runtime: %v", err)
	}
	if nonVoiceBadge != nil {
		t.Fatalf("non-voice runtime badge = %v, want nil (never probed)", *nonVoiceBadge)
	}
}

func probeBadgeStatus(t *testing.T, instanceID string) string {
	t.Helper()
	var status string
	waitFor(t, 5*time.Second, func() bool {
		err := testPool.QueryRow(context.Background(),
			`SELECT metadata->'credential_probe'->>'status' FROM agent_runtime WHERE id = $1`, instanceID).Scan(&status)
		return err == nil && status != ""
	})
	return status
}

// ---------------------------------------------------------------------------
// Projection layering: the initial context turn rides clientContent after
// setupComplete; nothing to project means no turn; goAway is consumed.
// ---------------------------------------------------------------------------

func seedEndedVoiceSessionWithSummary(t *testing.T, agentID, instanceID string) {
	t.Helper()
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO live_session (workspace_id, agent_id, runtime_instance_id, user_id, model, mode, status,
		                              context_snapshot, transcript, summary, started_at, ended_at)
		VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, 'stubby-live-1', 'gateway', 'ended', '{}'::jsonb, '[]'::jsonb,
		        'D1-CONTEXT-MARKER: previously discussed the quarterly review agenda', now() - interval '1 hour', now() - interval '50 minutes')
	`, testWorkspaceID, agentID, instanceID, testUserID); err != nil {
		t.Fatalf("seed ended live session: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM live_session WHERE agent_id = $1 AND summary LIKE 'D1-CONTEXT-MARKER%'`, agentID)
	})
}

func TestVoiceGateway_InjectsInitialContextTurnAfterSetupComplete(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	provider := newStubVoiceProvider(t)
	agentID, instanceID := seedUsableVoiceSetup(t, "Context Injection", "")
	seedEndedVoiceSessionWithSummary(t, agentID, instanceID)
	srv := voiceGatewayServer(t)

	conn := dialVoiceSession(t, srv, agentID)
	defer conn.Close()

	// The client still sees setupComplete first — the injection goes upstream.
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, first, err := conn.ReadMessage()
	if err != nil || !strings.Contains(string(first), "setupComplete") {
		t.Fatalf("first downstream frame = %s (%v), want setupComplete", first, err)
	}

	// The provider received exactly one injected clientContent turn, after
	// the setup, carrying the continuity projection.
	waitFor(t, 5*time.Second, func() bool {
		provider.mu.Lock()
		defer provider.mu.Unlock()
		return len(provider.clientMsgs) >= 1
	})
	provider.mu.Lock()
	turns := make([]string, len(provider.clientMsgs))
	for i, m := range provider.clientMsgs {
		turns[i] = string(m)
	}
	provider.mu.Unlock()
	if len(turns) != 1 {
		t.Fatalf("provider received %d upstream frames, want exactly the 1 context turn", len(turns))
	}
	var frame struct {
		ClientContent *struct {
			Turns []struct {
				Role  string `json:"role"`
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"turns"`
			TurnComplete bool `json:"turnComplete"`
		} `json:"clientContent"`
	}
	if err := json.Unmarshal([]byte(turns[0]), &frame); err != nil || frame.ClientContent == nil {
		t.Fatalf("injected frame = %s, want a clientContent turn", turns[0])
	}
	turn := frame.ClientContent.Turns[0]
	if turn.Role != "user" || !frame.ClientContent.TurnComplete {
		t.Fatalf("injected turn = %+v, want role user and turnComplete", turn)
	}
	if !strings.Contains(turn.Parts[0].Text, "D1-CONTEXT-MARKER") || !strings.Contains(turn.Parts[0].Text, "Recent voice session") {
		t.Fatalf("injected turn text missing the continuity projection: %q", turn.Parts[0].Text)
	}
}

func TestVoiceGateway_NoContextTurnWhenNothingToProject(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	provider := newStubVoiceProvider(t)
	agentID, _ := seedUsableVoiceSetup(t, "No Context", "")
	srv := voiceGatewayServer(t)

	conn := dialVoiceSession(t, srv, agentID)
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, first, err := conn.ReadMessage(); err != nil || !strings.Contains(string(first), "setupComplete") {
		t.Fatalf("expected setupComplete, got %v (%v)", first, err)
	}

	// §3.6: the brief never invents content — no prior session, no turn.
	waitFor(t, 2*time.Second, func() bool { return true })
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if len(provider.clientMsgs) != 0 {
		t.Fatalf("provider received %d upstream frames, want 0 (nothing to project)", len(provider.clientMsgs))
	}
}

func TestVoiceGateway_GoAwayRelayedDownstream(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	provider := newStubVoiceProvider(t)
	agentID, _ := seedUsableVoiceSetup(t, "GoAway", "")
	srv := voiceGatewayServer(t)

	conn := dialVoiceSession(t, srv, agentID)
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, first, err := conn.ReadMessage(); err != nil || !strings.Contains(string(first), "setupComplete") {
		t.Fatalf("expected setupComplete, got %v (%v)", first, err)
	}

	stubConn := provider.gatewayConn(t)
	provider.sendDownstream(stubConn, map[string]any{"goAway": map[string]any{"timeLeft": "12s"}})
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, downstream, err := conn.ReadMessage()
	if err != nil || !strings.Contains(string(downstream), "goAway") {
		t.Fatalf("goAway frame = %s (%v), want it relayed to the client", downstream, err)
	}
}

// ---------------------------------------------------------------------------
// The configured proxy is the egress actually used (integration level).
// ---------------------------------------------------------------------------

// stubSocks5Proxy is a minimal SOCKS5 peer: completes the no-auth handshake,
// records every CONNECT target, then either refuses (reply code 0x05) or
// closes — enough to prove the dialer routed through it without any real
// upstream network.
type stubSocks5Proxy struct {
	mu      sync.Mutex
	targets []string
	ln      net.Listener
	refuse  bool
}

func newStubSocks5Proxy(t *testing.T) *stubSocks5Proxy {
	t.Helper()
	p := &stubSocks5Proxy{}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen socks5 stub: %v", err)
	}
	p.ln = ln
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go p.serve(conn)
		}
	}()
	return p
}

func (p *stubSocks5Proxy) serve(conn net.Conn) {
	defer conn.Close()
	header := make([]byte, 2)
	if _, err := io.ReadFull(conn, header); err != nil {
		return
	}
	methods := make([]byte, header[1])
	if _, err := io.ReadFull(conn, methods); err != nil {
		return
	}
	if _, err := conn.Write([]byte{0x05, 0x00}); err != nil { // no auth
		return
	}
	req := make([]byte, 4)
	if _, err := io.ReadFull(conn, req); err != nil {
		return
	}
	var host string
	switch req[3] {
	case 0x01: // IPv4
		raw := make([]byte, 4)
		if _, err := io.ReadFull(conn, raw); err != nil {
			return
		}
		host = net.IP(raw).String()
	case 0x03: // domain
		n := make([]byte, 1)
		if _, err := io.ReadFull(conn, n); err != nil {
			return
		}
		raw := make([]byte, n[0])
		if _, err := io.ReadFull(conn, raw); err != nil {
			return
		}
		host = string(raw)
	default:
		return
	}
	port := make([]byte, 2)
	if _, err := io.ReadFull(conn, port); err != nil {
		return
	}
	p.mu.Lock()
	p.targets = append(p.targets, net.JoinHostPort(host, strconv.Itoa(int(binary.BigEndian.Uint16(port)))))
	p.mu.Unlock()
	if p.refuse {
		conn.Write([]byte{0x05, 0x05, 0x00, 0x01, 0, 0, 0, 0, 0, 0}) // connection refused
		return
	}
	conn.Close()
}

func (p *stubSocks5Proxy) connectTargets() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.targets...)
}

func TestVoiceGateway_DialsThroughConfiguredProxy(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	t.Run("provider dial rides the configured socks5 proxy", func(t *testing.T) {
		credentialTestBox(t)
		stubProbeTarget(t, http.StatusOK)
		agentID, _ := seedUsableVoiceSetup(t, "Proxy Ride", "")
		proxy := newStubSocks5Proxy(t)
		proxy.refuse = true // upstream never answers; the CONNECT record is the assertion

		previousURL, previousProxy := testHandler.VoiceProviderWSBaseURL, testHandler.VoiceProviderProxyURL
		testHandler.VoiceProviderProxyURL = "socks5://" + proxy.ln.Addr().String()
		testHandler.VoiceProviderWSBaseURL = "wss://generativelanguage.googleapis.com"
		t.Cleanup(func() {
			testHandler.VoiceProviderWSBaseURL, testHandler.VoiceProviderProxyURL = previousURL, previousProxy
		})

		if w := startVoiceSession(t, agentID); w.Code != http.StatusBadGateway {
			t.Fatalf("start = %d %s, want 502 (proxy refused the CONNECT)", w.Code, w.Body.String())
		}
		var found bool
		waitFor(t, 5*time.Second, func() bool {
			for _, target := range proxy.connectTargets() {
				if target == "generativelanguage.googleapis.com:443" {
					found = true
				}
			}
			return found
		})
		if !found {
			t.Fatalf("socks5 stub saw CONNECT targets %v, want the Gemini host", proxy.connectTargets())
		}
	})

	t.Run("an unreachable proxy degrades without hanging and without killing the entry", func(t *testing.T) {
		credentialTestBox(t)
		stubProbeTarget(t, http.StatusOK)
		agentID, instanceID := seedUsableVoiceSetup(t, "Proxy Dead", "")

		previousURL, previousProxy := testHandler.VoiceProviderWSBaseURL, testHandler.VoiceProviderProxyURL
		// An RFC5737 black-hole address: connect hangs/fails, never answers.
		testHandler.VoiceProviderProxyURL = "socks5://192.0.2.1:1080"
		testHandler.VoiceProviderWSBaseURL = "wss://generativelanguage.googleapis.com"
		t.Cleanup(func() {
			testHandler.VoiceProviderWSBaseURL, testHandler.VoiceProviderProxyURL = previousURL, previousProxy
		})

		if w := startVoiceSession(t, agentID); w.Code != http.StatusBadGateway {
			t.Fatalf("start = %d %s, want 502 VOICE_PROVIDER_UNREACHABLE", w.Code, w.Body.String())
		}
		assertProbeBadge(t, instanceID, "unreachable")

		// The entry stays usable: unreachable is a network verdict, not a
		// credential verdict (Owner 10:48 ruling) — the next start is not
		// gated by the badge.
		if w := startVoiceSession(t, agentID); w.Code != http.StatusBadGateway {
			t.Fatalf("second start = %d %s, want 502 again (entry not gated by unreachable)", w.Code, w.Body.String())
		}
	})
}

// ---------------------------------------------------------------------------
// Direct-connect endpoints are gone (RUYI-626 废弃, D1 scope 2).
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// The auth envelope is gateway-plane traffic: never forwarded upstream.
// ---------------------------------------------------------------------------

// TestVoiceGateway_HeaderPathSwallowsClientAuthFrame pins the dual-channel
// tolerance rule (RUYI-694 返工): a client whose upgrade request already
// carried identity (session cookie or bearer header) may still open the
// relay with the realtime first-frame auth envelope — mobile's RN cookie
// jar sends both channels at once, and desktop/web token-mode sessions do
// the same. The envelope must be swallowed: Google closes the provider
// connection on unknown message types, which killed every such session in
// under a second, and forwarding it would leak the session token to the
// provider besides.
func TestVoiceGateway_HeaderPathSwallowsClientAuthFrame(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	provider := newStubVoiceProvider(t)
	agentID, _ := seedUsableVoiceSetup(t, "Auth Swallow", "")
	srv := voiceGatewayServer(t)

	// dialVoiceSession authenticates via the session cookie — the header path.
	conn := dialVoiceSession(t, srv, agentID)
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, first, err := conn.ReadMessage(); err != nil || !strings.Contains(string(first), "setupComplete") {
		t.Fatalf("expected setupComplete, got %s (%v)", first, err)
	}

	// The dual-channel client's auth envelope rides first, then a real turn.
	auth := `{"type":"auth","payload":{"token":"redundant-with-cookie"}}`
	if err := conn.WriteMessage(websocket.TextMessage, []byte(auth)); err != nil {
		t.Fatalf("write auth frame: %v", err)
	}
	turn := `{"clientContent":{"turns":[{"role":"user","parts":[{"text":"hello"}]}],"turnComplete":true}}`
	if err := conn.WriteMessage(websocket.TextMessage, []byte(turn)); err != nil {
		t.Fatalf("write clientContent frame: %v", err)
	}

	waitFor(t, 5*time.Second, func() bool {
		provider.mu.Lock()
		defer provider.mu.Unlock()
		return len(provider.clientMsgs) >= 1
	})
	provider.mu.Lock()
	upstream := make([]string, len(provider.clientMsgs))
	for i, m := range provider.clientMsgs {
		upstream[i] = string(m)
	}
	provider.mu.Unlock()
	if len(upstream) != 1 || !strings.Contains(upstream[0], "clientContent") {
		t.Fatalf("provider received %v, want exactly the 1 clientContent turn (auth envelope must be swallowed, never forwarded)", upstream)
	}

	// The relay stays live after the swallow: a provider frame still reaches
	// the client instead of the session dying the way the bug did.
	stubConn := provider.gatewayConn(t)
	provider.sendDownstream(stubConn, map[string]any{"goAway": map[string]any{"timeLeft": "60s"}})
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, downstream, err := conn.ReadMessage(); err != nil || !strings.Contains(string(downstream), "goAway") {
		t.Fatalf("relay dead after auth swallow: frame=%s (%v)", downstream, err)
	}
}

func TestIsClientAuthFrame(t *testing.T) {
	cases := []struct {
		name string
		data string
		want bool
	}{
		{"realtime auth envelope", `{"type":"auth","payload":{"token":"x"}}`, true},
		{"auth without payload", `{"type":"auth"}`, true},
		{"auth with extra keys", `{"type":"auth","payload":{},"v":2}`, true},
		{"setup frame", `{"setup":{"model":"m"}}`, false},
		{"clientContent turn", `{"clientContent":{"turns":[]},"turnComplete":true}`, false},
		{"realtimeInput", `{"realtimeInput":{"audioChunks":[]}}`, false},
		{"auth as nested key only", `{"realtimeInput":{"auth":true}}`, false},
		{"type not auth", `{"type":"ping"}`, false},
		{"non-JSON", `hello`, false},
		{"empty", ``, false},
	}
	for _, tc := range cases {
		if got := isClientAuthFrame([]byte(tc.data)); got != tc.want {
			t.Errorf("%s: isClientAuthFrame(%s) = %v, want %v", tc.name, tc.data, got, tc.want)
		}
	}
}

func TestComposeVoiceSetupFrame_ToolsReserved(t *testing.T) {
	advanced := map[string]json.RawMessage{
		"temperature": json.RawMessage(`0.7`),
		"tools":       json.RawMessage(`[{"functionDeclarations":[{"name":"shadow_tool"}]}]`),
	}
	setup := composeVoiceSetupFrame("instructions", "m", advanced, []map[string]any{voiceSkillFunctionDeclaration()})["setup"].(map[string]any)

	// RUYI-692 D1 rule still holds: a USER-supplied tools list from the
	// advanced JSON must never reach the provider — a shadow declaration
	// with no server-side executor would stall the session.
	var advancedInjected bool
	decls := []string{"load_skill"}
	for _, group := range setup["tools"].([]map[string]any) {
		for _, raw := range group["functionDeclarations"].([]map[string]any) {
			if raw["name"] == "shadow_tool" {
				advancedInjected = true
			}
		}
	}
	if advancedInjected {
		t.Fatalf("advanced JSON injected a user tool declaration: %v", setup["tools"])
	}

	// RUYI-874 ③: the server COMPOSES the controlled bridge face itself, so
	// the setup carries exactly the registered bridge tool names.
	if len(decls) == 0 || !containsAllNames(setup, decls) {
		t.Fatalf("server-composed bridge tools missing from setup: %v (want %v)", setup["tools"], decls)
	}
	if raw, ok := setup["temperature"].(json.RawMessage); !ok || string(raw) != "0.7" {
		t.Fatalf("non-reserved advanced passthrough broken: %v", setup["temperature"])
	}
}

// containsAllNames reports whether every want name appears in the setup's
// composed functionDeclarations.
func containsAllNames(setup map[string]any, wants []string) bool {
	have := map[string]bool{}
	for _, group := range setup["tools"].([]map[string]any) {
		for _, decl := range group["functionDeclarations"].([]map[string]any) {
			name, _ := decl["name"].(string)
			have[name] = true
		}
	}
	for _, want := range wants {
		if !have[want] {
			return false
		}
	}
	return true
}
