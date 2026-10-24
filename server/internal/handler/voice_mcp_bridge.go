package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// voiceMCPBridge adapts Gemini function-calling to the canonical Multica MCP
// server. It deliberately has no business-tool registry: tools/list and
// tools/call remain the source of schemas, execution, and authorization.
type voiceMCPBridge struct {
	endpoint string
	token    string
	client   *http.Client
}

type voiceMCPTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

type voiceMCPRPCResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

const voiceMCPProtocolVersion = "2025-03-26"

func newVoiceMCPBridge(token string) (*voiceMCPBridge, error) {
	base := strings.TrimRight(strings.TrimSpace(os.Getenv("MCP_URL")), "/")
	if base == "" {
		return nil, fmt.Errorf("MCP_URL is not configured")
	}
	endpoint, err := url.Parse(base + "/mcp")
	if err != nil || endpoint.Scheme == "" || endpoint.Host == "" {
		return nil, fmt.Errorf("MCP_URL is invalid")
	}
	if strings.TrimSpace(token) == "" {
		return nil, fmt.Errorf("voice caller credential is unavailable")
	}
	return &voiceMCPBridge{endpoint: endpoint.String(), token: token, client: &http.Client{Timeout: 10 * time.Second}}, nil
}

func (b *voiceMCPBridge) request(ctx context.Context, id int, method string, params any) (voiceMCPRPCResponse, error) {
	payload := map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}
	raw, err := json.Marshal(payload)
	if err != nil {
		return voiceMCPRPCResponse{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.endpoint, bytes.NewReader(raw))
	if err != nil {
		return voiceMCPRPCResponse{}, err
	}
	req.Header.Set("Authorization", "Bearer "+b.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := b.client.Do(req)
	if err != nil {
		return voiceMCPRPCResponse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return voiceMCPRPCResponse{}, fmt.Errorf("MCP returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return voiceMCPRPCResponse{}, err
	}
	var decoded voiceMCPRPCResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return voiceMCPRPCResponse{}, fmt.Errorf("decode MCP response: %w", err)
	}
	if decoded.Error != nil {
		return voiceMCPRPCResponse{}, fmt.Errorf("MCP error %d: %s", decoded.Error.Code, decoded.Error.Message)
	}
	return decoded, nil
}

func (b *voiceMCPBridge) initialize(ctx context.Context) error {
	_, err := b.request(ctx, 1, "initialize", map[string]any{
		"protocolVersion": voiceMCPProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "multica-gemini-live", "version": "1"},
	})
	return err
}

func (b *voiceMCPBridge) tools(ctx context.Context) ([]voiceMCPTool, error) {
	if err := b.initialize(ctx); err != nil {
		return nil, err
	}
	response, err := b.request(ctx, 2, "tools/list", map[string]any{})
	if err != nil {
		return nil, err
	}
	var result struct {
		Tools []voiceMCPTool `json:"tools"`
	}
	if err := json.Unmarshal(response.Result, &result); err != nil {
		return nil, fmt.Errorf("decode MCP tool list: %w", err)
	}
	return result.Tools, nil
}

func (b *voiceMCPBridge) call(ctx context.Context, name string, arguments json.RawMessage) (json.RawMessage, error) {
	if err := b.initialize(ctx); err != nil {
		return nil, err
	}
	args := json.RawMessage(`{}`)
	if len(bytes.TrimSpace(arguments)) > 0 {
		args = arguments
	}
	response, err := b.request(ctx, 2, "tools/call", map[string]any{"name": name, "arguments": args})
	if err != nil {
		return nil, err
	}
	return response.Result, nil
}

func voiceMCPFunctionDeclarations(tools []voiceMCPTool) []map[string]any {
	decls := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		var schema any
		if json.Unmarshal(tool.InputSchema, &schema) != nil {
			continue
		}
		decls = append(decls, map[string]any{"name": tool.Name, "description": tool.Description, "parameters": schema})
	}
	return decls
}

// Names are context only. Gemini must load the actual skill body before it
// follows it, matching the Harness discovery/load boundary.
func voiceSkillFunctionDeclaration() map[string]any {
	return map[string]any{
		"name":        "load_skill",
		"description": "Load the complete instructions for one skill assigned to this agent before following it.",
		"parameters":  map[string]any{"type": "object", "properties": map[string]any{"name": map[string]any{"type": "string", "description": "Assigned skill name."}}, "required": []string{"name"}},
	}
}

func (h *Handler) executeVoiceSkill(ctx context.Context, session db.LiveSession, rawArgs json.RawMessage) (json.RawMessage, error) {
	var args struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(rawArgs, &args); err != nil || strings.TrimSpace(args.Name) == "" {
		return voiceToolError("invalid_arguments", "load_skill requires a skill name"), nil
	}
	skills, err := h.Queries.ListAgentSkills(ctx, session.AgentID)
	if err != nil {
		return voiceToolError("internal", "could not load assigned skills"), nil
	}
	for _, skill := range skills {
		if skill.Name == args.Name {
			files, err := h.Queries.ListSkillFiles(ctx, skill.ID)
			if err != nil {
				return voiceToolError("internal", "could not load assigned skill files"), nil
			}
			contents := make([]map[string]string, 0, len(files))
			for _, file := range files {
				contents = append(contents, map[string]string{"path": file.Path, "content": file.Content})
			}
			return json.Marshal(map[string]any{
				"name":        skill.Name,
				"description": skill.Description,
				"content":     skill.Content,
				"files":       contents,
			})
		}
	}
	return voiceToolError("not_found", "skill is not assigned to this agent"), nil
}

func voiceToolError(code, message string) json.RawMessage {
	raw, _ := json.Marshal(map[string]any{"error": map[string]string{"code": code, "message": message}})
	return raw
}

// listVoiceVisibleAgents is shared by the context projector. It is a
// visibility predicate, not an MCP executor; tool execution stays in MCP.
func (h *Handler) listVoiceVisibleAgents(ctx context.Context, session db.LiveSession, workspaceID pgtype.UUID) ([]db.Agent, bool) {
	if !session.UserID.Valid {
		return nil, false
	}
	member, err := h.getWorkspaceMember(ctx, uuidToString(session.UserID), uuidToString(workspaceID))
	if err != nil {
		return nil, false
	}
	agents, err := h.Queries.ListAgents(ctx, workspaceID)
	if err != nil {
		return nil, false
	}
	targetsByAgent, ok := h.loadInvocationTargetsByAgent(ctx, agents)
	if !ok {
		return nil, false
	}
	visible := make([]db.Agent, 0, len(agents))
	for _, agent := range agents {
		if memberAllowedToViewAgent(agent, targetsByAgent[uuidToString(agent.ID)], uuidToString(session.UserID), member.Role) {
			visible = append(visible, agent)
		}
	}
	return visible, true
}

func prepareVoiceToolDeclarations(ctx context.Context, plan *voiceSessionPlan, token string) []map[string]any {
	declarations := []map[string]any{voiceSkillFunctionDeclaration()}
	bridge, err := newVoiceMCPBridge(token)
	if err != nil {
		slog.Warn("voice MCP bridge unavailable during setup", "session_id", uuidToString(plan.session.ID), "error", err)
		return declarations
	}
	tools, err := bridge.tools(ctx)
	if err != nil {
		slog.Warn("voice MCP tool discovery failed", "session_id", uuidToString(plan.session.ID), "error", err)
		return declarations
	}
	plan.mcp = bridge
	return append(voiceMCPFunctionDeclarations(tools), declarations...)
}
