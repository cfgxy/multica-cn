// @vitest-environment jsdom

import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { I18nProvider } from "@multica/core/i18n/react";
// The real error class, not a stand-in: the dialog decides whether a failure
// is a drift by `instanceof ApiError`, so a look-alike would let the stale
// path pass here while never firing in the app.
import { ApiError } from "@multica/core/api";
import enCommon from "../locales/en/common.json";
import enPromptMarket from "../locales/en/prompt-market.json";

const mockPreview = vi.hoisted(() => vi.fn());
const mockApply = vi.hoisted(() => vi.fn());

const data = vi.hoisted(() => ({
  agents: [] as Array<Record<string, unknown>>,
  squads: [] as Array<Record<string, unknown>>,
  role: "owner" as "owner" | "admin" | "member",
}));

const apiError = (status: number, body?: unknown) =>
  new ApiError("request failed", status, "error", body);

vi.mock("@tanstack/react-query", () => ({
  useQuery: (options: { queryKey: readonly unknown[] }) => {
    const kind = options.queryKey[2];
    if (kind === "squads") return { data: data.squads, isLoading: false };
    return { data: data.agents, isLoading: false };
  },
}));

vi.mock("@multica/core/workspace/queries", () => ({
  agentListOptions: (wsId: string) => ({ queryKey: ["workspaces", wsId, "agents"] }),
  squadListOptions: (wsId: string) => ({ queryKey: ["workspaces", wsId, "squads"] }),
}));

vi.mock("@multica/core/workspace/mutations", () => ({
  usePreviewPromptApply: () => ({ mutateAsync: mockPreview, isPending: false }),
  useApplyPrompt: () => ({ mutateAsync: mockApply, isPending: false }),
}));

vi.mock("@multica/core/permissions", () => ({
  useCurrentMember: () => ({ role: data.role, isLoading: false }),
}));

vi.mock("@multica/core/utils", () => ({
  createSafeId: () => "operation-id",
}));

import { PromptApplyDialog } from "./prompt-apply-dialog";

const TEST_RESOURCES = {
  en: { common: enCommon, "prompt-market": enPromptMarket },
};

function Wrapper({ children }: { children: ReactNode }) {
  return (
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      {children}
    </I18nProvider>
  );
}

const install = (over: Record<string, unknown> = {}) => ({
  id: "install-1",
  series_id: "series-1",
  kind: "agent_prompt",
  installed_version_id: "version-1",
  installed_version: 2,
  installed_content_sha256: "incoming-hash",
  name: "Support triage",
  summary: "Sorts inbound tickets.",
  publisher_display_name: "Dana Wu",
  license_code: "cc0",
  installed_at: "2026-09-02T00:00:00Z",
  updated_at: "2026-09-02T00:00:00Z",
  ...over,
});

const preview = (over: Record<string, unknown> = {}) => ({
  target_type: "agent",
  target_id: "agent-1",
  current_content: "old line\nshared line\n",
  current_sha256: "current-hash",
  incoming_content: "new line\nshared line\n",
  incoming_sha256: "incoming-hash",
  target_empty: false,
  identical: false,
  requires_confirmation: true,
  preview_token: "token-1",
  preview_expires_at: "2026-09-08T12:00:00Z",
  ...over,
});

function renderDialog(over: Record<string, unknown> = {}) {
  return render(
    <PromptApplyDialog
      open
      wsId="ws-1"
      install={install(over) as never}
      onOpenChange={vi.fn()}
    />,
    { wrapper: Wrapper },
  );
}

/** Walks the wizard to the review step on the first listed target. */
async function reachReview(user: ReturnType<typeof userEvent.setup>) {
  await user.click(screen.getByRole("radio", { name: /Triage bot/ }));
  await user.click(screen.getByRole("button", { name: "Next" }));
  await screen.findByText(/Will replace existing content|Applying writes/);
}

beforeEach(() => {
  data.agents = [
    { id: "agent-1", name: "Triage bot", archived_at: null },
    { id: "agent-2", name: "Archived bot", archived_at: "2026-01-01T00:00:00Z" },
  ];
  data.squads = [{ id: "squad-1", name: "Support squad", archived_at: null }];
  data.role = "owner";
  mockPreview.mockReset().mockResolvedValue(preview());
  mockApply
    .mockReset()
    .mockResolvedValue({ applied_version: 2, can_restore: true });
});

describe("PromptApplyDialog target step", () => {
  it("lists only targets of the prompt's own kind", () => {
    renderDialog();

    expect(screen.getByRole("radio", { name: /Triage bot/ })).toBeInTheDocument();
    expect(screen.queryByText("Support squad")).not.toBeInTheDocument();
  });

  it("lists squads instead for a squad prompt", () => {
    renderDialog({ kind: "squad_prompt" });

    expect(screen.getByRole("radio", { name: /Support squad/ })).toBeInTheDocument();
    expect(screen.queryByText("Triage bot")).not.toBeInTheDocument();
  });

  it("leaves out archived targets", () => {
    renderDialog();

    expect(screen.queryByText("Archived bot")).not.toBeInTheDocument();
  });
});

describe("PromptApplyDialog review step", () => {
  // The one rule that makes the diff trustworthy: both sides come from the
  // preview response, never from an editor's in-memory draft.
  it("renders the diff from the preview response", async () => {
    const user = userEvent.setup();
    renderDialog();
    await reachReview(user);

    expect(screen.getByText("new line")).toBeInTheDocument();
    expect(screen.getByText("old line")).toBeInTheDocument();
    expect(mockPreview).toHaveBeenCalledWith({
      installId: "install-1",
      target_type: "agent",
      target_id: "agent-1",
    });
  });

  it("defaults to preserve and refuses to apply until replace is chosen", async () => {
    const user = userEvent.setup();
    renderDialog();
    await reachReview(user);

    const preserve = screen.getByRole("radio", { name: /Keep local content/ });
    expect(preserve).toHaveAttribute("aria-checked", "true");
    expect(screen.getByRole("button", { name: "Apply" })).toBeDisabled();

    await user.click(screen.getByRole("radio", { name: /Overwrite with the market/ }));
    await user.click(screen.getByRole("button", { name: "Overwrite and apply" }));

    await waitFor(() =>
      expect(mockApply).toHaveBeenCalledWith(
        expect.objectContaining({ strategy: "replace" }),
      ),
    );
  });

  it("applies straight away when the target is empty", async () => {
    mockPreview.mockResolvedValue(
      preview({ target_empty: true, requires_confirmation: false, current_content: "" }),
    );
    const user = userEvent.setup();
    renderDialog();
    await reachReview(user);

    // Nothing to lose, so no strategy choice is imposed.
    expect(
      screen.queryByRole("radio", { name: /Overwrite with the market/ }),
    ).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Apply" }));
    await waitFor(() =>
      expect(mockApply).toHaveBeenCalledWith(
        expect.objectContaining({ strategy: "preserve" }),
      ),
    );
  });

  it("blocks an apply that would change nothing", async () => {
    mockPreview.mockResolvedValue(
      preview({ identical: true, requires_confirmation: false }),
    );
    const user = userEvent.setup();
    renderDialog();
    await reachReview(user);

    expect(screen.getByText(/already holds exactly this text/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Apply" })).toBeDisabled();
  });

  it("echoes the preview token and the hash the diff was drawn from", async () => {
    const user = userEvent.setup();
    renderDialog();
    await reachReview(user);
    await user.click(screen.getByRole("radio", { name: /Overwrite with the market/ }));
    await user.click(screen.getByRole("button", { name: "Overwrite and apply" }));

    await waitFor(() =>
      expect(mockApply).toHaveBeenCalledWith(
        expect.objectContaining({
          preview_token: "token-1",
          expected_sha256: "current-hash",
          operation_id: "operation-id",
        }),
      ),
    );
  });
});

describe("PromptApplyDialog concurrency", () => {
  it("forces a re-preview when the target drifted, instead of retrying", async () => {
    mockApply.mockRejectedValue(
      apiError(409, { code: "prompt_preview_stale" }),
    );
    const user = userEvent.setup();
    renderDialog();
    await reachReview(user);
    await user.click(screen.getByRole("radio", { name: /Overwrite with the market/ }));
    await user.click(screen.getByRole("button", { name: "Overwrite and apply" }));

    expect(await screen.findByText("Preview expired")).toBeInTheDocument();
    // The confirm button is replaced, so there is no way to retry the write
    // against a comparison the user never saw.
    expect(
      screen.queryByRole("button", { name: "Overwrite and apply" }),
    ).not.toBeInTheDocument();

    mockApply.mockResolvedValue({ applied_version: 2, can_restore: true });
    await user.click(screen.getByRole("button", { name: "Preview again" }));

    await waitFor(() => expect(mockPreview).toHaveBeenCalledTimes(2));
    expect(mockApply).toHaveBeenCalledTimes(1);
  });

  it("reports a failed apply as leaving the target untouched", async () => {
    mockApply.mockRejectedValue(apiError(500));
    const user = userEvent.setup();
    renderDialog();
    await reachReview(user);
    await user.click(screen.getByRole("radio", { name: /Overwrite with the market/ }));
    await user.click(screen.getByRole("button", { name: "Overwrite and apply" }));

    expect(
      await screen.findByText(/target's content is unchanged/),
    ).toBeInTheDocument();
  });
});
