import { z } from "zod";
import { parseWithFallback } from "../api/schema";

export const modelRuntimeKinds = [
  "claude",
  "codex",
  "opencode",
  "openclaw",
  "grok",
  "kimi",
  "omp",
  "pi",
] as const;
export const modelAPITypeSchema = z.enum([
  "anthropic-messages",
  "openai-responses",
  "openai-chat-completions",
]);
const modelKeySchema = z.object({
  id: z.string().min(1),
  api_key_name: z.string(),
  status: z
    .enum(["enabled", "disabled", "expired", "missing"])
    .catch("missing"),
  allowed_runtimes: z.array(z.string()).catch([]),
  position: z.number().catch(0),
});
export const ModelKeysWireSchema = z.object({
  items: z.array(modelKeySchema),
  page: z.number().catch(1),
  has_more: z.boolean().catch(false),
});
export const ModelSourceWireSchema = z.object({
  configured: z.boolean(),
  base_url: z.string(),
  revision: z.number().int().nonnegative(),
  status: z
    .enum(["unconfigured", "unverified", "ready", "error"])
    .catch("error"),
  last_checked_at: z.string().nullable().catch(null),
  last_error_code: z.string().catch(""),
  directory_count: z.number().catch(0),
});
export const ModelUsersWireSchema = z.object({
  items: z.array(
    z.object({ id: z.string(), name: z.string(), email: z.string() }),
  ),
});
const templateFileSchema = z.object({
  id: z.string(),
  path: z.string(),
  format: z.enum(["json", "jsonc", "toml", "yaml"]),
  content: z.string(),
  bindings: z.record(z.string(), z.string()),
});
export const ModelTemplateWireSchema = z.object({
  runtime_kind: z.string(),
  revision: z.number(),
  files: z.array(templateFileSchema),
  supported_api_types: z.array(modelAPITypeSchema),
  default_model: z.string(),
  status: z.string(),
});
export const ModelTemplatesWireSchema = z.object({
  // A future template format must not block the other rows. Keep its identity,
  // but never expose a partial template as editable on an older client.
  items: z.array(
    ModelTemplateWireSchema.or(
      z.object({ runtime_kind: z.string().min(1) }).transform((item) => ({
        ...item,
        revision: 0,
        files: [],
        supported_api_types: [],
        default_model: "",
        status: "unsupported",
      })),
    ),
  ),
});
export const ModelSavedWireSchema = z.object({ saved: z.literal(true) });
export const ModelRefreshedWireSchema = z.object({ saved: z.literal(true) });
export const ModelConfigWireSchema = z.object({
  config_status: z.string().catch(""),
  operation_state: z
    .enum([
      "queued",
      "running",
      "succeeded",
      "failed",
      "cancelled",
      "interrupted",
    ])
    .optional()
    .catch(undefined),
  runtime_kind: z.string(),
  template_revision: z.number(),
  snapshot: z.object({
    files: z.record(z.string(), z.string().nullable()).nullable(),
    revision: z.string(),
  }),
  saved: z.boolean(),
  valid: z.boolean(),
  api_key_id: z.string(),
  model_id: z.string(),
  api_type: z.string(),
  operation_id: z.string(),
  endpoints: z.array(
    z.object({
      api_type: modelAPITypeSchema,
      base_url: z.string(),
      default_model: z.string(),
    }),
  ),
  models: z.array(
    z.object({
      id: z.string(),
      name: z.string(),
      api_types: z.array(modelAPITypeSchema),
    }),
  ),
  test: z
    .object({
      status: z.enum(["succeeded", "failed"]).catch("failed"),
      error_code: z.string(),
      duration_ms: z.number(),
      file_revision: z.string(),
      model: z.string(),
    })
    .optional(),
});

// Never log raw config files, template bodies or malformed credential responses.
// Reject malformed snapshots instead of presenting an empty, writable editor.
export function parseModelPayload<T>(data: unknown, schema: z.ZodType<T>): T {
  const parsed = schema.safeParse(data);
  if (!parsed.success) {
    parseWithFallback(null, schema, null, {
      endpoint: "model-configuration (redacted)",
    });
    throw new Error("Invalid model configuration response");
  }
  return parseWithFallback(data, schema, parsed.data, {
    endpoint: "model-configuration",
  });
}
export function modelKeysFromWire(data: z.infer<typeof ModelKeysWireSchema>) {
  return {
    items: data.items.map((k) => ({
      id: k.id,
      apiKeyName: k.api_key_name,
      status: k.status,
      allowedRuntimes: k.allowed_runtimes,
      position: k.position,
    })),
    page: data.page,
    hasMore: data.has_more,
  };
}
export function modelTemplateFromWire(
  t: z.infer<typeof ModelTemplateWireSchema>,
) {
  return {
    runtimeKind: t.runtime_kind,
    revision: t.revision,
    files: t.files,
    supportedAPITypes: t.supported_api_types,
    defaultModel: t.default_model,
    status: t.status,
  };
}
export type ModelTemplate = ReturnType<typeof modelTemplateFromWire>;
export type ModelKey = ReturnType<typeof modelKeysFromWire>["items"][number];
export function modelConfigFromWire(r: z.infer<typeof ModelConfigWireSchema>) {
  return {
    runtimeKind: r.runtime_kind,
    templateRevision: r.template_revision,
    snapshot: { files: r.snapshot.files ?? {}, revision: r.snapshot.revision },
    configStatus: r.config_status,
    operationState: r.operation_state,
    saved: r.saved,
    valid: r.valid,
    apiKeyId: r.api_key_id,
    modelId: r.model_id,
    apiType: r.api_type,
    operationId: r.operation_id,
    endpoints: r.endpoints.map((e) => ({
      apiType: e.api_type,
      baseURL: e.base_url,
      defaultModel: e.default_model,
    })),
    models: r.models.map((m) => ({
      id: m.id,
      name: m.name,
      apiTypes: m.api_types,
    })),
    test: r.test
      ? {
          status: r.test.status,
          errorCode: r.test.error_code,
          durationMs: r.test.duration_ms,
          fileRevision: r.test.file_revision,
          model: r.test.model,
        }
      : undefined,
  };
}
export type ModelConfigResult = ReturnType<typeof modelConfigFromWire>;
export type ModelConfigAction =
  | "read"
  | "connection"
  | "preview"
  | "check"
  | "save"
  | "test"
  | "result";
export type ModelConfigInput = {
  operationId?: string;
  password?: string;
  files?: Record<string, string>;
  apiKeyId?: string;
  modelId?: string;
  apiType?: string;
  expectedRevision?: string;
  createOnly?: boolean;
};
