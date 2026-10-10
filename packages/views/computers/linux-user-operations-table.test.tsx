import { toast } from "sonner";

vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: vi.fn() } }));

import { expect, it, vi } from "vitest";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { I18nProvider } from "@multica/core/i18n/react";
import type { RemoteOperation } from "@multica/core/computers";
import en from "../locales/en/settings.json";
import { LinuxUserOperationsTable } from "./linux-user-operations-table";

const failed: RemoteOperation = {
  id: "failed-install",
  binding_id: "binding",
  kind: "runtime_install",
  runtime_id: "codex",
  state: "failed",
  step: "installing_runtime",
  requested_version: "0.158.0",
  actual_version: "codex-cli 0.142.5",
  created_at: "2026-09-28T06:34:28Z",
  started_at: "2026-09-28T06:34:29Z",
  finished_at: "2026-09-28T06:34:42Z",
  error_code: "runtime_download_failed",
  error_summary: "unsafe raw error",
};
function mount(
  operations: RemoteOperation[],
  onRecover?: (op: RemoteOperation) => void,
  recovering = false,
) {
  return render(
    <I18nProvider locale="en" resources={{ en: { settings: en } }}>
      <LinuxUserOperationsTable
        operations={operations}
        operationId={failed.id}
        recovering={recovering}
        onRecover={onRecover}
      />
    </I18nProvider>,
  );
}

it("compares failed install versions and expands safe diagnostics without losing the selected operation", async () => {
  mount([failed]);
  const table = screen.getByRole("table", { name: en.linux_user.operations });
  expect(
    within(table).getByRole("columnheader", { name: "Requested → actual" }),
  ).toBeInTheDocument();
  expect(
    within(table).getByText("0.158.0 → codex-cli 0.142.5"),
  ).toBeInTheDocument();
  expect(within(table).getByText("13 s")).toBeInTheDocument();
  expect(document.getElementById(`operation-${failed.id}`)).toHaveAttribute(
    "aria-current",
    "true",
  );
  expect(
    screen.getByText(en.linux_user.errors.runtime_download_failed),
  ).toBeInTheDocument();
  expect(screen.queryByText("unsafe raw error")).not.toBeInTheDocument();
  const details = screen.getByRole("button", { name: /Details for.*codex/ });
  await userEvent.click(details);
  expect(details).toHaveAttribute("aria-expanded", "true");
  const expanded = document.getElementById(
    details.getAttribute("aria-controls")!,
  )!;
  expect(
    expanded.querySelector('time[datetime="2026-09-28T06:34:29Z"]'),
  ).toBeInTheDocument();
  expect(
    within(expanded).getByText(en.linux_user.errors.runtime_download_failed),
  ).toBeInTheDocument();
  await userEvent.click(details);
  expect(details).toHaveAttribute("aria-expanded", "false");
});

it("preserves cancellation and interruption acknowledgement and disables them during recovery", async () => {
  const recover = vi.fn();
  const queued: RemoteOperation = {
    ...failed,
    id: "queued",
    state: "queued",
    error_code: "",
    started_at: null,
    finished_at: null,
  };
  const interrupted: RemoteOperation = {
    ...failed,
    id: "interrupted",
    state: "interrupted",
    finished_at: null,
  };
  const result = mount([queued, interrupted, failed], recover);
  await userEvent.click(
    screen.getByRole("button", { name: en.linux_user.cancel }),
  );
  expect(recover).toHaveBeenLastCalledWith(queued);
  await userEvent.click(
    screen.getByRole("button", { name: en.linux_user.acknowledge }),
  );
  expect(recover).toHaveBeenLastCalledWith(interrupted);
  result.unmount();
  mount([queued, interrupted], recover, true);
  expect(
    screen.getByRole("button", { name: en.linux_user.cancel }),
  ).toBeDisabled();
  expect(
    screen.getByRole("button", { name: en.linux_user.acknowledge }),
  ).toBeDisabled();
});

it("shows a safe empty history and keeps administrative history read only", () => {
  const result = mount([]);
  expect(screen.getByText(en.linux_user.no_operations)).toBeInTheDocument();
  expect(screen.queryByRole("table")).not.toBeInTheDocument();
  result.unmount();
  mount([{ ...failed, state: "queued" }]);
  expect(
    screen.queryByRole("button", { name: en.linux_user.cancel }),
  ).not.toBeInTheDocument();
});

it("copies complete safe diagnostics over HTTP and reports success", async () => {
  const user = userEvent.setup();
  vi.stubGlobal(
    "navigator",
    Object.defineProperty(Object.create(navigator), "clipboard", {
      value: undefined,
    }),
  );
  let copied = "";
  const execCopy = vi.fn(() => {
    copied = (document.activeElement as HTMLTextAreaElement).value;
    return true;
  });
  Object.defineProperty(document, "execCommand", {
    configurable: true,
    value: execCopy,
  });
  try {
    mount([failed]);
    const button = screen.getByRole("button", {
      name: en.linux_user_pages.tables.copy_error,
    });
    button.focus();
    await user.click(button);
    expect(execCopy).toHaveBeenCalledWith("copy");
    expect(JSON.parse(copied)).toMatchObject({
      operation_id: failed.id,
      binding_id: failed.binding_id,
      runtime: "codex",
      step: "installing_runtime",
      requested_version: failed.requested_version,
      actual_version: failed.actual_version,
      error_code: failed.error_code,
      error: en.linux_user.errors.runtime_download_failed,
      started_at: failed.started_at,
      finished_at: failed.finished_at,
    });
    expect(copied).not.toContain("unsafe raw error");
    expect(button).toHaveFocus();
    expect(toast.success).toHaveBeenCalledWith(
      en.linux_user_pages.tables.error_copied,
    );
  } finally {
    vi.unstubAllGlobals();
    Reflect.deleteProperty(document, "execCommand");
  }
});

it("reports clipboard failure and offers copy only for failed operations", async () => {
  const user = userEvent.setup();
  vi.stubGlobal(
    "navigator",
    Object.defineProperty(Object.create(navigator), "clipboard", {
      value: undefined,
    }),
  );
  Object.defineProperty(document, "execCommand", {
    configurable: true,
    value: () => false,
  });
  try {
    mount([failed, { ...failed, id: "success", state: "succeeded" }]);
    const buttons = screen.getAllByRole("button", {
      name: en.linux_user_pages.tables.copy_error,
    });
    expect(buttons).toHaveLength(1);
    await user.click(buttons[0]!);
    expect(toast.error).toHaveBeenCalledWith(
      en.linux_user_pages.tables.error_copy_failed,
    );
  } finally {
    vi.unstubAllGlobals();
    Reflect.deleteProperty(document, "execCommand");
  }
});
