package legislation

// Unit tests for the jev advisory client (RUYI-347 batch A): the warn-only
// soft-judgment sidecar. The client must never fail its caller — every
// fault degrades to an unavailable AdvisoryReport with a reason — and a
// successful judgment must carry the service's true probability, threshold
// and warn verdict verbatim.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func stubJev(t *testing.T, pFailure float64, status int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/binary/judge" {
			http.NotFound(w, r)
			return
		}
		if status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		th := 0.3
		body := map[string]any{
			"task":             "binary_success_prediction",
			"p_success":        1 - pFailure,
			"p_failure":        pFailure,
			"threshold":        th,
			"warn":             pFailure >= th,
			"decision":         map[bool]string{true: "warn", false: "pass"}[pFailure >= th],
			"engine":           "kev_local",
			"threshold_source": "default",
		}
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestAdvisoryAssessWarnSemantics(t *testing.T) {
	// 高概率样本：warn=true 且概率/阈值逐字段透传。
	srv := stubJev(t, 0.62, http.StatusOK)
	c := &AdvisoryClient{BaseURL: srv.URL, Timeout: time.Second}
	rep := c.Assess(context.Background(), "submit", "高风险提案文本")
	if !rep.Available || !rep.Warn {
		t.Fatalf("expected available warn report, got %+v", rep)
	}
	if rep.PFailure != 0.62 || rep.Threshold != 0.3 || rep.Engine != "kev_local" {
		t.Fatalf("probability fields not passed through: %+v", rep)
	}
	if rep.Stage != "submit" || rep.Decision != "warn" {
		t.Fatalf("stage/decision mismatch: %+v", rep)
	}
	if len(rep.InputSHA256) != 64 {
		t.Fatalf("input_sha256 must be a sha256 hex, got %q", rep.InputSHA256)
	}

	// 低概率样本：warn=false。
	srv2 := stubJev(t, 0.05, http.StatusOK)
	c2 := &AdvisoryClient{BaseURL: srv2.URL, Timeout: time.Second}
	rep2 := c2.Assess(context.Background(), "gate", "合格样本")
	if !rep2.Available || rep2.Warn || rep2.Decision != "pass" {
		t.Fatalf("expected available pass report, got %+v", rep2)
	}
}

func TestAdvisoryAssessDisabled(t *testing.T) {
	c := &AdvisoryClient{BaseURL: "", Timeout: time.Second}
	rep := c.Assess(context.Background(), "gate", "文本")
	if rep.Available || rep.SkippedReason != "disabled" {
		t.Fatalf("empty BaseURL must degrade to disabled, got %+v", rep)
	}
	if rep.Stage != "gate" || len(rep.InputSHA256) != 64 || rep.CheckedAt == "" {
		t.Fatalf("degraded report must still carry stage/sha/checked_at: %+v", rep)
	}

	// nil 客户端同样降级（不 panic）。
	var nilClient *AdvisoryClient
	rep2 := nilClient.Assess(context.Background(), "submit", "文本")
	if rep2.Available || rep2.SkippedReason != "disabled" {
		t.Fatalf("nil client must degrade to disabled, got %+v", rep2)
	}
}

func TestAdvisoryAssessUnreachable(t *testing.T) {
	// 关闭的端口：连接拒绝必须毫秒级降级，绝不向调用方抛错。
	srv := stubJev(t, 0.9, http.StatusOK)
	url := srv.URL
	srv.Close() // 现在地址已死
	c := &AdvisoryClient{BaseURL: url, Timeout: 2 * time.Second}

	start := time.Now()
	rep := c.Assess(context.Background(), "gate", "文本")
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("connection-refused degrade took %v, want near-instant", elapsed)
	}
	if rep.Available || !strings.Contains(rep.SkippedReason, "unreachable") {
		t.Fatalf("dead endpoint must degrade to unreachable, got %+v", rep)
	}
}

func TestAdvisoryAssessHTTPError(t *testing.T) {
	srv := stubJev(t, 0.9, http.StatusInternalServerError)
	c := &AdvisoryClient{BaseURL: srv.URL, Timeout: time.Second}
	rep := c.Assess(context.Background(), "gate", "文本")
	if rep.Available || !strings.Contains(rep.SkippedReason, "unreachable") {
		t.Fatalf("5xx must degrade to unavailable, got %+v", rep)
	}
}

func TestAdvisoryAssessTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		_ = json.NewEncoder(w).Encode(map[string]any{})
	}))
	t.Cleanup(srv.Close)
	c := &AdvisoryClient{BaseURL: srv.URL, Timeout: 50 * time.Millisecond}
	rep := c.Assess(context.Background(), "gate", "文本")
	if rep.Available || !strings.Contains(rep.SkippedReason, "unreachable") {
		t.Fatalf("timeout must degrade to unavailable, got %+v", rep)
	}
}
