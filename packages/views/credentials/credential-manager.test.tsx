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
