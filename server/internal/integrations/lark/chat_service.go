package lark

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// pgSQLStateUniqueViolation is the Postgres SQLSTATE for unique
// constraint violations. Spelled out as a literal rather than imported
// from pgerrcode to avoid pulling in another dependency for a single
// constant. See https://www.postgresql.org/docs/current/errcodes-appendix.html
const pgSQLStateUniqueViolation = "23505"

// isUniqueViolation reports whether err is a Postgres unique-violation
// (SQLSTATE 23505). The lark_chat_session_binding
// UNIQUE (installation_id, lark_chat_id) constraint surfaces this
// code when two concurrent first messages on the same Lark chat race
// to create the binding row.
func isUniqueViolation(err error) bool {
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		return pg.Code == pgSQLStateUniqueViolation
	}
	return false
}

// TxStarter abstracts transaction creation. Re-declared in this
// package (rather than depending on internal/service) so the
// integrations layer does not back-reference into service — a circular
// dependency we want to avoid as both packages grow. Satisfied by
// *pgxpool.Pool.
type TxStarter interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

// chatSessionService is the concrete ChatSessionService. It enforces
// the architectural rules from doc.go:
//
//   - EnsureChatSession only creates / looks up rows; identity must
//     already be resolved by the caller (the sender argument is a
//     trusted Multica user UUID).
//
//   - AppendUserMessage runs message-write + session-touch in a single
//     transaction so a session that has received a message has its
//     `updated_at` advanced atomically. Per-Lark-message-id idempotency
//     is enforced by the Dispatcher's two-phase dedup gate
//     (ClaimLarkInboundDedup + Mark/Release) BEFORE AppendUserMessage
//     runs — see Dispatcher.Handle. AppendUserMessage trusts the
//     dispatcher's claim and does not re-check dedup itself; this is
//     what lets the dispatcher safely Release the claim on infra
//     failure (rolled-back tx → no chat_message → next replay
//     re-processes).
type chatSessionService struct {
	queries   *db.Queries
	txStarter TxStarter
}

// NewChatSessionService constructs a ChatSessionService backed by the
// supplied queries and tx starter. The tx starter is required;
// without it, AppendUserMessage cannot run dedup + insert atomically.
func NewChatSessionService(queries *db.Queries, tx TxStarter) ChatSessionService {
	return &chatSessionService{queries: queries, txStarter: tx}
}

// EnsureChatSession returns the chat_session.id bound to the given
// Lark chat. The implementation is the two-phase find-or-create
// expected by the interface contract:
//
//  1. Look up the existing lark_chat_session_binding.
//  2. If found, return its chat_session_id.
//  3. Otherwise, in one transaction: create chat_session +
//     lark_chat_session_binding. Commit.
//
// The race between two concurrent first messages on the same Lark
// chat is resolved by the UNIQUE (installation_id, lark_chat_id)
// constraint on lark_chat_session_binding: the loser of the race
// catches the unique violation, re-reads the existing row, and
// returns its chat_session_id.
func (s *chatSessionService) EnsureChatSession(ctx context.Context, p EnsureChatSessionParams) (pgtype.UUID, error) {
	// Fast path: existing binding.
	existing, err := s.queries.GetLarkChatSessionBinding(ctx, db.GetLarkChatSessionBindingParams{
		InstallationID: p.InstallationID,
		LarkChatID:     string(p.ChatID),
	})
	if err == nil {
		return existing.ChatSessionID, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return pgtype.UUID{}, fmt.Errorf("lookup chat session binding: %w", err)
	}

	// Create path: chat_session + binding atomically.
	id, err := s.createSessionAndBinding(ctx, p)
	if err == nil {
		return id, nil
	}

	// Lost the race: another goroutine created the binding between our
	// lookup and our insert. Re-read and return the winner's session.
	if isUniqueViolation(err) {
		existing, lookupErr := s.queries.GetLarkChatSessionBinding(ctx, db.GetLarkChatSessionBindingParams{
			InstallationID: p.InstallationID,
			LarkChatID:     string(p.ChatID),
		})
		if lookupErr == nil {
			return existing.ChatSessionID, nil
		}
		return pgtype.UUID{}, fmt.Errorf("race re-read after unique violation: %w", lookupErr)
	}
	return pgtype.UUID{}, err
}

func (s *chatSessionService) createSessionAndBinding(ctx context.Context, p EnsureChatSessionParams) (pgtype.UUID, error) {
	tx, err := s.txStarter.Begin(ctx)
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)
	qtx := s.queries.WithTx(tx)

	session, err := qtx.CreateChatSession(ctx, db.CreateChatSessionParams{
		WorkspaceID: p.WorkspaceID,
		AgentID:     p.AgentID,
		CreatorID:   p.Sender,
		Title:       defaultSessionTitle(p.ChatType),
	})
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("create chat session: %w", err)
	}

	if _, err := qtx.CreateLarkChatSessionBinding(ctx, db.CreateLarkChatSessionBindingParams{
		ChatSessionID:  session.ID,
		InstallationID: p.InstallationID,
		LarkChatID:     string(p.ChatID),
		LarkChatType:   string(p.ChatType),
	}); err != nil {
		return pgtype.UUID{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return pgtype.UUID{}, fmt.Errorf("commit: %w", err)
	}
	return session.ID, nil
}

// AppendUserMessage writes the user message into chat_session and
// (when the body parses as `/issue …`) returns the parsed command so
// the caller can dispatch through IssueService.
//
// Idempotency is enforced upstream by the Dispatcher's two-phase
// ClaimLarkInboundDedup gate (see Dispatcher.Handle step 2): a
// replayed message_id whose previous attempt reached a durable
// outcome — i.e. successfully returned from this method — is dropped
// before AppendUserMessage is ever called for it. A previous attempt
// that crashed or returned an infra error before reaching this method
// is explicitly released by the dispatcher, so the retry can re-claim
// and re-run the insert. AppendUserMessage itself therefore does no
// dedup; the transaction commit here is what triggers the dispatcher's
// MarkLarkInboundDedupProcessed call on the way out.
func (s *chatSessionService) AppendUserMessage(ctx context.Context, p AppendUserMessageParams) (AppendResult, error) {
	tx, err := s.txStarter.Begin(ctx)
	if err != nil {
		return AppendResult{}, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)
	qtx := s.queries.WithTx(tx)

	// Parse the command BEFORE the insert, so the "/issue alone → use
	// previous user message" fallback queries from the message set
	// that does NOT yet include the message currently being appended.
	// Otherwise the previous-message lookup would self-reference.
	cmd, _ := parseIssueCommand(p.Body)
	if cmd != nil && cmd.Title == "" {
		prev, err := qtx.GetMostRecentUserChatMessage(ctx, p.ChatSessionID)
		if err == nil {
			cmd.Title = titleFromPreviousMessage(prev.Content)
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return AppendResult{}, fmt.Errorf("previous message lookup: %w", err)
		}
	}

	if _, err := qtx.CreateChatMessage(ctx, db.CreateChatMessageParams{
		ChatSessionID: p.ChatSessionID,
		Role:          "user",
		Content:       p.Body,
	}); err != nil {
		return AppendResult{}, fmt.Errorf("create chat message: %w", err)
	}

	if err := qtx.TouchChatSession(ctx, p.ChatSessionID); err != nil {
		return AppendResult{}, fmt.Errorf("touch chat session: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return AppendResult{}, fmt.Errorf("commit: %w", err)
	}

	return AppendResult{IssueCommand: cmd}, nil
}

// titleFromPreviousMessage extracts a sensible title from a prior
// chat message. The spec says the previous "user message" is the
// fallback; in practice the previous message itself might also be an
// `/issue ...` invocation (the user typed two commands in a row), in
// which case stripping the prefix yields the real intent.
func titleFromPreviousMessage(body string) string {
	if cmd, ok := parseIssueCommand(body); ok {
		return cmd.Title
	}
	// First non-empty line, trimmed. Multi-line free text "becomes"
	// the title via its first line; description fallback for the
	// previous-message path is out of scope (the user's intent was a
	// title alone).
	for _, line := range strings.Split(body, "\n") {
		t := strings.TrimSpace(line)
		if t != "" {
			return t
		}
	}
	return ""
}

// defaultSessionTitle gives a freshly created chat_session a
// reasonable display title. We do not derive from message content —
// the first message hasn't been appended yet — so we use a stable
// per-chat-type label that the front-end can localize later.
func defaultSessionTitle(t ChatType) string {
	switch t {
	case ChatTypeGroup:
		return "Lark group chat"
	case ChatTypeP2P:
		return "Lark direct message"
	default:
		return "Lark chat"
	}
}
