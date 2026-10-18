// @vitest-environment jsdom
import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@multica/core/i18n/react";
import enAdmin from "../locales/en/admin.json";

const mockGetPolicy = vi.hoisted(() => vi.fn());
const mockUpdatePolicy = vi.hoisted(() => vi.fn());
const mockRestorePolicy = vi.hoisted(() => vi.fn());

vi.mock("@multica/core/api", () => ({
  api: {
    adminGetFileReadPolicy: mockGetPolicy,
    adminUpdateFileReadPolicy: mockUpdatePolicy,
    adminRestoreFileReadPolicy: mockRestorePolicy,
  },
}));

import { AdminFileReadPolicyPage } from "./admin-file-read-policy-page";

function renderPage() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  function Wrapper({ children }: { children: ReactNode }) {
    return (
      <I18nProvider locale="en" resources={{ en: { admin: enAdmin } }}>
        <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
      </I18nProvider>
    );
  }
  return render(<AdminFileReadPolicyPage />, { wrapper: Wrapper });
}

describe("AdminFileReadPolicyPage", () => {
  let persistedPolicy: {
    version: number;
    rules: Array<{ id: string; pattern: string; description: string; enabled: boolean; builtin: boolean }>;
  };

  beforeEach(() => {
    vi.clearAllMocks();
    persistedPolicy = {
      version: 4,
      rules: [{ id: "ssh", pattern: "**/.ssh/**", description: "SSH keys", enabled: true, builtin: true }],
    };
    mockGetPolicy.mockImplementation(async () => persistedPolicy);
    mockUpdatePolicy.mockImplementation(async (policy) => {
      persistedPolicy = { ...policy, version: policy.version + 1 };
      return persistedPolicy;
    });
    mockRestorePolicy.mockResolvedValue({ version: 5, rules: [] });
  });

  it("confirms and saves an enabled-state change with the loaded version", async () => {
    const user = userEvent.setup();
    const confirm = vi.spyOn(window, "confirm").mockReturnValue(true);
    renderPage();

    await waitFor(() => expect(screen.getByRole("checkbox")).toHaveProperty("checked", true));
    await user.click(screen.getByRole("checkbox"));
    await user.click(screen.getByRole("button", { name: "Save changes" }));

    expect(confirm).toHaveBeenCalledOnce();
    await waitFor(() => expect(mockUpdatePolicy).toHaveBeenCalledOnce());
    expect(mockUpdatePolicy).toHaveBeenCalledWith({
      version: 4,
      rules: [expect.objectContaining({ id: "ssh", enabled: false })],
    });
    confirm.mockRestore();
  });

  it("edits existing and new rule text, saves it, and reloads the persisted policy", async () => {
    const user = userEvent.setup();
    const confirm = vi.spyOn(window, "confirm").mockReturnValue(true);
    const page = renderPage();

    await waitFor(() => expect(screen.getAllByRole("textbox")).toHaveLength(4));
    const initialFields = screen.getAllByRole("textbox");
    const existingPattern = initialFields[0]!;
    const existingDescription = initialFields[1]!;
    const newPattern = initialFields[2]!;
    const newDescription = initialFields[3]!;
    await user.clear(existingPattern);
    await user.type(existingPattern, "**/.ssh/private/**");
    await user.clear(existingDescription);
    await user.type(existingDescription, "Private SSH keys");
    await user.type(newPattern, "**/.config/service/**");
    await user.type(newDescription, "Service credentials");
    await user.click(screen.getByRole("button", { name: "Add rule" }));

    const addedFields = screen.getAllByRole("textbox");
    const addedPattern = addedFields[2]!;
    const addedDescription = addedFields[3]!;
    await user.clear(addedPattern);
    await user.type(addedPattern, "**/.config/service/token");
    await user.clear(addedDescription);
    await user.type(addedDescription, "Service access token");
    await user.click(screen.getByRole("button", { name: "Save changes" }));

    await waitFor(() => expect(mockUpdatePolicy).toHaveBeenCalledOnce());
    expect(mockUpdatePolicy).toHaveBeenCalledWith({
      version: 4,
      rules: [
        expect.objectContaining({ id: "ssh", pattern: "**/.ssh/private/**", description: "Private SSH keys" }),
        expect.objectContaining({ pattern: "**/.config/service/token", description: "Service access token" }),
      ],
    });

    page.unmount();
    renderPage();
    await waitFor(() => expect(screen.getAllByRole("textbox")).toHaveLength(6));
    expect(screen.getAllByRole("textbox").slice(0, 4).map((input) => (input as HTMLInputElement).value)).toEqual([
      "**/.ssh/private/**",
      "Private SSH keys",
      "**/.config/service/token",
      "Service access token",
    ]);
    confirm.mockRestore();
  });
});
