// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { CredentialManager } from "./credential-manager";
import { renderWithI18n } from "../test/i18n";

const apiMock = vi.hoisted(() => ({
  getCredentialTransportKey: vi.fn(),
  createIssueCredential: vi.fn(),
  replaceIssueCredential: vi.fn(),
  listIssueCredentials: vi.fn(),
  revokeIssueCredential: vi.fn(),
  listCredentialAudit: vi.fn(),
  encryptCredentialForTransport: vi.fn(async (_plaintext: string, _key: unknown, _context: { resource_id: string }) => ({ version: 1, key_id: "key-id", algorithm: "RSA-OAEP-256+A256GCM", wrapped_data_key: "wrapped", nonce: "nonce", ciphertext: "ciphertext" })),
  memberRole: "member" as string,
  workspaceIssues: [] as unknown[],
}));

vi.mock("@multica/core/api", () => ({ api: apiMock }));
vi.mock("@multica/core/auth", () => ({ useAuthStore: (selector: (state: unknown) => unknown) => selector({ user: { id: "member-id" } }) }));
vi.mock("@multica/core/hooks", () => ({ useWorkspaceId: () => "workspace-id" }));
vi.mock("@multica/core/credentials/envelope", () => ({
  CREDENTIAL_ENVELOPE_VERSION: 1,
  encryptCredentialForTransport: apiMock.encryptCredentialForTransport,
}));
vi.mock("@multica/core/issues/queries", () => ({ issueListOptions: () => ({ queryKey: ["issues"], queryFn: async () => apiMock.workspaceIssues }) }));
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

function renderManager(workspaceMode = false, includeIssue = true) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return renderWithI18n(<QueryClientProvider client={queryClient}><CredentialManager {...(includeIssue ? { issue } : {})} workspaceMode={workspaceMode} /></QueryClientProvider>);
}

async function openCredentialForm() {
  fireEvent.click(await screen.findByRole("button", { name: "Credentials" }));
  fireEvent.click(screen.getByRole("button", { name: /deliver credential/i }));
}

describe("CredentialManager", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    apiMock.listIssueCredentials.mockResolvedValue([]);
    apiMock.getCredentialTransportKey.mockResolvedValue({ key_id: "key-id", algorithm: "RSA-OAEP-256+A256GCM", public_key_spki: "public-key" });
    apiMock.createIssueCredential.mockResolvedValue({ id: "saved-id", name: "Build token" });
    apiMock.replaceIssueCredential.mockResolvedValue({ id: "saved-id", name: "Build token" });
    apiMock.listCredentialAudit.mockResolvedValue([]);
    apiMock.memberRole = "member";
    apiMock.workspaceIssues = [issue];
  });

  it("starts collapsed and opens a connector-free delivery form", async () => {
    renderManager();
    const toggle = await screen.findByRole("button", { name: "Credentials" });
    expect(toggle).toHaveAttribute("aria-expanded", "false");
    expect(screen.queryByRole("button", { name: /deliver credential/i })).not.toBeInTheDocument();

    fireEvent.click(toggle);
    expect(toggle).toHaveAttribute("aria-expanded", "true");
    fireEvent.click(screen.getByRole("button", { name: /deliver credential/i }));
    expect(screen.getByLabelText("Name")).toBeInTheDocument();
    expect(screen.queryByLabelText("Connector")).not.toBeInTheDocument();
  });

  it("clears a failed secret and does not put plaintext or connector fields in the request", async () => {
    apiMock.createIssueCredential.mockRejectedValueOnce(new Error("request failed"));
    renderManager();
    await openCredentialForm();
    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "Build token" } });
    fireEvent.change(screen.getByLabelText("Purpose"), { target: { value: "Publish artifacts" } });
    fireEvent.change(screen.getByLabelText("Secret value"), { target: { value: "synthetic-secret-value" } });
    fireEvent.click(screen.getByRole("combobox", { name: "Allowed agent or squad" }));
    fireEvent.click(await screen.findByRole("option", { name: "Agent: Builder" }));
    fireEvent.click(screen.getByRole("button", { name: "Save and authorize" }));

    await waitFor(() => expect(screen.getByLabelText("Secret value")).toHaveValue(""));
    expect(await screen.findByRole("status")).toHaveTextContent("secret was cleared");
    const request = apiMock.createIssueCredential.mock.calls[0]?.[1];
    expect(JSON.stringify(request)).not.toContain("synthetic-secret-value");
    expect(request).not.toHaveProperty("connector_id");
    expect(request).not.toHaveProperty("target_service");
  });

  it("limits a responsible member to task scope and requires a separate subject selection", async () => {
    renderManager();
    await openCredentialForm();
    fireEvent.click(screen.getByRole("combobox", { name: "Resource access" }));
    expect(screen.getByRole("option", { name: "Project" })).toHaveAttribute("aria-disabled", "true");
    expect(screen.getByRole("option", { name: "This task (ABC-12)" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Save and authorize" })).toBeDisabled();
  });

  it("replaces through one atomic request without a follow-up revoke", async () => {
    apiMock.listIssueCredentials.mockResolvedValue([{
      id: "existing-id", name: "Build token", target_service: "Build token", purpose: "Publish artifacts", status: "active", resource_scope: "issue",
      subject_type: "agent", subject_id: "agent-id", expires_at: null, created_at: "2026-10-21T00:00:00Z",
    }]);
    renderManager();
    fireEvent.click(await screen.findByRole("button", { name: "Credentials" }));
    fireEvent.click(await screen.findByRole("button", { name: "Replace Build token" }));
    fireEvent.change(screen.getByLabelText("Secret value"), { target: { value: "replacement-secret" } });
    fireEvent.click(screen.getByRole("button", { name: "Replace atomically" }));
    await waitFor(() => expect(apiMock.replaceIssueCredential).toHaveBeenCalledOnce());
    expect(apiMock.revokeIssueCredential).not.toHaveBeenCalled();
    expect(JSON.stringify(apiMock.replaceIssueCredential.mock.calls[0]?.[2])).not.toContain("replacement-secret");
  });

  it("uses the global resource sentinel without a connector", async () => {
    apiMock.memberRole = "owner";
    const user = userEvent.setup();
    renderManager();
    await openCredentialForm();
    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "Global token" } });
    fireEvent.change(screen.getByLabelText("Purpose"), { target: { value: "Deploy shared assets" } });
    fireEvent.change(screen.getByLabelText("Secret value"), { target: { value: "synthetic-global-secret" } });
    await user.click(screen.getByRole("combobox", { name: "Allowed agent or squad" }));
    await user.click(await screen.findByRole("option", { name: "Agent: Builder" }));
    await user.click(screen.getByRole("combobox", { name: "Resource access" }));
    await user.click(await screen.findByRole("option", { name: "Global" }));
    fireEvent.change(await screen.findByLabelText(/Expires/), { target: { value: "2030-01-01T12:00" } });
    fireEvent.click(screen.getByRole("button", { name: "Save and authorize" }));
    await waitFor(() => expect(apiMock.createIssueCredential).toHaveBeenCalledOnce());
    expect(apiMock.createIssueCredential.mock.calls[0]?.[1].resource_id).toBe("global");
    expect(apiMock.encryptCredentialForTransport.mock.calls[0]?.[2].resource_id).toBe("global");
  });

  it("opens delivery from the Settings workspace mode without an issue prop", async () => {
    apiMock.memberRole = "owner";
    const user = userEvent.setup();
    renderManager(true, false);
    await user.click(await screen.findByRole("combobox", { name: "Task" }));
    await user.click(await screen.findByRole("option", { name: "ABC-12 · Build" }));
    await user.click(await screen.findByRole("button", { name: /deliver credential/i }));
    expect(screen.getByLabelText("Name")).toBeInTheDocument();
  });
});
