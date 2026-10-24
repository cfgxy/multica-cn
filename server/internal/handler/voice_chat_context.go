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

	"github.com/multica-ai/multica/server/internal/util"
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

// voiceMentionRe mirrors BaseMentionExtension's Markdown shape. Do not narrow
// this to known UUIDs: invalid IDs must be consumed and degraded rather than
// reaching the model as ordinary Markdown text.
var voiceMentionRe = regexp.MustCompile(`\[@?((\\.|[^\\\]])*)\]\(mention://(\w+)/([^)]+)\)`)

// voiceMentionURLRe catches malformed hand-written mention URLs after the
// structured parser has handled editor-generated links. A malformed link has
// no entity semantics, but it must not reach the model as ordinary text.
var voiceMentionURLRe = regexp.MustCompile(`mention://[^\s\])]+`)

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
	// A chat link is validated when the live session opens, but it can outlive a
	// membership revocation. Re-check the persisted session owner and current
	// workspace membership before reading either messages or mentioned entities.
	if !session.UserID.Valid || chat.CreatorID != session.UserID {
		slog.Warn("voice chat context: chat link owner no longer matches session user; degrading to plain voice",
			"session_id", uuidToString(session.ID))
		return nil
	}
	if _, err := h.getWorkspaceMember(ctx, uuidToString(session.UserID), uuidToString(session.WorkspaceID)); err != nil {
		slog.Warn("voice chat context: session user no longer belongs to workspace; degrading to plain voice",
			"session_id", uuidToString(session.ID))
		return nil
	}

	// Re-check the linked chat agent using the same single-agent gate as the
	// chat endpoint before reading any messages, attachments, or skills.
	chatAgent, aerr := h.Queries.GetAgent(ctx, chat.AgentID)
	if aerr != nil || !h.canAccessPrivateAgent(ctx, chatAgent, "member", uuidToString(session.UserID), uuidToString(session.WorkspaceID)) {
		slog.Warn("voice chat context: chat agent is no longer visible; degrading to plain voice",
			"session_id", uuidToString(session.ID), "agent_id", uuidToString(chat.AgentID))
		return nil
	}

	// Build the mention name map only from the HTTP ListAgents-visible set, so a
	// hand-written UUID cannot disclose an otherwise inaccessible agent name.
	names := map[string]string{}
	agents, ok := h.listVoiceVisibleAgents(ctx, session, session.WorkspaceID)
	if !ok {
		slog.Warn("voice chat context: visible agents load failed; agent mentions will degrade",
			"session_id", uuidToString(session.ID))
		return nil
	}
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
				b.WriteString(": " + stripVoiceMentionLinks(strings.TrimSpace(p.Description.String)))
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
				fmt.Fprintf(&b, "User: %s\n", h.rewriteVoiceMentions(ctx, m.Content, session.WorkspaceID, names))
				continue
			}
			// Assistant and tool messages are not user attachments. Strip every
			// mention from them so an older reply cannot disclose a now-hidden agent.
			fmt.Fprintf(&b, "%s: %s\n", m.Role, stripVoiceMentionLinks(m.Content))
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

// rewriteVoiceMentions renders all composer context mention types as verified
// entity context. It intentionally resolves from the database rather than
// trusting the Markdown label: labels are user input, while Issue and project
// content must only come from the live session user's current workspace.
func (h *Handler) rewriteVoiceMentions(ctx context.Context, content string, workspaceID pgtype.UUID, agentNames map[string]string) string {
	rewritten := voiceMentionRe.ReplaceAllStringFunc(content, func(m string) string {
		sub := voiceMentionRe.FindStringSubmatch(m)
		if len(sub) != 5 {
			return "[unavailable mention reference]"
		}
		switch sub[3] {
		case "agent":
			if name, ok := agentNames[sub[4]]; ok && strings.TrimSpace(name) != "" {
				return fmt.Sprintf("user mentions agent %q", name)
			}
			return "[unavailable agent reference]"
		case "issue":
			id, err := util.ParseUUID(sub[4])
			if err != nil {
				return "[unavailable issue reference]"
			}
			issue, err := h.Queries.GetIssueInWorkspace(ctx, db.GetIssueInWorkspaceParams{
				ID:          id,
				WorkspaceID: workspaceID,
			})
			if err != nil {
				return "[unavailable issue reference]"
			}
			return renderVoiceIssueReference(issue)
		case "project":
			id, err := util.ParseUUID(sub[4])
			if err != nil {
				return "[unavailable project reference]"
			}
			project, err := h.Queries.GetProjectInWorkspace(ctx, db.GetProjectInWorkspaceParams{
				ID:          id,
				WorkspaceID: workspaceID,
			})
			if err != nil {
				return "[unavailable project reference]"
			}
			return renderVoiceProjectReference(project)
		default:
			return "[unavailable mention reference]"
		}
	})
	return stripVoiceMentionURLs(rewritten)
}

func renderVoiceIssueReference(issue db.Issue) string {
	var b strings.Builder
	fmt.Fprintf(&b, "user references issue %q (status: %s; priority: %s", issue.Title, issue.Status, issue.Priority)
	if issue.Description.Valid && strings.TrimSpace(issue.Description.String) != "" {
		fmt.Fprintf(&b, "; description: %q", stripVoiceMentionLinks(strings.TrimSpace(issue.Description.String)))
	}
	b.WriteString(")")
	return b.String()
}

func renderVoiceProjectReference(project db.Project) string {
	var b strings.Builder
	fmt.Fprintf(&b, "user references project %q (status: %s; priority: %s", project.Title, project.Status, project.Priority)
	if project.Description.Valid && strings.TrimSpace(project.Description.String) != "" {
		fmt.Fprintf(&b, "; description: %q", stripVoiceMentionLinks(strings.TrimSpace(project.Description.String)))
	}
	b.WriteString(")")
	return b.String()
}

// stripVoiceMentionLinks protects nested rich text (for example an Issue
// description) from reintroducing a raw composer reference after its outer
// entity was validated. Nested links are not separate explicit attachments.
func stripVoiceMentionLinks(content string) string {
	return stripVoiceMentionURLs(voiceMentionRe.ReplaceAllString(content, "[nested mention omitted]"))
}

func stripVoiceMentionURLs(content string) string {
	return voiceMentionURLRe.ReplaceAllString(content, "[unavailable mention reference]")
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
