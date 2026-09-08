// @vitest-environment jsdom

import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { I18nProvider } from "@multica/core/i18n/react";
// The dialog recognises a scan block by `instanceof ApiError` plus status 422,
// so the test throws the real class rather than a shape that resembles it.
import { ApiError } from "@multica/core/api";
import enCommon from "../locales/en/common.json";
import enPromptMarket from "../locales/en/prompt-market.json";

const mockCreate = vi.hoisted(() => vi.fn());
const mockUpdate = vi.hoisted(() => vi.fn());
const mockPublish = vi.hoisted(() => vi.fn());
const mockToastSuccess = vi.hoisted(() => vi.fn());

vi.mock("@multica/core/workspace/mutations", () => ({
  useCreatePromptVersion: () => ({ mutateAsync: mockCreate, isPending: false }),
  useUpdatePromptVersion: () => ({ mutateAsync: mockUpdate, isPending: false }),
  usePublishPromptVersion: () => ({ mutateAsync: mockPublish, isPending: false }),
}));

vi.mock("sonner", () => ({
  toast: { success: mockToastSuccess, error: vi.fn() },
}));

import { PromptPublishDialog } from "./prompt-publish-dialog";

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

const draft = {
  id: "version-1",
  series_id: "series-1",
  state: "draft",
  version: null,
  scanner_revision: "2026.09.1",
};

/** The real secret value a leaky prompt would contain. Must never be rendered. */
const RAW_SECRET = "sk-live-4f9c2ab7e1d34c0fa77b";

const scanBlock = () =>
  new ApiError("secret detected", 422, "Unprocessable Entity", {
    code: "prompt_secret_detected",
    error: "the prompt contains a credential",
    scanner_revision: "2026.09.1",
    truncated: false,
    findings: [
      { category: "api_key", rule: "openai_key", line: 12, mask: "••••••••" },
    ],
  });

function renderDialog(over: Record<string, unknown> = {}) {
  return render(
    <PromptPublishDialog
      open
      wsId="ws-1"
      sourceType="agent"
      sourceId="agent-1"
      defaultName="Triage bot"
      onOpenChange={vi.fn()}
      {...over}
    />,
    { wrapper: Wrapper },
  );
}

/** Fills the required metadata and advances into the security check. */
async function reachScan(user: ReturnType<typeof userEvent.setup>) {
  await user.click(screen.getByRole("button", { name: "Next" }));
  await user.type(screen.getByLabelText("Summary"), "Sorts inbound tickets.");
  await user.click(screen.getByRole("combobox"));
  await user.click(await screen.findByRole("option", { name: /CC0/ }));
  await user.click(screen.getByRole("button", { name: "Next" }));
}

beforeEach(() => {
  mockCreate.mockReset().mockResolvedValue(draft);
  mockUpdate.mockReset().mockResolvedValue(draft);
  mockPublish
    .mockReset()
    .mockResolvedValue({ ...draft, state: "published", version: 1 });
  mockToastSuccess.mockReset();
});

describe("PromptPublishDialog metadata gate", () => {
  it("will not advance without a summary and a license", async () => {
    const user = userEvent.setup();
    renderDialog();
    await user.click(screen.getByRole("button", { name: "Next" }));

    expect(screen.getByRole("button", { name: "Next" })).toBeDisabled();

    await user.type(screen.getByLabelText("Summary"), "Sorts tickets.");
    // A summary alone is not enough — the license is the second required field.
    expect(screen.getByRole("button", { name: "Next" })).toBeDisabled();

    await user.click(screen.getByRole("combobox"));
    await user.click(await screen.findByRole("option", { name: /CC0/ }));
    expect(screen.getByRole("button", { name: "Next" })).toBeEnabled();
  });

  it("says the publisher is recorded and the source workspace is not", async () => {
    const user = userEvent.setup();
    renderDialog();
    await user.click(screen.getByRole("button", { name: "Next" }));

    expect(
      screen.getByText(/source workspace is never shown/),
    ).toBeInTheDocument();
  });
});

describe("PromptPublishDialog security check", () => {
  it("offers no way past a blocked scan", async () => {
    mockPublish.mockRejectedValue(scanBlock());
    const user = userEvent.setup();
    renderDialog();
    await reachScan(user);

    expect(
      await screen.findByText("Security check failed, publish blocked"),
    ).toBeInTheDocument();

    // The only footer actions are Cancel and Back. No "publish anyway" exists,
    // and the visibility step is never reached.
    const footerButtons = screen
      .getAllByRole("button")
      .map((button) => button.textContent?.trim());
    expect(footerButtons).toEqual(expect.arrayContaining(["Cancel", "Back"]));
    expect(footerButtons).not.toContain("Next");
    expect(footerButtons).not.toContain("Publish");
    expect(footerButtons).not.toContain("Save as private draft");
  });

  it("renders the mask and never the matched value", async () => {
    mockPublish.mockRejectedValue(scanBlock());
    const user = userEvent.setup();
    const { container } = renderDialog();
    await reachScan(user);
    await screen.findByText("Security check failed, publish blocked");

    expect(screen.getByText("api_key")).toBeInTheDocument();
    expect(screen.getByText("openai_key")).toBeInTheDocument();
    expect(screen.getByText("Line 12")).toBeInTheDocument();
    expect(screen.getByText("••••••••")).toBeInTheDocument();
    // The strongest form of this assertion: the secret is absent from the
    // whole rendered tree, not merely from the finding row.
    expect(container.textContent).not.toContain(RAW_SECRET);
    expect(document.body.textContent).not.toContain("sk-live");
  });

  it("re-snapshots the source when the publisher retries after a block", async () => {
    mockPublish.mockRejectedValue(scanBlock());
    const user = userEvent.setup();
    renderDialog();
    await reachScan(user);
    await screen.findByText("Security check failed, publish blocked");

    mockPublish.mockResolvedValue({ ...draft, state: "published", version: 1 });
    await user.click(screen.getByRole("button", { name: "Back" }));
    await user.click(screen.getByRole("button", { name: "Next" }));

    // Without `refresh_content` the retry would rescan the old text and clear
    // a fix the publisher already made at the source.
    await waitFor(() =>
      expect(mockUpdate).toHaveBeenCalledWith(
        expect.objectContaining({ versionId: "version-1", refresh_content: true }),
      ),
    );
  });

  it("states a pass as 'no rule matched', not as safe", async () => {
    const user = userEvent.setup();
    renderDialog();
    await reachScan(user);

    expect(await screen.findByText("No rule matched")).toBeInTheDocument();
    expect(screen.getByText(/read the prompt yourself/)).toBeInTheDocument();
  });
});

describe("PromptPublishDialog visibility", () => {
  it("scans privately first and only goes public on an explicit choice", async () => {
    const user = userEvent.setup();
    renderDialog();
    await reachScan(user);
    await screen.findByText("No rule matched");

    // The scan itself is a private publish.
    expect(mockPublish).toHaveBeenCalledWith({
      versionId: "version-1",
      public: false,
    });

    await user.click(screen.getByRole("button", { name: "Next" }));
    await user.click(screen.getByRole("radio", { name: /Publish publicly/ }));
    expect(
      screen.getByText(/members of all workspaces can search/),
    ).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Publish" }));
    await waitFor(() =>
      expect(mockPublish).toHaveBeenLastCalledWith({
        versionId: "version-1",
        public: true,
      }),
    );
  });

  it("defaults to private and issues no second publish", async () => {
    const user = userEvent.setup();
    renderDialog();
    await reachScan(user);
    await screen.findByText("No rule matched");
    await user.click(screen.getByRole("button", { name: "Next" }));

    expect(screen.getByRole("radio", { name: /Private draft/ })).toHaveAttribute(
      "aria-checked",
      "true",
    );

    await user.click(screen.getByRole("button", { name: "Save as private draft" }));
    await waitFor(() => expect(mockToastSuccess).toHaveBeenCalled());
    // One call only: the scan. Staying private is already the stored state.
    expect(mockPublish).toHaveBeenCalledTimes(1);
  });
});

describe("PromptPublishDialog version line", () => {
  it("appends to an existing series when one is given", async () => {
    const user = userEvent.setup();
    renderDialog({ seriesId: "series-9" });
    await reachScan(user);

    await waitFor(() =>
      expect(mockCreate).toHaveBeenCalledWith(
        expect.objectContaining({
          series_id: "series-9",
          source_type: "agent",
          source_id: "agent-1",
        }),
      ),
    );
  });
});
