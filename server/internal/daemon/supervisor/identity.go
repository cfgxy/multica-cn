package supervisor

import "regexp"

// ownerSanitizer folds every rune outside the namespace alphabet to a dash,
// one dash per invalid rune — no collapsing, so the identity is a
// deterministic encoding of the profile name, not a prettified one. Runs
// after substitution the string is pure ASCII, which is what makes the
// 64-byte cut below always land on a rune boundary.
var ownerSanitizer = regexp.MustCompile(`[^A-Za-z0-9._-]`)

// OwnerIdentity normalizes a daemon profile name into the single identity
// string that names this daemon's run-store namespace segment and stamps
// every manifest it launches (RUYI-607). The result is always a usable
// single path segment (alphabet [A-Za-z0-9._-], at most 64 bytes) and is
// deterministic across restarts of the same profile. Empty and dot-only
// names — invalid or dangerous as directory names — collapse to "default",
// the identity of the unnamed profile. Two profiles that sanitize to the
// same segment would share a namespace but still never cross-kill, because
// reconcile compares this exact string against the manifest owner before
// any destructive action.
func OwnerIdentity(profile string) string {
	if profile == "" || profile == "." || profile == ".." {
		return "default"
	}
	s := ownerSanitizer.ReplaceAllString(profile, "-")
	if len(s) > 64 {
		s = s[:64]
	}
	return s
}
