package lark

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// fakeQueries is the unit-test seam for DispatcherQueries. Each field
// is the canned response the fake returns from the corresponding
// method; ErrNoRows variants pin specific failure modes.
//
// Dedup state is modeled accurately: each row in `dedup` is either
// in-flight (processed=false) or terminal (processed=true), matching
// the lark_inbound_message_dedup.processed_at semantics. Tests that
// want to pre-seed a permanently-processed message (simulating a Lark
// reconnect that replays an already-handled event) drop a `true` entry
// into the map. Tests that want to exercise the in-flight-claim path
// drop a `false` entry. Empty map → first delivery for every
// message_id (the default).
type fakeQueries struct {
	installationByApp  db.LarkInstallation
	installationErr    error
	userBinding        db.LarkUserBinding
	userBindingErr     error
	chatSession        db.ChatSession
	chatSessionErr     error
	dedup              map[string]bool // message_id → processed?
	dedupClaimErr      error
	dedupReclaim       bool // when true, in-flight rows are re-claimable (simulates staleness)
	calledUserBinding  int
	calledChatSession  int
	calledInstallation int
	calledClaim        int
	calledMark         int
	calledRelease      int
}

func (f *fakeQueries) GetLarkInstallationByAppID(ctx context.Context, appID string) (db.LarkInstallation, error) {
	f.calledInstallation++
	return f.installationByApp, f.installationErr
}

func (f *fakeQueries) GetLarkUserBindingByOpenID(ctx context.Context, arg db.GetLarkUserBindingByOpenIDParams) (db.LarkUserBinding, error) {
	f.calledUserBinding++
	return f.userBinding, f.userBindingErr
}

func (f *fakeQueries) GetChatSession(ctx context.Context, id pgtype.UUID) (db.ChatSession, error) {
	f.calledChatSession++
	return f.chatSession, f.chatSessionErr
}

// ClaimLarkInboundDedup mirrors the production query's three outcomes:
//
//   - Row not present → INSERT succeeds → returns the row.
//   - Row present, processed=false, dedupReclaim=true → staleness
//     fallback re-takes the claim → returns the row.
//   - Row present otherwise → ON CONFLICT WHERE filter excludes the
//     UPDATE → RETURNING returns 0 rows → pgx.ErrNoRows.
func (f *fakeQueries) ClaimLarkInboundDedup(ctx context.Context, messageID string) (db.LarkInboundMessageDedup, error) {
	f.calledClaim++
	if f.dedupClaimErr != nil {
		return db.LarkInboundMessageDedup{}, f.dedupClaimErr
	}
	if f.dedup == nil {
		f.dedup = map[string]bool{}
	}
	processed, exists := f.dedup[messageID]
	if !exists {
		f.dedup[messageID] = false
		return db.LarkInboundMessageDedup{MessageID: messageID}, nil
	}
	if !processed && f.dedupReclaim {
		// In-flight claim re-taken (staleness fallback in prod).
		return db.LarkInboundMessageDedup{MessageID: messageID}, nil
	}
	return db.LarkInboundMessageDedup{}, pgx.ErrNoRows
}

func (f *fakeQueries) MarkLarkInboundDedupProcessed(ctx context.Context, messageID string) error {
	f.calledMark++
	if f.dedup == nil {
		f.dedup = map[string]bool{}
	}
	f.dedup[messageID] = true
	return nil
}

func (f *fakeQueries) ReleaseLarkInboundDedup(ctx context.Context, messageID string) error {
	f.calledRelease++
	if f.dedup == nil {
		return nil
	}
	// Mirror the SQL guard: only delete unprocessed rows.
	if processed, ok := f.dedup[messageID]; ok && !processed {
		delete(f.dedup, messageID)
	}
	return nil
}

// fakeChat is a stub ChatSessionService that records what the
// dispatcher asked of it and returns canned outcomes.
type fakeChat struct {
	ensureID         pgtype.UUID
	ensureErr        error
	appendResult     AppendResult
	appendErr        error
	calledEnsure     int
	calledAppend     int
	lastAppendParams AppendUserMessageParams
	lastEnsureParams EnsureChatSessionParams
}

func (f *fakeChat) EnsureChatSession(ctx context.Context, p EnsureChatSessionParams) (pgtype.UUID, error) {
	f.calledEnsure++
	f.lastEnsureParams = p
	return f.ensureID, f.ensureErr
}

func (f *fakeChat) AppendUserMessage(ctx context.Context, p AppendUserMessageParams) (AppendResult, error) {
	f.calledAppend++
	f.lastAppendParams = p
	return f.appendResult, f.appendErr
}

type fakeAudit struct {
	drops []AuditDropParams
}

func (f *fakeAudit) RecordDrop(ctx context.Context, p AuditDropParams) error {
	f.drops = append(f.drops, p)
	return nil
}

type fakeIssueCreator struct {
	called int
	params service.IssueCreateParams
	result service.IssueCreateResult
	err    error
}

func (f *fakeIssueCreator) Create(ctx context.Context, p service.IssueCreateParams, _ service.IssueCreateOpts) (service.IssueCreateResult, error) {
	f.called++
	f.params = p
	return f.result, f.err
}

type fakeEnqueuer struct {
	called int
	task   db.AgentTaskQueue
	err    error
}

func (f *fakeEnqueuer) EnqueueChatTask(ctx context.Context, _ db.ChatSession) (db.AgentTaskQueue, error) {
	f.called++
	return f.task, f.err
}

// validUUID builds a deterministic Valid pgtype.UUID from the supplied
// byte. Useful for distinguishing IDs in assertions.
func validUUID(b byte) pgtype.UUID {
	var u pgtype.UUID
	for i := range u.Bytes {
		u.Bytes[i] = b
	}
	u.Valid = true
	return u
}

func activeInstallation() db.LarkInstallation {
	return db.LarkInstallation{
		ID:              validUUID(0x11),
		WorkspaceID:     validUUID(0x22),
		AgentID:         validUUID(0x33),
		InstallerUserID: validUUID(0x99),
		Status:          string(InstallationActive),
	}
}

func boundUser() db.LarkUserBinding {
	return db.LarkUserBinding{
		ID:             validUUID(0x44),
		WorkspaceID:    validUUID(0x22),
		MulticaUserID:  validUUID(0x55),
		InstallationID: validUUID(0x11),
		LarkOpenID:     "ou_user_a",
	}
}

func TestDispatcher_UnknownAppDropped(t *testing.T) {
	queries := &fakeQueries{installationErr: pgx.ErrNoRows}
	audit := &fakeAudit{}
	d := &Dispatcher{Queries: queries, Audit: audit}

	res, err := d.Handle(context.Background(), InboundMessage{
		AppID:     "missing",
		EventType: "im.message.receive_v1",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Outcome != OutcomeDropped || res.DropReason != DropReasonInvalidEvent {
		t.Fatalf("unexpected outcome: %+v", res)
	}
	if len(audit.drops) != 1 || audit.drops[0].Reason != DropReasonInvalidEvent {
		t.Fatalf("expected one invalid_event audit row, got %+v", audit.drops)
	}
	if audit.drops[0].InstallationID.Valid {
		t.Fatalf("audit row should omit installation_id for unknown app: %+v", audit.drops[0])
	}
}

func TestDispatcher_RevokedInstallationDropped(t *testing.T) {
	inst := activeInstallation()
	inst.Status = string(InstallationRevoked)
	queries := &fakeQueries{installationByApp: inst}
	audit := &fakeAudit{}
	d := &Dispatcher{Queries: queries, Audit: audit}

	res, err := d.Handle(context.Background(), InboundMessage{AppID: "ok"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.DropReason != DropReasonRevokedInstallation {
		t.Fatalf("got drop reason %q", res.DropReason)
	}
	if len(audit.drops) != 1 || audit.drops[0].Reason != DropReasonRevokedInstallation {
		t.Fatalf("audit drops: %+v", audit.drops)
	}
}

func TestDispatcher_GroupWithoutMentionDropped(t *testing.T) {
	queries := &fakeQueries{installationByApp: activeInstallation()}
	audit := &fakeAudit{}
	d := &Dispatcher{Queries: queries, Audit: audit}

	res, _ := d.Handle(context.Background(), InboundMessage{
		AppID:          "ok",
		ChatType:       ChatTypeGroup,
		AddressedToBot: false,
	})
	if res.DropReason != DropReasonNotAddressedInGroup {
		t.Fatalf("got drop reason %q", res.DropReason)
	}
	if queries.calledUserBinding != 0 {
		t.Fatalf("identity check should be skipped before group filter, got %d calls", queries.calledUserBinding)
	}
}

func TestDispatcher_UnboundUserAsksForBinding(t *testing.T) {
	queries := &fakeQueries{
		installationByApp: activeInstallation(),
		userBindingErr:    pgx.ErrNoRows,
	}
	audit := &fakeAudit{}
	d := &Dispatcher{Queries: queries, Audit: audit}

	res, _ := d.Handle(context.Background(), InboundMessage{
		AppID:        "ok",
		ChatType:     ChatTypeP2P,
		SenderOpenID: "ou_user_a",
	})
	if res.Outcome != OutcomeNeedsBinding {
		t.Fatalf("expected OutcomeNeedsBinding, got %q", res.Outcome)
	}
	if res.DropReason != DropReasonUnboundUser {
		t.Fatalf("expected unbound_user drop reason, got %q", res.DropReason)
	}
	if res.SenderOpenID != "ou_user_a" {
		t.Fatalf("sender propagation broken: %q", res.SenderOpenID)
	}
	if len(audit.drops) != 1 || audit.drops[0].Reason != DropReasonUnboundUser {
		t.Fatalf("expected one unbound_user audit row, got %+v", audit.drops)
	}
}

func TestDispatcher_PlainMessageEnqueuesTask(t *testing.T) {
	sessionID := validUUID(0x66)
	queries := &fakeQueries{
		installationByApp: activeInstallation(),
		userBinding:       boundUser(),
		chatSession:       db.ChatSession{ID: sessionID, AgentID: validUUID(0x33)},
	}
	chat := &fakeChat{
		ensureID:     sessionID,
		appendResult: AppendResult{},
	}
	enq := &fakeEnqueuer{task: db.AgentTaskQueue{ID: validUUID(0x77)}}
	d := &Dispatcher{
		Queries:     queries,
		Chat:        chat,
		Audit:       &fakeAudit{},
		TaskService: enq,
	}

	res, err := d.Handle(context.Background(), InboundMessage{
		AppID:        "ok",
		ChatType:     ChatTypeP2P,
		SenderOpenID: "ou_user_a",
		Body:         "hi bot",
		MessageID:    "msg-1",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Outcome != OutcomeIngested {
		t.Fatalf("expected ingested, got %q", res.Outcome)
	}
	if !res.TaskID.Valid || res.TaskID != enq.task.ID {
		t.Fatalf("task id propagation broken: %+v", res.TaskID)
	}
	// For p2p the session creator should be the bound user, not the
	// installer — verifies the chat-type branch in Handle.
	if chat.lastEnsureParams.Sender != queries.userBinding.MulticaUserID {
		t.Fatalf("p2p session creator should be sender; got %+v", chat.lastEnsureParams.Sender)
	}
}

func TestDispatcher_GroupMessageUsesInstallerAsCreator(t *testing.T) {
	inst := activeInstallation()
	sessionID := validUUID(0x66)
	queries := &fakeQueries{
		installationByApp: inst,
		userBinding:       boundUser(),
		chatSession:       db.ChatSession{ID: sessionID, AgentID: inst.AgentID},
	}
	chat := &fakeChat{ensureID: sessionID, appendResult: AppendResult{}}
	d := &Dispatcher{
		Queries:     queries,
		Chat:        chat,
		Audit:       &fakeAudit{},
		TaskService: &fakeEnqueuer{task: db.AgentTaskQueue{ID: validUUID(0x77)}},
	}

	_, _ = d.Handle(context.Background(), InboundMessage{
		AppID:          "ok",
		ChatType:       ChatTypeGroup,
		AddressedToBot: true,
		SenderOpenID:   "ou_user_a",
		Body:           "hey",
		MessageID:      "msg-g",
	})
	if chat.lastEnsureParams.Sender != inst.InstallerUserID {
		t.Fatalf("group session creator should be installer; got %+v want %+v",
			chat.lastEnsureParams.Sender, inst.InstallerUserID)
	}
}

func TestDispatcher_DedupHitDoesNotEnqueue(t *testing.T) {
	// Pre-seed the dedup table so the top-level dedup gate trips on
	// the first Handle call — simulates a Lark reconnect replaying an
	// event we already processed in a previous run.
	queries := &fakeQueries{
		installationByApp: activeInstallation(),
		userBinding:       boundUser(),
		dedup:             map[string]bool{"msg-dup": true},
	}
	chat := &fakeChat{
		ensureID: validUUID(0x66),
	}
	enq := &fakeEnqueuer{}
	audit := &fakeAudit{}
	d := &Dispatcher{
		Queries:     queries,
		Chat:        chat,
		Audit:       audit,
		TaskService: enq,
	}

	res, _ := d.Handle(context.Background(), InboundMessage{
		AppID:        "ok",
		ChatType:     ChatTypeP2P,
		SenderOpenID: "ou_user_a",
		Body:         "replay",
		MessageID:    "msg-dup",
	})
	if res.Outcome != OutcomeDropped || res.DropReason != DropReasonDuplicate {
		t.Fatalf("expected duplicate drop, got %+v", res)
	}
	if enq.called != 0 {
		t.Fatalf("dedup hit must not enqueue task, called=%d", enq.called)
	}
	if chat.calledEnsure != 0 || chat.calledAppend != 0 {
		t.Fatalf("dedup hit must short-circuit before chat lookup; ensure=%d append=%d",
			chat.calledEnsure, chat.calledAppend)
	}
	if queries.calledUserBinding != 0 {
		t.Fatalf("dedup hit must short-circuit before identity check, got %d binding calls",
			queries.calledUserBinding)
	}
	if len(audit.drops) != 1 || audit.drops[0].Reason != DropReasonDuplicate {
		t.Fatalf("expected duplicate audit row, got %+v", audit.drops)
	}
}

// TestDispatcher_DedupBeforeGroupFilter pins the §4.3 ordering: a
// replayed group event that was NOT addressed to the Bot must NOT
// re-write a not_addressed_in_group audit row on every reconnect, and
// must NOT re-trigger any binding-prompt side effect. The top-level
// dedup gate is what guarantees this; before this fix the group
// filter ran first and unbounded replays produced unbounded audit
// noise + reply-card spam.
func TestDispatcher_DedupBeforeGroupFilter(t *testing.T) {
	queries := &fakeQueries{
		installationByApp: activeInstallation(),
		dedup:             map[string]bool{"msg-replay": true},
	}
	audit := &fakeAudit{}
	d := &Dispatcher{Queries: queries, Audit: audit}

	res, _ := d.Handle(context.Background(), InboundMessage{
		AppID:          "ok",
		ChatType:       ChatTypeGroup,
		AddressedToBot: false,
		MessageID:      "msg-replay",
	})
	if res.DropReason != DropReasonDuplicate {
		t.Fatalf("dedup must beat group filter; got drop reason %q", res.DropReason)
	}
	if len(audit.drops) != 1 || audit.drops[0].Reason != DropReasonDuplicate {
		t.Fatalf("expected exactly one duplicate audit row, got %+v", audit.drops)
	}
}

// TestDispatcher_DedupBeforeIdentityCheck pins the same ordering for
// unbound users: a replayed event from an unbound open_id must not
// re-fire the OutcomeNeedsBinding path on every reconnect — that
// would spam the user with binding-prompt cards.
func TestDispatcher_DedupBeforeIdentityCheck(t *testing.T) {
	queries := &fakeQueries{
		installationByApp: activeInstallation(),
		userBindingErr:    pgx.ErrNoRows, // unbound — would normally trigger OutcomeNeedsBinding
		dedup:             map[string]bool{"msg-replay": true},
	}
	audit := &fakeAudit{}
	d := &Dispatcher{Queries: queries, Audit: audit}

	res, _ := d.Handle(context.Background(), InboundMessage{
		AppID:        "ok",
		ChatType:     ChatTypeP2P,
		SenderOpenID: "ou_user_a",
		MessageID:    "msg-replay",
	})
	if res.Outcome != OutcomeDropped || res.DropReason != DropReasonDuplicate {
		t.Fatalf("dedup must beat identity check; got %+v", res)
	}
	if queries.calledUserBinding != 0 {
		t.Fatalf("identity check must not run for a deduped replay, got %d calls",
			queries.calledUserBinding)
	}
}

func TestDispatcher_IssueCommandCreatesIssue(t *testing.T) {
	sessionID := validUUID(0x66)
	inst := activeInstallation()
	queries := &fakeQueries{
		installationByApp: inst,
		userBinding:       boundUser(),
		chatSession:       db.ChatSession{ID: sessionID, AgentID: inst.AgentID},
	}
	chat := &fakeChat{
		ensureID: sessionID,
		appendResult: AppendResult{
			IssueCommand: &IssueCommand{Title: "ship it", Description: "ship the thing"},
		},
	}
	issueSvc := &fakeIssueCreator{result: service.IssueCreateResult{Issue: db.Issue{ID: validUUID(0x88), Number: 42}}}
	d := &Dispatcher{
		Queries:      queries,
		Chat:         chat,
		Audit:        &fakeAudit{},
		IssueService: issueSvc,
		TaskService:  &fakeEnqueuer{task: db.AgentTaskQueue{ID: validUUID(0x77)}},
	}

	res, err := d.Handle(context.Background(), InboundMessage{
		AppID:        "ok",
		ChatType:     ChatTypeP2P,
		SenderOpenID: "ou_user_a",
		Body:         "/issue ship it\nship the thing",
		MessageID:    "msg-ic",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if issueSvc.called != 1 {
		t.Fatalf("expected IssueService.Create called once, got %d", issueSvc.called)
	}
	if issueSvc.params.Title != "ship it" || issueSvc.params.Description.String != "ship the thing" {
		t.Fatalf("wrong issue params: %+v", issueSvc.params)
	}
	if issueSvc.params.OriginType.String != originLarkChat {
		t.Fatalf("origin_type should be lark_chat, got %q", issueSvc.params.OriginType.String)
	}
	if !issueSvc.params.AssigneeType.Valid || issueSvc.params.AssigneeType.String != "agent" ||
		issueSvc.params.AssigneeID != inst.AgentID {
		t.Fatalf("assignee should default to the installation's agent: %+v", issueSvc.params)
	}
	if !res.IssueID.Valid || res.IssueNumber != 42 {
		t.Fatalf("issue id/number not propagated: %+v", res)
	}
}

func TestDispatcher_EmptyTitleSurfacesError(t *testing.T) {
	sessionID := validUUID(0x66)
	inst := activeInstallation()
	queries := &fakeQueries{
		installationByApp: inst,
		userBinding:       boundUser(),
		chatSession:       db.ChatSession{ID: sessionID, AgentID: inst.AgentID},
	}
	chat := &fakeChat{
		ensureID: sessionID,
		appendResult: AppendResult{
			IssueCommand: &IssueCommand{Title: ""},
		},
	}
	issueSvc := &fakeIssueCreator{}
	d := &Dispatcher{
		Queries:      queries,
		Chat:         chat,
		Audit:        &fakeAudit{},
		IssueService: issueSvc,
		TaskService:  &fakeEnqueuer{},
	}

	_, err := d.Handle(context.Background(), InboundMessage{
		AppID:        "ok",
		ChatType:     ChatTypeP2P,
		SenderOpenID: "ou_user_a",
		Body:         "/issue",
		MessageID:    "msg-empty",
	})
	if !errors.Is(err, ErrEmptyIssueTitle) {
		t.Fatalf("expected ErrEmptyIssueTitle wrapped, got %v", err)
	}
	if issueSvc.called != 0 {
		t.Fatalf("IssueService.Create must not run when title is empty")
	}
}

func TestDispatcher_AgentOfflineFallsThroughCleanly(t *testing.T) {
	sessionID := validUUID(0x66)
	queries := &fakeQueries{
		installationByApp: activeInstallation(),
		userBinding:       boundUser(),
		chatSession:       db.ChatSession{ID: sessionID, AgentID: validUUID(0x33)},
	}
	chat := &fakeChat{ensureID: sessionID, appendResult: AppendResult{}}
	enq := &fakeEnqueuer{err: service.ErrChatTaskAgentNoRuntime}
	d := &Dispatcher{
		Queries:     queries,
		Chat:        chat,
		Audit:       &fakeAudit{},
		TaskService: enq,
	}

	res, err := d.Handle(context.Background(), InboundMessage{
		AppID:        "ok",
		ChatType:     ChatTypeP2P,
		SenderOpenID: "ou_user_a",
		Body:         "hi",
		MessageID:    "msg-off",
	})
	if err != nil {
		t.Fatalf("offline path should not return error, got %v", err)
	}
	if res.Outcome != OutcomeAgentOffline {
		t.Fatalf("expected OutcomeAgentOffline, got %q", res.Outcome)
	}
	if res.ChatSessionID != sessionID {
		t.Fatalf("session id not propagated: %+v", res.ChatSessionID)
	}
}

func TestDispatcher_AgentArchivedSurfacesDistinctOutcome(t *testing.T) {
	sessionID := validUUID(0x66)
	queries := &fakeQueries{
		installationByApp: activeInstallation(),
		userBinding:       boundUser(),
		chatSession:       db.ChatSession{ID: sessionID, AgentID: validUUID(0x33)},
	}
	chat := &fakeChat{ensureID: sessionID, appendResult: AppendResult{}}
	enq := &fakeEnqueuer{err: service.ErrChatTaskAgentArchived}
	d := &Dispatcher{
		Queries:     queries,
		Chat:        chat,
		Audit:       &fakeAudit{},
		TaskService: enq,
	}

	res, err := d.Handle(context.Background(), InboundMessage{
		AppID:        "ok",
		ChatType:     ChatTypeP2P,
		SenderOpenID: "ou_user_a",
		Body:         "hi",
		MessageID:    "msg-arch",
	})
	if err != nil {
		t.Fatalf("archived path should not return error, got %v", err)
	}
	if res.Outcome != OutcomeAgentArchived {
		t.Fatalf("expected OutcomeAgentArchived, got %q", res.Outcome)
	}
}

func TestDispatcher_InfraFailureSurfacesError(t *testing.T) {
	// A DB / load / create failure from TaskService.EnqueueChatTask is
	// NOT a productizable state — the WS adapter must see a real
	// error so it can retry or page, not an "offline" card that
	// silently hides the outage.
	sessionID := validUUID(0x66)
	queries := &fakeQueries{
		installationByApp: activeInstallation(),
		userBinding:       boundUser(),
		chatSession:       db.ChatSession{ID: sessionID, AgentID: validUUID(0x33)},
	}
	chat := &fakeChat{ensureID: sessionID, appendResult: AppendResult{}}
	infraErr := errors.New("create chat task: connection refused")
	enq := &fakeEnqueuer{err: infraErr}
	d := &Dispatcher{
		Queries:     queries,
		Chat:        chat,
		Audit:       &fakeAudit{},
		TaskService: enq,
	}

	_, err := d.Handle(context.Background(), InboundMessage{
		AppID:        "ok",
		ChatType:     ChatTypeP2P,
		SenderOpenID: "ou_user_a",
		Body:         "hi",
		MessageID:    "msg-infra",
	})
	if err == nil {
		t.Fatalf("infra failure should surface as error, got nil")
	}
	if !errors.Is(err, infraErr) {
		t.Fatalf("infra error should propagate (errors.Is), got %v", err)
	}
}

// TestDispatcher_EnsureChatSessionFailureReleasesClaim is the
// regression for the dedup-blocker Elon flagged in PR #3277: the
// pre-fix Dispatcher inserted the dedup row before EnsureChatSession,
// so an infra error in EnsureChatSession would leave a permanent
// dedup row behind and the WS adapter's retry would be mis-classified
// as a duplicate — the user's message would be silently lost.
//
// With the two-phase claim/Release contract, the first attempt's
// claim is released, and the retry must observe a fresh first
// delivery: identity check + EnsureChatSession + AppendUserMessage
// run normally, no duplicate drop.
func TestDispatcher_EnsureChatSessionFailureReleasesClaim(t *testing.T) {
	sessionID := validUUID(0x66)
	queries := &fakeQueries{
		installationByApp: activeInstallation(),
		userBinding:       boundUser(),
		chatSession:       db.ChatSession{ID: sessionID, AgentID: validUUID(0x33)},
	}
	chat := &fakeChat{
		ensureID:  sessionID,
		ensureErr: errors.New("ensure chat session: connection refused"),
	}
	d := &Dispatcher{
		Queries:     queries,
		Chat:        chat,
		Audit:       &fakeAudit{},
		TaskService: &fakeEnqueuer{task: db.AgentTaskQueue{ID: validUUID(0x77)}},
	}

	// First attempt — infra error in EnsureChatSession.
	_, err := d.Handle(context.Background(), InboundMessage{
		AppID:        "ok",
		ChatType:     ChatTypeP2P,
		SenderOpenID: "ou_user_a",
		Body:         "hi",
		MessageID:    "msg-retry",
	})
	if err == nil {
		t.Fatalf("first attempt should surface ensure-chat-session error, got nil")
	}
	if queries.calledMark != 0 {
		t.Fatalf("must not mark processed when no durable side effect landed; calledMark=%d", queries.calledMark)
	}
	if queries.calledRelease != 1 {
		t.Fatalf("must release the claim on pre-durable infra error; calledRelease=%d", queries.calledRelease)
	}
	if _, present := queries.dedup["msg-retry"]; present {
		t.Fatalf("release must delete the in-flight claim row; dedup=%+v", queries.dedup)
	}

	// Retry — same message_id, ensure succeeds this time. The claim
	// was released, so the retry must be able to re-claim and run
	// the full ingest pipeline. The bug being regressed would have
	// caused a DropReasonDuplicate here.
	chat.ensureErr = nil
	res, err := d.Handle(context.Background(), InboundMessage{
		AppID:        "ok",
		ChatType:     ChatTypeP2P,
		SenderOpenID: "ou_user_a",
		Body:         "hi",
		MessageID:    "msg-retry",
	})
	if err != nil {
		t.Fatalf("retry should succeed, got %v", err)
	}
	if res.Outcome != OutcomeIngested {
		t.Fatalf("retry must ingest; got outcome=%q reason=%q", res.Outcome, res.DropReason)
	}
	if chat.calledAppend != 1 {
		t.Fatalf("retry must reach AppendUserMessage; calledAppend=%d", chat.calledAppend)
	}
	if queries.calledMark != 1 {
		t.Fatalf("retry must mark processed; calledMark=%d", queries.calledMark)
	}
	if processed, ok := queries.dedup["msg-retry"]; !ok || !processed {
		t.Fatalf("retry must finalize claim as processed; dedup=%+v", queries.dedup)
	}

	// A third attempt with the same message_id (post-success replay)
	// must now be a duplicate-drop — the Mark from the retry is the
	// terminal state.
	queries.calledClaim = 0
	chat.calledAppend = 0
	res, err = d.Handle(context.Background(), InboundMessage{
		AppID:        "ok",
		ChatType:     ChatTypeP2P,
		SenderOpenID: "ou_user_a",
		Body:         "hi",
		MessageID:    "msg-retry",
	})
	if err != nil {
		t.Fatalf("post-success replay should not error, got %v", err)
	}
	if res.Outcome != OutcomeDropped || res.DropReason != DropReasonDuplicate {
		t.Fatalf("post-success replay must duplicate-drop; got %+v", res)
	}
	if chat.calledAppend != 0 {
		t.Fatalf("post-success replay must not re-append; calledAppend=%d", chat.calledAppend)
	}
}

// TestDispatcher_AppendUserMessageFailureReleasesClaim is the
// regression for the second variant of the dedup blocker: an infra
// error from AppendUserMessage (e.g. tx commit failure) must also
// release the claim so a retry can re-ingest. AppendUserMessage's
// transaction is atomic — an error means rollback, no chat_message
// landed.
func TestDispatcher_AppendUserMessageFailureReleasesClaim(t *testing.T) {
	sessionID := validUUID(0x66)
	queries := &fakeQueries{
		installationByApp: activeInstallation(),
		userBinding:       boundUser(),
		chatSession:       db.ChatSession{ID: sessionID, AgentID: validUUID(0x33)},
	}
	chat := &fakeChat{
		ensureID:  sessionID,
		appendErr: errors.New("create chat message: connection refused"),
	}
	d := &Dispatcher{
		Queries:     queries,
		Chat:        chat,
		Audit:       &fakeAudit{},
		TaskService: &fakeEnqueuer{task: db.AgentTaskQueue{ID: validUUID(0x77)}},
	}

	// First attempt — append fails.
	_, err := d.Handle(context.Background(), InboundMessage{
		AppID:        "ok",
		ChatType:     ChatTypeP2P,
		SenderOpenID: "ou_user_a",
		Body:         "hi",
		MessageID:    "msg-append-retry",
	})
	if err == nil {
		t.Fatalf("first attempt should surface append error, got nil")
	}
	if queries.calledMark != 0 {
		t.Fatalf("must not mark processed when AppendUserMessage rolled back; calledMark=%d", queries.calledMark)
	}
	if queries.calledRelease != 1 {
		t.Fatalf("must release the claim; calledRelease=%d", queries.calledRelease)
	}

	// Retry — same message_id, append succeeds.
	chat.appendErr = nil
	res, err := d.Handle(context.Background(), InboundMessage{
		AppID:        "ok",
		ChatType:     ChatTypeP2P,
		SenderOpenID: "ou_user_a",
		Body:         "hi",
		MessageID:    "msg-append-retry",
	})
	if err != nil {
		t.Fatalf("retry should succeed, got %v", err)
	}
	if res.Outcome != OutcomeIngested {
		t.Fatalf("retry must ingest; got %+v", res)
	}
	if chat.calledAppend != 2 {
		t.Fatalf("expected exactly two append attempts (1 failed + 1 retry); calledAppend=%d", chat.calledAppend)
	}
}

// TestDispatcher_DurableErrorMarksClaim pins the inverse of the
// release path: when AppendUserMessage has succeeded (chat_message
// committed) but a downstream step returns an error, the dispatcher
// MUST mark the claim processed. Otherwise a replay would re-process
// the message and write a second chat_message row.
func TestDispatcher_DurableErrorMarksClaim(t *testing.T) {
	sessionID := validUUID(0x66)
	queries := &fakeQueries{
		installationByApp: activeInstallation(),
		userBinding:       boundUser(),
		chatSession:       db.ChatSession{ID: sessionID, AgentID: validUUID(0x33)},
	}
	chat := &fakeChat{
		ensureID:     sessionID,
		appendResult: AppendResult{},
	}
	infraErr := errors.New("create chat task: connection refused")
	d := &Dispatcher{
		Queries:     queries,
		Chat:        chat,
		Audit:       &fakeAudit{},
		TaskService: &fakeEnqueuer{err: infraErr},
	}

	_, err := d.Handle(context.Background(), InboundMessage{
		AppID:        "ok",
		ChatType:     ChatTypeP2P,
		SenderOpenID: "ou_user_a",
		Body:         "hi",
		MessageID:    "msg-durable-err",
	})
	if !errors.Is(err, infraErr) {
		t.Fatalf("expected infra error to propagate, got %v", err)
	}
	if queries.calledRelease != 0 {
		t.Fatalf("must not release: chat_message already committed; calledRelease=%d", queries.calledRelease)
	}
	if queries.calledMark != 1 {
		t.Fatalf("must mark processed: chat_message committed before the enqueue error; calledMark=%d", queries.calledMark)
	}
	if processed, ok := queries.dedup["msg-durable-err"]; !ok || !processed {
		t.Fatalf("dedup row must end up processed=true; got %+v", queries.dedup)
	}
}

// TestDispatcher_DropMarksClaim pins that audit-drop branches (group
// filter, unbound user) finalize their claim as processed, so a
// reconnect replay does NOT re-write the audit row or re-fire any
// binding-prompt side effect. This is the "no audit / card spam"
// invariant from §4.3.
func TestDispatcher_DropMarksClaim(t *testing.T) {
	queries := &fakeQueries{
		installationByApp: activeInstallation(),
		userBindingErr:    pgx.ErrNoRows,
	}
	audit := &fakeAudit{}
	d := &Dispatcher{Queries: queries, Audit: audit}

	_, _ = d.Handle(context.Background(), InboundMessage{
		AppID:        "ok",
		ChatType:     ChatTypeP2P,
		SenderOpenID: "ou_user_a",
		MessageID:    "msg-unbound",
	})
	if queries.calledMark != 1 {
		t.Fatalf("unbound-user drop must mark claim processed; calledMark=%d", queries.calledMark)
	}
	if queries.calledRelease != 0 {
		t.Fatalf("unbound-user drop must not release; calledRelease=%d", queries.calledRelease)
	}
}

// TestDispatcher_EmptyMessageIDSkipsDedup pins that non-message
// events (no MessageID) bypass dedup entirely — there is no key to
// deduplicate by, and the dispatcher must not call Claim / Mark /
// Release for them.
func TestDispatcher_EmptyMessageIDSkipsDedup(t *testing.T) {
	queries := &fakeQueries{
		installationByApp: activeInstallation(),
	}
	d := &Dispatcher{Queries: queries, Audit: &fakeAudit{}}

	_, _ = d.Handle(context.Background(), InboundMessage{
		AppID:    "ok",
		ChatType: ChatTypeGroup, // group filter drops it
		// MessageID intentionally empty
	})
	if queries.calledClaim != 0 || queries.calledMark != 0 || queries.calledRelease != 0 {
		t.Fatalf("empty MessageID must skip dedup entirely; claim=%d mark=%d release=%d",
			queries.calledClaim, queries.calledMark, queries.calledRelease)
	}
}

// TestDispatcher_InFlightClaimDropsReplay covers the "another worker
// is processing" branch: a fresh in-flight claim (processed=false,
// not yet stale) must duplicate-drop a concurrent replay, NOT
// re-process. This is the protection against two replicas
// simultaneously consuming the same Lark event during a brief
// double-leased window.
func TestDispatcher_InFlightClaimDropsReplay(t *testing.T) {
	queries := &fakeQueries{
		installationByApp: activeInstallation(),
		userBinding:       boundUser(),
		// In-flight claim (processed=false) and reclaim disabled
		// (simulates "the row is fresh — not stale yet").
		dedup: map[string]bool{"msg-inflight": false},
	}
	chat := &fakeChat{}
	d := &Dispatcher{
		Queries:     queries,
		Chat:        chat,
		Audit:       &fakeAudit{},
		TaskService: &fakeEnqueuer{},
	}

	res, err := d.Handle(context.Background(), InboundMessage{
		AppID:        "ok",
		ChatType:     ChatTypeP2P,
		SenderOpenID: "ou_user_a",
		Body:         "race",
		MessageID:    "msg-inflight",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Outcome != OutcomeDropped || res.DropReason != DropReasonDuplicate {
		t.Fatalf("in-flight claim must drop the replay; got %+v", res)
	}
	if chat.calledEnsure != 0 || chat.calledAppend != 0 {
		t.Fatalf("in-flight drop must short-circuit before chat lookup; ensure=%d append=%d",
			chat.calledEnsure, chat.calledAppend)
	}
}

// TestDispatcher_StaleInFlightClaimReclaimable covers the
// crash-recovery branch: an in-flight claim older than the staleness
// TTL must be re-takeable so a process crash mid-pipeline does not
// leave the message stuck forever.
func TestDispatcher_StaleInFlightClaimReclaimable(t *testing.T) {
	sessionID := validUUID(0x66)
	queries := &fakeQueries{
		installationByApp: activeInstallation(),
		userBinding:       boundUser(),
		chatSession:       db.ChatSession{ID: sessionID, AgentID: validUUID(0x33)},
		dedup:             map[string]bool{"msg-stale": false},
		dedupReclaim:      true, // simulates received_at < now() - 60s
	}
	chat := &fakeChat{ensureID: sessionID, appendResult: AppendResult{}}
	d := &Dispatcher{
		Queries:     queries,
		Chat:        chat,
		Audit:       &fakeAudit{},
		TaskService: &fakeEnqueuer{task: db.AgentTaskQueue{ID: validUUID(0x77)}},
	}

	res, err := d.Handle(context.Background(), InboundMessage{
		AppID:        "ok",
		ChatType:     ChatTypeP2P,
		SenderOpenID: "ou_user_a",
		Body:         "after-crash retry",
		MessageID:    "msg-stale",
	})
	if err != nil {
		t.Fatalf("stale-claim retry should succeed, got %v", err)
	}
	if res.Outcome != OutcomeIngested {
		t.Fatalf("stale-claim retry must ingest; got %+v", res)
	}
	if queries.calledMark != 1 {
		t.Fatalf("stale-claim retry must mark processed; calledMark=%d", queries.calledMark)
	}
}
