package lark

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// hintTestCreds builds credentials whose AppSecret must NEVER appear in a
// rendered hint card — the tests assert that explicitly.
func hintTestCreds() InstallationCredentials {
	return InstallationCredentials{
		AppID:     "cli_test_app",
		AppSecret: "super-secret-do-not-leak",
		TenantKey: "tk_1",
	}
}

// hintDeniedErr is the canonical Lark no-permission business error.
func hintDeniedErr() error {
	return &APIError{Op: "probe", Code: 99991672, Msg: "no permission"}
}

// newHintSenderForTest builds a sender whose sends run inline so tests are
// deterministic (production sends asynchronously).
func newHintSenderForTest(client APIClient) *PermissionHintSender {
	s := NewPermissionHintSender(client, newDiscardLogger())
	s.goSend = func(f func()) { f() }
	return s
}

func TestPermissionHintCardJSONFeishu(t *testing.T) {
	creds := hintTestCreds()
	raw, err := permissionHintCardJSON(creds, CapabilityReadHistory)
	if err != nil {
		t.Fatalf("permissionHintCardJSON: %v", err)
	}
	if strings.Contains(raw, creds.AppSecret) {
		t.Fatalf("card leaks the app secret: %s", raw)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatalf("card is not valid JSON: %v\n%s", err, raw)
	}
	for _, scope := range ScopesForCapability(CapabilityReadHistory) {
		if !strings.Contains(raw, scope) {
			t.Fatalf("card missing catalog scope %s (scopes must come from the catalog, not be hardcoded)", scope)
		}
	}
	if !strings.Contains(raw, "/app/cli_test_app/auth") {
		t.Fatalf("card missing the dev-console authorization deep link: %s", raw)
	}
}

func TestPermissionHintCardJSONLarkRegionHost(t *testing.T) {
	creds := hintTestCreds()
	creds.Region = RegionLark
	raw, err := permissionHintCardJSON(creds, CapabilityContactLookup)
	if err != nil {
		t.Fatalf("permissionHintCardJSON: %v", err)
	}
	if !strings.Contains(raw, "https://open.larksuite.com/app/cli_test_app/auth") {
		t.Fatalf("lark-region card must deep-link the larksuite host: %s", raw)
	}
}

func TestPermissionHintCardJSONUnknownCapability(t *testing.T) {
	if _, err := permissionHintCardJSON(hintTestCreds(), CapabilityID("bogus")); err == nil {
		t.Fatalf("unknown capability must error, not render an empty card")
	}
}

func TestPermissionHintDeduperClaimRelease(t *testing.T) {
	var d permissionHintDeduper
	key := permissionHintKey{appID: "cli", chatID: "oc_1", capability: CapabilityReadHistory}
	if !d.claim(key) {
		t.Fatalf("first claim must win")
	}
	if d.claim(key) {
		t.Fatalf("second claim while held must lose")
	}
	d.release(key)
	if !d.claim(key) {
		t.Fatalf("claim after release must win again")
	}
}

func TestPermissionHintDeduperBounded(t *testing.T) {
	var d permissionHintDeduper
	for i := 0; i < permissionHintMaxTrackedKeys+16; i++ {
		d.claim(permissionHintKey{
			appID:      "cli",
			chatID:     ChatID(fmt.Sprintf("oc_%d", i)),
			capability: CapabilityReadHistory,
		})
	}
	d.mu.Lock()
	n := len(d.sent)
	d.mu.Unlock()
	if n > permissionHintMaxTrackedKeys {
		t.Fatalf("dedup map exceeded its bound: %d > %d", n, permissionHintMaxTrackedKeys)
	}
}

func TestPermissionHintSenderDeniedDedupsPerChatAndCapability(t *testing.T) {
	fake := newEnricherFake()
	s := newHintSenderForTest(fake)
	creds := hintTestCreds()
	ctx := context.Background()

	s.ObserveDenied(ctx, creds, "oc_a", CapabilityReadHistory, hintDeniedErr())
	s.ObserveDenied(ctx, creds, "oc_a", CapabilityReadHistory, hintDeniedErr())
	if len(fake.cardSends) != 1 {
		t.Fatalf("same chat+capability denied twice must send exactly one card, got %d", len(fake.cardSends))
	}
	if got := fake.cardSends[0].ChatID; got != "oc_a" {
		t.Fatalf("card chat = %q, want oc_a", got)
	}

	s.ObserveDenied(ctx, creds, "oc_b", CapabilityReadHistory, hintDeniedErr())
	if len(fake.cardSends) != 2 {
		t.Fatalf("a different chat must get its own card, got %d sends", len(fake.cardSends))
	}
	s.ObserveDenied(ctx, creds, "oc_a", CapabilityContactLookup, hintDeniedErr())
	if len(fake.cardSends) != 3 {
		t.Fatalf("a different capability must get its own card, got %d sends", len(fake.cardSends))
	}

	// Probe/runtime recovery (ObserveSuccess) clears the marker: the next
	// denial notifies again instead of staying silent forever.
	s.ObserveSuccess(creds.AppID, "oc_a", CapabilityReadHistory)
	s.ObserveDenied(ctx, creds, "oc_a", CapabilityReadHistory, hintDeniedErr())
	if len(fake.cardSends) != 4 {
		t.Fatalf("after recovery the next denial must send again, got %d sends", len(fake.cardSends))
	}
}

func TestPermissionHintSenderCardBodyIsCatalogSourced(t *testing.T) {
	fake := newEnricherFake()
	s := newHintSenderForTest(fake)
	s.ObserveDenied(context.Background(), hintTestCreds(), "oc_a", CapabilityReadHistory, hintDeniedErr())
	if len(fake.cardSends) != 1 {
		t.Fatalf("card sends = %d, want 1", len(fake.cardSends))
	}
	if !strings.Contains(fake.cardSends[0].CardJSON, "im:message.history:readonly") {
		t.Fatalf("card body must carry the catalog scopes: %s", fake.cardSends[0].CardJSON)
	}
}

func TestPermissionHintSenderIgnoresNonPermissionErrors(t *testing.T) {
	fake := newEnricherFake()
	s := newHintSenderForTest(fake)
	creds := hintTestCreds()
	ctx := context.Background()

	s.ObserveDenied(ctx, creds, "oc_a", CapabilityReadHistory, errors.New("context deadline exceeded"))
	s.ObserveDenied(ctx, creds, "oc_a", CapabilityReadHistory, &APIError{Op: "list", Code: 230020, Msg: "too many requests"})
	s.ObserveDenied(ctx, creds, "oc_a", CapabilityReadHistory, &APIError{Op: "list", Code: 230011, Msg: "deleted"})
	s.ObserveDenied(ctx, creds, "oc_a", CapabilityReadHistory, nil)
	if len(fake.cardSends) != 0 {
		t.Fatalf("non-permission failures must not send cards, got %d", len(fake.cardSends))
	}
}

func TestPermissionHintSenderSendFailureReleasesClaim(t *testing.T) {
	fake := newEnricherFake()
	fake.cardErrSeq = []error{errors.New("transport boom"), errors.New("transport boom"), nil}
	s := newHintSenderForTest(fake)
	creds := hintTestCreds()
	ctx := context.Background()

	// A failed card send must not wedge the dedup marker: each later
	// inbound message with the same denial retries the hint once, so a
	// transient send failure never permanently silences the guidance.
	for i := 0; i < 3; i++ {
		s.ObserveDenied(ctx, creds, "oc_a", CapabilityReadHistory, hintDeniedErr())
	}
	if len(fake.cardSends) != 3 {
		t.Fatalf("failed sends must release the claim for retry, got %d attempts", len(fake.cardSends))
	}
}

func TestPermissionHintSenderNilReceiverAndUnknownCapability(t *testing.T) {
	var s *PermissionHintSender
	s.ObserveDenied(context.Background(), hintTestCreds(), "oc_a", CapabilityReadHistory, hintDeniedErr())
	s.ObserveSuccess("cli", "oc_a", CapabilityReadHistory)

	fake := newEnricherFake()
	real := newHintSenderForTest(fake)
	real.ObserveDenied(context.Background(), hintTestCreds(), "oc_a", CapabilityID("bogus"), hintDeniedErr())
	if len(fake.cardSends) != 0 {
		t.Fatalf("unknown capability must not send, got %d", len(fake.cardSends))
	}
	real.ObserveDenied(context.Background(), hintTestCreds(), "oc_a", CapabilityReadHistory, hintDeniedErr())
	if len(fake.cardSends) != 1 {
		t.Fatalf("unknown capability must not wedge the deduper, sends = %d", len(fake.cardSends))
	}
}
