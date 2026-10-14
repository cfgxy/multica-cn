package handler

import (
	"context"
	"log/slog"
	"strings"

	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// RUYI-667: server-side suppression of same-issue agent self-triggers
// (RUYI-652 Owner decision 2, 2026-10-10). A comment authored by an agent
// whose trigger target is the author itself would re-fire the very run that
// wrote the comment — the paste-a-self-mention-loop the prompt-level mention
// hygiene rules defend against by hand. When the author has an ACTIVE run on
// the comment's issue, that trigger is suppressed; a cross-issue self-wake
// (the parent-handoff channel this workspace's collaboration contract depends
// on) stays admitted, because the exemption channel is exactly "the comment's
// issue != the issue of the author's active run".
//
// Suppression is VISIBLE, never a silent drop: an explicit mention target is
// rewritten to blocked/self_trigger_suppressed (the dispatch/reason.go:71
// semantics the squad-leader guard already reports), so the trigger-outcome
// channel and the composer preview both explain why nothing ran. Implicit
// routing has no per-target outcome channel by design (MUL-4525 §2: a route
// the author never named reports no per-target outcome), so there the trigger
// is simply removed.
//
// The check reuses HasActiveTaskForIssueAndAgent — "the author's active run is
// on this issue" is exactly "the (issue, agent) pair has a non-terminal task".
// A query failure admits the trigger (fail-open): the alternative mis-kills a
// legal cross-issue handoff wake during a DB hiccup, which is the failure this
// feature must never cause; a leaked same-issue self-trigger still has the
// one-pending-task unique index and the prompt hygiene as backstops.
//
// Rollback: cfg.SelfTriggerSuppressionEnabled (MULTICA_SELF_TRIGGER_SUPPRESSION_ENABLED,
// default on). Off means the filter is an identity and every path behaves
// byte-for-byte as before.

// filterSameIssueSelfTriggers is the pure decision core. enabled=false makes
// it an identity (acceptance: switch-off equals current behavior). Triggers
// whose executing agent is authorAgentID are removed when authorActiveOnIssue
// holds; every mention target whose TargetID or ExecAgentID matches is marked
// blocked/self_trigger_suppressed with ExecAgentID cleared — clearing it is
// load-bearing, because commentTriggerOutcomes treats a target with an
// ExecAgentID but no enqueue result as "composer-suppressed" and drops the
// outcome, which would turn the suppression into exactly the silent no-op it
// must not be. Targets that already carry a terminal status keep it: the
// squad-leader heuristic (MUL-4525) owns its more precise verdicts
// (deferred/already_active) and this filter never rewrites them.
func filterSameIssueSelfTriggers(triggers []commentAgentTrigger, targets []commentMentionTarget, authorAgentID string, authorActiveOnIssue, enabled bool) ([]commentAgentTrigger, []commentMentionTarget) {
	if !enabled || authorAgentID == "" || !authorActiveOnIssue {
		return triggers, targets
	}
	suppressed := false
	filtered := make([]commentAgentTrigger, 0, len(triggers))
	for _, trigger := range triggers {
		if strings.EqualFold(uuidToString(trigger.Agent.ID), authorAgentID) {
			suppressed = true
			continue
		}
		filtered = append(filtered, trigger)
	}
	if !suppressed {
		return triggers, targets
	}
	for i := range targets {
		t := &targets[i]
		if t.Status != "" {
			continue
		}
		if strings.EqualFold(t.ExecAgentID, authorAgentID) || strings.EqualFold(t.TargetID, authorAgentID) {
			t.ExecAgentID = ""
			t.Status = DispatchBlocked
			t.ReasonCode = ReasonSelfTriggerSuppressed
		}
	}
	return filtered, targets
}

// suppressSameIssueSelfTriggers resolves whether the author agent has an
// active run on this issue and applies filterSameIssueSelfTriggers. Non-agent
// authors can never self-trigger as an agent, so they short-circuit before
// any query.
func (h *Handler) suppressSameIssueSelfTriggers(ctx context.Context, issue db.Issue, triggers []commentAgentTrigger, targets []commentMentionTarget, actorType, actorID string) ([]commentAgentTrigger, []commentMentionTarget) {
	if !h.cfg.SelfTriggerSuppressionEnabled || actorType != "agent" || actorID == "" || len(triggers) == 0 {
		return triggers, targets
	}
	authorUUID, err := util.ParseUUID(actorID)
	if err != nil {
		return triggers, targets
	}
	active, err := h.hasActiveTaskForIssueAndAgent(ctx, issue.ID, authorUUID)
	if err != nil || !active {
		if err != nil {
			slog.WarnContext(ctx, "same-issue self-trigger check failed; admitting triggers (fail-open)",
				"issue_id", uuidToString(issue.ID), "agent_id", actorID, "error", err)
		}
		return triggers, targets
	}
	return filterSameIssueSelfTriggers(triggers, targets, actorID, true, true)
}
