// For GitHub URLs, return "owner/repo" so truncated display labels stay
// meaningful when URLs share a long prefix. Handles:
//   https://github.com/owner/repo[/...]
//   https://www.github.com/owner/repo
//   ssh://git@github.com/owner/repo.git
//   git@github.com:owner/repo.git   (scp shorthand)
// The .git suffix is stripped in all cases. When the resulting label is still
// long, middle-truncation keeps the distinguishing tail visible (the case where
// many repos share a long common prefix, e.g. customer-alpha vs customer-beta).
// Non-GitHub input (enterprise hosts, malformed strings) is returned unchanged.
export function githubShortLabel(url: string): string {
  // scp shorthand — new URL() throws on these, so match before the try block.
  const scp = url.match(/^(?:[^@/]+@)?github\.com:([^/]+)\/([^/]+?)(?:\.git)?$/);
  if (scp) return midTruncate(`${scp[1]}/${scp[2]}`);
  try {
    const u = new URL(url);
    if (u.hostname === "github.com" || u.hostname === "www.github.com") {
      const [owner, repo] = u.pathname.split("/").filter(Boolean);
      if (owner && repo) return midTruncate(`${owner}/${repo.replace(/\.git$/, "")}`);
    }
  } catch {
    // not a parseable URL — fall through and return as-is
  }
  return url;
}

// Label for any git remote, not just GitHub. GitHub keeps its "owner/repo"
// label so existing github_repo resources render exactly as before; every
// other host keeps its own name and the full namespace path, because a
// self-hosted GitLab may hold `group/alpha/api` and `group/beta/api` at once
// and an owner/repo label would render both as "alpha/api" vs "beta/api" —
// or worse, collapse them. Credentials embedded in the URL never reach the
// label: both branches read the host, which excludes userinfo.
export function gitRepoShortLabel(url: string): string {
  const githubLabel = githubShortLabel(url);
  if (githubLabel !== url) return githubLabel;
  // scp shorthand ([user@]host:path) — new URL() throws on these.
  const scp = url.includes("://") ? null : url.match(/^(?:[^@\s/]+@)?([^:\s/]+):(.+)$/);
  if (scp?.[1] && scp[2]) return `${scp[1]}/${stripDotGit(scp[2])}`;
  try {
    const parsed = new URL(url);
    if (parsed.host && parsed.pathname !== "/") {
      return `${parsed.host}${stripDotGit(parsed.pathname)}`;
    }
  } catch {
    // Not parseable — keep the raw string so the row stays readable.
  }
  return url;
}

function stripDotGit(s: string): string {
  return s.replace(/\.git$/, "");
}

// Whether a repo URL can be opened in a browser. ssh:// and the scp shorthand
// are clone transports, not pages: rendering them as an anchor gives the user
// a link the browser cannot follow. Only http(s) qualifies — this also keeps
// `javascript:` and `file:` out of an href.
export function isWebLinkableRepoUrl(url: string): boolean {
  try {
    const { protocol } = new URL(url);
    return protocol === "http:" || protocol === "https:";
  } catch {
    return false;
  }
}

// Middle-truncate a string to at most maxLen characters, preserving both the
// leading and trailing portions. The trailing portion is what distinguishes
// repos that share a long common prefix (e.g. customer-alpha vs customer-beta).
export function midTruncate(s: string, maxLen = 40): string {
  if (s.length <= maxLen) return s;
  const tail = Math.floor((maxLen - 1) / 2);
  const head = maxLen - 1 - tail;
  return `${s.slice(0, head)}…${s.slice(-tail)}`;
}
