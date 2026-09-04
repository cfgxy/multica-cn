package handler

import (
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// ── Path composition & masking ──────────────────────────────────────────────

func TestAgentWebhookPathForToken(t *testing.T) {
	got := agentWebhookPathForToken("awt_abc")
	if got != "/api/webhooks/agents/awt_abc" {
		t.Fatalf("path = %q, want /api/webhooks/agents/awt_abc", got)
	}
}

func TestAgentWebhookMaskSegmentIsFixedWidth(t *testing.T) {
	// The mask must be a constant independent of any token so a masked
	// display leaks nothing about credential length or shape.
	if webhookURLMaskSegment != "••••••••••••" {
		t.Fatalf("mask segment drifted: %q", webhookURLMaskSegment)
	}
	// Rune count, not len(): each bullet is 3 bytes in UTF-8 and the display
	// contract is 12 characters.
	if strings.Contains(webhookURLMaskSegment, "awt_") || utf8.RuneCountInString(webhookURLMaskSegment) != 12 {
		t.Fatalf("mask must stay 12 fixed characters: %q", webhookURLMaskSegment)
	}
}

// ── Response shaping ────────────────────────────────────────────────────────

func agentWebhookRowFixture() db.AgentWebhook {
	return db.AgentWebhook{
		ID:      parseUUID("00000000-0000-7000-8000-000000000001"),
		Token:   "awt_secrettoken",
		Name:    "push",
		Prompt:  "review",
		Enabled: true,
	}
}

func TestAgentWebhookToResponse_ManagerSeesCredential(t *testing.T) {
	h := &Handler{cfg: Config{PublicURL: "https://mica.example"}}
	resp := h.agentWebhookToResponse(agentWebhookRowFixture(), true)

	if resp.WebhookToken == nil || *resp.WebhookToken != "awt_secrettoken" {
		t.Fatalf("manager must receive the token, got %#v", resp.WebhookToken)
	}
	if resp.WebhookPath == nil || *resp.WebhookPath != "/api/webhooks/agents/awt_secrettoken" {
		t.Fatalf("manager path = %#v", resp.WebhookPath)
	}
	if resp.WebhookURL == nil || *resp.WebhookURL != "https://mica.example/api/webhooks/agents/awt_secrettoken" {
		t.Fatalf("manager url = %#v", resp.WebhookURL)
	}
}

func TestAgentWebhookToResponse_MaskedPathNeverCarriesToken(t *testing.T) {
	h := &Handler{}
	row := agentWebhookRowFixture()

	manager := h.agentWebhookToResponse(row, true)
	viewer := h.agentWebhookToResponse(row, false)

	for _, resp := range []AgentWebhookResponse{manager, viewer} {
		if strings.Contains(resp.WebhookPathMasked, "secrettoken") {
			t.Fatalf("masked path leaked the token: %q", resp.WebhookPathMasked)
		}
		if resp.WebhookPathMasked != "/api/webhooks/agents/"+webhookURLMaskSegment {
			t.Fatalf("masked path = %q", resp.WebhookPathMasked)
		}
	}
	if viewer.WebhookToken != nil || viewer.WebhookPath != nil || viewer.WebhookURL != nil {
		t.Fatalf("non-manager must receive no credential fields: %#v", viewer)
	}
	// Without MULTICA_PUBLIC_URL the absolute URL stays nil even for managers.
	if manager.WebhookURL != nil {
		t.Fatalf("no public URL configured: webhook_url must be nil, got %q", *manager.WebhookURL)
	}
}

// ── Field validation ────────────────────────────────────────────────────────

func TestValidateAgentWebhookFields_TrimsAndAccepts(t *testing.T) {
	w := httptest.NewRecorder()
	name, prompt, ok := validateAgentWebhookFields(w, "  push hook  ", "\n check commits \t")
	if !ok {
		t.Fatalf("valid input rejected: %s", w.Body.String())
	}
	if name != "push hook" || prompt != "check commits" {
		t.Fatalf("trim mismatch: name=%q prompt=%q", name, prompt)
	}
}

func TestValidateAgentWebhookFields_RejectsBlankAndOversize(t *testing.T) {
	cases := []struct {
		name    string
		rawName string
		prompt  string
	}{
		{"blank name", "   ", "p"},
		{"missing prompt", "n", "   "},
		{"name over 50 runes", strings.Repeat("名", 51), "p"},
		{"prompt over 4000 runes", "n", strings.Repeat("字", 4001)},
	}
	for _, tc := range cases {
		w := httptest.NewRecorder()
		if _, _, ok := validateAgentWebhookFields(w, tc.rawName, tc.prompt); ok {
			t.Fatalf("%s: expected rejection", tc.name)
		}
		if w.Code == 200 {
			t.Fatalf("%s: expected an error status, got %d", tc.name, w.Code)
		}
	}
}

func TestValidateAgentWebhookFields_AcceptsBoundaryAndMultibyte(t *testing.T) {
	w := httptest.NewRecorder()
	// 50 CJK runes = 150 bytes but exactly at the rune limit: length caps are
	// rune-based so the DB CHECK (char_length) and the handler agree.
	if _, _, ok := validateAgentWebhookFields(w, strings.Repeat("名", 50), strings.Repeat("字", 4000)); !ok {
		t.Fatalf("boundary input rejected: %s", w.Body.String())
	}
}
