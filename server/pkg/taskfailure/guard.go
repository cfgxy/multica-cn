package taskfailure

import (
	"fmt"
	"strconv"
	"strings"
)

// GuardKind classifies a delivery_guard refusal by its shape (RUYI-579 W3),
// so a failure event says which recovery applies instead of leaving the user
// to parse prose.
const (
	// GuardKindAncestorBreak: the delivered tip is the task branch's own, but
	// it lost the turn's base commit from its ancestry — the shape a
	// mid-turn reset/rebase onto the mainline produces. The finalize
	// self-heal re-anchors this shape when it can.
	GuardKindAncestorBreak = "ancestor_break"
	// GuardKindBranchMismatch: the worktree's delivered tip is not what the
	// branch points at — an off-branch or detached delivery. Held to the
	// pre-existing refusal behaviour.
	GuardKindBranchMismatch = "branch_mismatch"
	// GuardKindUnmergedEdits: the worktree still holds an unresolved merge,
	// which is never committed.
	GuardKindUnmergedEdits = "unmerged_edits"
)

// GuardMetaLine is the machine-readable trailer the daemon appends to a
// delivery_guard task failure message. The server parses it back out when it
// builds the task:failed event payload (guard_kind / guard_heal_attempted)
// and strips it from the user-facing error text. It is a wire convention
// between internal/daemon and internal/service — both ends live in this
// repo, so the format has no versioning beyond this constant.
const GuardMetaLine = "multica-delivery-guard"

// FormatGuardMeta renders the trailer for a delivery_guard failure with the
// given kind and self-heal outcome. An unclassified refusal (empty kind, no
// heal attempted) gets no trailer — there is nothing to say that the error
// prose does not already say.
func FormatGuardMeta(kind string, healAttempted bool) string {
	if kind == "" && !healAttempted {
		return ""
	}
	heal := 0
	if healAttempted {
		heal = 1
	}
	return fmt.Sprintf("%s: kind=%s heal_attempted=%d", GuardMetaLine, kind, heal)
}

// AppendGuardMeta attaches the trailer to a task failure message. An empty
// trailer leaves the message untouched.
func AppendGuardMeta(message, kind string, healAttempted bool) string {
	meta := FormatGuardMeta(kind, healAttempted)
	if meta == "" {
		return message
	}
	return message + "\n" + meta
}

// ParseGuardMeta extracts the trailer from a delivery_guard failure message,
// returning the kind, whether a self-heal was attempted, and the message
// with the trailer line removed. ok is false — and the message returned
// unchanged — when the message carries no trailer.
func ParseGuardMeta(message string) (kind string, healAttempted bool, remainder string, ok bool) {
	remainder = message
	idx := strings.LastIndex(message, "\n"+GuardMetaLine+":")
	if idx < 0 {
		if strings.HasPrefix(message, GuardMetaLine+":") {
			idx = 0
		} else {
			return "", false, message, false
		}
	}
	lineStart := idx
	if idx > 0 {
		lineStart++ // past the newline
	}
	line := message[lineStart:]
	remainder = message[:idx]

	fields := map[string]string{}
	for _, pair := range strings.Fields(line) {
		if pair == GuardMetaLine+":" {
			continue
		}
		key, value, found := strings.Cut(pair, "=")
		if found {
			fields[key] = value
		}
	}
	kind = fields["kind"]
	if kind != "" && !isGuardKind(kind) {
		// A kind this build does not know is still surfaced verbatim — the
		// field's purpose is visibility, not enumeration enforcement.
		kind = fields["kind"]
	}
	if heal, err := strconv.Atoi(fields["heal_attempted"]); err == nil {
		healAttempted = heal != 0
	}
	return kind, healAttempted, remainder, true
}

func isGuardKind(kind string) bool {
	switch kind {
	case GuardKindAncestorBreak, GuardKindBranchMismatch, GuardKindUnmergedEdits:
		return true
	}
	return false
}
