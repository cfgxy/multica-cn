package supervisor

import (
	"strings"
	"testing"
)

// OwnerIdentity is the single identity string serving as both the run-store
// namespace segment and the manifest owner stamp (RUYI-607). It must be a
// usable single path segment on every OS and deterministic across restarts
// of the same profile; empty or pathological inputs collapse to "default".
func TestOwnerIdentity(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", "default"},
		{"dev1", "dev1"},
		{"Desktop-21801", "Desktop-21801"},
		{"ruyi514qa", "ruyi514qa"},
		{"..", "default"},
		{".", "default"},
		{"a/b", "a-b"},
		{"a b", "a-b"},
		{"..\\\\escape", "..--escape"},
		{"开发", "--"},
	}
	for _, tc := range cases {
		if got := OwnerIdentity(tc.in); got != tc.want {
			t.Errorf("OwnerIdentity(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	if got := OwnerIdentity(strings.Repeat("x", 100)); got != strings.Repeat("x", 64) {
		t.Errorf("OwnerIdentity(long) = %d chars, want 64", len(got))
	}
}
