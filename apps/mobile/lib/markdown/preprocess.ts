/**
 * Pure string transforms applied before marked.lexer parses the content.
 *
 *   1. Legacy mention shortcodes `[@ id="..." label="..."]` → modern
 *      mention link `[@Label](mention://member/id)`. Old DB rows from before
 *      the April 2026 migration use the shortcode form; the modern form is
 *      what marked.js can naturally tokenize as a markdown link. Calls into
 *      `@multica/core/markdown` (single source of truth — same regex web/
 *      desktop run).
 *
 *   2. Bare reference tokens `MR !N` / `owner/repo#N` → markdown links
 *      (RUYI-728): an MR number becomes `mention://merge-request/<N>` (the
 *      tap resolves the number to a PR and hands it to the OS — see
 *      `link-route.ts`), `owner/repo#N` becomes the canonical GitHub PR URL
 *      (GitHub redirects /pull/N ↔ /issues/N automatically). Issue mention
 *      links stay links — the RUYI-635 demote pass is gone: web now
 *      linkifies in-body references (rich-content.tsx) and mobile follows,
 *      while `IssueReferenceList` still aggregates the same tokens into the
 *      tail sheet. Lives in `@multica/core/markdown` so the token grammar
 *      can't drift from web.
 *
 *   3. File card lines `!file[name](url)` → standard link `[📎 name](url)`.
 *      marked.js doesn't recognize the `!file` prefix; web's preprocess
 *      turns it into HTML, which mobile can't render natively. Rewriting
 *      to a normal link with a 📎 emoji makes it a tappable link that
 *      `Linking.openURL` opens in the system viewer (Safari for PDFs,
 *      QuickLook for docs, share sheet for arbitrary files).
 *
 * NOTE: Web's preprocess also has a pass that detects bare CDN
 * URLs as legacy file links. We skip that because mobile doesn't bootstrap
 * the cdnDomain config. Old comments using the legacy form render as plain
 * hyperlinks — same tap behavior, just no 📎 prefix. Acceptable degradation.
 */
import {
  autolinkReferenceTokens,
  preprocessMentionShortcodes,
} from "@multica/core/markdown";

export interface PreprocessMobileMarkdownOptions {
  workspacePrefix?: string | null;
}

const BARE_ISSUE_RE = /(?<![A-Za-z0-9_-])([A-Z][A-Z0-9]*-\d+)(?![A-Za-z0-9_-])/g;
const BODY_PULL_REQUEST_RE = /(?<![A-Za-z0-9])PR ?#(\d+)(?![A-Za-z0-9])/g;

function overlaps(
  range: { start: number; end: number },
  other: { start: number; end: number },
): boolean {
  return range.start < other.end && other.start < range.end;
}

function protectedRanges(input: string): Array<{ start: number; end: number }> {
  const ranges: Array<{ start: number; end: number }> = [];
  const patterns = [
    /```[\s\S]*?```/g,
    /(?<!`)`(?!`)[^`\n]+`(?!`)/g,
    /!?\[[^\]]*\]\([^)]*\)/g,
    /https?:\/\/[^\s)]+/g,
  ];
  for (const pattern of patterns) {
    let match: RegExpExecArray | null;
    while ((match = pattern.exec(input)) !== null) {
      ranges.push({ start: match.index, end: match.index + match[0].length });
    }
  }
  return ranges;
}

function rewriteBodyReferences(input: string, workspacePrefix?: string | null): string {
  if (!/[A-Z][A-Z0-9]*-\d|\bPR ?#\d/.test(input)) return input;
  const protectedTextRanges = protectedRanges(input);
  const rewrites: Array<{ start: number; end: number; replacement: string }> = [];

  const addMatches = (
    pattern: RegExp,
    replacement: (match: string, id: string) => string,
    prefixGated = false,
  ) => {
    pattern.lastIndex = 0;
    let match: RegExpExecArray | null;
    while ((match = pattern.exec(input)) !== null) {
      const token = match[0];
      const id = match[1];
      if (!token || !id) continue;
      const start = match.index;
      const end = start + token.length;
      if (protectedTextRanges.some((range) => overlaps({ start, end }, range))) continue;
      if (
        (input[end] === "." && /[A-Za-z0-9]/.test(input[end + 1] ?? "")) ||
        input[end] === "/" ||
        input[start - 1] === "/" ||
        input[start - 1] === "."
      ) {
        continue;
      }
      if (prefixGated && workspacePrefix && !id.toUpperCase().startsWith(`${workspacePrefix.toUpperCase()}-`)) continue;
      rewrites.push({ start, end, replacement: replacement(token, id) });
    }
  };

  addMatches(BARE_ISSUE_RE, (token) => `[${token}](mention://issue/${token})`, true);
  addMatches(BODY_PULL_REQUEST_RE, (token, number) => `[${token}](mention://pull-request/${number})`);
  if (!rewrites.length) return input;
  rewrites.sort((a, b) => a.start - b.start);
  let output = "";
  let cursor = 0;
  for (const rewrite of rewrites) {
    if (rewrite.start < cursor) continue;
    output += input.slice(cursor, rewrite.start) + rewrite.replacement;
    cursor = rewrite.end;
  }
  return output + input.slice(cursor);
}

// File-card line matcher, kept in sync with web's parser in
// `packages/ui/markdown/file-cards.ts` (NEW_FILE_CARD_RE + FILE_CARD_URL_PATTERN):
//
//   - Label allows backslash-escaped metacharacters (`\[ \] \\ \( \)`) so a
//     filename like `a]b.pdf` — which the CLI escapes to `a\]b.pdf` in its
//     `!file[...]` output (see cmd_attachment.go escapeMarkdownLabel) — is
//     captured whole. Backslash is excluded from the negated class so
//     overlapping alternatives can't backtrack (ReDoS, web #4881).
//   - URL is restricted to the same allowlist web accepts: site-relative
//     `/uploads/...` and `/api/attachments/<UUID>/download`, plus absolute
//     `http(s)://`. Anything else (`javascript:`, `data:`, `//host`, other
//     `/api/…`) is left as plain text so a stored card can't become an
//     out-of-band navigation.
const ATTACHMENT_UUID =
  "[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}";
const FILE_CARD_URL = `/uploads/[^)]*|https?://[^)]+|/api/attachments/${ATTACHMENT_UUID}/download`;
const FILE_LINE_RE = new RegExp(
  `^!file\\[((?:\\\\.|[^\\]\\\\])*)\\]\\((${FILE_CARD_URL})\\)$`,
);

// Unescape the file-card label back to the real filename (mirrors web's
// `newMatch[1].replace(/\\([[\]\\()])/g, "$1")`).
function unescapeFileLabel(label: string): string {
  return label.replace(/\\([[\]\\()])/g, "$1");
}

// Re-escape only the characters that would break a markdown LINK label, so the
// emitted `[📎 name](url)` stays valid markdown. Mobile's target is a link
// (re-parsed by marked / the enriched renderer), unlike web's HTML
// `data-filename` attribute — so a raw `]` must not truncate the link text.
function escapeLinkLabel(name: string): string {
  return name.replace(/([\\[\]])/g, "\\$1");
}

function preprocessFileCards(input: string): string {
  return input
    .split("\n")
    .map((line) => {
      const m = line.trim().match(FILE_LINE_RE);
      if (!m) return line;
      const label = escapeLinkLabel(unescapeFileLabel(m[1]!));
      return `[📎 ${label}](${m[2]})`;
    })
    .join("\n");
}

/**
 * Give `mention://comment/<id>` links the chip affordance web and desktop
 * render as a real component (RUYI-108).
 *
 * Web builds `CommentMentionCard`: an icon, the label, a border and a
 * resolved-vs-degraded split. Mobile cannot — enriched-markdown renders prose
 * natively and refuses injected React for any leaf node (issues #54 / #232 /
 * #246), so an inline chip has no place to mount. What enriched DOES expose is
 * `linkVariants`, which restyles a link by URL pattern; `markdown-style.ts`
 * uses it to give this scheme the chip's tinted background instead of the
 * generic underlined-link look. This pass supplies the other half — the
 * leading 💬 glyph that marks the reference as a comment at a glance, the same
 * way `!file` cards get 📎 above.
 *
 * The resolved / degraded distinction stays a TAP-time answer on mobile
 * (`resolveCommentAnchor` → focus or a warning haptic). Deciding it at render
 * time would need the timeline inside the markdown renderer, and a chip that
 * silently changed shape as older comments paged in would read as a glitch.
 *
 * Idempotent: a label already carrying the glyph is left alone.
 */
const COMMENT_ANCHOR_GLYPH = "💬";
// Label allows escaped metacharacters for the same reason the file-card
// pattern does, and excludes bare backslash so the alternatives cannot
// backtrack against each other (ReDoS).
const COMMENT_ANCHOR_RE =
  /\[((?:\\.|[^\]\\])*)\]\((mention:\/\/comment\/[^)\s]+)\)/g;

function preprocessCommentAnchors(input: string): string {
  return input.replace(COMMENT_ANCHOR_RE, (match, label: string, url: string) => {
    const text = label.trim();
    if (text.startsWith(COMMENT_ANCHOR_GLYPH)) return match;
    // An empty label would render as an untappable zero-width link; the glyph
    // alone still gives a target and still says "this points at a comment".
    return `[${COMMENT_ANCHOR_GLYPH} ${label}](${url})`;
  });
}

/**
 * Add GFM strikethrough markers around the content of checked task list items
 * so they render with `~~text~~` styling — matching Linear / Notion / Apple
 * Reminders / Things 3, where a checked item is visually crossed out.
 *
 * GFM itself does not specify that checked items SHOULD be struck through;
 * enriched-markdown's task-list renderer only changes the checkbox glyph and
 * (via `checkedTextColor`) dims the text. Without the strikethrough the
 * "done" state reads weakly, and users who expect the platform pattern from
 * other task apps assume the checkbox didn't take effect.
 *
 * Idempotent: skips lines whose body is already wrapped in `~~ ... ~~`.
 * Conservative regex — only matches `- [x]` / `* [x]` / `+ [x]` at the start
 * of a line (allowing leading whitespace), case-insensitive on the `x`.
 */
const TASK_DONE_RE = /^(\s*[-*+]\s+\[[xX]\]\s+)(.+)$/gm;

function preprocessTaskListStrikethrough(input: string): string {
  return input.replace(TASK_DONE_RE, (match, prefix, body) => {
    const trimmed = body.trim();
    if (trimmed.startsWith("~~") && trimmed.endsWith("~~")) return match;
    return `${prefix}~~${body}~~`;
  });
}

/**
 * Strip embedded HTML before marked sees it. Mobile cannot do what web does
 * (rehype-raw + sanitize → render real <br> / <sub> / <details>) — RN has
 * no inline HTML. Without this pass, users see literal `<br>` tags in the
 * comment body. Strategy:
 *
 *   - `<br>` / `<br/>` / `<br />` → `"  \n"` (two trailing spaces + newline,
 *     the canonical CommonMark hard-break syntax). md4c respects it as a
 *     hard line break inside a paragraph; bare `\n` would be treated as a
 *     space (CommonMark default), losing intentional `<br>` semantics.
 *   - HTML comments `<!-- ... -->` → removed entirely.
 *   - Every other tag → strip the tag, keep the inner text. So
 *     `<sub>2</sub>` becomes `2`. Loses formatting but keeps content; far
 *     better than showing raw HTML.
 *
 * Does not parse — pure regex. Cannot handle nested tags with attributes
 * containing `>`, but those don't appear in our editor output.
 */
function stripHtml(input: string): string {
  return input
    .replace(/<!--[\s\S]*?-->/g, "")
    .replace(/<br\s*\/?>/gi, "  \n")
    .replace(/<\/?[a-z][^>]*>/gi, "");
}

export function preprocessMobileMarkdown(
  input: string,
  options?: PreprocessMobileMarkdownOptions,
): string {
  if (!input) return "";
  return preprocessTaskListStrikethrough(
    preprocessCommentAnchors(
      preprocessFileCards(
        autolinkReferenceTokens(
          rewriteBodyReferences(
            preprocessMentionShortcodes(stripHtml(input)),
            options?.workspacePrefix,
          ),
        ),
      ),
    ),
  );
}
