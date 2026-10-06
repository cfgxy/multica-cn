package handler

// RUYI-425 阶段 6 收口：§4.5 删除引用检查必须覆盖 voice 槽位；在途会话遇
// 禁用开关必须优雅终止（关闭帧，而非错误切断）且终态 write-back 仍完成。
// Provider 全程 in-process stub，仅使用 fake key——不触真实 Gemini 端点。

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/multica-ai/multica/server/internal/testutil"
)

// §4.5 删除：仅被 voice 槽引用的 runtime 必须拒绝硬删除并返回引用 Agent
// 清单——只查 runtime_id（text 槽）会让 voice 引用绕过检查（要么 FK 层报
// 500 形状的错，要么被 teardown 静默解绑）。两条真实路径都要守：
//  1. profile 删除（§4.3 手动注册实例的常规删除入口）
//  2. 无 profile 的孤儿实例直删
func TestDeleteAgentRuntime_VoiceSlotReferenceRefuses(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	t.Run("profile_delete_path", func(t *testing.T) {
		profileID := insertRuntimeProfileFixture(t, ctx, "Voice Delete Guard Profile", "gemini_live", "")
		instanceID := dbfx.Runtime(t, "Voice Delete Guard", testutil.Cols{
			"provider":            "gemini_live",
			"registration_source": "manual",
			"profile_id":          profileID,
			"visibility":          "public",
		})
		agentID := insertVoiceBoundAgentFixture(t, "Voice Delete Guard Agent", instanceID)

		w := httptest.NewRecorder()
		req := newRequest("DELETE", "/api/workspaces/"+testWorkspaceID+"/runtime-profiles/"+profileID, nil)
		req = withURLParams(req, "id", testWorkspaceID, "profileId", profileID)
		testHandler.DeleteRuntimeProfile(w, req)

		if w.Code != http.StatusConflict {
			t.Fatalf("expected 409 for a voice-referenced profile, got %d: %s", w.Code, w.Body.String())
		}
		var body struct {
			Code         string          `json:"code"`
			ActiveAgents []AgentResponse `json:"active_agents"`
		}
		if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if body.Code != "runtime_has_voice_bindings" {
			t.Fatalf("code = %q, want runtime_has_voice_bindings", body.Code)
		}
		if len(body.ActiveAgents) != 1 || body.ActiveAgents[0].ID != agentID {
			t.Fatalf("expected the referencing agent %s in the response, got %+v", agentID, body.ActiveAgents)
		}

		var rows int
		if err := testPool.QueryRow(ctx, `SELECT count(*) FROM runtime_profile WHERE id = $1`, profileID).Scan(&rows); err != nil {
			t.Fatalf("count profile: %v", err)
		}
		if rows != 1 {
			t.Fatalf("the referenced profile must survive the refusal")
		}
	})

	t.Run("orphan_instance_delete_path", func(t *testing.T) {
		instanceID := dbfx.Runtime(t, "Voice Orphan Guard", testutil.Cols{
			"provider":            "gemini_live",
			"registration_source": "manual",
			"visibility":          "public",
		})
		agentID := insertVoiceBoundAgentFixture(t, "Voice Orphan Guard Agent", instanceID)

		w := httptest.NewRecorder()
		req := newRequest("DELETE", "/api/runtimes/"+instanceID, nil)
		req = withURLParam(req, "runtimeId", instanceID)
		testHandler.DeleteAgentRuntime(w, req)

		if w.Code != http.StatusConflict {
			t.Fatalf("expected 409 for a voice-referenced runtime, got %d: %s", w.Code, w.Body.String())
		}
		var body struct {
			Code         string          `json:"code"`
			ActiveAgents []AgentResponse `json:"active_agents"`
		}
		if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if body.Code != "runtime_has_voice_bindings" {
			t.Fatalf("code = %q, want runtime_has_voice_bindings", body.Code)
		}
		if len(body.ActiveAgents) != 1 || body.ActiveAgents[0].ID != agentID {
			t.Fatalf("expected the referencing agent %s in the response, got %+v", agentID, body.ActiveAgents)
		}

		var rows int
		if err := testPool.QueryRow(ctx, `SELECT count(*) FROM agent_runtime WHERE id = $1`, instanceID).Scan(&rows); err != nil {
			t.Fatalf("count runtime: %v", err)
		}
		if rows != 1 {
			t.Fatalf("the referenced runtime must survive the refusal")
		}
	})
}

// §4.5 禁用：会话进行中拨动 §4.3 开关，relay 必须优雅终止——两侧收到正常
// 关闭帧（1000），终态 write-back 携带已累积的转录完成；同时新会话被发起
// 闸门以 instance_disabled 拒绝。
func TestVoiceGateway_DisableMidSessionGracefulTermination(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	credentialTestBox(t)
	stubProbeTarget(t, http.StatusOK)

	provider := newStubVoiceProvider(t)
	agentID, instanceID := seedUsableVoiceSetup(t, "Disable Mid Session", "")
	srv := voiceGatewayServer(t)

	previous := testHandler.VoiceDisablePollInterval
	testHandler.VoiceDisablePollInterval = 20 * time.Millisecond
	t.Cleanup(func() { testHandler.VoiceDisablePollInterval = previous })

	conn := dialVoiceSession(t, srv, agentID)
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, first, err := conn.ReadMessage(); err != nil || !strings.Contains(string(first), "setupComplete") {
		t.Fatalf("expected setupComplete, got %v (%v)", first, err)
	}
	stubConn := provider.gatewayConn(t)
	provider.sendDownstream(stubConn, map[string]any{
		"serverContent": map[string]any{"inputTranscription": map[string]any{"text": "禁用前的话"}},
	})
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, frame, err := conn.ReadMessage(); err != nil || !strings.Contains(string(frame), "禁用前的话") {
		t.Fatalf("expected the relayed transcript frame, got %v (%v)", frame, err)
	}

	if _, err := testPool.Exec(context.Background(),
		`UPDATE agent_runtime SET metadata = COALESCE(metadata,'{}'::jsonb) || '{"disabled": true}'::jsonb WHERE id = $1`,
		instanceID,
	); err != nil {
		t.Fatalf("disable instance: %v", err)
	}

	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, _, err := conn.ReadMessage()
	if err == nil {
		t.Fatalf("expected the relay to close after the disable")
	}
	if cls, ok := err.(*websocket.CloseError); !ok || cls.Code != websocket.CloseNormalClosure {
		t.Fatalf("expected a normal close frame (1000), got %v", err)
	}

	var status, transcriptRaw string
	waitFor(t, 5*time.Second, func() bool {
		scanErr := testPool.QueryRow(context.Background(), `
			SELECT status::text, transcript::text FROM live_session WHERE agent_id = $1
		`, agentID).Scan(&status, &transcriptRaw)
		return scanErr == nil && status == "ended"
	})
	if !strings.Contains(transcriptRaw, "禁用前的话") {
		t.Fatalf("transcript lost on disable termination: %s", transcriptRaw)
	}

	expectVoiceUnavailable(t, "instance_disabled_after_termination", startVoiceSession(t, agentID), "instance_disabled")
}

// §4.5 失效→恢复：探针把凭据打成 credential_invalid 后新会话被拒（阶段 3
// 已覆盖的方向）；同一凭据在探针恢复 ok 后必须重新放行——拒绝态是探针事实
// 的快照，不是永久拉黑。补阶段 4 QA 未覆盖的恢复方向，正链走完整 relay
// 握手证明可发起会话。
func TestVoiceSessionGate_InvalidCredentialRecoversAfterProbeOK(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	credentialTestBox(t)
	stubProbeTarget(t, http.StatusUnauthorized)
	instanceID := insertOnlineVoiceInstanceFixture(t, "Recover After Invalid", "")
	agentID := insertVoiceBoundAgentFixture(t, "Recover After Invalid Agent", instanceID)
	if w := putRuntimeCredential(t, instanceID, "api_key", voiceTestAPIKey); w.Code != http.StatusOK {
		t.Fatalf("seed credential: %d %s", w.Code, w.Body.String())
	}
	expectVoiceUnavailable(t, "credential_invalid_before_recovery", startVoiceSession(t, agentID), "credential_invalid")

	// 用户重存密钥 → 凭据 PUT 同步重探 → ok。
	stubProbeTarget(t, http.StatusOK)
	if w := putRuntimeCredential(t, instanceID, "api_key", voiceTestAPIKey); w.Code != http.StatusOK {
		t.Fatalf("re-save credential: %d %s", w.Code, w.Body.String())
	}
	var status string
	waitFor(t, 5*time.Second, func() bool {
		scanErr := testPool.QueryRow(context.Background(),
			`SELECT metadata->'credential_probe'->>'status' FROM agent_runtime WHERE id = $1`, instanceID).Scan(&status)
		return scanErr == nil && status == "ok"
	})

	newStubVoiceProvider(t)
	srv := voiceGatewayServer(t)
	conn := dialVoiceSession(t, srv, agentID)
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, first, err := conn.ReadMessage(); err != nil || !strings.Contains(string(first), "setupComplete") {
		t.Fatalf("expected setupComplete after recovery, got %v (%v)", first, err)
	}
}
