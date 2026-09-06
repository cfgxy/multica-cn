/**
 * Token redaction for logs and error messages.
 *
 * Multica bearer tokens are `mul_` (personal access token), `mat_` (task
 * token) and `mcn_` (cloud node token) followed by 40 lowercase hex chars.
 * Nothing carrying that shape may reach the log stream: the stdio transport
 * is launched by AI clients whose logs are frequently pasted into issues,
 * so a leaked token would be indistinguishable from a shared one.
 */

const TOKEN_PATTERN = /\b(?:mul|mat|mcn)_[0-9a-f]{40}\b/gi;
const MASK = "mul_***redacted***";

export function redactTokens(input: string): string {
  return input.replace(TOKEN_PATTERN, MASK);
}

export function redactUnknown(value: unknown): string {
  const text =
    typeof value === "string"
      ? value
      : value instanceof Error
        ? value.message
        : String(value);
  return redactTokens(text);
}
