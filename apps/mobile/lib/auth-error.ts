import i18n from "i18next";

/**
 * Map backend auth errors to user-facing strings. The backend returns raw
 * English messages that are fine for logs but should not surface as-is —
 * we map the known shapes to friendlier copy and fall back to the caller's
 * default for anything unrecognised.
 *
 * 映射结果走 i18n（`auth:mobile.errors.*`），后端返回的英文原文只用于匹配
 * 形态，不直接渲染 —— 否则登录失败提示会在中文界面里冒出英文。
 */
export function mapAuthError(err: unknown, fallback: string): string {
  if (!(err instanceof Error)) return fallback;
  const msg = err.message.toLowerCase();
  if (/invalid|incorrect|wrong/.test(msg)) {
    return i18n.t(
      "auth:mobile.errors.code_mismatch",
      "That code didn't match. Double-check and try again.",
    );
  }
  if (/expired/.test(msg)) {
    return i18n.t(
      "auth:mobile.errors.code_expired",
      "That code has expired. Tap resend to get a new one.",
    );
  }
  if (/rate.?limit|too many|throttle/.test(msg)) {
    return i18n.t(
      "auth:mobile.errors.rate_limited",
      "Too many attempts. Wait a moment and try again.",
    );
  }
  if (/network|fetch|timeout|unreachable/.test(msg)) {
    return i18n.t(
      "auth:mobile.errors.unreachable",
      "Can't reach Multica. Check your connection and retry.",
    );
  }
  return fallback;
}
