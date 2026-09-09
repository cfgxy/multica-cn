// @vitest-environment jsdom

import type { ReactNode } from "react";
import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { I18nProvider } from "@multica/core/i18n/react";
import type { MarketplaceListing } from "@multica/core/types";
import enCommon from "../../locales/en/common.json";
import enSettings from "../../locales/en/settings.json";
import enAgents from "../../locales/en/agents.json";
import { MarketplacePublishDialog } from "./marketplace-publish-dialog";

const TEST_RESOURCES = {
  en: { common: enCommon, settings: enSettings, agents: enAgents },
};

function Wrapper({ children }: { children: ReactNode }) {
  return (
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      {children}
    </I18nProvider>
  );
}

const listing = (over: Partial<MarketplaceListing> = {}): MarketplaceListing => ({
  id: "11111111-1111-4111-8111-111111111111",
  key: "mcp:acme/search",
  kind: "mcp",
  name: "search",
  publisher_display_name: "Acme",
  summary: "Search the web.",
  description: "",
  homepage_url: "",
  categories: ["web"],
  placeholders: [],
  state: "published",
  revision: 3,
  created_at: "2026-09-01T00:00:00Z",
  updated_at: "2026-09-01T00:00:00Z",
  ...over,
});

function renderDialog(
  props: Partial<Parameters<typeof MarketplacePublishDialog>[0]> = {},
) {
  const onSubmit = vi.fn();
  render(
    <MarketplacePublishDialog
      open
      kind="mcp"
      listing={null}
      submitting={false}
      findings={[]}
      scannerRevision=""
      errorMessage=""
      onOpenChange={vi.fn()}
      onSubmit={onSubmit}
      {...props}
    />,
    { wrapper: Wrapper },
  );
  return { onSubmit };
}

function submitButton(editing: boolean) {
  return screen.getByRole("button", { name: editing ? "Save" : "Publish" });
}

describe("MarketplacePublishDialog public metadata rules", () => {
  it("refuses to submit without a summary", async () => {
    const user = userEvent.setup();
    renderDialog({ kind: "skill" });

    await user.type(screen.getByLabelText("Name"), "pdf");
    await user.type(screen.getByLabelText("Source URL"), "https://example.com/s");
    await user.click(screen.getByRole("button", { name: "Documents" }));

    expect(submitButton(false)).toBeDisabled();
    expect(screen.getByText("A summary is required.")).toBeInTheDocument();
  });

  it("refuses to submit without a category", async () => {
    const user = userEvent.setup();
    renderDialog({ kind: "skill" });

    await user.type(screen.getByLabelText("Name"), "pdf");
    await user.type(screen.getByLabelText("Summary"), "Reads PDFs.");
    await user.type(screen.getByLabelText("Source URL"), "https://example.com/s");

    expect(submitButton(false)).toBeDisabled();
    expect(screen.getByText("Choose at least one category.")).toBeInTheDocument();
  });

  it("refuses a summary longer than the server accepts", async () => {
    const user = userEvent.setup();
    renderDialog({
      kind: "skill",
      listing: null,
    });

    await user.type(screen.getByLabelText("Name"), "pdf");
    await user.type(screen.getByLabelText("Source URL"), "https://example.com/s");
    await user.click(screen.getByRole("button", { name: "Documents" }));
    // paste rather than type: 201 keystrokes is slow and buys nothing here.
    await user.click(screen.getByLabelText("Summary"));
    await user.paste("x".repeat(201));

    expect(submitButton(false)).toBeDisabled();
    expect(
      screen.getByText("Keep the summary under 200 characters."),
    ).toBeInTheDocument();
  });

  it("submits only categories from the controlled set", async () => {
    const user = userEvent.setup();
    const { onSubmit } = renderDialog({ kind: "skill" });

    await user.type(screen.getByLabelText("Name"), "pdf");
    await user.type(screen.getByLabelText("Summary"), "Reads PDFs.");
    await user.type(screen.getByLabelText("Source URL"), "https://example.com/s");
    await user.click(screen.getByRole("button", { name: "Documents" }));
    await user.click(screen.getByRole("button", { name: "Files" }));
    await user.click(submitButton(false));

    expect(onSubmit).toHaveBeenCalledWith(
      expect.objectContaining({ categories: ["documents", "files"] }),
    );
  });

  it("drops a category the server no longer offers when reopening a listing", async () => {
    renderDialog({
      listing: listing({ categories: ["web", "legacy-category"] }),
    });

    expect(screen.getByRole("button", { name: "Web" })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    expect(screen.queryByText("legacy-category")).not.toBeInTheDocument();
  });
});

describe("MarketplacePublishDialog template fidelity", () => {
  const template = {
    type: "streamable-http",
    url: "https://acme.test/mcp",
    headers: { Authorization: "Bearer ${api_token}" },
    // A runtime field this form does not model. Editing metadata must not
    // silently strip it from what the catalog serves.
    timeout: 30,
  };

  const mcpListing = listing({
    config_template: template,
    transport: "http",
    placeholders: [
      {
        key: "api_token",
        label: "API token",
        description: "",
        secret: true,
        required: true,
      },
    ],
  });

  it("republishes the stored template verbatim when only metadata changed", async () => {
    const user = userEvent.setup();
    const { onSubmit } = renderDialog({ listing: mcpListing });

    await user.clear(screen.getByLabelText("Summary"));
    await user.type(screen.getByLabelText("Summary"), "Searches the web.");
    await user.click(submitButton(true));

    expect(onSubmit).toHaveBeenCalledTimes(1);
    expect(onSubmit.mock.calls[0]?.[0]?.config_template).toEqual(template);
  });

  it("overwrites only the field the publisher actually edited", async () => {
    const user = userEvent.setup();
    const { onSubmit } = renderDialog({ listing: mcpListing });

    await user.clear(screen.getByLabelText("URL"));
    await user.type(screen.getByLabelText("URL"), "https://acme.test/v2");
    await user.click(submitButton(true));

    expect(onSubmit.mock.calls[0]?.[0]?.config_template).toEqual({
      ...template,
      url: "https://acme.test/v2",
    });
  });

  it("drops the other transport's keys only when the transport is switched", async () => {
    const user = userEvent.setup();
    // No placeholders on this one: switching transport drops the headers that
    // referenced them, which the unused-placeholder guard would rightly block.
    const { onSubmit } = renderDialog({
      listing: listing({
        config_template: { ...template, headers: { "X-Client": "multica" } },
        transport: "http",
        placeholders: [],
      }),
    });

    await user.click(screen.getByRole("button", { name: "stdio" }));
    await user.type(screen.getByLabelText("Command"), "acme-mcp");
    await user.click(submitButton(true));

    expect(onSubmit.mock.calls[0]?.[0]?.config_template).toEqual({
      type: "stdio",
      command: "acme-mcp",
      timeout: 30,
    });
  });

  it("keeps an empty args list absent rather than inventing one", async () => {
    const user = userEvent.setup();
    const stdio = listing({
      config_template: { type: "local", command: "acme-mcp" },
      transport: "stdio",
      placeholders: [],
    });
    const { onSubmit } = renderDialog({ listing: stdio });

    await user.clear(screen.getByLabelText("Summary"));
    await user.type(screen.getByLabelText("Summary"), "Runs locally.");
    await user.click(submitButton(true));

    expect(onSubmit.mock.calls[0]?.[0]?.config_template).toEqual({
      type: "local",
      command: "acme-mcp",
    });
  });
});
