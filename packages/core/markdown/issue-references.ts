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
 * Deliberately NOT done here: dedup (raw collection keeps every occurrence —
 * `dedupeIssueReferences` below is the raw-token policy; collapsing a UUID
 * mention with the same issue's bare identifier needs the resolved issue id
 * and belongs to the render layer). Over-detection is also harmless downstream:
 * an unresolved reference degrades to plain text.
 */

export type IssueReferenceForm = "mention" | "identifier";

export interface IssueReference {
  /** UUID (mention form) or bare identifier (identifier form). */
  ref: string;
  /** Authored link label; `null` for bare identifiers. */
  label: string | null;
  form: IssueReferenceForm;
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

// Label allows backslash-escaped metacharacters and excludes bare backslash so
// the alternatives cannot backtrack against each other (ReDoS, web #4881).
const ISSUE_MENTION_LINK_RE =
  /\[((?:\\.|[^\]\\])*)\]\(mention:\/\/issue\/([^)\s]+)\)/g;

/**
 * Collect every issue reference in `text` (mention links + bare identifiers),
 * ordered by position of first appearance. Pure — no workspace, no network.
 */
export function extractIssueReferences(text: string): IssueReference[] {
  if (!text) return [];

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

    hits.push({ ref: identifier, label: null, form: "identifier", start });
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

function demoteMatch(_match: string, label: string, ref: string): string {
  // Same display contract as web's plain-text mention rendering: identifier
  // form shows the identifier, a UUID mention shows its authored label. An
  // empty label falls back to the id segment so the reference never vanishes
  // from the prose (web renders an empty span there; on mobile the link is
  // gone entirely, so an empty demote would be a silent data loss).
  return isIssueIdentifier(ref) ? ref : label || ref;
}

/**
 * Rewrite issue mention links to the plain text the body should read
 * (RUYI-635, mobile half). Web renders mentions as plain text at the component
 * layer; mobile has no component layer in its markdown pipeline — only string
 * passes — so the rewrite happens here, and the SAME display contract applies:
 * `[MUL-7](mention://issue/MUL-7)` → `MUL-7`, `[label](mention://issue/<uuid>)`
 * → `label`. Navigation moves to the tail aggregation list.
 *
 * Code spans and fenced blocks keep their literal link text, matching both the
 * extractor's skip contract and web (where ReactMarkdown never parses link
 * syntax inside code). Other mention schemes are untouched.
 *
 * Idempotent: demoted output contains no issue mention links to rewrite.
 */
export function demoteIssueMentionLinks(text: string): string {
  if (!text.includes("mention://issue/")) return text;

  const codeRanges = findCodeRanges(text).sort((a, b) => a.start - b.start);
  if (codeRanges.length === 0) {
    return text.replace(ISSUE_MENTION_LINK_RE, demoteMatch);
  }

  // Rebuild segment-wise: demote outside code ranges, keep ranges verbatim.
  let out = "";
  let cursor = 0;
  for (const range of codeRanges) {
    if (range.start < cursor) continue; // overlap guard — keep first claim
    if (range.start > cursor) {
      out += text
        .slice(cursor, range.start)
        .replace(ISSUE_MENTION_LINK_RE, demoteMatch);
    }
    out += text.slice(range.start, range.end);
    cursor = range.end;
  }
  if (cursor < text.length) {
    out += text.slice(cursor).replace(ISSUE_MENTION_LINK_RE, demoteMatch);
  }
  return out;
}
