// @vitest-environment jsdom
import { act, cleanup, renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { ApiError } from "../api/client";
import { AdminComputerRuntimeSchema } from "./admin-schema";
import {
  installRuntimeAndWait,
  runtimeInstallItem,
  useComputerBindingRuntimeBatch,
} from "./runtime-batch";

const api = vi.hoisted(() => ({
  installComputerBindingRuntime: vi.fn(),
  listComputerOperations: vi.fn(),
}));
vi.mock("../api", async (original) => ({
  ...(await original<typeof import("../api")>()),
  api,
}));
const runtime = (id: string) =>
  AdminComputerRuntimeSchema.parse({
    id,
    display_name: id,
    installed_version: "",
    can_install: true,
    latest_version: "2.0",
    latest_version_state: "ready",
    supports_version: true,
  });
const item = { id: "codex", version: "2.0", skip: false };
const clients: QueryClient[] = [];
function setup() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  clients.push(client);
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  );
  return {
    client,
    wrapper,
    ...renderHook(() => useComputerBindingRuntimeBatch("user", "binding"), {
      wrapper,
    }),
  };
}
beforeEach(() => {
  vi.resetAllMocks();
  api.installComputerBindingRuntime.mockImplementation(
    async (_binding, id) => ({ operation_id: id, state: "queued" }),
  );
  api.listComputerOperations.mockResolvedValue([
    { id: "codex", state: "succeeded" },
    { id: "omp", state: "succeeded" },
  ]);
});
afterEach(() => {
  cleanup();
  clients.splice(0).forEach((client) => client.clear());
  vi.useRealTimers();
});

it("chooses known latest or latest-only and skips only confirmed current versions", () => {
  const current = {
    ...runtime("codex"),
    installed_version: "2.0",
    update_available: false,
    probe_state: "installed" as const,
  };
  expect(runtimeInstallItem(current)).toEqual({ ...item, skip: true });
  expect(runtimeInstallItem({ ...current, probe_error: "failed" }).skip).toBe(
    false,
  );
  expect(runtimeInstallItem({ ...current, probe_state: "missing" }).skip).toBe(
    false,
  );
  expect(
    runtimeInstallItem({ ...current, latest_version_state: "unavailable" }),
  ).toEqual({ ...item, version: "latest" });
  expect(
    runtimeInstallItem({ ...runtime("grok"), supports_version: false }).version,
  ).toBe("latest");
});

it("waits for actual completion, skips failed installations, and prevents duplicate batches", async () => {
  let finish!: (value: unknown[]) => void;
  api.listComputerOperations.mockImplementationOnce(
    () =>
      new Promise((resolve) => {
        finish = resolve;
      }),
  );
  const view = setup();
  act(() => {
    view.result.current.start([runtime("codex"), runtime("omp")]);
    view.result.current.start([runtime("codex")]);
  });
  await waitFor(() =>
    expect(api.listComputerOperations).toHaveBeenCalledTimes(1),
  );
  expect(api.installComputerBindingRuntime).toHaveBeenCalledTimes(1);
  expect(view.result.current.results.get("omp")).toBe("waiting");
  await act(async () =>
    finish([
      { id: "codex", state: "failed", error_summary: "Installer failed" },
    ]),
  );
  await waitFor(() => expect(view.result.current.isPending).toBe(false));
  expect(
    api.installComputerBindingRuntime.mock.calls.map((call) => call[1]),
  ).toEqual(["codex", "omp"]);
  expect([...view.result.current.results.values()]).toEqual([
    "failed",
    "succeeded",
  ]);
});

it("continues after a rejected runtime and does not submit confirmed current versions", async () => {
  api.installComputerBindingRuntime.mockRejectedValueOnce(
    new ApiError("Unsupported", 422, "Unprocessable"),
  );
  const view = setup();
  act(() =>
    view.result.current.start([
      runtime("codex"),
      runtime("omp"),
      { ...runtime("pi"), installed_version: "2.0", update_available: false },
    ]),
  );
  await waitFor(() =>
    expect(view.result.current.results.get("pi")).toBe("skipped"),
  );
  expect(api.installComputerBindingRuntime).toHaveBeenCalledTimes(2);
  expect([...view.result.current.results.values()]).toEqual([
    "failed",
    "succeeded",
    "skipped",
  ]);
});

it("continues while the runtime view is unmounted and restores results on remount", async () => {
  let finish!: (value: unknown[]) => void;
  api.listComputerOperations.mockImplementationOnce(
    () =>
      new Promise((resolve) => {
        finish = resolve;
      }),
  );
  const view = setup();
  act(() => view.result.current.start([runtime("codex"), runtime("omp")]));
  await waitFor(() =>
    expect(api.listComputerOperations).toHaveBeenCalledTimes(1),
  );
  view.unmount();
  await act(async () => finish([{ id: "codex", state: "failed" }]));
  const remounted = renderHook(
    () => useComputerBindingRuntimeBatch("user", "binding"),
    { wrapper: view.wrapper },
  );
  await waitFor(() =>
    expect(remounted.result.current.results.get("omp")).toBe("succeeded"),
  );
  expect(remounted.result.current.results.get("codex")).toBe("failed");
});

it("stops before submitting the next item if submission outcome is unknown", async () => {
  api.installComputerBindingRuntime.mockRejectedValueOnce(
    new TypeError("Failed to fetch"),
  );
  const view = setup();
  act(() => view.result.current.start([runtime("codex"), runtime("omp")]));
  await waitFor(() => expect(view.result.current.stopped).toBe(true));
  expect(api.installComputerBindingRuntime).toHaveBeenCalledTimes(1);
  expect([...view.result.current.results.values()]).toEqual([
    "stopped",
    "stopped",
  ]);
});

it("polls queued and running operations without submitting another installation", async () => {
  vi.useFakeTimers();
  api.listComputerOperations
    .mockResolvedValueOnce([{ id: "codex", state: "queued" }])
    .mockResolvedValueOnce([{ id: "codex", state: "running" }]);
  const promise = installRuntimeAndWait("binding", item);
  await vi.advanceTimersByTimeAsync(4000);
  await expect(promise).resolves.toEqual({ status: "succeeded" });
  expect(api.installComputerBindingRuntime).toHaveBeenCalledTimes(1);
  expect(api.listComputerOperations).toHaveBeenCalledTimes(3);
});

it("tolerates temporary polling failures", async () => {
  vi.useFakeTimers();
  api.listComputerOperations.mockRejectedValueOnce(new Error("Network"));
  const promise = installRuntimeAndWait("binding", item);
  await vi.advanceTimersByTimeAsync(2000);
  await expect(promise).resolves.toEqual({ status: "succeeded" });
});

it("stops for interrupted operations instead of treating them as ordinary installer failures", async () => {
  api.listComputerOperations.mockResolvedValue([
    { id: "codex", state: "interrupted" },
  ]);
  await expect(installRuntimeAndWait("binding", item)).rejects.toThrow(
    "interrupted",
  );
});
