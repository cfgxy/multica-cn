import type { ReactNode } from "react";
import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { I18nProvider } from "@multica/core/i18n/react";
import type { PluginPreview } from "@multica/core/types";
import enCommon from "../../locales/en/common.json";
import enSettings from "../../locales/en/settings.json";

import { PluginConsent } from "./plugin-consent";

// The boundary matrix for "which required fields are still unanswered" lives in
// plugin-config-field.test.ts. What is asserted here is the wiring the upgrade
// case depends on: that the screen reads `configured_keys` rather than assuming
// an upgrade needs nothing, and that the button follows it.

const TEST_RESOURCES = { en: { common: enCommon, settings: enSettings } };

function Wrapper({ children }: { children: ReactNode }) {
  return <I18nProvider locale="en" resources={TEST_RESOURCES}>{children}</I18nProvider>;
}

function preview(overrides: Partial<PluginPreview> = {}): PluginPreview {
  return {
    manifest: {
      manifest_version: 1,
      key: "com.example.hello",
      name: "Hello Panel",
      version: "2.0.0",
      author: { name: "example" },
      scopes: ["issues:read"],
      contributes: {},
    },
    version: "2.0.0",
    version_id: "version-2",
    scopes: ["issues:read"],
    added_scopes: [],
    installed: false,
    // The generated inputs carry no accessible label, so a placeholder is what
    // a test can address them by.
    config_schema: [
      { key: "repo", type: "string", label: "Repo", required: true, placeholder: "owner/name" },
      { key: "channel", type: "string", label: "Channel", required: true, placeholder: "#channel" },
      { key: "token", type: "secret", label: "Token", required: true, placeholder: "sk-..." },
    ],
    configured_keys: [],
    ...overrides,
  } as PluginPreview;
}

function renderConsent(value: PluginPreview, onConfirm = vi.fn()) {
  render(
    <Wrapper>
      <PluginConsent
        preview={value}
        installing={false}
        canInstall
        onCancel={vi.fn()}
        onConfirm={onConfirm}
      />
    </Wrapper>,
  );
  return onConfirm;
}

function confirmButton(): HTMLButtonElement {
  return screen.getByRole("button", { name: /install|upgrade/i }) as HTMLButtonElement;
}

describe("PluginConsent required configuration", () => {
  it("blocks a fresh install until every required field is answered", async () => {
    renderConsent(preview());
    expect(confirmButton()).toBeDisabled();

    const user = userEvent.setup();
    await user.type(screen.getByPlaceholderText("owner/name"), "multica-ai/multica");
    await user.type(screen.getByPlaceholderText("#channel"), "#platform");
    expect(confirmButton()).toBeDisabled();
    await user.type(screen.getByPlaceholderText("sk-..."), "sk-typed");
    expect(confirmButton()).toBeEnabled();
  });

  // The rework's point: an upgrade is not automatically unblocked. A v2 that
  // adds `channel` must still ask for it, while `repo` and `token` the previous
  // version already holds are left alone.
  it("still asks for a field the new version introduces", async () => {
    renderConsent(
      preview({
        installed: true,
        installed_version: "1.0.0",
        configured_keys: ["repo", "token"],
      }),
    );
    expect(confirmButton()).toBeDisabled();
    // The reason has to be on screen, and it has to name the one field that is
    // actually missing — not the two the workspace already holds.
    const reason = screen.getByText(/Fill in the required fields/);
    expect(reason.textContent).toContain("Channel");
    expect(reason.textContent).not.toContain("Token");

    const user = userEvent.setup();
    await user.type(screen.getByPlaceholderText("#channel"), "#platform");
    expect(confirmButton()).toBeEnabled();
  });

  it("does not ask again for what the upgrade already holds", () => {
    renderConsent(
      preview({
        installed: true,
        installed_version: "1.0.0",
        configured_keys: ["repo", "channel", "token"],
      }),
    );
    expect(confirmButton()).toBeEnabled();
  });

  // A secret is write-only: the form has nothing to prefill, so the only way an
  // administrator can tell "already held" from "still empty" is the badge.
  it("marks a stored secret as already set", () => {
    renderConsent(preview({ installed: true, configured_keys: ["token"] }));
    expect(screen.getAllByText(enSettings.plugins.config.configured).length).toBeGreaterThan(0);
  });

  // A backend that omits the field must fail towards asking, not towards
  // letting an empty required credential through.
  it("treats a missing configured_keys as nothing being configured", () => {
    const withoutField = preview({ installed: true }) as PluginPreview & {
      configured_keys?: string[];
    };
    delete withoutField.configured_keys;
    renderConsent(withoutField);
    expect(confirmButton()).toBeDisabled();
  });

  it("submits only what was typed", async () => {
    const onConfirm = renderConsent(
      preview({ installed: true, configured_keys: ["repo", "token"] }),
    );
    const user = userEvent.setup();
    await user.type(screen.getByPlaceholderText("#channel"), "#platform");
    await user.click(confirmButton());
    expect(onConfirm).toHaveBeenCalledWith({ channel: "#platform" });
  });
});
