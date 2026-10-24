// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { CredentialManager } from "./credential-manager";
import { renderWithI18n } from "../test/i18n";

const apiMock = vi.hoisted(() => ({
  getCredentialTransportKey: vi.fn(),
  listCredentialConnectors: vi.fn(),
  createCredentialConnector: vi.fn(),
  updateCredentialConnector: vi.fn(),
  createIssueCredential: vi.fn(),
  replaceIssueCredential: vi.fn(),
  listIssueCredentials: vi.fn(),
  revokeIssueCredential: vi.fn(),
  listCredentialAudit: vi.fn(),
  encryptCredentialForTransport: vi.fn(async (_plaintext: string, _key: unknown, _context: { resource_id: string }) => ({ version: 1, key_id: "key-id", algorithm: "RSA-OAEP-256+A256GCM", wrapped_data_key: "wrapped", nonce: "nonce", ciphertext: "ciphertext" })),
  memberRole: "member" as string,
}));

vi.mock("@multica/core/api", () => ({ api: apiMock }));
vi.mock("@multica/core/auth", () => ({ useAuthStore: (selector: (state: unknown) => unknown) => selector({ user: { id: "member-id" } }) }));
vi.mock("@multica/core/hooks", () => ({ useWorkspaceId: () => "workspace-id" }));
vi.mock("@multica/core/credentials/envelope", () => ({
  CREDENTIAL_ENVELOPE_VERSION: 1,
  encryptCredentialForTransport: apiMock.encryptCredentialForTransport,
}));
vi.mock("@multica/core/issues/queries", () => ({ issueListOptions: () => ({ queryKey: ["issues"], queryFn: async () => [] }) }));
vi.mock("@multica/core/workspace/queries", () => ({
  memberListOptions: () => ({ queryKey: ["members"], queryFn: async () => [{ user_id: "member-id", role: apiMock.memberRole }] }),
  agentListOptions: () => ({ queryKey: ["agents"], queryFn: async () => [{ id: "agent-id", name: "Builder", archived_at: null }] }),
  squadListOptions: () => ({ queryKey: ["squads"], queryFn: async () => [] }),
}));

const issue = {
  id: "issue-id",
  identifier: "ABC-12",
  title: "Build",
  status: "todo",
  status_category: "todo",
  project_id: "project-id",
  assignee_type: "member",
  assignee_id: "member-id",
} as never;

function renderManager(workspaceMode = false) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return renderWithI18n(<QueryClientProvider client={queryClient}><CredentialManager issue={issue} workspaceMode={workspaceMode} /></QueryClientProvider>);
}

describe("CredentialManager", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    apiMock.listCredentialConnectors.mockResolvedValue([{ id: "connector-id", name: "Build service", status: "active", revision: 1 }]);
    apiMock.listIssueCredentials.mockResolvedValue([]);
    apiMock.getCredentialTransportKey.mockResolvedValue({ key_id: "key-id", algorithm: "RSA-OAEP-256+A256GCM", public_key_spki: "public-key" });
    apiMock.createIssueCredential.mockResolvedValue({ id: "saved-id", name: "Build token" });
    apiMock.replaceIssueCredential.mockResolvedValue({ id: "saved-id", name: "Build token" });
    apiMock.listCredentialAudit.mockResolvedValue([]);
    apiMock.createCredentialConnector.mockResolvedValue({ id: "new-connector-id", name: "New target", kind: "mcp_http_bearer", target_url: "https://example.com/mcp", adapter_policy: "mcp_http_v1", auth_field: "authorization_bearer", status: "active", revision: 1 });
    apiMock.updateCredentialConnector.mockResolvedValue({ id: "connector-id", name: "Build service", kind: "mcp_http_bearer", target_url: "https://build.example.com", adapter_policy: "mcp_http_v1", auth_field: "authorization_bearer", status: "disabled", revision: 2 });
    apiMock.memberRole = "member";
  });

  it("clears the secret after a failed save and never puts plaintext in the request envelope", async () => {
    apiMock.createIssueCredential.mockRejectedValueOnce(new Error("request failed"));
    renderManager();
    fireEvent.click(await screen.findByRole("button", { name: /deliver credential/i }));
    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "Build token" } });
    fireEvent.change(screen.getByLabelText("Purpose"), { target: { value: "Publish artifacts" } });
    fireEvent.change(screen.getByLabelText("Secret value"), { target: { value: "synthetic-secret-value" } });
    fireEvent.click(screen.getByRole("combobox", { name: "Connector" }));
    fireEvent.click(await screen.findByRole("option", { name: "Build service" }));
    fireEvent.click(screen.getByRole("combobox", { name: "Allowed agent or squad" }));
    fireEvent.click(await screen.findByRole("option", { name: "Agent: Builder" }));
    fireEvent.click(screen.getByRole("button", { name: "Save and authorize" }));

    expect(screen.getByLabelText("Secret value")).toHaveValue("");
    await waitFor(() => expect(screen.getByLabelText("Secret value")).toHaveValue(""));
    expect(await screen.findByRole("status")).toHaveTextContent("secret was cleared");
    const request = apiMock.createIssueCredential.mock.calls[0]?.[1];
    expect(request).toBeDefined();
    expect(JSON.stringify(request.envelope)).not.toContain("synthetic-secret-value");
    expect(JSON.stringify(request)).not.toContain("synthetic-secret-value");
  });

  it("limits a responsible member to task scope and requires a separate subject selection", async () => {
    renderManager();
    fireEvent.click(await screen.findByRole("button", { name: /deliver credential/i }));
    fireEvent.click(screen.getByRole("combobox", { name: "Resource access" }));
    expect(screen.getByRole("option", { name: "Project" })).toHaveAttribute("aria-disabled", "true");
    expect(screen.getByRole("option", { name: "This task (ABC-12)" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Save and authorize" })).toBeDisabled();
  });

  it("replaces through one atomic request without a follow-up revoke", async () => {
    apiMock.listIssueCredentials.mockResolvedValue([{
      id: "existing-id", name: "Build token", target_service: "Build service", connector_id: "connector-id",
      connector_revision: 1, purpose: "Publish artifacts", status: "active", resource_scope: "issue",
      subject_type: "agent", subject_id: "agent-id", expires_at: null, created_at: "2026-10-21T00:00:00Z",
    }]);
    renderManager();
    fireEvent.click(await screen.findByRole("button", { name: "Replace Build token" }));
    fireEvent.change(screen.getByLabelText("Secret value"), { target: { value: "replacement-secret" } });
    fireEvent.click(screen.getByRole("button", { name: "Replace atomically" }));
    await waitFor(() => expect(apiMock.replaceIssueCredential).toHaveBeenCalledOnce());
    expect(apiMock.revokeIssueCredential).not.toHaveBeenCalled();
    expect(JSON.stringify(apiMock.replaceIssueCredential.mock.calls[0]?.[2])).not.toContain("replacement-secret");
  });

  it("keeps the narrow replacement form clear of the chat launcher without exposing its secret", async () => {
    apiMock.listIssueCredentials.mockResolvedValue([{
      id: "existing-id", name: "Build token", target_service: "Build service", connector_id: "connector-id",
      connector_revision: 1, purpose: "Publish artifacts", status: "active", resource_scope: "issue",
      subject_type: "agent", subject_id: "agent-id", expires_at: null, created_at: "2026-10-21T00:00:00Z",
    }]);
    const user = userEvent.setup();
    renderManager();
    await user.click(await screen.findByRole("button", { name: "Replace Build token" }));

    const form = screen.getByRole("textbox", { name: "Purpose" }).closest("form");
    expect(form).toHaveClass("max-md:pe-chat-launcher");

    const secret = screen.getByLabelText("Secret value");
    expect(secret).toHaveAttribute("type", "password");
    expect(secret).toHaveAttribute("aria-describedby", "credential-secret-help");
    for (let index = 0; index < 12 && document.activeElement !== secret; index += 1) {
      await user.tab();
    }
    expect(secret).toHaveFocus();
    await user.type(secret, "replacement-secret-canary");
    expect(form).toHaveTextContent("Text machine credentials only");
    expect(form?.textContent).not.toContain("replacement-secret-canary");
    expect(screen.getByRole("button", { name: "Replace atomically" })).toBeVisible();
  });

  it("submits global scope with the global resource sentinel in both request and envelope context", async () => {
    apiMock.memberRole = "owner";
    const user = userEvent.setup();
    renderManager();
    fireEvent.click(await screen.findByRole("button", { name: /deliver credential/i }));
    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "Global token" } });
    fireEvent.change(screen.getByLabelText("Purpose"), { target: { value: "Deploy shared assets" } });
    fireEvent.change(screen.getByLabelText("Secret value"), { target: { value: "synthetic-global-secret" } });
    await user.click(screen.getByRole("combobox", { name: "Connector" }));
    await user.click(await screen.findByRole("option", { name: "Build service" }));
    await user.click(screen.getByRole("combobox", { name: "Allowed agent or squad" }));
    await user.click(await screen.findByRole("option", { name: "Agent: Builder" }));
    await user.click(screen.getByRole("combobox", { name: "Resource access" }));
    await user.click(await screen.findByRole("option", { name: "Global" }));
    fireEvent.change(await screen.findByLabelText(/Expires/), { target: { value: "2030-01-01T12:00" } });
    fireEvent.click(screen.getByRole("button", { name: "Save and authorize" }));
    await waitFor(() => expect(apiMock.createIssueCredential).toHaveBeenCalledOnce());
    expect(apiMock.createIssueCredential.mock.calls[0]?.[1].resource_id).toBe("global");
    expect(apiMock.encryptCredentialForTransport.mock.calls[0]?.[2].resource_id).toBe("global");
    expect(JSON.stringify(apiMock.createIssueCredential.mock.calls[0]?.[1])).not.toContain("synthetic-global-secret");
  });

  it("explains the empty connector state instead of rendering a dead dropdown", async () => {
    apiMock.listCredentialConnectors.mockResolvedValue([]);
    renderManager();
    fireEvent.click(await screen.findByRole("button", { name: /deliver credential/i }));
    const trigger = screen.getByRole("combobox", { name: "Connector" });
    fireEvent.click(trigger);
    expect(await screen.findByText(/no connectors yet/i)).toBeInTheDocument();
    expect(screen.getByText(/no active connector yet/i)).toBeInTheDocument();
    // The empty dropdown must not leave a save path silently blocked:
    // the form's save button stays disabled until a connector exists.
    expect(screen.getByRole("button", { name: "Save and authorize" })).toBeDisabled();
  });

  it("surfaces a connector load failure with a retry affordance", async () => {
    apiMock.memberRole = "owner";
    apiMock.listCredentialConnectors.mockRejectedValue(new Error("network down"));
    renderManager(true);
    expect(await screen.findByText(/could not load connectors/i)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Retry" }));
    await waitFor(() => expect(apiMock.listCredentialConnectors).toHaveBeenCalledTimes(2));
  });

  it("lets an owner add a connector and toggle its status from the management panel", async () => {
    apiMock.memberRole = "owner";
    apiMock.listCredentialConnectors.mockResolvedValue([{ id: "connector-id", name: "Build service", status: "disabled", revision: 1 }]);
    renderManager(true);
    expect(await screen.findByText(/connectors/i, { selector: "h4" })).toBeInTheDocument();
    fireEvent.change(screen.getByLabelText("Add connector"), { target: { value: "Deploy target" } });
    const targetInput = screen.getByPlaceholderText(/https:\/\/example\.com/i);
    fireEvent.change(targetInput, { target: { value: "https://deploy.example.com/mcp" } });
    fireEvent.click(screen.getByRole("button", { name: "Add connector" }));
    await waitFor(() => expect(apiMock.createCredentialConnector).toHaveBeenCalledWith("workspace-id", { name: "Deploy target", target_url: "https://deploy.example.com/mcp" }));
    expect(await screen.findByText("Connector added.")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Enable" }));
    await waitFor(() => expect(apiMock.updateCredentialConnector).toHaveBeenCalledWith("workspace-id", "connector-id", expect.objectContaining({ status: "active", revision: 1 })));
  });

  it("shows credential audit metadata only to workspace owners", async () => {
    apiMock.memberRole = "owner";
    apiMock.listCredentialAudit.mockResolvedValue([{
      id: "audit-id", created_at: "2026-10-21T00:00:00Z", credential_id: "credential-id", event_type: "credential.broker_resolved",
      result: "denied", reason_code: "subject_mismatch", actor_type: "agent", actor_id: "agent-id",
      project_id: "project-id", project_title: "Build project", issue_id: "issue-id", issue_title: "Build issue",
      task_id: "task-id", agent_id: "agent-id", purpose: "Publish artifacts",
    }]);
    renderManager(true);
    expect(await screen.findByText(/credential\.broker_resolved/)).toBeInTheDocument();
    expect(screen.getByText(/Build project/)).toBeInTheDocument();
    expect(screen.getByText(/Build issue/)).toBeInTheDocument();
    expect(screen.getByText(/agent-id/)).toBeInTheDocument();
    expect(screen.getByText(/Publish artifacts/)).toBeInTheDocument();
    expect(apiMock.listCredentialAudit).toHaveBeenCalledWith("workspace-id");
  });
});
