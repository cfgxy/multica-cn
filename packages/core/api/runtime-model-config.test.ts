// @vitest-environment node
import { afterEach, expect, it, vi } from "vitest";
import { ApiClient } from "./client";
const base = {
  runtime_kind: "codex",
  template_revision: 0,
  snapshot: { files: null, revision: "" },
  saved: false,
  valid: false,
  api_key_id: "",
  model_id: "",
  api_type: "",
  operation_id: "operation",
  endpoints: [],
  models: [],
};
const response = (data: unknown, status = 200) =>
  new Response(JSON.stringify(data), {
    status,
    headers: { "Content-Type": "application/json" },
  });
afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});
it.each([
  [502, "ssh_unreachable"],
  [504, "ssh_timeout"],
] as const)("preserves the final %i operation error %s without replaying the action", async (status, code) => {
  vi.useFakeTimers();
  const fetch = vi.fn()
    .mockResolvedValueOnce(response({ ...base, operation_state: "queued" }, 202))
    .mockResolvedValueOnce(response({ code, error: "Classified failure", operation_id: "operation" }, status));
  vi.stubGlobal("fetch", fetch);
  const pending = new ApiClient("https://api.example.test").runtimeModelConfig("binding", "codex", "test", { password: "fixture-password" });
  const assertion = expect(pending).rejects.toMatchObject({ status, body: { code, operation_id: "operation" } });
  await vi.runAllTimersAsync();
  await assertion;
  expect(fetch).toHaveBeenCalledTimes(2);
  expect(JSON.parse(fetch.mock.calls[1]?.[1]?.body)).toEqual({ operation_id: "operation" });
});

it("keeps a lost result pending without replaying the operation", async () => {
  vi.useFakeTimers();
  const fetch = vi.fn()
    .mockResolvedValueOnce(response({ ...base, operation_state: "queued" }, 202))
    .mockRejectedValueOnce(new TypeError("Network unavailable"));
  vi.stubGlobal("fetch", fetch);
  const pending = new ApiClient("https://api.example.test").runtimeModelConfig("binding", "codex", "save", { password: "fixture-password" });
  const assertion = expect(pending).rejects.toMatchObject({ body: { code: "model_operation_pending", operation_id: "operation" } });
  await vi.runAllTimersAsync();
  await assertion;
  expect(fetch).toHaveBeenCalledTimes(2);
});

it("sends only file contents for a static check even if the caller supplies a password", async () => {
  const fetch = vi.fn().mockResolvedValue(response({ ...base, valid: true }));
  vi.stubGlobal("fetch", fetch);
  await new ApiClient("https://api.example.test").runtimeModelConfig("binding", "codex", "check", { password: "fixture-password", files: { ".codex/config.toml": "model='a'" } });
  expect(JSON.parse(fetch.mock.calls[0]?.[1]?.body)).toEqual({ files: { ".codex/config.toml": "model='a'" } });
});
it("starts a test once and polls without resending the password or files", async () => {
  vi.useFakeTimers();
  const fetch = vi
    .fn()
    .mockResolvedValueOnce(
      response({ ...base, operation_state: "queued" }, 202),
    )
    .mockResolvedValueOnce(response({ ...base, operation_state: "running" }))
    .mockResolvedValueOnce(
      response({
        ...base,
        operation_state: "succeeded",
        test: {
          status: "succeeded",
          error_code: "",
          duration_ms: 1,
          file_revision: "revision",
          model: "model-a",
        },
      }),
    );
  vi.stubGlobal("fetch", fetch);
  const pending = new ApiClient("https://api.example.test").runtimeModelConfig(
    "binding",
    "codex",
    "test",
    { password: "fixture-password" },
  );
  await vi.runAllTimersAsync();
  const result = await pending;
  expect(result.test?.model).toBe("model-a");
  expect(fetch).toHaveBeenCalledTimes(3);
  expect(fetch.mock.calls[0]?.[1]?.headers).toMatchObject({
    Prefer: "respond-async",
  });
  for (const [url, request] of fetch.mock.calls.slice(1)) {
    expect(String(url)).toMatch(/\/result$/);
    expect(JSON.parse(request.body)).toEqual({ operation_id: "operation" });
  }
});
it("retains the editor file bodies after a metadata-only asynchronous save", async () => {
  vi.useFakeTimers();
  const fetch = vi
    .fn()
    .mockResolvedValueOnce(
      response({ ...base, operation_state: "queued" }, 202),
    )
    .mockResolvedValueOnce(
      response({
        ...base,
        saved: true,
        operation_state: "succeeded",
        snapshot: {
          files: { ".codex/config.toml": null, ".codex/auth.json": null },
          revision: "new",
        },
      }),
    );
  vi.stubGlobal("fetch", fetch);
  const pending = new ApiClient("https://api.example.test").runtimeModelConfig(
    "binding",
    "codex",
    "save",
    {
      files: { ".codex/config.toml": "fixture-content" },
      password: "fixture-password",
    },
  );
  await vi.runAllTimersAsync();
  expect((await pending).snapshot).toEqual({
    files: {
      ".codex/config.toml": "fixture-content",
      ".codex/auth.json": null,
    },
    revision: "new",
  });
});
