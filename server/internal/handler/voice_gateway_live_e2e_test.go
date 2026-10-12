package handler

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// RUYI-692 real-network E2E for the D1 provider dial path. Opt-in only:
// MULTICA_VOICE_LIVE_E2E=1 plus MULTICA_VOICE_LIVE_E2E_KEY gate the real
// Gemini Live contact, so CI and every default `go test` run stay hermetic.
// The credential rides the environment into the x-goog-api-key header of the
// production dial and nowhere else — it is never logged, never marshalled
// into a frame, never asserted on.
//
// The three scenarios triangulate the egress claim ("the dial rode the
// configured proxy, not direct"): an unusable proxy fails closed, a direct
// dial observes this host's actual Google egress (expected dead here, and
// must never ride the environment's proxy variables), and the configured
// proxy completes a full conversation round — which is only explicable as
// proxy egress when the direct leg is dead.

const (
	// liveE2EInstructions keeps the session identity minimal; the round's
	// answer is deterministic so the transcript is checkable by eye.
	liveE2EInstructions = "你是语音数学助手。收到算术后只回答结果本身，不要解释。"
	liveE2EQuestion     = "13加29等于多少？"
	// liveE2EReadTimeout bounds one provider→gateway frame wait; Gemini Live
	// answers a setup and a short turn well inside it on a working link.
	liveE2EReadTimeout = 45 * time.Second
)

func liveE2EEnabled(t *testing.T) (proxy, model, key string) {
	t.Helper()
	if os.Getenv("MULTICA_VOICE_LIVE_E2E") != "1" {
		t.Skip("MULTICA_VOICE_LIVE_E2E!=1: real-network Gemini Live E2E stays skipped")
	}
	key = strings.TrimSpace(os.Getenv("MULTICA_VOICE_LIVE_E2E_KEY"))
	if key == "" {
		t.Skip("MULTICA_VOICE_LIVE_E2E_KEY empty: no credential to dial with")
	}
	proxy = strings.TrimSpace(os.Getenv("MULTICA_GEMINI_PROXY_URL"))
	model = strings.TrimSpace(os.Getenv("MULTICA_VOICE_LIVE_E2E_MODEL"))
	if model == "" {
		model = defaultVoiceModel
	}
	return proxy, model, key
}

// liveE2EReadFrame reads one JSON frame under a fresh deadline and decodes
// it as the provider-frame subset the assertions need.
func liveE2EReadFrame(t *testing.T, conn *websocket.Conn, timeout time.Duration) map[string]json.RawMessage {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	_, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read provider frame: %v", err)
	}
	var frame map[string]json.RawMessage
	if err := json.Unmarshal(data, &frame); err != nil {
		t.Fatalf("provider frame is not JSON: %v (%.80s)", err, data)
	}
	return frame
}

// liveE2EDial runs the production dial and returns the connection, timing it.
func liveE2EDial(t *testing.T, h *Handler, key string) (*websocket.Conn, time.Duration) {
	t.Helper()
	start := time.Now()
	conn, resp, err := h.dialVoiceProvider(context.Background(), DefaultVoiceProviderWSBaseURL, key)
	elapsed := time.Since(start)
	if err != nil {
		if resp != nil {
			t.Fatalf("provider dial failed after %s: status %d: %v", elapsed, resp.StatusCode, err)
		}
		t.Fatalf("provider dial failed after %s: %v", elapsed, err)
	}
	return conn, elapsed
}

// TestVoiceGatewayLiveProviderE2E exercises the real Gemini Live endpoint
// through the exact production dial/frame code (dialVoiceProvider +
// composeVoiceSetupFrame + the gateway's clientContent turn shape).
func TestVoiceGatewayLiveProviderE2E(t *testing.T) {
	proxy, model, key := liveE2EEnabled(t)

	// Scenario 1: an unusable configured proxy fails closed — the dialer
	// must not silently fall back to direct (D1's egress-policy contract).
	t.Run("unusable proxy fails closed", func(t *testing.T) {
		h := &Handler{VoiceProviderProxyURL: "http://127.0.0.1:1"}
		conn, resp, err := h.dialVoiceProvider(context.Background(), DefaultVoiceProviderWSBaseURL, key)
		if err == nil {
			conn.Close()
			t.Fatal("dial through a dead local proxy succeeded; fail-closed contract broken")
		}
		if resp != nil && resp.Body != nil {
			resp.Body.Close()
		}
		t.Logf("dead-proxy dial refused as expected: %v", err)
	})

	// Scenario 2: a direct dial must neither inherit the environment's
	// proxy variables nor (on a no-egress host) succeed. Inheriting the
	// bogus env proxies would surface as a proxyconnect error — that is the
	// live-level assertion of D1's "never inherit" guarantee; a pure direct
	// failure is this environment's expected answer and is what makes the
	// proxied scenario meaningful.
	t.Run("direct dial does not inherit environment proxies", func(t *testing.T) {
		t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
		t.Setenv("https_proxy", "http://127.0.0.1:1")
		t.Setenv("WSS_PROXY", "http://127.0.0.1:1")
		h := &Handler{}
		start := time.Now()
		conn, resp, err := h.dialVoiceProvider(context.Background(), DefaultVoiceProviderWSBaseURL, key)
		elapsed := time.Since(start)
		if err == nil {
			conn.Close()
			t.Fatalf("direct dial succeeded in %s: this host has Google egress, so the proxied scenario no longer proves egress routing — re-evaluate the evidence chain", elapsed)
		}
		if strings.Contains(err.Error(), "proxyconnect") {
			t.Fatalf("direct dial inherited the environment proxy variables: %v (D1 never-inherit contract broken)", err)
		}
		if resp != nil && resp.Body != nil {
			resp.Body.Close()
		}
		t.Logf("direct dial failed after %s as expected on a no-egress host: %v", elapsed, err)
	})

	// Scenario 3: the real round through the configured proxy — dial,
	// setup, setupComplete, one user turn, model response back.
	t.Run("configured proxy completes a conversation round", func(t *testing.T) {
		if proxy == "" {
			t.Skip("MULTICA_GEMINI_PROXY_URL empty: nothing to prove about proxy egress")
		}
		h := &Handler{VoiceProviderProxyURL: proxy}
		conn, handshake := liveE2EDial(t, h, key)
		defer conn.Close()
		t.Logf("handshake through the configured proxy completed in %s", handshake)

		if err := conn.WriteJSON(composeVoiceSetupFrame(liveE2EInstructions, model, nil)); err != nil {
			t.Fatalf("setup write failed: %v", err)
		}

		// setupComplete closes the handshake+setup leg.
		setupStart := time.Now()
		for {
			frame := liveE2EReadFrame(t, conn, liveE2EReadTimeout)
			if _, ok := frame["setupComplete"]; ok {
				break
			}
			if errMsg, ok := frame["error"]; ok {
				t.Fatalf("provider rejected setup: %s", string(errMsg))
			}
		}
		t.Logf("setupComplete after %s (model %s)", time.Since(setupStart), model)

		// One conversation round: a user text turn in, the model's answer
		// back. Same clientContent shape the gateway injects with.
		turn, err := json.Marshal(map[string]any{
			"clientContent": map[string]any{
				"turns": []map[string]any{{
					"role":  "user",
					"parts": []map[string]any{{"text": liveE2EQuestion}},
				}},
				"turnComplete": true,
			},
		})
		if err != nil {
			t.Fatalf("marshal clientContent: %v", err)
		}
		turnStart := time.Now()
		if err := conn.WriteMessage(websocket.TextMessage, turn); err != nil {
			t.Fatalf("clientContent write failed: %v", err)
		}

		var (
			reply          strings.Builder
			frames         int
			audioChars     int
			firstReplyAt   time.Duration
			turnCompleteAt time.Duration
			done           bool
		)
		for !done {
			frame := liveE2EReadFrame(t, conn, liveE2EReadTimeout)
			frames++
			if errMsg, ok := frame["error"]; ok {
				t.Fatalf("provider error mid-turn: %s", string(errMsg))
			}
			raw, ok := frame["serverContent"]
			if !ok {
				continue
			}
			var sc struct {
				ModelTurn *struct {
					Parts []struct {
						Text       string `json:"text"`
						InlineData *struct {
							MimeType string `json:"mimeType"`
							Data     string `json:"data"`
						} `json:"inlineData"`
					} `json:"parts"`
				} `json:"modelTurn"`
				OutputTranscription *struct {
					Text string `json:"text"`
				} `json:"outputTranscription"`
				TurnComplete bool `json:"turnComplete"`
			}
			if json.Unmarshal(raw, &sc) != nil {
				continue
			}
			if sc.ModelTurn != nil {
				for _, part := range sc.ModelTurn.Parts {
					if part.Text != "" {
						reply.WriteString(part.Text)
					}
					if part.InlineData != nil && strings.HasPrefix(part.InlineData.MimeType, "audio/") {
						audioChars += len(part.InlineData.Data)
					}
				}
			}
			if sc.OutputTranscription != nil && sc.OutputTranscription.Text != "" {
				reply.WriteString(sc.OutputTranscription.Text)
			}
			if reply.Len() > 0 && firstReplyAt == 0 {
				firstReplyAt = time.Since(turnStart)
			}
			if sc.TurnComplete {
				turnCompleteAt = time.Since(turnStart)
				done = true
			}
		}

		answer := strings.TrimSpace(reply.String())
		if answer == "" && audioChars == 0 {
			t.Fatalf("turn completed (%s) but no model response text or audio came back across %d frames", turnCompleteAt, frames)
		}
		t.Logf("conversation round closed in %s: %d frames, first response at %s, audio payload %d base64 chars",
			turnCompleteAt, frames, firstReplyAt, audioChars)
		t.Logf("model reply (transcript): %q", answer)
		if answer != "" && !strings.Contains(answer, "42") && !strings.Contains(answer, "四十二") {
			t.Logf("note: reply did not literally contain 42/四十二 — model phrasing varies; the transcript above is the evidence")
		}
	})
}
