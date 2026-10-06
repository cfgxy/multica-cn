package agent

import (
	"log/slog"
	"strings"
	"testing"
)

// TestSupportedTypesLockstepWithNew guards the whitelist contract: every type
// in SupportedTypes must be recognized by IsSupportedType; CLI families must
// be constructable by New; API-backed voice families (RUYI-425) must be
// REFUSED by New with the voice-family error, because a task dispatched
// through the CLI factory onto a voice family would be the quiet way around
// the slot capability gates. New must still reject anything not in
// SupportedTypes. This is the single source of truth the custom runtime
// profile protocol_family validation (handler) and the runtime_profile
// protocol_family CHECK (migration 120 plus later tightening migrations) are aligned to. If a backend is added
// to New, it must be added here too — and to the migration CHECK.
func TestSupportedTypesLockstepWithNew(t *testing.T) {
	cfg := Config{Logger: slog.Default()}

	for _, typ := range SupportedTypes {
		if !IsSupportedType(typ) {
			t.Errorf("IsSupportedType(%q) = false, but it is in SupportedTypes", typ)
		}
		_, err := New(typ, cfg)
		if IsVoiceProtocolFamily(typ) {
			if err == nil {
				t.Errorf("New(%q) succeeded for an API-backed voice family; tasks must not be dispatchable onto one", typ)
			} else if !strings.Contains(err.Error(), "voice family") {
				t.Errorf("New(%q) error should name the voice-family refusal, got: %v", typ, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("New(%q) returned error for a SupportedTypes entry: %v", typ, err)
		}
	}

	// A type outside the whitelist must be rejected by both.
	const bogus = "definitely-not-a-real-backend"
	if IsSupportedType(bogus) {
		t.Errorf("IsSupportedType(%q) = true, want false", bogus)
	}
	if _, err := New(bogus, cfg); err == nil {
		t.Errorf("New(%q) succeeded, want error for an unsupported type", bogus)
	}
}

// TestSupportedTypesMatchesMigrationWhitelist pins the exact set so a drift
// from the runtime_profile.protocol_family CHECK fails loudly. The latest
// CHECK is migration 925, which added the first voice family.
func TestSupportedTypesMatchesMigrationWhitelist(t *testing.T) {
	want := map[string]bool{
		"claude": true, "codebuddy": true, "codex": true, "copilot": true,
		"opencode": true, "codearts": true, "deveco": true, "openclaw": true, "hermes": true,
		"pi": true, "cursor": true, "kimi": true, "reasonix": true, "dsh": true, "kiro": true, "antigravity": true,
		"qoder": true, "qoderclicn": true, "traecli": true, "grok": true, "qwen": true, "qwenpaw": true, "mcode": true,
		"dim": true, "zeroclaw": true, "deerflow": true, "zcode": true,
		"gemini_live": true,
	}
	if len(SupportedTypes) != len(want) {
		t.Fatalf("SupportedTypes has %d entries, migration whitelist has %d; keep them in lockstep", len(SupportedTypes), len(want))
	}
	for _, typ := range SupportedTypes {
		if !want[typ] {
			t.Errorf("SupportedTypes contains %q which is not in the latest protocol_family CHECK", typ)
		}
	}
}
