package taskfailure

import (
	"strings"
	"testing"
)

func TestFormatGuardMetaRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		kind string
	}{
		{"ancestor break", GuardKindAncestorBreak},
		{"branch mismatch", GuardKindBranchMismatch},
		{"unmerged edits", GuardKindUnmergedEdits},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			original := "refusing to record branch agent/j/mul-6881: the delivered commit abc1234 no longer contains def5678; the task worktree is preserved at /tmp/wt"
			appended := AppendGuardMeta(original, tc.kind)

			if !strings.Contains(appended, GuardMetaLine+":") {
				t.Fatalf("appended message carries no trailer:\n%s", appended)
			}
			gotKind, remainder, ok := ParseGuardMeta(appended)
			if !ok {
				t.Fatal("ParseGuardMeta did not recognise the trailer FormatGuardMeta wrote")
			}
			if gotKind != tc.kind {
				t.Errorf("kind = %q, want %q", gotKind, tc.kind)
			}
			if remainder != original {
				t.Errorf("remainder = %q, want the original message %q", remainder, original)
			}
		})
	}
}

func TestAppendGuardMetaLeavesUnclassifiedRefusalUntouched(t *testing.T) {
	original := "refusing to record branch agent/j/mul-6881: branch has no commit of this task's own to prove it by"
	if got := AppendGuardMeta(original, ""); got != original {
		t.Errorf("unclassified refusal gained a trailer:\n%s", got)
	}
}

func TestParseGuardMetaWithoutTrailer(t *testing.T) {
	original := "an ordinary failure message"
	kind, remainder, ok := ParseGuardMeta(original)
	if ok {
		t.Error("ParseGuardMeta reported a trailer on a message with none")
	}
	if kind != "" {
		t.Errorf("kind = %q, want empty", kind)
	}
	if remainder != original {
		t.Errorf("remainder = %q, want the original message", remainder)
	}
}

func TestStripGuardMeta(t *testing.T) {
	plain := "plain failure"
	if got := StripGuardMeta(plain); got != plain {
		t.Errorf("StripGuardMeta changed a trailer-free message to %q", got)
	}
	withTrailer := "the refusal prose\n" + GuardMetaLine + ": kind=" + GuardKindAncestorBreak
	want := "the refusal prose"
	if got := StripGuardMeta(withTrailer); got != want {
		t.Errorf("StripGuardMeta = %q, want %q", got, want)
	}
}

func TestParseGuardMetaToleratesUnknownKind(t *testing.T) {
	// A newer daemon may classify a shape this build does not know. The
	// field's purpose is visibility, not enumeration enforcement: the value
	// surfaces verbatim.
	line := GuardMetaLine + ": kind=guard_kind_from_the_future"
	kind, _, ok := ParseGuardMeta(line)
	if !ok {
		t.Fatal("trailer not recognised")
	}
	if kind != "guard_kind_from_the_future" {
		t.Errorf("kind = %q, want the verbatim unknown value", kind)
	}
}

func TestParseGuardMetaOnlyStripsTheTrailerLine(t *testing.T) {
	prose := "first line mentions " + GuardMetaLine + " in passing\nsecond line"
	withTrailer := prose + "\n" + GuardMetaLine + ": kind=" + GuardKindBranchMismatch
	_, remainder, ok := ParseGuardMeta(withTrailer)
	if !ok {
		t.Fatal("trailer not recognised")
	}
	if remainder != prose {
		t.Errorf("remainder = %q, want the prose kept intact:\n%s", remainder, prose)
	}
}
