package opskind

import (
	"strings"
	"testing"
)

// Matrix group 1 (RUYI-645 design §8): whitelist membership, unit-slot
// injection rejection and integer-slot range enforcement. These tests must
// fail before the registry exists and pin the dual-end contract shared by
// the server submission gate and the daemon pre-execution re-check.

func TestLookupUnknownKind(t *testing.T) {
	// Assertion 1 (validator half): a kind outside the whitelist must not
	// resolve. Also guards against a server bypass: the daemon re-checks
	// with the same table.
	for _, kind := range []Kind{
		"",
		"reboot",
		"systemctl_start",
		"sh",
		"systemctl_status ",
		"SYSTEMCTL_STATUS",
	} {
		if Lookup(kind) != nil {
			t.Fatalf("Lookup(%q) = spec, want nil", kind)
		}
	}
}

func TestWhitelistExactMembership(t *testing.T) {
	want := []Kind{
		KindSystemctlStatus,
		KindJournalctlTail,
		KindPsSnapshot,
		KindDfReport,
		KindLaunchctlList,
		KindMacLogShow,
		KindDockerPs,
	}
	if got := Kinds(); len(got) != len(want) {
		t.Fatalf("Kinds() has %d entries, want %d (v1 whitelist is fixed)", len(got), len(want))
	}
	for _, kind := range want {
		spec := Lookup(kind)
		if spec == nil {
			t.Fatalf("Lookup(%q) = nil, want spec", kind)
		}
		if spec.Kind != kind {
			t.Fatalf("Lookup(%q).Kind = %q", kind, spec.Kind)
		}
		if len(spec.Argv) == 0 {
			t.Fatalf("kind %q has an empty argv template", kind)
		}
	}
}

func TestUnitSlotInjectionRejected(t *testing.T) {
	injections := []string{
		"",
		strings.Repeat("a", 129), // over the 128-char limit
		"-x",                     // flag injection: unit must not start with '-'
		"--foo",
		"a b", // whitespace splits argv
		"a;b",
		"a&b",
		"a|b",
		"a\nb",
		"a\x00b",
		"a$PATH",
		"a`id`",
		"a'quote",
		"a\"quote",
		"服务", // outside the [a-zA-Z0-9_@.-] charset
		"a/b",
		"a\\b",
	}
	for _, unit := range injections {
		args := map[string]any{"unit": unit}
		if err := ValidateArgs(KindSystemctlStatus, args); err == nil {
			t.Fatalf("ValidateArgs(systemctl_status, unit=%q) = nil error, want rejection", unit)
		}
		if err := ValidateArgs(KindJournalctlTail, args); err == nil {
			t.Fatalf("ValidateArgs(journalctl_tail, unit=%q) = nil error, want rejection", unit)
		}
	}
}

func TestUnitSlotAccepted(t *testing.T) {
	valid := []string{
		"multica-server.service",
		"nginx",
		"user@1000.service",
		"docker.service",
		strings.Repeat("a", 128),
		"my-worker@.service",
	}
	for _, unit := range valid {
		args := map[string]any{"unit": unit}
		if err := ValidateArgs(KindSystemctlStatus, args); err != nil {
			t.Fatalf("ValidateArgs(systemctl_status, unit=%q) = %v, want accept", unit, err)
		}
	}
}

func TestIntegerSlotRangeEnforced(t *testing.T) {
	// journalctl_tail lines ∈ [50,500]; mac_log_show minutes ∈ [10,120].
	cases := []struct {
		kind Kind
		key  string
		_lo  int
		hi   int
	}{
		{KindJournalctlTail, "lines", 50, 500},
		{KindMacLogShow, "minutes", 10, 120},
	}
	for _, tc := range cases {
		base := map[string]any{}
		if tc.kind == KindJournalctlTail {
			base["unit"] = "nginx"
		}
		lo := cloneArgs(base)
		lo[tc.key] = tc._lo
		if err := ValidateArgs(tc.kind, lo); err != nil {
			t.Fatalf("%s %s=%d (lower bound) rejected: %v", tc.kind, tc.key, tc._lo, err)
		}
		hi := cloneArgs(base)
		hi[tc.key] = tc.hi
		if err := ValidateArgs(tc.kind, hi); err != nil {
			t.Fatalf("%s %s=%d (upper bound) rejected: %v", tc.kind, tc.key, tc.hi, err)
		}
		for _, out := range []int{tc._lo - 1, tc.hi + 1, 0, -1} {
			args := cloneArgs(base)
			args[tc.key] = out
			if err := ValidateArgs(tc.kind, args); err == nil {
				t.Fatalf("%s %s=%d accepted, want rejection", tc.kind, tc.key, out)
			}
		}
		// Non-integer types must be rejected, not coerced.
		for _, bad := range []any{"100", 3.5, true, nil, []any{100}} {
			args := cloneArgs(base)
			args[tc.key] = bad
			if err := ValidateArgs(tc.kind, args); err == nil {
				t.Fatalf("%s %s=%v (type %T) accepted, want rejection", tc.kind, tc.key, bad, bad)
			}
		}
	}
}

func cloneArgs(src map[string]any) map[string]any {
	dst := make(map[string]any, len(src)+1)
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func TestDefaultsApplied(t *testing.T) {
	normalized, err := NormalizeArgs(KindJournalctlTail, map[string]any{"unit": "nginx"})
	if err != nil {
		t.Fatalf("NormalizeArgs with defaults: %v", err)
	}
	lines, ok := normalized["lines"].(int)
	if !ok || lines != 100 {
		t.Fatalf("journalctl_tail default lines = %v, want 100", normalized["lines"])
	}

	normalized, err = NormalizeArgs(KindMacLogShow, map[string]any{})
	if err != nil {
		t.Fatalf("NormalizeArgs mac_log_show defaults: %v", err)
	}
	minutes, ok := normalized["minutes"].(int)
	if !ok || minutes != 30 {
		t.Fatalf("mac_log_show default minutes = %v, want 30", normalized["minutes"])
	}
}

func TestUnknownArgKeysRejected(t *testing.T) {
	err := ValidateArgs(KindPsSnapshot, map[string]any{"aux": "x"})
	if err == nil {
		t.Fatal("ps_snapshot with unknown arg accepted, want rejection")
	}
	err = ValidateArgs(KindSystemctlStatus, map[string]any{"unit": "nginx", "lines": 100})
	if err == nil {
		t.Fatal("systemctl_status with foreign slot accepted, want rejection")
	}
}

func TestArgvTemplateBuild(t *testing.T) {
	argv, err := BuildArgv(KindJournalctlTail, map[string]any{"unit": "nginx", "lines": 100}, "")
	if err != nil {
		t.Fatalf("BuildArgv: %v", err)
	}
	want := []string{"/usr/bin/journalctl", "-u", "nginx", "-n", "100", "--no-pager"}
	if strings.Join(argv, " ") != strings.Join(want, " ") {
		t.Fatalf("BuildArgv journalctl_tail = %v, want %v", argv, want)
	}

	argv, err = BuildArgv(KindDockerPs, map[string]any{}, "/usr/local/bin/docker")
	if err != nil {
		t.Fatalf("BuildArgv docker_ps: %v", err)
	}
	if argv[0] != "/usr/local/bin/docker" || strings.Join(argv[1:], " ") != "ps -a" {
		t.Fatalf("BuildArgv docker_ps = %v, want [/usr/local/bin/docker ps -a]", argv)
	}
	// docker_bin is daemon-local config; the server never injects it and an
	// empty resolution must fail closed rather than exec an empty path.
	if _, err := BuildArgv(KindDockerPs, map[string]any{}, ""); err == nil {
		t.Fatal("BuildArgv docker_ps with empty docker_bin accepted, want fail-closed")
	}
	// Args must never smuggle extra argv elements.
	argv, err = BuildArgv(KindSystemctlStatus, map[string]any{"unit": "nginx status --no-pager"}, "")
	if err == nil {
		t.Fatal("BuildArgv with a unit containing spaces accepted, want rejection")
	}
	_ = argv
}

func TestCapsAvailabilityMatrix(t *testing.T) {
	cases := []struct {
		kind Kind
		caps []string
		want bool
	}{
		{KindSystemctlStatus, []string{}, false},
		{KindSystemctlStatus, []string{"systemd"}, true},
		{KindJournalctlTail, []string{"systemd"}, true},
		{KindPsSnapshot, []string{}, true}, // machine-face read, no cap needed
		{KindDfReport, []string{}, true},
		{KindLaunchctlList, []string{}, false},
		{KindLaunchctlList, []string{"launchd"}, true},
		{KindMacLogShow, []string{"launchd"}, true},
		{KindDockerPs, []string{}, false},
		{KindDockerPs, []string{"systemd"}, false}, // OR-of-AND: unrelated caps don't qualify
		{KindDockerPs, []string{"docker"}, true},
		{KindDockerPs, []string{"docker", "systemd"}, true},
	}
	for _, tc := range cases {
		set := make(map[string]bool, len(tc.caps))
		for _, c := range tc.caps {
			set[c] = true
		}
		if got := Available(tc.kind, set); got != tc.want {
			t.Fatalf("Available(%q, %v) = %v, want %v", tc.kind, tc.caps, got, tc.want)
		}
	}
}

func TestKindsReportRequiredCaps(t *testing.T) {
	if caps := RequiredCaps(KindDockerPs); len(caps) == 0 {
		t.Fatal("docker_ps reports no caps, want a non-empty OR-of-AND matrix")
	}
	if caps := RequiredCaps(KindPsSnapshot); len(caps) != 0 {
		t.Fatalf("ps_snapshot reports caps %v, want none", caps)
	}
}
