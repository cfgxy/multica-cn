export function repositoryIdentity(rawURL: string): string | null {
  const value = rawURL.trim();
  if (!value || /[\s?#\\]/.test(rawURL) || /%(?:2f|5c|3f|23)/i.test(value)) {
    return null;
  }
  let decoded: string;
  try {
    decoded = decodeURIComponent(value);
    for (const char of decoded) {
      const code = char.codePointAt(0) ?? 0;
      if (code < 32 || code === 127) return null;
    }
  } catch {
    return null;
  }
  if (/(?:^|[/:])\.{1,2}(?:\/|$)/.test(decoded)) return null;

  let host = "";
  let path = "";
  if (!value.includes("://")) {
    const scpLike = value.match(/^[A-Za-z0-9._-]+@([A-Za-z0-9.-]+):([^\s?#]+)$/);
    if (scpLike) {
      host = scpLike[1] ?? "";
      path = scpLike[2] ?? "";
    }
  }
  if (!host) {
    try {
      const parsed = new URL(value);
      if (
        !["https:", "http:", "ssh:"].includes(parsed.protocol) ||
        parsed.search ||
        parsed.hash ||
        (parsed.username && !(parsed.protocol === "ssh:" && parsed.username === "git")) ||
        parsed.password
      ) return null;
      host = parsed.hostname;
      path = parsed.pathname;
    } catch {
      return null;
    }
  }

  const trimmedPath = path.replace(/^\/|\/$/g, "");
  if (trimmedPath.split("/").some((segment) => !segment)) return null;
  const normalizedPath = trimmedPath.replace(/\.git$/i, "");
  if (!host || !normalizedPath || normalizedPath.endsWith("/")) return null;
  return `${host.toLowerCase()}/${normalizedPath}`;
}
