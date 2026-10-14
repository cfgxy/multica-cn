import LinkifyIt from "linkify-it";

/**
 * Collect issue references from raw markdown, in order of first appearance.
 *
 * This is the shared detector behind RUYI-635: comments, issue descriptions and
 * chat messages no longer render issue mentions inline (web) or unlinked
 * (mobile) — both clients aggregate every reference into a deduplicated list
 * at the content tail. The SAME function must run on both ends so the tail row
 * count can never disagree between web and mobile for the same content (the
 * 2026-05-09 inbox count-drift rule, applied to references).
 *
 * Two reference forms are collected:
 *   - mention links  `[label](mention://issue/<uuid-or-identifier>)` — what the
 *     editor serialises an @-mention to;
 *   - bare identifiers `MUL-123` — the Linear-style autolink form; on web the
 *     readonly preprocessor rewrites these to mention links at render time, on
 *     mobile they stay plain text in the body, and both ends list them here.
 *
 * Two further text-reference forms ride the same skip rules and aggregation
 * (`form` distinguishes them, the render layer picks the resolver):
 *   - `pull_request` (`owner/repo#N`, RUYI-635 增量) — resolved against the
 *     workspace's known PR snapshot by owner/repo/number;
 *   - `merge_request` (`MR !N`, RUYI-728) — the spoken GitLab-style form. It
 *     carries no repo context, so resolution goes through the workspace's
 *     by-number PR lookup and only a UNIQUE hit resolves; the ref is
 *     normalised to `MR !N` so the spaced and no-space spellings dedupe to
 *     one token.
 *
 * Skip rules are ported from `packages/ui/markdown` so a token listed in the
 * tail is exactly a token the web body would have chipped: fenced/inline code
 * and math (`findCodeRanges`), existing markdown links
 * (`findMarkdownLinkRanges`), and detected URLs / emails / file paths
 * (`detectLinks`), plus the dotted-filename and path-segment context checks
 * from `ui/markdown/issue-identifiers.ts`. KEEP THE FOUR PORTS IN SYNC with
 * their ui originals — the ui copies stay because `packages/ui` cannot import
 * from `packages/core` (package boundary rules, root CLAUDE.md), and the
 * rewrite-side preprocessor lives there.
 *
 * Bare identifiers are scoped to the CURRENT workspace (RUYI-705): `AC-1` in
 * a body is acceptance-criteria numbering, not a reference — the server
 * resolves identifiers against the workspace, so a wrong-prefix token could
 * never resolve anyway. When the caller passes `opts.workspacePrefix`,
 * identifier-form hits whose prefix does not match `issue_prefix` are not
 * collected at all: no tail row, no resolution query. An unknown or empty
 * prefix falls through (matches everything) so callers without workspace
 * context keep the pre-RUYI-705 behaviour; the exact-match resolver stays the
 * correctness backstop. Mention-form references are NEVER prefix-filtered —
 * an explicit link is explicit, whatever it points at.
 *
 * Deliberately NOT done here: dedup (raw collection keeps every occurrence —
 * `dedupeIssueReferences` below is the raw-token policy; collapsing a UUID
 * mention with the same issue's bare identifier needs the resolved issue id
 * and belongs to the render layer). Remaining over-detection (unknown prefix,
 * unloaded workspace list) is harmless downstream: an unresolved reference
 * degrades to plain text.
 */

export type IssueReferenceForm =
  | "mention"
  | "identifier"
  | "pull_request"
  | "merge_request";

export interface IssueReference {
  /** UUID (mention form) or bare identifier (identifier form). */
  ref: string;
  /** Authored link label; `null` for bare identifiers. */
  label: string | null;
  form: IssueReferenceForm;
}

export interface ExtractIssueReferencesOptions {
  /**
   * Current workspace's `issue_prefix` (`RUYI` for `RUYI-123`). Bare
   * identifiers carry the workspace's own prefix, so a token prefixed
   * differently is not a reference to anything resolvable here (RUYI-705).
   * `null`/`undefined`/empty = unknown — fall through and collect (the
   * resolver's exact match decides), matching `useResolveIssueIdentifier`'s
   * documented loading-state behaviour.
   */
  workspacePrefix?: string | null;
}

// ---------------------------------------------------------------------------
// Range helpers — ported from packages/ui/markdown/linkify.ts (keep in sync)
// ---------------------------------------------------------------------------

interface CodeRange {
  start: number;
  end: number;
}

function findCodeRanges(text: string): CodeRange[] {
  const ranges: CodeRange[] = [];
  let match: RegExpExecArray | null;

  const fencedRegex = /```[\s\S]*?```/g;
  while ((match = fencedRegex.exec(text)) !== null) {
    ranges.push({ start: match.index, end: match.index + match[0].length });
  }

  const displayMathRegex = /\$\$[\s\S]*?\$\$/g;
  while ((match = displayMathRegex.exec(text)) !== null) {
    const pos = match.index;
    if (!ranges.some((r) => pos >= r.start && pos < r.end)) {
      ranges.push({ start: pos, end: pos + match[0].length });
    }
  }

  const inlineMathRegex = /(?<!\$)\$(?!\$)([^$\n]+)\$(?!\$)/g;
  while ((match = inlineMathRegex.exec(text)) !== null) {
    const pos = match.index;
    if (!ranges.some((r) => pos >= r.start && pos < r.end)) {
      ranges.push({ start: pos, end: pos + match[0].length });
    }
  }

  const inlineCodeRegex = /(?<!`)`(?!`)([^`\n]+)`(?!`)/g;
  while ((match = inlineCodeRegex.exec(text)) !== null) {
    const pos = match.index;
    if (!ranges.some((r) => pos >= r.start && pos < r.end)) {
      ranges.push({ start: pos, end: pos + match[0].length });
    }
  }

  return ranges;
}

function isInsideCode(pos: number, ranges: CodeRange[]): boolean {
  return ranges.some((r) => pos >= r.start && pos < r.end);
}

function rangesOverlap(
  a: { start: number; end: number },
  b: { start: number; end: number },
): boolean {
  return a.start < b.end && b.start < a.end;
}

function isEscaped(text: string, index: number): boolean {
  let slashCount = 0;
  for (let i = index - 1; i >= 0 && text[i] === "\\"; i--) {
    slashCount++;
  }
  return slashCount % 2 === 1;
}

function findMatchingBracket(text: string, openIndex: number): number {
  let depth = 0;
  for (let i = openIndex; i < text.length; i++) {
    if (isEscaped(text, i)) continue;
    const char = text[i];
    if (char === "[") {
      depth++;
    } else if (char === "]") {
      depth--;
      if (depth === 0) return i;
    }
  }
  return -1;
}

function findInlineLinkEnd(text: string, openParenIndex: number): number {
  let depth = 0;
  for (let i = openParenIndex; i < text.length; i++) {
    if (isEscaped(text, i)) continue;
    const char = text[i];
    if (char === "(") {
      depth++;
    } else if (char === ")") {
      depth--;
      if (depth === 0) return i + 1;
    }
  }
  return -1;
}

function findMarkdownLinkRanges(text: string): CodeRange[] {
  const ranges: CodeRange[] = [];
  for (let i = 0; i < text.length; i++) {
    if (text[i] !== "[" || isEscaped(text, i)) continue;
    if (ranges.some((r) => i >= r.start && i < r.end)) continue;

    const labelEnd = findMatchingBracket(text, i);
    if (labelEnd === -1) continue;

    const start =
      i > 0 && text[i - 1] === "!" && !isEscaped(text, i - 1) ? i - 1 : i;
    const nextChar = text[labelEnd + 1];

    if (nextChar === "(") {
      const end = findInlineLinkEnd(text, labelEnd + 1);
      if (end !== -1) {
        ranges.push({ start, end });
        i = end - 1;
      }
      continue;
    }

    if (nextChar === "[") {
      const referenceEnd = findMatchingBracket(text, labelEnd + 1);
      if (referenceEnd !== -1) {
        ranges.push({ start, end: referenceEnd + 1 });
        i = referenceEnd;
      }
    }
  }
  return ranges;
}

// ---------------------------------------------------------------------------
// URL / email / file-path detection — ported from detectLinks in
// packages/ui/markdown/linkify.ts (keep in sync)
// ---------------------------------------------------------------------------

const linkify = new LinkifyIt();

const FILE_EXTENSIONS =
  "ts|tsx|js|jsx|mjs|cjs|md|json|yaml|yml|py|go|rs|css|scss|less|html|htm|txt|log|sh|bash|zsh|swift|kt|java|c|cpp|h|hpp|rb|php|xml|toml|ini|cfg|conf|env|sql|graphql|vue|svelte|astro|prisma|dockerfile|makefile|gitignore";

const FILE_PATH_REGEX = new RegExp(
  `(?:^|[\\s([{<])((\\/|~\\/|\\.\\/)[\\w\\-./@]+\\.(?:${FILE_EXTENSIONS}))(?=[\\s)\\]}.,;:!?>]|$)`,
  "gi",
);

const CJK_URL_TERMINATOR_REGEX = /[！-／：-＠［-｀｛-～、。「-】]/;

const TRAILING_MD_DELIMITER = /[*~]+$/;

interface DetectedLink {
  start: number;
  end: number;
}

interface RawLinkifyMatch {
  text: string;
  url: string;
  schema: string;
  index: number;
}

function shouldAutoLink(value: string): boolean {
  return (
    /^https?:\/\//i.test(value) ||
    /^www\./i.test(value) ||
    /^[^\s@/:]+@[^\s@/:]+\.[^\s@/:]+$/.test(value)
  );
}

function collectLinkifyMatches(
  text: string,
  offset: number,
  out: DetectedLink[],
): void {
  const matches = linkify.match(text) as RawLinkifyMatch[] | null;
  if (!matches) return;

  for (const match of matches) {
    const cjkIdx = match.text.search(CJK_URL_TERMINATOR_REGEX);
    if (cjkIdx === 0) continue;

    const truncate = cjkIdx > 0;
    const matchText = (
      truncate ? match.text.slice(0, cjkIdx) : match.text
    ).replace(TRAILING_MD_DELIMITER, "");

    if (matchText.length > 0 && shouldAutoLink(matchText)) {
      out.push({
        start: match.index + offset,
        end: match.index + matchText.length + offset,
      });
    }

    if (truncate) {
      const tailStart = match.index + cjkIdx + 1;
      collectLinkifyMatches(text.slice(tailStart), offset + tailStart, out);
      return;
    }
  }
}

function detectLinks(text: string): DetectedLink[] {
  const links: DetectedLink[] = [];

  collectLinkifyMatches(text, 0, links);

  FILE_PATH_REGEX.lastIndex = 0;
  let fileMatch: RegExpExecArray | null;
  while ((fileMatch = FILE_PATH_REGEX.exec(text)) !== null) {
    const path = fileMatch[1];
    if (!path) continue;

    const fullMatch = fileMatch[0];
    const pathOffset = fullMatch.indexOf(path);
    const start = fileMatch.index + pathOffset;

    const pathRange = { start, end: start + path.length };
    if (links.some((link) => rangesOverlap(pathRange, link))) continue;

    links.push({ start, end: start + path.length });
  }

  return links.sort((a, b) => a.start - b.start);
}

// ---------------------------------------------------------------------------
// Reference extraction
// ---------------------------------------------------------------------------

// Same shape as ui/markdown/issue-identifiers.ts IDENTIFIER_RE (keep in sync):
// uppercase prefix + `-` + digits, standalone token on both sides.
const IDENTIFIER_RE = /(?<![A-Za-z0-9_-])([A-Z][A-Z0-9]*-\d+)(?![A-Za-z0-9_-])/g;

// `owner/repo#N` — the GitHub-style PR text reference (RUYI-635 增量, Owner
// point ②). Owner/repo follow GitHub's loose lexical shape (alphanumeric start,
// hyphens; dots inside the repo between alphanumerics). The left lookbehind
// rejects path (`/x/y/repo#1`) and dotted-name (`file.x/repo#1`) contexts, the
// same boundaries the bare-identifier rule enforces. Latin letter glue on the
// left (`seexowner/repo#1`) is indistinguishable from a legitimate owner and
// is absorbed — resolution misses degrade to plain text, so over-detection is
// harmless. `form: "pull_request"`, `ref` = the full `owner/repo#N` token.
const PR_REFERENCE_RE =
  /(?<![A-Za-z0-9_.\-/])([A-Za-z0-9][A-Za-z0-9-]*)\/([A-Za-z0-9][A-Za-z0-9_-]*(?:\.[A-Za-z0-9_-]+)*)#(\d+)(?![A-Za-z0-9])/g;

// `MR !N` / `MR!N` — the spoken GitLab-style merge-request form (RUYI-728).
// Case-sensitive `MR` on purpose, same rationale as IDENTIFIER_RE: lowercase
// `mr` in prose is not an author-intent marker. The optional single space
// mirrors how authors type it; both spellings normalise to the spaced form in
// `ref` so they dedupe to one token. A preceding `/` is allowed to fall
// through to the URL-overlap skip below (the `MR` inside a detected URL is
// inside that URL's range, so the shared skip rules reject it).
const MR_REFERENCE_RE = /(?<![A-Za-z0-9])MR ?!([0-9]+)(?![A-Za-z0-9])/g;

// Label allows backslash-escaped metacharacters and excludes bare backslash so
// the alternatives cannot backtrack against each other (ReDoS, web #4881).
const ISSUE_MENTION_LINK_RE =
  /\[((?:\\.|[^\]\\])*)\]\(mention:\/\/issue\/([^)\s]+)\)/g;

/**
 * True when the bare identifier `identifier` can belong to a workspace whose
 * issue prefix is `prefix` (RUYI-705 gate). Case-insensitive; the dash in the
 * compared form keeps a proper prefix (`RUYI`) from matching a longer one
 * (`RUYIX-1`). An unknown prefix (`null`/`undefined`/empty) matches
 * everything — callers without workspace context fall through and let the
 * exact-match resolver decide.
 *
 * The ui copy of the surrounding extraction rules lives in
 * `packages/ui/markdown/issue-identifiers.ts` (package boundary); this helper
 * is the single authority for the prefix comparison itself — the views hooks
 * (`useResolveIssueIdentifier`, `useIssueReferenceResolutions`) and the
 * editor's autolink resolver use it too.
 */
export function identifierMatchesPrefix(
  identifier: string,
  prefix: string | null | undefined,
): boolean {
  if (!prefix) return true;
  return identifier.toUpperCase().startsWith(`${prefix.toUpperCase()}-`);
}

/**
 * Collect every issue reference in `text` (mention links + bare identifiers),
 * ordered by position of first appearance. Pure — no workspace, no network;
 * the workspace only enters as the `opts.workspacePrefix` string.
 */
export function extractIssueReferences(
  text: string,
  opts?: ExtractIssueReferencesOptions,
): IssueReference[] {
  if (!text) return [];

  const workspacePrefix = opts?.workspacePrefix;
  const codeRanges = findCodeRanges(text);
  const linkRanges = findMarkdownLinkRanges(text);
  const detectedLinks = detectLinks(text);

  const hits: (IssueReference & { start: number })[] = [];

  ISSUE_MENTION_LINK_RE.lastIndex = 0;
  let match: RegExpExecArray | null;
  while ((match = ISSUE_MENTION_LINK_RE.exec(text)) !== null) {
    if (isInsideCode(match.index, codeRanges)) continue;
    const ref = match[2];
    if (!ref) continue;
    hits.push({ ref, label: match[1] ?? null, form: "mention", start: match.index });
  }

  IDENTIFIER_RE.lastIndex = 0;
  while ((match = IDENTIFIER_RE.exec(text)) !== null) {
    const identifier = match[1];
    if (!identifier) continue;
    const start = match.index;
    const end = start + identifier.length;
    const range = { start, end };

    if (isInsideCode(start, codeRanges)) continue;
    if (linkRanges.some((r) => rangesOverlap(range, r))) continue;
    if (detectedLinks.some((l) => rangesOverlap(range, l))) continue;

    // Dotted continuation (`ABC-123.ts`) — a `.` followed by an alphanumeric
    // means the token is part of a larger dotted name. A sentence-final `.`
    // stays collectable.
    const after = text[end];
    if (after === "." && /[A-Za-z0-9]/.test(text[end + 1] ?? "")) continue;
    // Path segment (`FOO-1/bar`, `foo/BAR-1`) — a `/` on either side signals
    // a path rather than a standalone reference.
    if (after === "/" || text[start - 1] === "/") continue;
    // Embedded in a dotted name on the left (`file.MUL-1`).
    if (text[start - 1] === ".") continue;
    // Wrong workspace prefix (`AC-1` while this is `RUYI`) — acceptance-
    // criteria-style numbering, not a reference (RUYI-705). Applied after the
    // shape skips: the prefix filter only ever removes tokens that would
    // otherwise be collected.
    if (!identifierMatchesPrefix(identifier, workspacePrefix)) continue;

    hits.push({ ref: identifier, label: null, form: "identifier", start });
  }

  PR_REFERENCE_RE.lastIndex = 0;
  while ((match = PR_REFERENCE_RE.exec(text)) !== null) {
    const token = match[0];
    if (!token) continue;
    const start = match.index;
    const end = start + token.length;
    const range = { start, end };

    if (isInsideCode(start, codeRanges)) continue;
    if (linkRanges.some((r) => rangesOverlap(range, r))) continue;
    if (detectedLinks.some((l) => rangesOverlap(range, l))) continue;

    // Same dotted-continuation tail check as identifiers: `repo#338.tar.gz`
    // is an artifact name, not a reference. A sentence-final `.` stays.
    const after = text[end];
    if (after === "." && /[A-Za-z0-9]/.test(text[end + 1] ?? "")) continue;
    // `repo#338/commits` — a following `/` is a deeper path, not a reference.
    if (after === "/") continue;

    hits.push({ ref: token, label: null, form: "pull_request", start });
  }

  MR_REFERENCE_RE.lastIndex = 0;
  while ((match = MR_REFERENCE_RE.exec(text)) !== null) {
    const number = match[1];
    if (!number) continue;
    const start = match.index;
    const end = start + match[0].length;
    const range = { start, end };

    if (isInsideCode(start, codeRanges)) continue;
    if (linkRanges.some((r) => rangesOverlap(range, r))) continue;
    if (detectedLinks.some((l) => rangesOverlap(range, l))) continue;

    // Same dotted-continuation and path tails as the PR form.
    const after = text[end];
    if (after === "." && /[A-Za-z0-9]/.test(text[end + 1] ?? "")) continue;
    if (after === "/") continue;

    // Ref is the normalised spaced spelling so `MR!468` and `MR !468`
    // collapse to one token in dedupeIssueReferences.
    hits.push({ ref: `MR !${number}`, label: null, form: "merge_request", start });
  }

  return hits
    .sort((a, b) => a.start - b.start)
    .map(({ start: _start, ...ref }) => ref);
}

/**
 * Collapse repeated references to the same raw token (same form + ref),
 * keeping the first occurrence's position and authored label. Cross-form
 * collapse (a UUID mention and the same issue's bare identifier) needs the
 * resolved issue id and is the render layer's job.
 */
export function dedupeIssueReferences(refs: IssueReference[]): IssueReference[] {
  const seen = new Set<string>();
  const out: IssueReference[] = [];
  for (const ref of refs) {
    const key = `${ref.form}:${ref.ref}`;
    if (seen.has(key)) continue;
    seen.add(key);
    out.push(ref);
  }
  return out;
}

/**
 * Whole-string form of the identifier rule above (ui/markdown's
 * isIssueIdentifier keeps its own copy — package boundary). Used to decide
 * which display token a mention link's id segment maps to.
 */
export function isIssueIdentifier(value: string): boolean {
  return /^[A-Z][A-Z0-9]*-\d+$/.test(value);
}

export interface PullRequestRefParts {
  owner: string;
  repo: string;
  number: number;
}

/**
 * Split a `pull_request`-form reference token (`owner/repo#N`) into the parts
 * a PR lookup needs, or `null` when the token is not that shape (an issue
 * identifier, a mention UUID, garbage). Pure and total — render layers call
 * this before spending a query.
 */
export function parsePullRequestReference(
  ref: string,
): PullRequestRefParts | null {
  const match = /^([A-Za-z0-9][A-Za-z0-9-]*)\/([A-Za-z0-9][A-Za-z0-9_.-]*)#(\d+)$/.exec(
    ref,
  );
  if (!match) return null;
  return { owner: match[1]!, repo: match[2]!, number: Number(match[3]) };
}

/**
 * Split a `merge_request`-form reference token (`MR !N`, the normalised
 * spelling `extractIssueReferences` emits) into the parts a by-number PR
 * lookup needs, or `null` for anything else. Pure and total, same contract
 * as `parsePullRequestReference`.
 */
export function parseMergeRequestReference(
  ref: string,
): { number: number } | null {
  const match = /^MR !(\d+)$/.exec(ref);
  if (!match) return null;
  return { number: Number(match[1]) };
}

/**
 * Rewrite text-form references into markdown links the readonly bodies can
 * render as underlined links (RUYI-728, Owner points ①②):
 *
 *   - `MR !468` / `MR!468` → `[MR !468](mention://merge-request/468)` — the
 *     render layer resolves the bare number against the workspace's by-number
 *     PR lookup and keeps the token as plain text on a miss;
 *   - `owner/repo#468` → `[owner/repo#468](https://github.com/owner/repo/pull/468)`
 *     — a canonical external URL needs no resolution; GitHub itself redirects
 *     `/pull/N` to `/issues/N` when the number belongs to an issue.
 *
 * The shared readonly preprocessing runs this on web (preprocessMarkdown,
 * opt-in) and mobile (preprocessMobileMarkdown) so both bodies linkify the
 * same tokens. Bare issue identifiers are NOT handled here — the ui-side
 * `preprocessIssueIdentifiers` pass owns that form and runs before it.
 *
 * Skips the same contexts as the extractor: code/math, existing markdown
 * links, detected URLs/emails/file paths, dotted tails and path
 * continuations. Idempotent: the output's new links are skipped as existing
 * markdown links on a second pass.
 */
export function autolinkReferenceTokens(text: string): string {
  const hasMR = /(^|[^A-Za-z0-9])MR ?![0-9]/.test(text);
  const hasPR = /(^|[^A-Za-z0-9_.\-/])[A-Za-z0-9][A-Za-z0-9-]*\/[A-Za-z0-9][A-Za-z0-9_-]*(?:\.[A-Za-z0-9_-]+)*#[0-9]/.test(
    text,
  );
  if (!hasMR && !hasPR) return text;

  const codeRanges = findCodeRanges(text);
  const linkRanges = findMarkdownLinkRanges(text);
  const detectedLinks = detectLinks(text);

  interface Rewrite {
    start: number;
    end: number;
    replacement: string;
  }
  const rewrites: Rewrite[] = [];

  MR_REFERENCE_RE.lastIndex = 0;
  let match: RegExpExecArray | null;
  while ((match = MR_REFERENCE_RE.exec(text)) !== null) {
    const number = match[1];
    if (!number) continue;
    const start = match.index;
    const end = start + match[0].length;
    const range = { start, end };
    if (isInsideCode(start, codeRanges)) continue;
    if (linkRanges.some((r) => rangesOverlap(range, r))) continue;
    if (detectedLinks.some((l) => rangesOverlap(range, l))) continue;
    const after = text[end];
    if (after === "." && /[A-Za-z0-9]/.test(text[end + 1] ?? "")) continue;
    if (after === "/") continue;
    rewrites.push({
      start,
      end,
      replacement: `[${text.slice(start, end)}](mention://merge-request/${number})`,
    });
  }

  PR_REFERENCE_RE.lastIndex = 0;
  while ((match = PR_REFERENCE_RE.exec(text)) !== null) {
    const token = match[0];
    if (!token) continue;
    const start = match.index;
    const end = start + token.length;
    const range = { start, end };
    if (isInsideCode(start, codeRanges)) continue;
    if (linkRanges.some((r) => rangesOverlap(range, r))) continue;
    if (detectedLinks.some((l) => rangesOverlap(range, l))) continue;
    const after = text[end];
    if (after === "." && /[A-Za-z0-9]/.test(text[end + 1] ?? "")) continue;
    if (after === "/") continue;
    const parts = parsePullRequestReference(token);
    if (!parts) continue;
    rewrites.push({
      start,
      end,
      replacement: `[${token}](https://github.com/${parts.owner}/${parts.repo}/pull/${parts.number})`,
    });
  }

  if (rewrites.length === 0) return text;
  rewrites.sort((a, b) => a.start - b.start);

  let out = "";
  let cursor = 0;
  for (const rewrite of rewrites) {
    if (rewrite.start < cursor) continue; // overlap guard — keep first claim
    out += text.slice(cursor, rewrite.start);
    out += rewrite.replacement;
    cursor = rewrite.end;
  }
  out += text.slice(cursor);
  return out;
}
