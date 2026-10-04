package lark

import (
	"context"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/integrations/channel/engine"
)

// Media-resolver-side wiring of the runtime permission hint: a permission-
// denied resource download (im:resource scope missing) sends the
// media_resources hint card into the same chat, deduped until a later
// download succeeds (recovery re-arms the notification).

func newHintMediaResolver(sender *fakeSender) engine.MediaResolver {
	return NewFeishuMediaResolver(sender, fakeCreds{secret: "plain"}, &fakeMediaStorage{}, &fakeMediaLedger{}, newDiscardLogger(), newHintSenderForTest(sender))
}

func hintImageMsg() InboundMessage {
	return InboundMessage{
		MessageID:   "om_i",
		MessageType: "image",
		Body:        "[Image]",
		Content:     `{"image_key":"img_k"}`,
		ChatID:      "oc_media",
	}
}

func hintResolveMedia(t *testing.T, resolver engine.MediaResolver, chatMessageID string) {
	t.Helper()
	resolver.ResolveMedia(context.Background(), testMediaInstallation(t), engine.ResolvedIdentity{},
		uuidFromString(t, "22222222-2222-2222-2222-222222222222"), uuidFromString(t, chatMessageID),
		channelMessageFromLark(hintImageMsg()))
}

func TestFeishuMediaResolverDownloadDeniedSendsHintCard(t *testing.T) {
	sender := &fakeSender{downloadErrByKey: map[string]error{"img_k": hintDeniedErr()}}
	resolver := newHintMediaResolver(sender)

	hintResolveMedia(t, resolver, "33333333-3333-4333-8333-333333333333")

	if len(sender.cardSends) != 1 {
		t.Fatalf("permission-denied download must send one hint card, got %d", len(sender.cardSends))
	}
	if got := sender.cardSends[0].ChatID; got != "oc_media" {
		t.Fatalf("hint card must target the message's chat, got %q", got)
	}
	if !strings.Contains(sender.cardSends[0].CardJSON, "im:resource") {
		t.Fatalf("media card must carry the media_resources catalog scopes: %s", sender.cardSends[0].CardJSON)
	}
}

func TestFeishuMediaResolverDownloadDeniedDedupsUntilRecovery(t *testing.T) {
	sender := &fakeSender{downloadErrByKey: map[string]error{"img_k": hintDeniedErr()}}
	resolver := newHintMediaResolver(sender)

	hintResolveMedia(t, resolver, "33333333-3333-4333-8333-333333333331")
	hintResolveMedia(t, resolver, "33333333-3333-4333-8333-333333333332")
	if len(sender.cardSends) != 1 {
		t.Fatalf("repeat denials must dedup to one card, got %d", len(sender.cardSends))
	}

	delete(sender.downloadErrByKey, "img_k") // grant happened; download recovers
	hintResolveMedia(t, resolver, "33333333-3333-4333-8333-333333333333")

	sender.downloadErrByKey["img_k"] = hintDeniedErr() // revoked again
	hintResolveMedia(t, resolver, "33333333-3333-4333-8333-333333333334")

	if len(sender.cardSends) != 2 {
		t.Fatalf("denied → success → denied must send 2 cards, got %d", len(sender.cardSends))
	}
}

func TestFeishuMediaResolverNilHintsSendsNoCard(t *testing.T) {
	sender := &fakeSender{downloadErrByKey: map[string]error{"img_k": hintDeniedErr()}}
	resolver := NewFeishuMediaResolver(sender, fakeCreds{secret: "plain"}, &fakeMediaStorage{}, &fakeMediaLedger{}, newDiscardLogger(), nil)

	hintResolveMedia(t, resolver, "33333333-3333-4333-8333-333333333333")

	if len(sender.cardSends) != 0 {
		t.Fatalf("nil hints must disable the feature, got %d sends", len(sender.cardSends))
	}
}
