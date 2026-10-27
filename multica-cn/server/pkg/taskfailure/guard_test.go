package taskfailure

import (
	"strings"
	"testing"
)

func TestFormatGuardMetaRoundTrip(t *testing.T) {
	cases := []struct {
		name          string
		kind          string
		healAttempted bool
	}{
		{"ancestor break with heal", GuardKindAncestorBreak, true},
		{"ancestor break without heal", GuardKindAncestorBreak, false},
		{"branch mismatch", GuardKindBranchMismatch, false},
		{"unmerged edits", GuardKindUnmergedEdits, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			original := "refusing to record branch agent/j/mul-6881: the delivered commit abc1234 no longer contains def5678; the task worktree is preserved at /tmp/wt"
			appended := AppendGuardMeta(original, tc.kind, tc.healAttempted)

			if !strings.Contains(appended, GuardMetaLine+":") {
				t.Fatalf("appended message carries no trailer:\n%s", appended)
			}
			gotKind, gotHeal, remainder, ok := ParseGuardMeta(appended)
			if !ok {
				t.Fatal("ParseGuardMeta did not recognise the trailer FormatGuardMeta wrote")
			}
			if gotKind != tc.kind {
				t.Errorf("kind = %q, want %q", gotKind, tc.kind)
			}
			if gotHeal != tc.healAttempted {
				t.Errorf("healAttempted = %v, want %v", gotHeal, tc.healAttempted)
			}
			if remainder != original {
				t.Errorf("remainder = %q, want the original message %q", remainder, original)
			}
		})
	}
}

func TestAppendGuardMetaLeavesUnclassifiedRefusalUntouched(t *testing.T) {
	original := "refusing to record branch agent/j/mul-6881: branch has no commit of this task's own to prove it by"
	if got := AppendGuardMeta(original, "", false); got != original {
		t.Errorf("unclassified refusal gained a trailer:\n%s", got)
	}
}

func TestParseGuardMetaWithoutTrailer(t *testing.T) {
	original := "an ordinary failure message"
	kind, heal, remainder, ok := ParseGuardMeta(original)
	if ok {
		t.Error("ParseGuardMeta reported a trailer on a message with none")
	}
	if kind != "" || heal {
		t.Errorf("kind = %q, heal = %v, want empty and false", kind, heal)
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
	withTrailer := "the refusal prose\n" + GuardMetaLine + ": kind=" + GuardKindAncestorBreak + " heal_attempted=1"
	want := "the refusal prose"
	if got := StripGuardMeta(withTrailer); got != want {
		t.Errorf("StripGuardMeta = %q, want %q", got, want)
	}
}

func TestParseGuardMetaToleratesUnknownKind(t *testing.T) {
	// A newer daemon may classify a shape this build does not know. The
	// field's purpose is visibility, not enumeration enforcement: the value
	// surfaces verbatim.
	line := GuardMetaLine + ": kind=guard_kind_from_the_future heal_attempted=0"
	kind, heal, _, ok := ParseGuardMeta(line)
	if !ok {
		t.Fatal("trailer not recognised")
	}
	if kind != "guard_kind_from_the_future" {
		t.Errorf("kind = %q, want the verbatim unknown value", kind)
	}
	if heal {
		t.Error("heal = true, want false")
	}
}

func TestParseGuardMetaOnlyStripsTheTrailerLine(t *testing.T) {
	prose := "first line mentions " + GuardMetaLine + " in passing\nsecond line"
	withTrailer := prose + "\n" + GuardMetaLine + ": kind=" + GuardKindBranchMismatch + " heal_attempted=0"
	_, _, remainder, ok := ParseGuardMeta(withTrailer)
	if !ok {
		t.Fatal("trailer not recognised")
	}
	if remainder != prose {
		t.Errorf("remainder = %q, want the prose kept intact:\n%s", remainder, prose)
	}
}
