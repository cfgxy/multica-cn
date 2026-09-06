/**
 * Hand-rolled argument validation for MCP tool inputs.
 *
 * The tool input schema is plain JSON Schema (no zod: the low-level MCP SDK
 * surface lets us declare schemas directly, keeping the dependency tree
 * minimal). Client-side validation mirrors the backend handlers' rules so a
 * bad call fails fast with an actionable message instead of a 400 round-trip.
 */

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
