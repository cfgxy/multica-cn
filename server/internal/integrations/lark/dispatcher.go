package lark

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// InboundMessage is the normalized shape the WebSocket adapter hands
// to the Dispatcher. The adapter (Phase 2 PR) translates the raw Lark
// event payload into this struct; the Dispatcher does NOT know what a
// Lark event JSON object looks like. This keeps event-schema changes
// from rippling into business logic.
//
// AddressedToBot is the adapter's verdict on whether a group-chat
// message is an interaction with the Bot (@-mention or reply to a
// Bot card). For p2p messages this field is ignored.
type InboundMessage struct {
	EventType      string
	EventID        string
	AppID          string
	ChatID         ChatID
	ChatType       ChatType
	MessageID      string
	SenderOpenID   OpenID
	Body           string
	AddressedToBot bool
}

// Outcome categorizes what the Dispatcher decided to do with an
// inbound message. The WS adapter inspects this and chooses what to
// reply with on the Lark side.
type Outcome string

const (
	// OutcomeDropped — the message was not ingested (identity failed,
	// dedup hit, group filter, etc.). DispatchResult.DropReason holds
	// the audit category.
	OutcomeDropped Outcome = "dropped"

	// OutcomeNeedsBinding — the open_id is unbound; the WS adapter
	// should mint a binding token via BindingTokenService and send
	// the "click here to bind" card. DispatchResult.SenderOpenID and
	// .InstallationID are populated so the adapter can target the
	// reply.
	OutcomeNeedsBinding Outcome = "needs_binding"

	// OutcomeIngested — the message landed in chat_session and an
	// agent task was enqueued. Empty IssueCommand means a plain chat
	// message; non-empty means /issue ran (see IssueID for the new
	// issue's UUID).
	OutcomeIngested Outcome = "ingested"

	// OutcomeAgentOffline — the message landed in chat_session, but
	// the agent has no runtime bound at all (agent.runtime_id IS
	// NULL). The adapter should reply with "agent offline, will run
	// on next online." The chat_message row remains so the agent
	// picks it up on resume.
	//
	// IMPORTANT: this is NOT triggered when a daemon is merely
	// disconnected. If agent.runtime_id IS set, the chat task is
	// enqueued and waits for the daemon to claim it on next online;
	// that path returns OutcomeIngested with a TaskID.
	OutcomeAgentOffline Outcome = "agent_offline"

	// OutcomeAgentArchived — the message landed in chat_session, but
	// the agent has been archived. The adapter should reply with a
	// distinct copy ("this agent has been archived; ask an admin to
	// unarchive or rebind"). Kept separate from OutcomeAgentOffline
	// because the user-facing remediation differs.
	OutcomeAgentArchived Outcome = "agent_archived"
)

// DispatchResult is the typed return from Dispatcher.Handle. Callers
// (the WS adapter) consume this to drive their outbound side; nothing
// here implies the adapter MUST reply, only that it CAN.
type DispatchResult struct {
	Outcome        Outcome
	DropReason     DropReason
	InstallationID pgtype.UUID
	ChatSessionID  pgtype.UUID
	SenderOpenID   OpenID
	TaskID         pgtype.UUID
	IssueID        pgtype.UUID
	IssueNumber    int32
}

// IssueCreator is the narrow subset of service.IssueService the
// Dispatcher needs. Declared here as an interface so this package can
// be unit-tested without bringing the full service graph along.
type IssueCreator interface {
	Create(ctx context.Context, p service.IssueCreateParams, opts service.IssueCreateOpts) (service.IssueCreateResult, error)
}

// ChatTaskEnqueuer is the narrow subset of service.TaskService the
// Dispatcher needs. It exists for the same reason as IssueCreator:
// the Dispatcher is small enough that depending on the whole
// TaskService struct is gratuitous.
type ChatTaskEnqueuer interface {
	EnqueueChatTask(ctx context.Context, session db.ChatSession) (db.AgentTaskQueue, error)
}

// DispatcherQueries is the narrow subset of *db.Queries the Dispatcher
// needs for installation routing, identity lookup, dedup, and session
// reload. *db.Queries satisfies it directly; tests substitute a fake.
//
// Dedup is two-phase: ClaimLarkInboundDedup acquires an in-flight
// claim, then exactly one of MarkLarkInboundDedupProcessed (durable
// outcome) or ReleaseLarkInboundDedup (infra failure before durable
// outcome) finalizes it. The two-phase contract is what prevents an
// infra error mid-pipeline from permanently swallowing a message — the
// claim row is released so the next replay attempt can proceed instead
// of being mis-classified as a duplicate.
type DispatcherQueries interface {
	GetLarkInstallationByAppID(ctx context.Context, appID string) (db.LarkInstallation, error)
	GetLarkUserBindingByOpenID(ctx context.Context, arg db.GetLarkUserBindingByOpenIDParams) (db.LarkUserBinding, error)
	GetChatSession(ctx context.Context, id pgtype.UUID) (db.ChatSession, error)
	ClaimLarkInboundDedup(ctx context.Context, messageID string) (db.LarkInboundMessageDedup, error)
	MarkLarkInboundDedupProcessed(ctx context.Context, messageID string) error
	ReleaseLarkInboundDedup(ctx context.Context, messageID string) error
}

// Dispatcher is the single per-message entry point on the inbound
// path. It owns the order in which identity check, group filter,
// dedup, ingest, /issue, and task enqueue happen — the WS adapter
// MUST NOT bypass it. That ordering is the invariant that keeps the
// design's §4.3 safety property ("unbound users never reach
// chat_session") true at runtime.
type Dispatcher struct {
	Queries      DispatcherQueries
	Chat         ChatSessionService
	Audit        AuditLogger
	IssueService IssueCreator
	TaskService  ChatTaskEnqueuer
}

// Handle processes one inbound Lark message end-to-end. It never
// returns an error for "this message was dropped" — those are
// reported via Outcome + DropReason and a non-nil err is reserved for
// real infrastructure failures (DB down, etc.) that the WS adapter
// should retry.
//
// Dedup is two-phase. After the installation lookup, ClaimLarkInbound-
// Dedup acquires an in-flight claim on msg.MessageID. After the rest
// of the pipeline runs, the claim is finalized exactly once:
//
//   - MarkLarkInboundDedupProcessed — a durable outcome was reached
//     (audit drop row persisted, OR chat_message + session touched).
//     Future replays of this message_id are dropped as duplicates.
//
//   - ReleaseLarkInboundDedup — an infra error occurred BEFORE any
//     durable side effect. The claim row is deleted so the WS
//     adapter's retry can re-acquire it immediately; otherwise the
//     message would be permanently swallowed as a duplicate even
//     though it never actually landed in chat_session.
func (d *Dispatcher) Handle(ctx context.Context, msg InboundMessage) (DispatchResult, error) {
	// 1. Route to installation. The app_id is the only identifier
	//    that ties an event to its installation row. These two drop
	//    branches run BEFORE the dedup claim because they have no
	//    valid installation row to attach to — see the spec note on
	//    lark_inbound_audit allowing a NULL installation_id.
	inst, err := d.Queries.GetLarkInstallationByAppID(ctx, msg.AppID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			_ = d.Audit.RecordDrop(ctx, AuditDropParams{
				EventType:     msg.EventType,
				LarkEventID:   msg.EventID,
				LarkMessageID: msg.MessageID,
				ChatID:        msg.ChatID,
				Reason:        DropReasonInvalidEvent,
			})
			return DispatchResult{Outcome: OutcomeDropped, DropReason: DropReasonInvalidEvent}, nil
		}
		return DispatchResult{}, fmt.Errorf("load installation: %w", err)
	}
	if InstallationStatus(inst.Status) != InstallationActive {
		return d.drop(ctx, msg, inst.ID, DropReasonRevokedInstallation), nil
	}

	// 2. Two-phase dedup claim. Spec §4.3 puts this before group
	//    filter and identity check so a WebSocket reconnect that
	//    replays an event cannot:
	//      a) re-trigger the binding prompt for an unbound user, or
	//      b) re-write the not_addressed_in_group / unbound_user audit
	//         rows, or
	//      c) re-touch the chat_session for a bound message.
	//
	//    Empty MessageID means the event has no Lark message_id at all
	//    (non-message events, malformed payloads); skipping dedup is
	//    the safe default — we have no key to deduplicate by, and no
	//    claim to finalize at the end.
	claimed := false
	if msg.MessageID != "" {
		_, err := d.Queries.ClaimLarkInboundDedup(ctx, msg.MessageID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				// Either the row is processed_at IS NOT NULL
				// (terminal) or another worker is actively
				// processing. Either way, the right behavior is to
				// drop without re-doing the work.
				return d.drop(ctx, msg, inst.ID, DropReasonDuplicate), nil
			}
			return DispatchResult{}, fmt.Errorf("dedup claim: %w", err)
		}
		claimed = true
	}

	res, durable, err := d.processClaimed(ctx, msg, inst)

	if claimed {
		// Finalize exactly once. The decision rule is "was a durable
		// outcome reached" rather than "did Handle return nil err",
		// because some error paths (e.g. ErrEmptyIssueTitle wrapped
		// after AppendUserMessage already committed) sit *after* a
		// chat_message has already landed and must NOT be re-processed
		// on a subsequent replay.
		d.finalizeDedupClaim(ctx, msg.MessageID, durable)
	}

	return res, err
}

// processClaimed runs the post-dedup pipeline. It returns (result,
// durable, error) where durable=true means the run produced at least
// one persisted, user-visible side effect (audit drop row OR
// chat_message + session touch). The caller uses this to decide
// whether to Mark or Release the dedup claim.
//
// Boundary contract per step:
//
//   - Group filter / unbound-user drop → audit row written → durable.
//   - EnsureChatSession error → tx rolled back, no durable side effect.
//   - AppendUserMessage success → chat_message committed; everything
//     past this point is durable, including subsequent error returns
//     for /issue, session reload, and task enqueue.
func (d *Dispatcher) processClaimed(ctx context.Context, msg InboundMessage, inst db.LarkInstallation) (DispatchResult, bool, error) {
	// 3. Group-mention filter (group chats only). We do this BEFORE
	//    identity check so that an unbound user's idle group chatter
	//    never produces an "you need to bind" reply card spam — the
	//    Bot is not addressed, so we say nothing.
	if msg.ChatType == ChatTypeGroup && !msg.AddressedToBot {
		return d.drop(ctx, msg, inst.ID, DropReasonNotAddressedInGroup), true, nil
	}

	// 4. Identity check. A row in lark_user_binding means the open_id
	//    maps to a current workspace member (the composite FK to
	//    member cascades the binding away on membership revocation).
	binding, err := d.Queries.GetLarkUserBindingByOpenID(ctx, db.GetLarkUserBindingByOpenIDParams{
		InstallationID: inst.ID,
		LarkOpenID:     string(msg.SenderOpenID),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			_ = d.Audit.RecordDrop(ctx, AuditDropParams{
				InstallationID: inst.ID,
				ChatID:         msg.ChatID,
				EventType:      msg.EventType,
				LarkEventID:    msg.EventID,
				LarkMessageID:  msg.MessageID,
				Reason:         DropReasonUnboundUser,
			})
			return DispatchResult{
				Outcome:        OutcomeNeedsBinding,
				DropReason:     DropReasonUnboundUser,
				InstallationID: inst.ID,
				SenderOpenID:   msg.SenderOpenID,
			}, true, nil
		}
		return DispatchResult{}, false, fmt.Errorf("load user binding: %w", err)
	}

	// 5. Resolve the chat_session. For group chats, the session
	//    creator is the INSTALLER (stable workspace identity that
	//    won't cascade-delete when individual group members churn);
	//    for p2p, the sender is the one and only human in the chat
	//    so we use them.
	sessionCreator := binding.MulticaUserID
	if msg.ChatType == ChatTypeGroup {
		sessionCreator = inst.InstallerUserID
	}
	sessionID, err := d.Chat.EnsureChatSession(ctx, EnsureChatSessionParams{
		WorkspaceID:    inst.WorkspaceID,
		InstallationID: inst.ID,
		AgentID:        inst.AgentID,
		ChatID:         msg.ChatID,
		ChatType:       msg.ChatType,
		Sender:         sessionCreator,
	})
	if err != nil {
		// chat_session create + lark_chat_session_binding create are
		// in a single tx; an error here means the tx rolled back and
		// nothing landed. Safe to release the dedup claim.
		return DispatchResult{}, false, fmt.Errorf("ensure chat session: %w", err)
	}

	// 6. Append message — the durable transition point. After this
	//    returns nil, a chat_message row exists; any subsequent
	//    failure must NOT release the dedup claim, or a replay would
	//    re-write the same message into chat_session.
	appendRes, err := d.Chat.AppendUserMessage(ctx, AppendUserMessageParams{
		ChatSessionID: sessionID,
		Sender:        binding.MulticaUserID,
		Body:          msg.Body,
		LarkMessageID: msg.MessageID,
	})
	if err != nil {
		// AppendUserMessage's transaction either commits or rolls
		// back atomically; an error means rollback, so no
		// chat_message was written. Safe to release.
		return DispatchResult{}, false, fmt.Errorf("append user message: %w", err)
	}

	res := DispatchResult{
		Outcome:        OutcomeIngested,
		InstallationID: inst.ID,
		ChatSessionID:  sessionID,
		SenderOpenID:   msg.SenderOpenID,
	}

	// 7. /issue command, if present. chat_message is already durable
	//    above; from here all error returns must signal durable=true.
	if appendRes.IssueCommand != nil {
		issueRes, err := d.createIssueFromCommand(ctx, inst, binding.MulticaUserID, sessionID, *appendRes.IssueCommand)
		if err != nil {
			return DispatchResult{}, true, fmt.Errorf("create issue from command: %w", err)
		}
		res.IssueID = issueRes.Issue.ID
		res.IssueNumber = issueRes.Issue.Number
	}

	// 8. Enqueue the chat task that triggers the agent run. Only the
	//    productizable rejections from EnqueueChatTask (agent
	//    archived, agent has no runtime configured) are mapped to a
	//    user-visible Outcome; real infra failures bubble up as
	//    errors so the WS adapter can retry or page.
	//
	//    Note: a daemon that's merely disconnected is NOT an error
	//    here. As long as agent.runtime_id is set, the chat task is
	//    enqueued and waits for the daemon to claim it on next
	//    online; this path returns OutcomeIngested with a TaskID.
	session, err := d.Queries.GetChatSession(ctx, sessionID)
	if err != nil {
		return DispatchResult{}, true, fmt.Errorf("reload chat session: %w", err)
	}
	task, err := d.TaskService.EnqueueChatTask(ctx, session)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrChatTaskAgentNoRuntime):
			res.Outcome = OutcomeAgentOffline
			return res, true, nil
		case errors.Is(err, service.ErrChatTaskAgentArchived):
			res.Outcome = OutcomeAgentArchived
			return res, true, nil
		default:
			return DispatchResult{}, true, fmt.Errorf("enqueue chat task: %w", err)
		}
	}
	res.TaskID = task.ID
	return res, true, nil
}

// finalizeDedupClaim flips the in-flight claim row to its terminal
// state. Best-effort by design: a failure here cannot abort the
// outcome (the user's message is already in chat_session or the audit
// row already exists), and the worst case is a stuck in-flight row
// that the 60-second staleness fallback in ClaimLarkInboundDedup
// re-takes on retry.
func (d *Dispatcher) finalizeDedupClaim(ctx context.Context, messageID string, durable bool) {
	if durable {
		_ = d.Queries.MarkLarkInboundDedupProcessed(ctx, messageID)
		return
	}
	_ = d.Queries.ReleaseLarkInboundDedup(ctx, messageID)
}

func (d *Dispatcher) drop(ctx context.Context, msg InboundMessage, instID pgtype.UUID, reason DropReason) DispatchResult {
	_ = d.Audit.RecordDrop(ctx, AuditDropParams{
		InstallationID: instID,
		ChatID:         msg.ChatID,
		EventType:      msg.EventType,
		LarkEventID:    msg.EventID,
		LarkMessageID:  msg.MessageID,
		Reason:         reason,
	})
	return DispatchResult{
		Outcome:        OutcomeDropped,
		DropReason:     reason,
		InstallationID: instID,
	}
}

func (d *Dispatcher) createIssueFromCommand(
	ctx context.Context,
	inst db.LarkInstallation,
	creatorUserID pgtype.UUID,
	sessionID pgtype.UUID,
	cmd IssueCommand,
) (service.IssueCreateResult, error) {
	// Empty title at this point means the /issue alone fallback found
	// no previous user message either. The product copy ("请填标题")
	// belongs in the WS adapter's reply card; we surface this to the
	// caller as ErrEmptyIssueTitle so the dispatcher can short-circuit
	// without paying the IssueService cost.
	if cmd.Title == "" {
		return service.IssueCreateResult{}, ErrEmptyIssueTitle
	}
	params := service.IssueCreateParams{
		WorkspaceID:  inst.WorkspaceID,
		Title:        cmd.Title,
		Description:  pgtype.Text{String: cmd.Description, Valid: cmd.Description != ""},
		Status:       "todo",
		Priority:     "none",
		AssigneeType: pgtype.Text{String: "agent", Valid: true},
		AssigneeID:   inst.AgentID,
		CreatorType:  "member",
		CreatorID:    creatorUserID,
		OriginType:   pgtype.Text{String: originLarkChat, Valid: true},
		OriginID:     sessionID,
	}
	return d.IssueService.Create(ctx, params, service.IssueCreateOpts{})
}

// originLarkChat is the issue.origin_type label written for issues
// created via the Lark `/issue` command. The analytics classifier in
// service.classifyOrigin currently maps unknown origin_type values to
// SourceManual with a warning — that is acceptable for MVP. A
// dedicated analytics source label can be added when product asks for
// it.
const originLarkChat = "lark_chat"

// ErrEmptyIssueTitle is returned by createIssueFromCommand when the
// caller invoked /issue with no title AND the previous-user-message
// fallback found nothing usable. The WS adapter translates this into
// the "please supply a title" reply card per §2.3.
var ErrEmptyIssueTitle = errors.New("issue title is empty")
