package handler

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"log/slog"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// RUYI-874 link ②: the rich context the gateway injects when a voice
// session was opened FROM a chat surface (the validated chat_session_id
// link, openVoiceSession). The injection carries exactly what the chat
// input box carries, under the caller's own permissions — the session was
// already gated to its creator and its agent, and every query below stays
// inside that session's workspace:
//
//   - the bound project (chat_session.project_id);
//   - the recent conversation, with @agent mentions rendered SEMANTICALLY
//     (a mention is an action, not raw markdown the model has to guess at);
//   - the agent's skill surface (names only — the model must know the
//     skills exist; the skill text itself is pulled on demand, the same
//     way the task runner injects it);
//   - the session's image attachments, as inlineData parts capped by a
//     byte budget so one conversation cannot blow the live turn.
//
// Nothing here is synthesized: every field is read from rows the caller
// already owns. A load failure degrades to "less context", never to
// invented context.

const (
	// voiceChatContextMessageLimit bounds how many recent messages are READ
	// (newest-first page); voiceChatContextRenderLimit bounds how many are
	// RENDERED into the injected turn.
	voiceChatContextMessageLimit = 24
	voiceChatContextRenderLimit  = 16
	// The image part budget: from the tail (newest images first), at most
	// voiceChatImageContextMax parts, capped at voiceChatImageBytesMax total.
	voiceChatImageContextMax = 2
	voiceChatImageBytesMax   = 512 * 1024
)

// voiceMentionAgentRe matches the chat markdown mention of an agent.
var voiceMentionAgentRe = regexp.MustCompile(`\[([^\]]*)\]\(mention://agent/([0-9a-fA-F-]{36})\)`)

// voiceChatContext is one injection unit: the rendered text section plus
// zero or more image parts. Nil means "nothing to inject".
type voiceChatContext struct {
	Text       string
	ImageParts []map[string]any
}

// composeVoiceChatContext builds the rich context for the voice session's
// chat link. Returns nil when there is no validated link or nothing to
// render — the caller then falls back to the plain voice summary section.
func (h *Handler) composeVoiceChatContext(ctx context.Context, session db.LiveSession) *voiceChatContext {
	if !session.ChatSessionID.Valid {
		return nil
	}
	chat, err := h.Queries.GetChatSessionInWorkspace(ctx, db.GetChatSessionInWorkspaceParams{
		ID:          session.ChatSessionID,
		WorkspaceID: session.WorkspaceID,
	})
	if err != nil {
		slog.Warn("voice chat context: chat session load failed; degrading to plain voice",
			"session_id", uuidToString(session.ID), "error", err)
		return nil
	}

	// Agent names for the mention rewrite (workspace-level, one read).
	agents, _ := h.Queries.ListAgents(ctx, session.WorkspaceID)
	names := map[string]string{}
	for _, a := range agents {
		names[uuidToString(a.ID)] = a.Name
	}

	var b strings.Builder

	// 1) bound project.
	if chat.ProjectID.Valid {
		if p, perr := h.Queries.GetProjectInWorkspace(ctx, db.GetProjectInWorkspaceParams{
			ID:          chat.ProjectID,
			WorkspaceID: session.WorkspaceID,
		}); perr == nil {
			b.WriteString("### Chat project\n" + p.Title)
			if p.Description.Valid && strings.TrimSpace(p.Description.String) != "" {
				b.WriteString(": " + strings.TrimSpace(p.Description.String))
			}
			b.WriteString("\n\n")
		}
	}

	// 2) recent conversation, page reversed to oldest-first, mentions
	// rewritten to their semantic reading.
	if msgs, merr := h.Queries.ListChatMessagesPage(ctx, db.ListChatMessagesPageParams{
		ChatSessionID: chat.ID,
		Limit:         voiceChatContextMessageLimit,
	}); merr == nil && len(msgs) > 0 {
		// The query pages newest-first; reverse for the model.
		rev := make([]db.ChatMessage, len(msgs))
		for i, m := range msgs {
			rev[len(msgs)-1-i] = m
		}
		if keep := len(rev) - voiceChatContextRenderLimit; keep > 0 {
			rev = rev[keep:]
		}
		b.WriteString("### Chat conversation so far\n")
		for _, m := range rev {
			if strings.TrimSpace(m.Content) == "" {
				continue
			}
			if m.Role == "user" {
				fmt.Fprintf(&b, "User: %s\n", rewriteVoiceMentions(m.Content, names))
			} else {
				fmt.Fprintf(&b, "%s: %s\n", m.Role, m.Content)
			}
		}
		b.WriteString("\n")
	}

	// 3) the agent's skill surface — names only, the injection tells the
	// model what it can rely on without pasting every skill body.
	if skills, serr := h.Queries.ListAgentSkills(ctx, chat.AgentID); serr == nil && len(skills) > 0 {
		skillNames := make([]string, 0, len(skills))
		for _, s := range skills {
			skillNames = append(skillNames, s.Name)
		}
		fmt.Fprintf(&b, "The chat agent has these skills available: %s\n\n", strings.Join(skillNames, ", "))
	}

	out := &voiceChatContext{Text: strings.TrimRight(b.String(), "\n")}
	out.ImageParts = h.loadVoiceChatImageParts(ctx, chat.ID, session.WorkspaceID)

	if strings.TrimSpace(out.Text) == "" && len(out.ImageParts) == 0 {
		return nil
	}
	return out
}

// rewriteVoiceMentions renders chat markdown agent mentions as their
// semantic reading: `user mentions agent "X"`. The raw markdown is never
// shown to the model as plain text — link ②'s "@ 不得当纯文本".
func rewriteVoiceMentions(content string, names map[string]string) string {
	return voiceMentionAgentRe.ReplaceAllStringFunc(content, func(m string) string {
		sub := voiceMentionAgentRe.FindStringSubmatch(m)
		name := strings.TrimSpace(sub[1])
		if n, ok := names[sub[2]]; ok {
			name = n
		}
		if name == "" {
			name = sub[2]
		}
		return fmt.Sprintf("user mentions agent %q", name)
	})
}

// loadVoiceChatImageParts reads the session's newest image attachments from
// object storage, base64-encoded into the provider's inlineData part shape
// (the same shape voice_transcription.go's audio hop uses). The budget is
// enforced from the tail: newest images first, up to the part count and the
// total byte cap. Any read failure skips that image — degraded context,
// never a failed voice start.
func (h *Handler) loadVoiceChatImageParts(ctx context.Context, chatSessionID, workspaceID pgtype.UUID) []map[string]any {
	if h.Storage == nil {
		return nil
	}
	atts, err := h.Queries.ListAttachmentsByChatSession(ctx, db.ListAttachmentsByChatSessionParams{
		ChatSessionID: chatSessionID,
		WorkspaceID:   workspaceID,
	})
	if err != nil || len(atts) == 0 {
		return nil
	}
	// The query is created_at ASC (oldest first); the budget takes the
	// newest, so collect the candidates and walk from the tail.
	type cand struct{ url, mime string }
	var cands []cand
	for _, a := range atts {
		if strings.HasPrefix(a.ContentType, "image/") && a.Url != "" {
			cands = append(cands, cand{a.Url, a.ContentType})
		}
	}
	var parts []map[string]any
	var total int
	for i := len(cands) - 1; i >= 0 && len(parts) < voiceChatImageContextMax; i-- {
		c := cands[i]
		key := h.Storage.KeyFromURL(c.url)
		reader, rerr := h.Storage.GetReader(ctx, key)
		if rerr != nil {
			slog.Warn("voice chat image read failed; skipping attachment", "key", key, "error", rerr)
			continue
		}
		data, rerr := io.ReadAll(io.LimitReader(reader, int64(voiceChatImageBytesMax-total)))
		reader.Close()
		if rerr != nil {
			slog.Warn("voice chat image read failed; skipping attachment", "key", key, "error", rerr)
			continue
		}
		if len(data) == 0 {
			continue
		}
		parts = append(parts, map[string]any{
			"inlineData": map[string]string{
				"mimeType": c.mime,
				"data":     base64.StdEncoding.EncodeToString(data),
			},
		})
		total += len(data)
		if total >= voiceChatImageBytesMax {
			break
		}
	}
	return parts
}
