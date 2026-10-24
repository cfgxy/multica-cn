package handler

import (
	"context"
	"log/slog"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/pkg/agentcontext"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// RUYI-874 link ①: the chat write-back for a voice session opened from a
// chat surface. The merged voice turns land into the SAME chat session the
// voice UI was opened from — the user transcript and the assistant reply
// become ordinary, reconnect-visible chat history rows (message_kind
// 'voice', so a re-rendering client can distinguish them from typed
// messages), deduplicated against replay by a deterministic UUIDv5 key.
//
// Two invariants keep this safe:
//
//   - IDEMPOTENT: the row id is UUIDv5(live_session_id, role, turn_index),
//     so re-running the terminal write (a relay retry, a crash replay)
//     inserts zero new rows — ON CONFLICT (id) DO NOTHING collapses the
//     replay. Only a row whose insert actually affected 1 row is
//     broadcast; the replay broadcast is the dedup no-op.
//   - SAME CONVERSATION: only the session's own validated chat link is
//     written (openVoiceSession gated it to creator + agent + access), so
//     a voice session can never duplicate or pollinate another session's
//     history.

// voiceChatWritebackRow is one merged voice turn ready for idempotent
// insert: its deterministic id and the payload to land.
type voiceChatWritebackRow struct {
	RowID     pgtype.UUID
	Role      string
	Content   string
	CreatedAt pgtype.Timestamptz
}

// voiceChatWritebackRows folds the session's transcript into its merged
// turns and keys each for idempotent insert. Index is the 1-based merged
// turn position, so a replay of the SAME transcript reproduces the SAME
// keys (the fragment-merge is deterministic, extract.go).
func voiceChatWritebackRows(session db.LiveSession, transcript []voiceTranscriptEntry) []voiceChatWritebackRow {
	turns := agentcontext.MergeVoiceTurns(transcriptEntries(transcript))
	rows := make([]voiceChatWritebackRow, 0, len(turns))
	base := uuidToString(session.ID)
	created := time.Now().UTC()
	for _, turn := range turns {
		if turn.Text == "" {
			continue
		}
		key := base + "|" + turn.Role + "|" + strconv.Itoa(turn.Index)
		rows = append(rows, voiceChatWritebackRow{
			RowID:     uuidv5Key(key),
			Role:      turn.Role,
			Content:   turn.Text,
			CreatedAt: pgtype.Timestamptz{Time: created, Valid: true},
		})
	}
	return rows
}

// uuidv5Key is the deterministic write-back row id (precedent: audit.go's
// event id and autopilot.go's idempotency key — UUIDv5 over a stable
// namespace, so the same logical row always lands on the same physical
// id and ON CONFLICT dedups it).
func uuidv5Key(key string) pgtype.UUID {
	return pgtype.UUID{Bytes: uuid.NewSHA1(uuid.NameSpaceOID, []byte(key)), Valid: true}
}

// writeBackVoiceChatSession performs the chat write-back for the session's
// validated chat link. A session without a link (plain voice) is a silent
// no-op. Call it from endLiveSession AFTER the terminal row lands, so the
// transcript is already persisted and a write-back failure loses nothing
// (replay can re-run the idempotent insert).
func (h *Handler) writeBackVoiceChatSession(ctx context.Context, session db.LiveSession, transcript []voiceTranscriptEntry) {
	if !session.ChatSessionID.Valid {
		return
	}
	rows := voiceChatWritebackRows(session, transcript)
	if len(rows) == 0 {
		return
	}
	workspaceID := uuidToString(session.WorkspaceID)
	actorType, actorID := "member", uuidToString(session.UserID)
	chatSessionID := uuidToString(session.ChatSessionID)
	for _, row := range rows {
		created, err := h.Queries.CreateVoiceChatMessageIfAbsent(ctx, db.CreateVoiceChatMessageIfAbsentParams{
			ChatSessionID: session.ChatSessionID,
			Role:          row.Role,
			Content:       row.Content,
			ID:            row.RowID,
			CreatedAt:     row.CreatedAt,
		})
		if err != nil {
			slog.Error("voice chat write-back insert failed",
				"session_id", uuidToString(session.ID), "role", row.Role, "error", err)
			return
		}
		// created==0 is the replay no-op (ON CONFLICT dedup) — expected on
		// an abnormal-disconnect replay, so only a genuine first insert
		// broadcasts; re-broadcasting would double the client's history.
		if created != 1 {
			continue
		}
		h.publishChat(protocol.EventChatMessage, workspaceID, actorType, actorID, chatSessionID, protocol.ChatMessagePayload{
			ChatSessionID: chatSessionID,
			MessageID:     uuidToString(row.RowID),
			Role:          row.Role,
			Content:       row.Content,
			CreatedAt:     timestampToString(row.CreatedAt),
		})
	}
}
