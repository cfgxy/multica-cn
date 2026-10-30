package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

const credentialMCPMaxRequestBytes = 1 << 20

type credentialMCPResolver func(context.Context, CredentialCapability) ([]byte, error)

type credentialMCPServer struct {
	capabilities map[string]credentialMCPEntry
	resolve      credentialMCPResolver
	mu           sync.RWMutex
	path         string
	logger       *slog.Logger
}

type credentialMCPEntry struct {
	capability CredentialCapability
	plaintext  []byte
}

func taskHasDirectCredentialCapability(capabilities []CredentialCapability) bool {
	for _, capability := range capabilities {
		if capability.CredentialID != "" {
			return true
		}
	}
	return false
}

type credentialMCPSet struct {
	server      *http.Server
	listener    net.Listener
	credentials *credentialMCPServer
	once        sync.Once
}

func (set *credentialMCPSet) Close() {
	if set == nil {
		return
	}
	set.once.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if set.server != nil {
			_ = set.server.Shutdown(ctx)
		}
		if set.listener != nil {
			_ = set.listener.Close()
		}
		if set.credentials != nil {
			set.credentials.zeroCredentials()
		}
	})
}

func startTaskCredentialMCP(lifetimeCtx context.Context, taskID string, capabilities []CredentialCapability, resolve credentialMCPResolver, logger *slog.Logger) (json.RawMessage, *credentialMCPSet, error) {
	if resolve == nil {
		return nil, nil, nil
	}

	byCredentialID := make(map[string]credentialMCPEntry)
	for _, capability := range capabilities {
		if capability.CredentialID == "" {
			continue
		}
		if capability.ConnectorID != "" || capability.ConnectorRevision != 0 || capability.Capability == "" {
			return nil, nil, errors.New("invalid direct credential capability")
		}
		if _, duplicate := byCredentialID[capability.CredentialID]; duplicate {
			for _, entry := range byCredentialID {
				zeroCredentialBytes(entry.plaintext)
			}
			return nil, nil, errors.New("duplicate direct credential capability")
		}
		byCredentialID[capability.CredentialID] = credentialMCPEntry{capability: capability}
	}
	if len(byCredentialID) == 0 {
		return nil, nil, nil
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, nil, fmt.Errorf("listen for credential MCP server: %w", err)
	}
	pathToken, err := randomBrokerToken()
	if err != nil {
		_ = listener.Close()
		return nil, nil, err
	}

	handler := &credentialMCPServer{
		capabilities: byCredentialID,
		resolve:      resolve,
		path:         "/" + pathToken,
		logger:       logger,
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	set := &credentialMCPSet{server: server, listener: listener, credentials: handler}
	go func() {
		if serveErr := server.Serve(listener); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) && logger != nil {
			logger.Warn("credential MCP server stopped unexpectedly", "task_id", taskID, "error", serveErr)
		}
	}()
	go func() {
		<-lifetimeCtx.Done()
		set.Close()
	}()

	raw, err := json.Marshal(map[string]any{"mcpServers": map[string]any{
		credentialMCPServerName(taskID): map[string]any{
			"type": "http",
			"url":  "http://" + listener.Addr().String() + handler.path,
		},
	}})
	if err != nil {
		set.Close()
		return nil, nil, err
	}
	return raw, set, nil
}

func credentialMCPServerName(taskID string) string {
	digest := sha256.Sum256([]byte(taskID))
	return "multica-credentials-" + hex.EncodeToString(digest[:16])
}

func (s *credentialMCPServer) zeroCredentials() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for credentialID, entry := range s.capabilities {
		zeroCredentialBytes(entry.plaintext)
		delete(s.capabilities, credentialID)
	}
}

func (s *credentialMCPServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != s.path || r.Method != http.MethodPost {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, credentialMCPMaxRequestBytes))
	if err != nil {
		writePluginHookMCPError(w, nil, -32700, "could not read the request")
		return
	}
	var request pluginHookMCPRequest
	if err := json.Unmarshal(body, &request); err != nil {
		writePluginHookMCPError(w, nil, -32700, "request is not valid JSON-RPC")
		return
	}

	switch request.Method {
	case "initialize":
		writePluginHookMCPResult(w, request.ID, map[string]any{
			"protocolVersion": pluginHookMCPProtocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "multica-credentials", "version": "1"},
		})
	case "notifications/initialized":
		w.WriteHeader(http.StatusAccepted)
	case "tools/list":
		writePluginHookMCPResult(w, request.ID, map[string]any{"tools": s.toolDescriptors()})
	case "tools/call":
		s.handleCall(w, r, request)
	default:
		writePluginHookMCPError(w, request.ID, -32601, "unsupported method "+request.Method)
	}
}

func (s *credentialMCPServer) toolDescriptors() []map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	credentials := make([]map[string]string, 0, len(s.capabilities))
	for credentialID, entry := range s.capabilities {
		capability := entry.capability
		credentials = append(credentials, map[string]string{
			"id": credentialID, "name": capability.CredentialName, "purpose": capability.Purpose,
		})
	}
	sort.Slice(credentials, func(i, j int) bool { return credentials[i]["id"] < credentials[j]["id"] })
	return []map[string]any{{
		"name":        "get_credential",
		"description": "Returns one authorized credential for the current task. Available credentials: " + formatCredentialMCPDescriptions(credentials),
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"credential_id": map[string]any{"type": "string", "enum": credentialMCPIDs(s.capabilities)},
			},
			"required": []string{"credential_id"},
		},
	}}
}

func credentialMCPIDs(capabilities map[string]credentialMCPEntry) []string {
	ids := make([]string, 0, len(capabilities))
	for credentialID := range capabilities {
		ids = append(ids, credentialID)
	}
	sort.Strings(ids)
	return ids
}

func formatCredentialMCPDescriptions(credentials []map[string]string) string {
	parts := make([]string, 0, len(credentials))
	for _, credential := range credentials {
		name := strings.TrimSpace(credential["name"])
		purpose := strings.TrimSpace(credential["purpose"])
		if purpose == "" {
			parts = append(parts, name)
			continue
		}
		parts = append(parts, name+" ("+purpose+")")
	}
	return strings.Join(parts, "; ")
}

func (s *credentialMCPServer) handleCall(w http.ResponseWriter, r *http.Request, request pluginHookMCPRequest) {
	var params struct {
		Name      string `json:"name"`
		Arguments struct {
			CredentialID string `json:"credential_id"`
		} `json:"arguments"`
	}
	if err := json.Unmarshal(request.Params, &params); err != nil || params.Name != "get_credential" {
		writePluginHookMCPError(w, request.ID, -32602, "invalid credential tool call")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.capabilities[strings.TrimSpace(params.Arguments.CredentialID)]
	if !ok {
		writePluginHookMCPError(w, request.ID, -32602, "credential is not available for this task")
		return
	}
	if len(entry.plaintext) == 0 {
		if s.resolve == nil {
			writePluginHookMCPError(w, request.ID, -32603, "credential capability resolver unavailable")
			return
		}
		plaintext, err := s.resolve(r.Context(), entry.capability)
		if err != nil {
			writePluginHookMCPError(w, request.ID, -32603, "credential capability rejected")
			return
		}
		if len(plaintext) == 0 {
			writePluginHookMCPError(w, request.ID, -32603, "resolved credential is empty")
			return
		}
		entry.plaintext = plaintext
		s.capabilities[entry.capability.CredentialID] = entry
	}
	writePluginHookMCPResult(w, request.ID, map[string]any{
		"content": []map[string]any{{"type": "text", "text": string(entry.plaintext)}},
	})
}
