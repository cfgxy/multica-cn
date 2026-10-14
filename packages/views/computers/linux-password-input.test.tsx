import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { I18nProvider } from "@multica/core/i18n/react";
import en from "../locales/en/settings.json";
import { LinuxPasswordInput } from "./linux-password-input";

describe("Linux password visibility", () => {
  it("shows and hides the same password without submitting the form", async () => {
    const submit = vi.fn((event) => event.preventDefault());
    render(
      <I18nProvider locale="en" resources={{ en: { settings: en } }}>
        <form onSubmit={submit}>
          <LinuxPasswordInput defaultValue="test-password" />
        </form>
      </I18nProvider>,
    );
    const user = userEvent.setup();
    const input = screen.getByLabelText("Linux password");
    expect(input).toHaveAttribute("type", "password");
    await user.click(
      screen.getByRole("button", { name: "Show Linux password" }),
    );
    expect(input).toHaveAttribute("type", "text");
    expect(input).toHaveValue("test-password");
    await user.click(
      screen.getByRole("button", { name: "Hide Linux password" }),
    );
    expect(input).toHaveAttribute("type", "password");
    expect(input).toHaveValue("test-password");
    expect(submit).not.toHaveBeenCalled();
  });
});
