package lark

import (
	"context"
	"strings"
	"testing"
)

// Enricher-side wiring of the runtime permission hint: when a real enrich
// call fails with a Lark permission business code, the matching capability's
// authorization hint card goes to the SAME chat (deduped by the sender);
// recovery (a later success) re-arms it. The enrich body degradation must
// stay byte-for-byte as before — the card is additive, never a replacement.

func newHintEnricher(fake *enricherFakeClient, recentSize int) Enricher {
	return NewInboundEnricher(fake, InboundEnricherConfig{
		RecentContextSize: recentSize,
		Logger:            newDiscardLogger(),
		Hints:             newHintSenderForTest(fake),
	})
}

func hintGroupMsg(id string) InboundMessage {
	return InboundMessage{
		EventID:        "ev_" + id,
		MessageID:      id,
		ChatID:         "oc_grp",
		ChatType:       ChatTypeGroup,
		AddressedToBot: true,
		SenderOpenID:   "ou_user",
		Body:           "帮我看看",
		CreateTime:     "1700000000000",
	}
}

func TestEnrichRecentContextDeniedSendsHintCard(t *testing.T) {
	fake := newEnricherFake()
	fake.errByChat["oc_grp"] = hintDeniedErr()
	e := newHintEnricher(fake, 5)

	got := e.Enrich(context.Background(), hintGroupMsg("om_1"), hintTestCreds())

	// The enrich degradation note is UNCHANGED by the hint feature (regression).
	if !strings.Contains(got.Body, "Recent Lark context unavailable") {
		t.Fatalf("degradation note must survive unchanged, body: %q", got.Body)
	}
	if len(fake.cardSends) != 1 {
		t.Fatalf("permission-denied recent fetch must send exactly one hint card, got %d", len(fake.cardSends))
	}
	if got := fake.cardSends[0].ChatID; got != "oc_grp" {
		t.Fatalf("hint card must target the triggering chat, got %q", got)
	}
}

func TestEnrichRecentContextSuccessClearsHintDedup(t *testing.T) {
	fake := newEnricherFake()
	fake.errSeqChat["oc_grp"] = []error{hintDeniedErr(), nil, hintDeniedErr()}
	e := newHintEnricher(fake, 5)

	e.Enrich(context.Background(), hintGroupMsg("om_1"), hintTestCreds())
	e.Enrich(context.Background(), hintGroupMsg("om_2"), hintTestCreds()) // success = recovery
	e.Enrich(context.Background(), hintGroupMsg("om_3"), hintTestCreds()) // revoked again

	if len(fake.cardSends) != 2 {
		t.Fatalf("denied → success → denied must send 2 cards (no repeat while missing, re-notify after recovery), got %d", len(fake.cardSends))
	}
}

func TestEnrichQuotedFetchDeniedSendsHintCard(t *testing.T) {
	fake := newEnricherFake()
	fake.errByID["om_parent"] = hintDeniedErr()
	// RecentContextSize 0 isolates the quote path from the recent window.
	e := newHintEnricher(fake, 0)

	msg := hintGroupMsg("om_child")
	msg.ParentID = "om_parent"
	got := e.Enrich(context.Background(), msg, hintTestCreds())

	if len(fake.cardSends) != 1 {
		t.Fatalf("permission-denied quote fetch must send one hint card, got %d", len(fake.cardSends))
	}
	if !strings.Contains(fake.cardSends[0].CardJSON, "im:message.group_msg") {
		t.Fatalf("quote-path card must carry the read_history catalog scopes: %s", fake.cardSends[0].CardJSON)
	}
	_ = got
}

func TestEnrichNameResolutionDeniedSendsHintCard(t *testing.T) {
	fake := newEnricherFake()
	fake.usersErr = hintDeniedErr()
	e := newHintEnricher(fake, 5)

	e.Enrich(context.Background(), hintGroupMsg("om_1"), hintTestCreds())

	if len(fake.cardSends) != 1 {
		t.Fatalf("permission-denied contact lookup must send one hint card, got %d", len(fake.cardSends))
	}
	if !strings.Contains(fake.cardSends[0].CardJSON, "contact:user.base:readonly") {
		t.Fatalf("contact-path card must carry the contact_lookup catalog scopes: %s", fake.cardSends[0].CardJSON)
	}
}

func TestEnrichTimeoutSendsNoHintCard(t *testing.T) {
	fake := newEnricherFake()
	fake.errByChat["oc_grp"] = errTimeoutStub{}
	e := newHintEnricher(fake, 5)

	got := e.Enrich(context.Background(), hintGroupMsg("om_1"), hintTestCreds())

	if len(fake.cardSends) != 0 {
		t.Fatalf("non-permission failure must not send hint cards, got %d", len(fake.cardSends))
	}
	if !strings.Contains(got.Body, "Recent Lark context temporarily unavailable") {
		t.Fatalf("timeout degradation note must survive, body: %q", got.Body)
	}
}

func TestEnrichNilHintsBehavesAsBefore(t *testing.T) {
	fake := newEnricherFake()
	fake.errByChat["oc_grp"] = hintDeniedErr()
	e := NewInboundEnricher(fake, InboundEnricherConfig{
		RecentContextSize: 5,
		Logger:            newDiscardLogger(),
	})

	got := e.Enrich(context.Background(), hintGroupMsg("om_1"), hintTestCreds())

	if len(fake.cardSends) != 0 {
		t.Fatalf("nil hints must disable the feature entirely, got %d sends", len(fake.cardSends))
	}
	if !strings.Contains(got.Body, "Recent Lark context unavailable") {
		t.Fatalf("degradation note must survive, body: %q", got.Body)
	}
}

// errTimeoutStub mimics a network timeout with no Lark business code.
type errTimeoutStub struct{}

func (errTimeoutStub) Error() string {
	return "Get \"https://open.feishu.cn\": context deadline exceeded"
}
