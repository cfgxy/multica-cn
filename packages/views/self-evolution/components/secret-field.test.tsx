// @vitest-environment jsdom

import { describe, it, expect, vi } from "vitest";
import { render, fireEvent } from "@testing-library/react";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enSelfEvolution from "../../locales/en/self-evolution.json";
import { SecretField } from "./secret-field";

/**
 * The write-only credential field (RUYI-551 §5.2 / walk #13): masked while
 * typed, and because the server returns only `has_api_key`, it never renders
 * a stored value — emptiness means "keep the existing key", and the
 * placeholder must say so. (The field renders bare here; the visible <Label>
 * lives in ModelConfigCard, so queries go by id.)
 */

const TEST_RESOURCES = { en: { common: enCommon, "self-evolution": enSelfEvolution } };

const input = () => document.getElementById("secret") as HTMLInputElement;

function mount(overrides: Partial<Parameters<typeof SecretField>[0]> = {}) {
  return render(
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      <SecretField
        id="secret"
        value=""
        onChange={() => {}}
        hasExisting={false}
        {...overrides}
      />
    </I18nProvider>,
  );
}

describe("SecretField", () => {
  it("is a password input that never echoes a stored value", () => {
    mount({ hasExisting: true });
    // A masked field is deliberately not a textbox role.
    expect(input().getAttribute("type")).toBe("password");
    expect(input().value).toBe("");
  });

  it("placeholders distinguish 'enter new' from 'keep existing'", () => {
    const { rerender } = mount({ hasExisting: false });
    expect(input().placeholder).toBe("Enter a new value");
    rerender(
      <I18nProvider locale="en" resources={TEST_RESOURCES}>
        <SecretField id="secret" value="" onChange={() => {}} hasExisting />
      </I18nProvider>,
    );
    expect(input().placeholder).toBe("Leave empty to keep the existing value");
  });

  it("forwards typed characters to onChange", () => {
    const onChange = vi.fn();
    mount({ onChange });
    // Password inputs are excluded from getByRole("textbox"), so change the
    // element directly; the field is controlled, so one change event carries
    // the whole value.
    fireEvent.change(input(), { target: { value: "sk-live-9f" } });
    expect(onChange).toHaveBeenCalledWith("sk-live-9f");
  });
});
