// @vitest-environment node
import { describe, expect, it, vi } from "vitest";
import { reloadForServerSwitch } from "./server-window-switch";

function makeWindow() {
  return { isDestroyed: () => false, close: vi.fn(), webContents: { reload: vi.fn() } };
}

describe("reloadForServerSwitch", () => {
  it("closes all old issue windows before reloading the main window", () => {
    const main = makeWindow();
    const issue = makeWindow();
    const second = makeWindow();
    const calls: string[] = [];
    issue.close.mockImplementation(() => calls.push("close"));
    second.close.mockImplementation(() => calls.push("close"));
    main.webContents.reload.mockImplementation(() => calls.push("reload"));
    expect(reloadForServerSwitch(issue, main, new Set([issue, second]))).toBe(true);
    expect(calls).toEqual(["close", "close", "reload"]);
  });

  it("ignores a sender outside the known main and issue windows", () => {
    const main = makeWindow();
    const issue = makeWindow();
    expect(reloadForServerSwitch(makeWindow(), main, new Set([issue]))).toBe(false);
    expect(main.webContents.reload).not.toHaveBeenCalled();
    expect(issue.close).not.toHaveBeenCalled();
  });
});
