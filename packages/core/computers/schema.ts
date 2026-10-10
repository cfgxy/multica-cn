import { z } from "zod";
import { parseWithFallback } from "../api/schema";

export const ComputerSettingsSchema = z.object({
  git_name: z.string(),
  git_email: z.string(),
  gitlab_url: z.string(),
  gitlab_token: z.string(),
  git_ssh_key: z.string().default(""),
  git_known_hosts: z.string().default(""),
  model_env: z.string(),
  multica_pat: z.string(),
});
export type ComputerSettings = z.infer<typeof ComputerSettingsSchema>;
const CredentialWriteResultSchema = z.object({ saved: z.literal(true) });

export function parseCredentialWriteResult(data: unknown) {
  const result = CredentialWriteResultSchema.safeParse(data);
  if (!result.success) {
    parseWithFallback(null, CredentialWriteResultSchema, null, { endpoint: "computer-credentials/write (redacted)" });
    throw new Error("Could not confirm credentials were written");
  }
  return parseWithFallback(result.data, CredentialWriteResultSchema, result.data, { endpoint: "computer-credentials/write" });
}
export const PersonalComputerSettingsSchema = z.object({
  operator: z.boolean(),
  settings: ComputerSettingsSchema,
});
export const ComputerSchema = z.object({
  id: z.string(),
  name: z.string(),
  host: z.string(),
  port: z.number(),
  ssh_user: z.string(),
  enabled: z.boolean().default(true),
});
export type Computer = z.infer<typeof ComputerSchema>;
const BindingOperationSummarySchema = z.object({
  id: z.string(), kind: z.string(),
  state: z.enum(["queued", "running", "succeeded", "failed", "cancelled", "interrupted"]).or(z.literal("unknown")).catch("unknown"),
  error_code: z.string().catch(""),
  finished_at: z.string().nullable().catch(null),
});
export const ComputerBindingSchema = z.object({
  id: z.string(),
  computer_id: z.string(),
  workspace_id: z.string(),
  workspace_name: z.string().catch(""),
  workspace_access: z.enum(["accessible", "unavailable", "none", "unknown"]).catch("unknown"),
  latest_operation: BindingOperationSummarySchema.nullable().catch(null),
  username: z.string(),
  state: z.string(),
  last_error: z.string(),
  verified: z.boolean().default(false),
  account_state: z.enum(["unknown", "present", "missing", "unavailable"]).catch("unknown"),
  operation_busy: z.boolean().default(false),
});
export type ComputerBinding = z.infer<typeof ComputerBindingSchema>;
export type ComputerOperation = {
  computer_id: string;
  workspace_id: string;
  username: string;
  password: string;
  action: "create_account" | "provision" | "sync" | "upgrade" | "remove";
};

// Never send credential-bearing malformed responses to the schema logger.
// A malformed secret response must not become an empty form that overwrites it.
export function parseComputerSettings(data: unknown) {
  const result = PersonalComputerSettingsSchema.safeParse(data);
  if (!result.success) {
    parseWithFallback(null, PersonalComputerSettingsSchema, null, {
      endpoint: "/api/me/computer-settings (redacted)",
    });
    throw new Error("Could not read personal Computer settings");
  }
  return parseWithFallback(
    result.data,
    PersonalComputerSettingsSchema,
    result.data,
    { endpoint: "/api/me/computer-settings" },
  );
}

export const RemoteOperationSchema = z.object({
  id: z.string(), binding_id: z.string(), kind: z.string(),
  runtime_id: z.string().default(""), requested_version: z.string().default(""), actual_version: z.string().default(""),
  state: z.enum(["queued", "running", "succeeded", "failed", "cancelled", "interrupted"]).catch("interrupted"),
  step: z.string().default(""), error_code: z.string().default(""), error_summary: z.string().default(""),
  created_at: z.string(), started_at: z.string().nullable().default(null), finished_at: z.string().nullable().default(null),
});
export type RemoteOperation = z.infer<typeof RemoteOperationSchema>;
export const ComputerBindingDetailSchema = ComputerBindingSchema.extend({
  computer_name: z.string(), workspace_name: z.string().catch(""), daemon_id: z.string(),
  account_state: z.enum(["unknown", "present", "missing", "unavailable"]).catch("unknown"),
  daemon_state: z.enum(["running", "stopped", "failed", "unknown"]).catch("unknown"),
  daemon_checked_at: z.string().nullable().default(null),
  checked_at: z.string().nullable().default(null), archived_at: z.string().nullable().default(null), last_seen_at: z.string().nullable().default(null),
});
export const AcceptedComputerOperationSchema = z.object({ operation_id: z.string(), state: z.string() });
export type ComputerLifecycleInput = { action: "remove" | "delete_user" | "archive"; password?: string; confirm_username: string };
export type ComputerBindingDetail = z.infer<typeof ComputerBindingDetailSchema>;
export type AcceptedComputerOperation = z.infer<typeof AcceptedComputerOperationSchema>;
export const ComputerLifecycleResultSchema = z.union([AcceptedComputerOperationSchema, z.object({ saved: z.literal(true) })]);
export type ComputerLifecycleResult = z.infer<typeof ComputerLifecycleResultSchema>;
