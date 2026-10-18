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
  beforeEach(() => {
    vi.clearAllMocks();
    mockGetPolicy.mockResolvedValue({
      version: 4,
      rules: [{ id: "ssh", pattern: "**/.ssh/**", description: "SSH keys", enabled: true, builtin: true }],
    });
    mockUpdatePolicy.mockImplementation(async (policy) => ({ ...policy, version: policy.version + 1 }));
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
});
