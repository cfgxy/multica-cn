package agent

import (
	"strings"
	"testing"
)

// TestCapabilitiesForFamily pins the per-family baselines (design §4.4):
// gemini_live speaks realtime voice and calls tools but cannot run text
// tasks, every pre-RUYI-425 family is a text CLI, and an unknown family
// resolves to the CLI baseline rather than guessing voice support.
func TestCapabilitiesForFamily(t *testing.T) {
	cases := []struct {
		family string
		want   Capabilities
	}{
		{"gemini_live", Capabilities{Text: false, RealtimeVoice: true, Tools: true}},
		{"claude", Capabilities{Text: true, Tools: true}},
		{"kimi", Capabilities{Text: true, Tools: true}},
		{"zcode", Capabilities{Text: true, Tools: true}},
		{"no_such_family", Capabilities{Text: true, Tools: true}},
	}
	for _, tc := range cases {
		if got := CapabilitiesForFamily(tc.family); got != tc.want {
			t.Errorf("CapabilitiesForFamily(%q) = %+v, want %+v", tc.family, got, tc.want)
		}
	}
}

// TestIsVoiceProtocolFamily guards the daemon-side filter: voice families are
// API-backed and invisible to daemon discovery, so the predicate must be
// exactly "baseline declares realtime_voice".
func TestIsVoiceProtocolFamily(t *testing.T) {
	for _, family := range []string{"gemini_live"} {
		if !IsVoiceProtocolFamily(family) {
			t.Errorf("IsVoiceProtocolFamily(%q) = false, want true", family)
		}
	}
	for _, family := range []string{"claude", "kimi", "codex", "no_such_family"} {
		if IsVoiceProtocolFamily(family) {
			t.Errorf("IsVoiceProtocolFamily(%q) = true, want false", family)
		}
	}
}

// TestCapabilitiesSupportsUnknownFailsClosed: a capability name outside the
// declared set is never reported as supported.
func TestCapabilitiesSupportsUnknownFailsClosed(t *testing.T) {
	caps := Capabilities{Text: true, RealtimeVoice: true, Tools: true}
	for _, name := range []string{CapabilityText, CapabilityRealtimeVoice, CapabilityTools} {
		if !caps.Supports(name) {
			t.Errorf("fully-capable declaration must support %q", name)
		}
	}
	if caps.Supports("telepathy") {
		t.Error(`Supports("telepathy") = true, want false — unknown capabilities fail closed`)
	}
	if (Capabilities{}).Supports(CapabilityText) {
		t.Error("empty declaration must support nothing")
	}
}

func TestResolveCapabilities(t *testing.T) {
	t.Run("empty shapes fall back to the family baseline", func(t *testing.T) {
		for _, raw := range [][]byte{nil, []byte(""), []byte("  "), []byte("{}"), []byte("null")} {
			got, err := ResolveCapabilities("gemini_live", raw)
			if err != nil {
				t.Fatalf("ResolveCapabilities(gemini_live, %q): %v", raw, err)
			}
			if want := (Capabilities{RealtimeVoice: true, Tools: true}); got != want {
				t.Errorf("ResolveCapabilities(gemini_live, %q) = %+v, want baseline %+v", raw, got, want)
			}
		}
		for _, raw := range [][]byte{nil, []byte("{}")} {
			got, err := ResolveCapabilities("kimi", raw)
			if err != nil {
				t.Fatalf("ResolveCapabilities(kimi, %q): %v", raw, err)
			}
			if want := (Capabilities{Text: true, Tools: true}); got != want {
				t.Errorf("ResolveCapabilities(kimi, %q) = %+v, want baseline %+v", raw, got, want)
			}
		}
	})

	t.Run("non-empty column is authoritative", func(t *testing.T) {
		// A row that explicitly revokes voice on a voice family must be
		// honored — the column is the Type-layer declaration, not a hint.
		got, err := ResolveCapabilities("gemini_live", []byte(`{"text":false,"realtime_voice":false,"tools":true}`))
		if err != nil {
			t.Fatalf("ResolveCapabilities: %v", err)
		}
		if got.RealtimeVoice {
			t.Errorf("explicit realtime_voice:false resolved to %+v, want voice revoked", got)
		}
		// Absent booleans are false, not inherited from the baseline: the
		// stored shape is three explicit booleans so rows stay comparable.
		got, err = ResolveCapabilities("kimi", []byte(`{"text":true}`))
		if err != nil {
			t.Fatalf("ResolveCapabilities: %v", err)
		}
		if want := (Capabilities{Text: true}); got != want {
			t.Errorf("partial override = %+v, want %+v (absent booleans are false, not baseline-inherited)", got, want)
		}
	})

	t.Run("malformed JSON fails closed", func(t *testing.T) {
		for _, raw := range []string{`{"text":`, `not json`, `["text"]`} {
			got, err := ResolveCapabilities("kimi", []byte(raw))
			if err == nil {
				t.Errorf("ResolveCapabilities(kimi, %q) = %+v, want an error", raw, got)
			}
			if got != (Capabilities{}) {
				t.Errorf("ResolveCapabilities(kimi, %q) returned non-zero capabilities %+v alongside the error", raw, got)
			}
		}
	})
}

// TestMarshalCapabilitiesRoundTrip: the stored shape is exactly the three
// explicit booleans, and encoding survives a marshal/unmarshal cycle.
func TestMarshalCapabilitiesRoundTrip(t *testing.T) {
	caps := Capabilities{Text: true, RealtimeVoice: true, Tools: true}
	raw, err := MarshalCapabilities(caps)
	if err != nil {
		t.Fatalf("MarshalCapabilities: %v", err)
	}
	if string(raw) != `{"text":true,"realtime_voice":true,"tools":true}` {
		t.Errorf("MarshalCapabilities = %s, want the three explicit booleans", raw)
	}
	back, err := ResolveCapabilities("gemini_live", raw)
	if err != nil {
		t.Fatalf("ResolveCapabilities: %v", err)
	}
	if back != caps {
		t.Errorf("round trip = %+v, want %+v", back, caps)
	}
}

func TestValidateSlotCapability(t *testing.T) {
	geminiLive := Capabilities{RealtimeVoice: true, Tools: true}
	claudeCode := Capabilities{Text: true, Tools: true}

	cases := []struct {
		name        string
		slot        AgentSlot
		family      string
		caps        Capabilities
		wantErrText string
	}{
		{"gemini_live occupies the voice slot", SlotVoice, "gemini_live", geminiLive, ""},
		{"claude occupies the text slot", SlotText, "claude", claudeCode, ""},
		{"claude cannot enter the voice slot", SlotVoice, "claude", claudeCode, "realtime_voice"},
		{"gemini_live cannot enter the text slot", SlotText, "gemini_live", geminiLive, `"text"`},
		{"unknown slot is rejected outright", AgentSlot("aux"), "claude", claudeCode, "unknown runtime slot"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateSlotCapability(tc.slot, tc.family, tc.caps)
			if tc.wantErrText == "" {
				if err != nil {
					t.Fatalf("ValidateSlotCapability(%s, %s) = %v, want nil", tc.slot, tc.family, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("ValidateSlotCapability(%s, %s) = nil, want an error", tc.slot, tc.family)
			}
			if !strings.Contains(err.Error(), tc.wantErrText) {
				t.Errorf("error %q does not mention %q", err, tc.wantErrText)
			}
		})
	}
}

// TestAgentSlotWireValues: slot names appear in API errors and are the wire
// contract of the dual-slot design; changing them silently would break every
// client that matches on them.
func TestAgentSlotWireValues(t *testing.T) {
	if string(SlotText) != "text" {
		t.Errorf(`SlotText = %q, want "text"`, SlotText)
	}
	if string(SlotVoice) != "voice" {
		t.Errorf(`SlotVoice = %q, want "voice"`, SlotVoice)
	}
}
