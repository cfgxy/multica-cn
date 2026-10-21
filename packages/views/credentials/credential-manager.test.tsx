import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { CredentialManager } from "./credential-manager";
import { renderWithI18n } from "../test/i18n";

const apiMock = vi.hoisted(() => ({
  getCredentialTransportKey: vi.fn(),
  listCredentialConnectors: vi.fn(),
  createIssueCredential: vi.fn(),
  listIssueCredentials: vi.fn(),
  revokeIssueCredential: vi.fn(),
}));

vi.mock("@multica/core/api", () => ({ api: apiMock }));
vi.mock("@multica/core/auth", () => ({ useAuthStore: (selector: (state: unknown) => unknown) => selector({ user: { id: "member-id" } }) }));
vi.mock("@multica/core/hooks", () => ({ useWorkspaceId: () => "workspace-id" }));
vi.mock("@multica/core/credentials/envelope", () => ({
  CREDENTIAL_ENVELOPE_VERSION: 1,
  encryptCredentialForTransport: vi.fn(async () => ({ version: 1, key_id: "key-id", algorithm: "RSA-OAEP-256+A256GCM", wrapped_data_key: "wrapped", nonce: "nonce", ciphertext: "ciphertext" })),
}));
vi.mock("@multica/core/issues/queries", () => ({ issueListOptions: () => ({ queryKey: ["issues"], queryFn: async () => [] }) }));
vi.mock("@multica/core/workspace/queries", () => ({
  memberListOptions: () => ({ queryKey: ["members"], queryFn: async () => [{ user_id: "member-id", role: "member" }] }),
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

function renderManager() {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return renderWithI18n(<QueryClientProvider client={queryClient}><CredentialManager issue={issue} /></QueryClientProvider>);
}

describe("CredentialManager", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    apiMock.listCredentialConnectors.mockResolvedValue([{ id: "connector-id", name: "Build service", status: "active", revision: 1 }]);
    apiMock.listIssueCredentials.mockResolvedValue([]);
    apiMock.getCredentialTransportKey.mockResolvedValue({ key_id: "key-id", algorithm: "RSA-OAEP-256+A256GCM", public_key_spki: "public-key" });
    apiMock.createIssueCredential.mockResolvedValue({ id: "saved-id", name: "Build token" });
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
});
