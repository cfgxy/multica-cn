package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func TestTaskCredentialMCPResolvesOnlyListedCredential(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	capability := CredentialCapability{
		Capability: "capability", CredentialID: "credential-id", CredentialName: "Deploy token", Purpose: "Publish the release",
	}
	var resolved CredentialCapability
	config, set, err := startTaskCredentialMCP(ctx, "task-id", []CredentialCapability{capability}, func(_ context.Context, candidate CredentialCapability) ([]byte, error) {
		resolved = candidate
		return []byte("synthetic-secret"), nil
	}, nil)
	if err != nil {
		t.Fatalf("startTaskCredentialMCP() error = %v", err)
	}
	defer set.Close()
	var parsed struct {
		MCPServers map[string]struct {
			URL string `json:"url"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(config, &parsed); err != nil {
		t.Fatal(err)
	}
	url := parsed.MCPServers[credentialMCPServerName("task-id")].URL
	if url == "" {
		t.Fatalf("MCP config = %s", config)
	}

	response := callTaskCredentialMCP(t, url, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	tools, ok := response["result"].(map[string]any)["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("tools/list response = %#v", response)
	}
	if resolved.CredentialID != "" {
		t.Fatalf("resolver ran before the credential tool was called: %#v", resolved)
	}

	response = callTaskCredentialMCP(t, url, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"get_credential","arguments":{"credential_id":"credential-id"}}}`)
	result, ok := response["result"].(map[string]any)
	if !ok || result["isError"] == true {
		t.Fatalf("tools/call response = %#v", response)
	}
	content, ok := result["content"].([]any)
	if !ok || len(content) != 1 || content[0].(map[string]any)["text"] != "synthetic-secret" {
		t.Fatalf("tools/call content = %#v", result["content"])
	}
	if resolved.CredentialID != capability.CredentialID || resolved.ConnectorID != "" {
		t.Fatalf("resolver capability = %#v", resolved)
	}

	response = callTaskCredentialMCP(t, url, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"get_credential","arguments":{"credential_id":"other"}}}`)
	if _, ok := response["error"]; !ok {
		t.Fatalf("unlisted credential response = %#v", response)
	}
}

func TestTaskCredentialMCPResolveFailureDoesNotExposeCredentials(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	capability := CredentialCapability{Capability: "capability", CredentialID: "credential-id"}
	config, set, err := startTaskCredentialMCP(ctx, "task-id", []CredentialCapability{capability}, func(context.Context, CredentialCapability) ([]byte, error) {
		return nil, errors.New("capability rejected")
	}, nil)
	if err != nil {
		t.Fatalf("startTaskCredentialMCP() error = %v", err)
	}
	defer set.Close()
	var parsed struct {
		MCPServers map[string]struct {
			URL string `json:"url"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(config, &parsed); err != nil {
		t.Fatal(err)
	}
	url := parsed.MCPServers[credentialMCPServerName("task-id")].URL
	response := callTaskCredentialMCP(t, url, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_credential","arguments":{"credential_id":"credential-id"}}}`)
	if _, ok := response["error"]; !ok {
		t.Fatalf("rejected credential response = %#v", response)
	}
}

func TestCredentialMCPServerNameIsTaskScoped(t *testing.T) {
	first := credentialMCPServerName("task-a")
	second := credentialMCPServerName("task-b")
	if first == second || first == "multica-credentials" || second == "multica-credentials" {
		t.Fatalf("credential MCP names must be task scoped: %q, %q", first, second)
	}
}

func TestTaskCredentialMCPRejectsMixedCapability(t *testing.T) {
	_, _, err := startTaskCredentialMCP(context.Background(), "task-id", []CredentialCapability{{
		Capability: "capability", CredentialID: "credential-id", ConnectorID: "connector-id",
	}}, func(context.Context, CredentialCapability) ([]byte, error) {
		return nil, nil
	}, nil)
	if err == nil {
		t.Fatal("mixed capability unexpectedly started a local credential MCP server")
	}
}

func TestCredentialMCPCloseIsSafeWithConcurrentReads(t *testing.T) {
	server := &credentialMCPServer{capabilities: map[string]credentialMCPEntry{
		"credential-id": {
			capability: CredentialCapability{CredentialID: "credential-id", CredentialName: "Deploy token"},
			plaintext:  []byte("synthetic-secret"),
		},
	}}
	set := &credentialMCPSet{credentials: server}
	start := make(chan struct{})
	var wait sync.WaitGroup
	for range 8 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			for range 100 {
				_ = server.toolDescriptors()
				recorder := httptest.NewRecorder()
				server.handleCall(recorder, nil, pluginHookMCPRequest{
					ID:     json.RawMessage("1"),
					Method: "tools/call",
					Params: json.RawMessage(`{"name":"get_credential","arguments":{"credential_id":"credential-id"}}`),
				})
			}
		}()
	}
	close(start)
	set.Close()
	wait.Wait()
	if descriptors := server.toolDescriptors(); len(descriptors) != 1 {
		t.Fatalf("tools/list descriptors after close = %#v", descriptors)
	}
}

func callTaskCredentialMCP(t *testing.T, url, body string) map[string]any {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, url, bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("response %q: %v", data, err)
	}
	return decoded
}
