// @vitest-environment node
import { expect, it, vi } from "vitest";
import { setSchemaLogger } from "../api/schema";
import {
  ModelConfigWireSchema,
  ModelKeysWireSchema,
  ModelSourceWireSchema,
  ModelUsersWireSchema,
  ModelTemplatesWireSchema,
  ModelSavedWireSchema,
  ModelRefreshedWireSchema,
  parseModelPayload,
  modelKeysFromWire,
  modelConfigFromWire,
} from "./model-config-schema";

it("isolates unsupported templates instead of opening a partial editor or losing other rows", () => {
  const valid = { runtime_kind: "codex", revision: 2, files: [{ id: "auth", path: ".codex/auth.json", format: "json", content: "{}", bindings: {} }], supported_api_types: ["openai-responses"], default_model: "", status: "builtin" };
  const result = parseModelPayload({ items: [valid, { ...valid, runtime_kind: "future", supported_api_types: ["future-protocol"] }, { ...valid, runtime_kind: "other", files: [{ ...valid.files[0], format: "future" }] }] }, ModelTemplatesWireSchema);
  expect(result.items[0]).toEqual(valid);
  expect(result.items.slice(1).map((item) => ({ kind: item.runtime_kind, status: item.status, files: item.files }))).toEqual([
    { kind: "future", status: "unsupported", files: [] },
    { kind: "other", status: "unsupported", files: [] },
  ]);
});

it("rejects malformed configuration endpoints without logging keys, passwords or file bodies", () => {
  const warn = vi.fn();
  setSchemaLogger({ warn, info: vi.fn(), error: vi.fn(), debug: vi.fn() });
  const secret = "never-log-this-model-token";
  for (const schema of [
    ModelConfigWireSchema,
    ModelKeysWireSchema,
    ModelSourceWireSchema,
    ModelUsersWireSchema,
    ModelTemplatesWireSchema,
    ModelSavedWireSchema,
    ModelRefreshedWireSchema,
  ]) {
    expect(() =>
      parseModelPayload<unknown>(
        {
          snapshot: { files: { ".codex/auth.json": secret } },
          directory_token: secret,
        },
        schema,
      ),
    ).toThrow("Invalid model configuration response");
  }
  expect(warn).toHaveBeenCalled();
  expect(JSON.stringify(warn.mock.calls)).not.toContain(secret);
});
it("keeps precise IDs and defaults unknown grants to unavailable", () => {
  const keys = modelKeysFromWire(
    parseModelPayload(
      {
        items: [
          { id: "9007199254740993", api_key_name: "shared", status: "future" },
        ],
      },
      ModelKeysWireSchema,
    ),
  );
  expect(keys).toEqual({
    items: [
      {
        id: "9007199254740993",
        apiKeyName: "shared",
        status: "missing",
        allowedRuntimes: [],
        position: 0,
      },
    ],
    page: 1,
    hasMore: false,
  });
});
it("preserves missing versus empty files and never treats an unknown test outcome as success", () => {
  const result = modelConfigFromWire(
    parseModelPayload(
      {
        runtime_kind: "codex",
        template_revision: 0,
        snapshot: {
          files: { ".codex/config.toml": "", ".codex/auth.json": null },
          revision: "rev",
        },
        saved: false,
        valid: false,
        api_key_id: "",
        model_id: "",
        api_type: "",
        operation_id: "op",
        endpoints: [],
        models: [],
        test: {
          status: "future",
          error_code: "new-error",
          duration_ms: 1,
          file_revision: "rev",
          model: "",
        },
      },
      ModelConfigWireSchema,
    ),
  );
  expect(result.snapshot.files).toEqual({
    ".codex/config.toml": "",
    ".codex/auth.json": null,
  });
  expect(result.test?.status).toBe("failed");
  expect(() =>
    parseModelPayload({ saved: false }, ModelSavedWireSchema),
  ).toThrow();
});
