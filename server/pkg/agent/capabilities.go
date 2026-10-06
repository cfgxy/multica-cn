package agent

import (
	"encoding/json"
	"fmt"
	"strings"
)

// RUYI-425 stage 1: runtime capability declarations (design §4.4).
//
// A capability belongs to a protocol FAMILY, not to a runtime instance: two
// Gemini Live instances have the same capabilities and differ only in
// credentials. The declaration lives on runtime_profile.capabilities (the
// Type layer); agent_runtime instances inherit it through their profile_id,
// and built-in instances resolve it from their provider family directly.
//
// The JSON shape persisted in runtime_profile.capabilities is exactly this
// struct — three explicit booleans, no extra keys — so rows stay comparable
// and auditable.
const (
	CapabilityText          = "text"
	CapabilityRealtimeVoice = "realtime_voice"
	CapabilityTools         = "tools"
)

// AgentSlot names one of the two agent runtime slots (design §7.1). The text
// slot is the historical single runtime_id column; the voice slot is the new
// nullable voice_runtime_id column.
type AgentSlot string

const (
	SlotText  AgentSlot = "text"
	SlotVoice AgentSlot = "voice"
)

// Capabilities is a protocol family's boolean capability declaration.
type Capabilities struct {
	Text          bool `json:"text"`
	RealtimeVoice bool `json:"realtime_voice"`
	Tools         bool `json:"tools"`
}

// Supports reports whether the declaration includes the named capability.
// Unknown capability names fail closed.
func (c Capabilities) Supports(capability string) bool {
	switch capability {
	case CapabilityText:
		return c.Text
	case CapabilityRealtimeVoice:
		return c.RealtimeVoice
	case CapabilityTools:
		return c.Tools
	default:
		return false
	}
}

// cliFamilyCapabilities is the baseline for every text-CLI protocol family:
// they run tasks and call tools, and none of them speaks realtime voice.
var cliFamilyCapabilities = Capabilities{Text: true, Tools: true}

// familyCapabilities declares per-family capability baselines that differ
// from the CLI default (design §4.4 baseline declarations). Families absent
// from this map are text CLIs by definition — adding a new voice family
// means one entry here plus one SupportedTypes entry plus the migration
// whitelist, in lockstep.
var familyCapabilities = map[string]Capabilities{
	"gemini_live": {RealtimeVoice: true, Tools: true},
}

// CapabilitiesForFamily returns the baseline capability declaration for a
// protocol family. Unknown families resolve to the CLI baseline: every family
// in SupportedTypes that predates RUYI-425 is a text CLI, and a family that
// is not in SupportedTypes at all is rejected upstream of this call.
func CapabilitiesForFamily(family string) Capabilities {
	if caps, ok := familyCapabilities[family]; ok {
		return caps
	}
	return cliFamilyCapabilities
}

// IsVoiceProtocolFamily reports whether the family's baseline includes
// realtime voice. Voice families are API-backed: they have no CLI binary for
// the daemon to probe, so daemon discovery and profile-pull paths skip them.
func IsVoiceProtocolFamily(family string) bool {
	return CapabilitiesForFamily(family).RealtimeVoice
}

// ResolveCapabilities resolves the effective capabilities of a runtime
// profile row: a non-empty capabilities column is authoritative, an empty
// object (the migration default on pre-existing rows) falls back to the
// family baseline. Malformed JSON fails closed — callers reject the request
// rather than guessing.
func ResolveCapabilities(family string, raw []byte) (Capabilities, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "{}" || trimmed == "null" {
		return CapabilitiesForFamily(family), nil
	}
	var caps Capabilities
	if err := json.Unmarshal(raw, &caps); err != nil {
		return Capabilities{}, fmt.Errorf("runtime profile capabilities: %w", err)
	}
	return caps, nil
}

// MarshalCapabilities encodes the declaration for the runtime_profile
// capabilities column. All three booleans are explicit so stored rows have a
// stable, diffable shape.
func MarshalCapabilities(c Capabilities) ([]byte, error) {
	return json.Marshal(c)
}

// ValidateSlotCapability enforces design §4.4 rules 1 and 2: a runtime may
// only occupy the slot its family's capabilities allow. Claude Code cannot
// enter the voice slot, Gemini Live cannot enter the text slot. The returned
// errors are user-facing (the agent API answers 400 with them verbatim).
func ValidateSlotCapability(slot AgentSlot, family string, caps Capabilities) error {
	var required string
	switch slot {
	case SlotText:
		required = CapabilityText
	case SlotVoice:
		required = CapabilityRealtimeVoice
	default:
		return fmt.Errorf("unknown runtime slot %q", string(slot))
	}
	if caps.Supports(required) {
		return nil
	}
	return fmt.Errorf(
		"runtime protocol family %q cannot be used as the %s runtime: it does not declare the %q capability",
		family, string(slot), required,
	)
}
