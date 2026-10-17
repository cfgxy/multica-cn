// Package opskind is the single source of truth for the RUYI-645 read-only
// node-ops command whitelist. The server submission gate and the daemon
// pre-execution re-check both import this package, so the whitelist, the
// typed argument slots and the argv templates can never drift between the
// two ends (same pattern as MeshConsole's internal/cmdkind).
//
// Security contract (design §2/§6): the v1 whitelist is fixed at compile
// time — no runtime registration path exists. Every executable path is a
// fixed absolute path baked into the argv template; the only exception is
// the docker binary, which the daemon resolves from its own local config
// and which the server never sees or validates. Argument values are
// restricted to typed slots with character-set or closed-range checks, so
// no argument can add or alter an argv element.
package opskind

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

// Kind names one whitelisted read-only command. The set below is the v1
// whitelist absorbed from MeshConsole; adding or removing kinds is a design
// change that must go through Leader/Owner review, not a code edit.
type Kind string

const (
	KindSystemctlStatus Kind = "systemctl_status"
	KindJournalctlTail  Kind = "journalctl_tail"
	KindPsSnapshot      Kind = "ps_snapshot"
	KindDfReport        Kind = "df_report"
	KindLaunchctlList   Kind = "launchctl_list"
	KindMacLogShow      Kind = "mac_log_show"
	KindDockerPs        Kind = "docker_ps"
)

// Cap names a daemon-side capability probed at registration time.
type Cap string

const (
	CapSystemd Cap = "systemd"
	CapLaunchd Cap = "launchd"
	CapDocker  Cap = "docker"
)

// MaxArgsJSONBytes caps the raw args JSON a client may submit.
const MaxArgsJSONBytes = 4096

// Unit limits for the systemd unit slot (design §2).
const (
	UnitMaxLen  = 128
	unitCharset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_@.-"
)

// Spec is the immutable description of one whitelisted kind.
type Spec struct {
	Kind Kind
	// Platforms lists the GOOS values the command makes sense on. Empty
	// means any platform.
	Platforms []string
	// CapSets is the OR-of-AND availability matrix: the kind is available
	// on a node when at least one inner set is fully contained in the
	// node's reported caps. An empty outer slice means no caps required.
	CapSets [][]Cap
	// Argv is the fixed template. Placeholders: {unit} (unit slot) and
	// {n} (integer slot), plus the leading {docker_bin} on docker_ps.
	Argv []string
	// Slots describes the accepted argument keys.
	Slots []Slot
}

// Slot is one typed argument slot.
type Slot struct {
	Key string
	// Type is "unit" (string, charset-checked) or "int".
	Type    string
	Min     int
	Max     int
	Default int
	HasDef  bool
	Req     bool
}

var registry = map[Kind]*Spec{
	KindSystemctlStatus: {
		Kind:      KindSystemctlStatus,
		Platforms: []string{"linux"},
		CapSets:   [][]Cap{{CapSystemd}},
		Argv:      []string{"/usr/bin/systemctl", "status", "{unit}"},
		Slots:     []Slot{{Key: "unit", Type: "unit", Req: true}},
	},
	KindJournalctlTail: {
		Kind:      KindJournalctlTail,
		Platforms: []string{"linux"},
		CapSets:   [][]Cap{{CapSystemd}},
		Argv:      []string{"/usr/bin/journalctl", "-u", "{unit}", "-n", "{n}", "--no-pager"},
		Slots: []Slot{
			{Key: "unit", Type: "unit", Req: true},
			{Key: "lines", Type: "int", Min: 50, Max: 500, Default: 100, HasDef: true},
		},
	},
	KindPsSnapshot: {
		Kind:      KindPsSnapshot,
		Platforms: []string{"linux", "darwin"},
		Argv:      []string{"/bin/ps", "aux"},
	},
	KindDfReport: {
		Kind:      KindDfReport,
		Platforms: []string{"linux", "darwin"},
		Argv:      []string{"/bin/df", "-h"},
	},
	KindLaunchctlList: {
		Kind:      KindLaunchctlList,
		Platforms: []string{"darwin"},
		// launchctl ships with every macOS install; the launchd cap is the
		// darwin probe itself.
		CapSets: [][]Cap{{CapLaunchd}},
		Argv:    []string{"/bin/launchctl", "list"},
	},
	KindMacLogShow: {
		Kind:      KindMacLogShow,
		Platforms: []string{"darwin"},
		CapSets:   [][]Cap{{CapLaunchd}},
		Argv:      []string{"/usr/bin/log", "show", "--last", "{n}m", "--style", "compact"},
		Slots:     []Slot{{Key: "minutes", Type: "int", Min: 10, Max: 120, Default: 30, HasDef: true}},
	},
	KindDockerPs: {
		Kind:      KindDockerPs,
		Platforms: []string{"linux"},
		CapSets:   [][]Cap{{CapDocker}},
		// {docker_bin} resolves from daemon-local config at execution time;
		// the server never learns or validates the resolved path, so a
		// console caller cannot inject a binary path through args.
		Argv: []string{"{docker_bin}", "ps", "-a"},
	},
}

// Lookup returns the spec for a whitelist kind, or nil for anything else.
func Lookup(kind Kind) *Spec {
	return registry[kind]
}

// Kinds lists the whitelist in stable order.
func Kinds() []Kind {
	out := make([]Kind, 0, len(registry))
	for k := range registry {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// RequiredCaps returns the OR-of-AND matrix for a kind (nil for kinds with
// no caps). Unknown kinds return nil.
func RequiredCaps(kind Kind) [][]Cap {
	spec := registry[kind]
	if spec == nil {
		return nil
	}
	return spec.CapSets
}

// Available reports whether a node with the given cap set may run kind.
func Available(kind Kind, caps map[string]bool) bool {
	spec := registry[kind]
	if spec == nil {
		return false
	}
	if len(spec.CapSets) == 0 {
		return true
	}
	for _, set := range spec.CapSets {
		ok := true
		for _, c := range set {
			if !caps[string(c)] {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

// ValidateArgs checks args against the kind's typed slots without applying
// defaults. Unknown keys are rejected: a slot that only the daemon knows
// about must not pass server validation by hiding in an ignored field.
func ValidateArgs(kind Kind, args map[string]any) error {
	_, err := normalize(kind, args, false)
	return err
}

// NormalizeArgs validates args and returns a normalized copy with defaults
// applied. Integer slots normalize to int.
func NormalizeArgs(kind Kind, args map[string]any) (map[string]any, error) {
	return normalize(kind, args, true)
}

func normalize(kind Kind, args map[string]any, withDefaults bool) (map[string]any, error) {
	spec := registry[kind]
	if spec == nil {
		return nil, fmt.Errorf("unknown ops command kind %q", kind)
	}
	byKey := make(map[string]Slot, len(spec.Slots))
	for _, s := range spec.Slots {
		byKey[s.Key] = s
	}
	out := make(map[string]any, len(args))
	for key, val := range args {
		slot, ok := byKey[key]
		if !ok {
			return nil, fmt.Errorf("kind %q does not accept argument %q", kind, key)
		}
		switch slot.Type {
		case "unit":
			s, ok := val.(string)
			if !ok {
				return nil, fmt.Errorf("slot %q must be a string", key)
			}
			if err := ValidateUnit(s); err != nil {
				return nil, err
			}
			out[key] = s
		case "int":
			n, ok := val.(int)
			if !ok {
				// JSON decoding yields float64; accept only integral values
				// so "3.5" or true can never slip through as a number.
				f, isFloat := val.(float64)
				if !isFloat || f != float64(int64(f)) {
					return nil, fmt.Errorf("slot %q must be an integer", key)
				}
				n = int(f)
			}
			if n < slot.Min || n > slot.Max {
				return nil, fmt.Errorf("slot %q value %d outside [%d,%d]", key, n, slot.Min, slot.Max)
			}
			out[key] = n
		}
	}
	if withDefaults {
		for _, slot := range spec.Slots {
			if _, present := out[slot.Key]; !present && slot.HasDef {
				out[slot.Key] = slot.Default
			}
		}
	}
	for _, slot := range spec.Slots {
		if slot.Req {
			if _, present := out[slot.Key]; !present {
				return nil, fmt.Errorf("kind %q requires argument %q", kind, slot.Key)
			}
		}
	}
	return out, nil
}

// ValidateUnit enforces the systemd unit slot contract: non-empty, at most
// UnitMaxLen bytes, restricted to [a-zA-Z0-9_@.-], and never starting with
// '-' so a value cannot introduce an argv flag.
func ValidateUnit(unit string) error {
	if unit == "" {
		return fmt.Errorf("unit must not be empty")
	}
	if len(unit) > UnitMaxLen {
		return fmt.Errorf("unit longer than %d bytes", UnitMaxLen)
	}
	if !utf8.ValidString(unit) {
		return fmt.Errorf("unit is not valid UTF-8")
	}
	if strings.HasPrefix(unit, "-") {
		return fmt.Errorf("unit must not start with '-'")
	}
	for _, r := range unit {
		if !strings.ContainsRune(unitCharset, r) {
			return fmt.Errorf("unit contains character %q outside the allowed set", r)
		}
	}
	return nil
}

// BuildArgv renders the kind's fixed template with normalized args. It is
// the daemon-side executable path; dockerBin is the daemon's locally
// resolved docker binary (required only by docker_ps). Args must come from
// NormalizeArgs — an unnormalized unit with a space would join into a new
// argv element, so BuildArgv re-validates rather than trusting its caller.
func BuildArgv(kind Kind, args map[string]any, dockerBin string) ([]string, error) {
	spec := registry[kind]
	if spec == nil {
		return nil, fmt.Errorf("unknown ops command kind %q", kind)
	}
	norm, err := normalize(kind, args, true)
	if err != nil {
		return nil, err
	}
	argv := make([]string, 0, len(spec.Argv))
	for _, part := range spec.Argv {
		switch part {
		case "{unit}":
			argv = append(argv, norm["unit"].(string))
		case "{n}":
			argv = append(argv, fmt.Sprintf("%d", norm["lines"].(int)))
		case "{docker_bin}":
			if strings.TrimSpace(dockerBin) == "" {
				return nil, fmt.Errorf("docker binary is not configured on this node")
			}
			argv = append(argv, dockerBin)
		default:
			// Composite placeholders like mac_log_show's "{n}m" stay inside
			// one argv element; n is an int so the substitution cannot
			// introduce separators.
			if strings.Contains(part, "{") {
				part = strings.ReplaceAll(part, "{n}", fmt.Sprintf("%d", norm["minutes"].(int)))
			}
			argv = append(argv, part)
		}
	}
	return argv, nil
}
