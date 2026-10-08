/**
 * Hand-rolled argument validation for MCP tool inputs.
 *
 * The tool input schema is plain JSON Schema (no zod: the low-level MCP SDK
 * surface lets us declare schemas directly, keeping the dependency tree
 * minimal). Client-side validation mirrors the backend handlers' rules so a
 * bad call fails fast with an actionable message instead of a 400 round-trip.
 */

import type { ProjectResourceType } from "./types.js";

export class ToolInputError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "ToolInputError";
  }
}

export function requireString(
  args: Record<string, unknown>,
  key: string,
  options: { maxLength?: number } = {},
): string {
  const value = args[key];
  if (typeof value !== "string" || value.trim().length === 0) {
    throw new ToolInputError(`'${key}' is required and must be a non-empty string`);
  }
  const trimmed = value.trim();
  if (options.maxLength !== undefined && trimmed.length > options.maxLength) {
    throw new ToolInputError(
      `'${key}' exceeds the maximum length of ${options.maxLength} characters`,
    );
  }
  return trimmed;
}

export function optionalString(
  args: Record<string, unknown>,
  key: string,
  options: { maxLength?: number } = {},
): string | undefined {
  const value = args[key];
  if (value === undefined || value === null || value === "") {
    return undefined;
  }
  if (typeof value !== "string") {
    throw new ToolInputError(`'${key}' must be a string`);
  }
  const trimmed = value.trim();
  if (trimmed.length === 0) {
    return undefined;
  }
  if (options.maxLength !== undefined && trimmed.length > options.maxLength) {
    throw new ToolInputError(
      `'${key}' exceeds the maximum length of ${options.maxLength} characters`,
    );
  }
  return trimmed;
}

export function optionalStringArray(
  args: Record<string, unknown>,
  key: string,
): string[] | undefined {
  const value = args[key];
  if (value === undefined || value === null) {
    return undefined;
  }
  if (!Array.isArray(value) || value.some((item) => typeof item !== "string")) {
    throw new ToolInputError(`'${key}' must be an array of strings`);
  }
  const items = (value as string[]).map((item) => item.trim()).filter((item) => item.length > 0);
  if (items.length === 0) {
    return undefined;
  }
  return items;
}

export function optionalInt(
  args: Record<string, unknown>,
  key: string,
  options: { min?: number; max?: number } = {},
): number | undefined {
  const value = args[key];
  if (value === undefined || value === null || value === "") {
    return undefined;
  }
  if (typeof value !== "number" || !Number.isInteger(value)) {
    throw new ToolInputError(`'${key}' must be an integer`);
  }
  if (options.min !== undefined && value < options.min) {
    throw new ToolInputError(`'${key}' must be >= ${options.min}`);
  }
  if (options.max !== undefined && value > options.max) {
    throw new ToolInputError(`'${key}' must be <= ${options.max}`);
  }
  return value;
}

export function optionalBoolean(
  args: Record<string, unknown>,
  key: string,
): boolean | undefined {
  const value = args[key];
  if (value === undefined || value === null) {
    return undefined;
  }
  if (typeof value !== "boolean") {
    throw new ToolInputError(`'${key}' must be a boolean`);
  }
  return value;
}

export function optionalEnum<T extends string>(
  args: Record<string, unknown>,
  key: string,
  allowed: readonly T[],
): T | undefined {
  const value = optionalString(args, key);
  if (value === undefined) {
    return undefined;
  }
  const hit = allowed.find((candidate) => candidate === value);
  if (hit === undefined) {
    throw new ToolInputError(
      `'${key}' must be one of: ${allowed.join(", ")} (got '${value}')`,
    );
  }
  return hit;
}

/**
 * PATCH argument that may be explicitly cleared.
 * Absent → undefined (keep: the key stays out of the request body); null or
 * "" → null (clear: the null must survive JSON serialization — the server
 * decides "clear" by rawFields key presence, server/internal/handler/
 * issue.go UpdateIssue); otherwise the trimmed string.
 */
export function optionalClearableString(
  args: Record<string, unknown>,
  key: string,
  options: { pattern?: RegExp; patternMessage?: string } = {},
): string | null | undefined {
  if (!(key in args) || args[key] === undefined) {
    return undefined;
  }
  const value = args[key];
  if (value === null) {
    return null;
  }
  if (typeof value !== "string") {
    throw new ToolInputError(`'${key}' must be a string or null`);
  }
  const trimmed = value.trim();
  if (trimmed.length === 0) {
    return null;
  }
  if (options.pattern !== undefined && !options.pattern.test(trimmed)) {
    throw new ToolInputError(
      options.patternMessage ?? `'${key}' has an invalid format (got '${trimmed}')`,
    );
  }
  return trimmed;
}

// ---- project resource refs ---------------------------------------
//
// resource_ref is a type-discriminated object (packages/core/types/project.ts):
// github_repo needs url, local_directory needs an absolute local_path and a
// daemon_id. The handlers' validateAndNormalizeResourceRef stays authoritative
// (URL grammar, per-daemon conflicts); these checks catch the obvious shape
// mistakes before a round trip.

export function optionalProjectResourceRef(
  args: Record<string, unknown>,
  key: string,
): Record<string, unknown> | undefined {
  const value = args[key];
  if (value === undefined || value === null) {
    return undefined;
  }
  if (typeof value !== "object" || Array.isArray(value)) {
    throw new ToolInputError(`'${key}' must be an object`);
  }
  const record = value as Record<string, unknown>;
  if (Object.keys(record).length === 0) {
    throw new ToolInputError(`'${key}' must not be empty`);
  }
  return record;
}

export function requireProjectResourceRef(
  args: Record<string, unknown>,
  key: string,
  resourceType: ProjectResourceType,
): Record<string, unknown> {
  const ref = optionalProjectResourceRef(args, key);
  if (ref === undefined) {
    throw new ToolInputError(`'${key}' is required and must be an object`);
  }
  const refString = (field: string): string => {
    const value = ref[field];
    if (typeof value !== "string" || value.trim().length === 0) {
      throw new ToolInputError(`'${key}.${field}' is required and must be a non-empty string`);
    }
    return value;
  };
  if (resourceType === "github_repo") {
    refString("url");
  } else {
    refString("local_path");
    refString("daemon_id");
    const mode = ref["execution_mode"];
    if (mode !== undefined && mode !== "in_place" && mode !== "worktree") {
      throw new ToolInputError(
        `'${key}.execution_mode' must be 'in_place' or 'worktree' (got '${String(mode)}')`,
      );
    }
  }
  return ref;
}
