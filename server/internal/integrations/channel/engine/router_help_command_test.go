package engine

import (
	"context"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/integrations/channel"
)

// withSlashFeedback re-registers the harness resolver set with the
// slash-command opt-in, the way the Feishu adapter wires it in production.
// The default harness stays opt-out so every pre-existing test keeps the
// legacy "slash text is an ordinary message" behavior.
func (h *harness) withSlashFeedback() {
	h.router.Register(channel.TypeFeishu, ResolverSet{
		Installation: h.inst,
		Identity:     h.ident,
		Dedup:        h.dedup,
		Session:      h.binder,
		Audit:        h.audit,
		Replier:      h.replier,
		Typing:       h.typing,
		Media:        h.media,
		OriginType:   "lark_chat",
		// SlashCommandFeedback: true
		SlashCommandFeedback: true,
	})
}

func commandMessage(t *testing.T, text string) channel.InboundMessage {
	t.Helper()
	msg := p2pMessage(t)
	msg.Text = text
	msg.CommandText = text
	return msg
}

func TestRouter_HelpCommand_AnswersWithoutSessionWrite(t *testing.T) {
	h := newHarness(t)
	h.withSlashFeedback()
	if err := h.router.Handle(context.Background(), commandMessage(t, "/help")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if h.dedup.marks() != 1 {
		t.Fatalf("help must finalize Mark (1), got %d", h.dedup.marks())
	}
	if h.binder.ensureCalls != 0 || h.binder.startCalls != 0 {
		t.Fatalf("help must not touch the session: ensure=%d start=%d", h.binder.ensureCalls, h.binder.startCalls)
	}
	if h.media.calls() != 0 || h.typing.calls() != 0 {
		t.Fatal("help must not resolve media or show typing")
	}
	if !waitFor(time.Second, func() bool {
		for _, r := range h.replier.calls() {
			if r.Outcome != OutcomeHelp || len(r.HelpCommands) != 3 {
				continue
			}
			if r.HelpCommands[0].Name != "/help" || r.HelpCommands[1].Name != "/new" || r.HelpCommands[2].Name != "/issue" {
				t.Fatalf("help card commands out of registry order: %+v", r.HelpCommands)
			}
			return true
		}
		return false
	}) {
		t.Fatalf("expected a help reply, got %+v", h.replier.calls())
	}
}

func TestRouter_HelpCommand_ToleratesArguments(t *testing.T) {
	h := newHarness(t)
	h.withSlashFeedback()
	if err := h.router.Handle(context.Background(), commandMessage(t, "/help 新对话怎么用")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !waitFor(time.Second, func() bool {
		for _, r := range h.replier.calls() {
			return r.Outcome == OutcomeHelp
		}
		return false
	}) {
		t.Fatalf("expected the first token /help to classify as help, got %+v", h.replier.calls())
	}
}

func TestRouter_UnknownCommand_GuidesToHelp(t *testing.T) {
	h := newHarness(t)
	h.withSlashFeedback()
	if err := h.router.Handle(context.Background(), commandMessage(t, "/stop 一切")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if h.dedup.marks() != 1 {
		t.Fatalf("unknown command must finalize Mark (1), got %d", h.dedup.marks())
	}
	if h.binder.ensureCalls != 0 || h.binder.startCalls != 0 {
		t.Fatalf("unknown command must not touch the session: ensure=%d start=%d", h.binder.ensureCalls, h.binder.startCalls)
	}
	if !waitFor(time.Second, func() bool {
		for _, r := range h.replier.calls() {
			return r.Outcome == OutcomeUnknownCommand && r.CommandToken == "/stop"
		}
		return false
	}) {
		t.Fatalf("expected unknown-command guidance for /stop, got %+v", h.replier.calls())
	}
}

// Unknown classification shares the /issue parser's precision: "/ISSUE" is
// not "/issue", so it is an unknown command — guided to /help, never
// silently dispatched as an issue or as agent input.
func TestRouter_UnknownCommand_CaseSensitive(t *testing.T) {
	h := newHarness(t)
	h.withSlashFeedback()
	if err := h.router.Handle(context.Background(), commandMessage(t, "/ISSUE 修复登录页")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !waitFor(time.Second, func() bool {
		for _, r := range h.replier.calls() {
			return r.Outcome == OutcomeUnknownCommand && r.CommandToken == "/ISSUE"
		}
		return false
	}) {
		t.Fatalf("expected /ISSUE to classify unknown, got %+v", h.replier.calls())
	}
	if h.issues.called {
		t.Fatal("unknown case-variant must not create an issue")
	}
}

// Without the platform opt-in, leading slash text keeps reaching the agent as
// an ordinary message — the classification must never silently swallow input
// on a channel whose replier cannot render the feedback.
func TestRouter_SlashFeedbackDisabled_KeepsLegacyIngest(t *testing.T) {
	for _, text := range []string{"/help", "/stop 一切"} {
		h := newHarness(t)
		if err := h.router.Handle(context.Background(), commandMessage(t, text)); err != nil {
			t.Fatalf("unexpected error for %q: %v", text, err)
		}
		if h.binder.ensureCalls != 1 {
			t.Fatalf("%q must keep the ordinary ingest path (ensure=1), got %d", text, h.binder.ensureCalls)
		}
		if !waitFor(time.Second, func() bool {
			for _, r := range h.replier.calls() {
				return r.Outcome == OutcomeIngested
			}
			return false
		}) {
			t.Fatalf("expected %q to ingest normally, got %+v", text, h.replier.calls())
		}
	}
}

func TestRouter_HelpInGroupUnaddressed_Drops(t *testing.T) {
	h := newHarness(t)
	h.withSlashFeedback()
	msg := commandMessage(t, "/help")
	msg.Source.ChatType = channel.ChatTypeGroup
	msg.AddressedToBot = false
	if err := h.router.Handle(context.Background(), msg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r, _ := h.audit.last(); r != DropReasonNotAddressedInGroup {
		t.Fatalf("expected not_addressed_in_group, got %q", r)
	}
	if !waitFor(time.Second, func() bool { return len(h.replier.calls()) == 1 && h.replier.calls()[0].Outcome == OutcomeDropped }) {
		t.Fatalf("expected silent drop, got %+v", h.replier.calls())
	}
}

// Identity wins over the menu: an unbound sender asking /help gets the
// binding prompt, because no command is usable without a binding.
func TestRouter_HelpUnboundSender_NeedsBinding(t *testing.T) {
	h := newHarness(t)
	h.withSlashFeedback()
	h.ident.err = ErrSenderUnbound
	if err := h.router.Handle(context.Background(), commandMessage(t, "/help")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !waitFor(time.Second, func() bool {
		for _, r := range h.replier.calls() {
			return r.Outcome == OutcomeNeedsBinding
		}
		return false
	}) {
		t.Fatalf("expected binding prompt to win over help, got %+v", h.replier.calls())
	}
	if h.binder.ensureCalls != 0 {
		t.Fatalf("unbound help must not touch the session, ensure=%d", h.binder.ensureCalls)
	}
}

// Registered non-help commands keep their existing pipeline handling even
// with feedback enabled: /issue still creates the issue.
func TestRouter_IssueCommand_NotIntercepted(t *testing.T) {
	h := newHarness(t)
	h.withSlashFeedback()
	h.binder.parseIssue = true
	h.media.noMedia = true
	if err := h.router.Handle(context.Background(), commandMessage(t, "/issue 修复登录页")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !h.issues.called {
		t.Fatal("/issue must keep creating the issue")
	}
	if h.binder.ensureCalls != 1 {
		t.Fatalf("/issue must go through the ordinary append path, ensure=%d", h.binder.ensureCalls)
	}
}

// Bare /clear keeps its fresh-pending handling — the control parser consumes
// it before the registry ever sees it.
func TestRouter_ClearCommand_NotClassifiedUnknown(t *testing.T) {
	h := newHarness(t)
	h.withSlashFeedback()
	if err := h.router.Handle(context.Background(), commandMessage(t, "/clear")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !waitFor(time.Second, func() bool {
		for _, r := range h.replier.calls() {
			return r.Outcome == OutcomeFreshPending
		}
		return false
	}) {
		t.Fatalf("expected fresh-pending for /clear, got %+v", h.replier.calls())
	}
}

// Prose that merely mentions a slash command later in the body is not a
// command — it ingests normally.
func TestRouter_MidBodySlashMention_IngestsNormally(t *testing.T) {
	h := newHarness(t)
	h.withSlashFeedback()
	if err := h.router.Handle(context.Background(), commandMessage(t, "你可以输入 /help 查看命令")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !waitFor(time.Second, func() bool {
		for _, r := range h.replier.calls() {
			return r.Outcome == OutcomeIngested
		}
		return false
	}) {
		t.Fatalf("expected ordinary ingest, got %+v", h.replier.calls())
	}
}

// AC4 contract: a help-card button click is synthesized with the command as
// BOTH Text and CommandText and AddressedToBot set, which routes it through
// the identical /new pipeline as a typed command — same StartSession call
// shape (bare, PersistMessage=false), same OutcomeChatStarted reply. No
// copied business logic exists anywhere on the button path.
func TestRouter_CardButtonNew_SharesTypedDispatchPath(t *testing.T) {
	h := newHarness(t)
	h.withSlashFeedback()

	// What the Lark adapter synthesizes for a validated /new button click.
	clicked := commandMessage(t, "/new")
	clicked.Source.ChatType = channel.ChatTypeGroup
	clicked.AddressedToBot = true

	h.media.noMedia = true
	if err := h.router.Handle(context.Background(), clicked); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if h.binder.startCalls != 1 {
		t.Fatalf("button /new must start a session exactly once, got %d", h.binder.startCalls)
	}
	if h.binder.lastStart.PersistMessage {
		t.Fatal("bare button /new must not persist a first turn, matching a typed /new")
	}
	if !waitFor(time.Second, func() bool {
		for _, r := range h.replier.calls() {
			return r.Outcome == OutcomeChatStarted
		}
		return false
	}) {
		t.Fatalf("expected chat_started, got %+v", h.replier.calls())
	}
}
