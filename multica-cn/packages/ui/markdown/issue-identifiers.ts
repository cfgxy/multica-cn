import {
  detectLinks,
  findCodeRanges,
  findMarkdownLinkRanges,
  isInsideCode,
  rangesOverlap,
} from './linkify'

/**
 * Linear-style bare issue identifier autolinking for markdown preprocessing.
 *
 * Rewrites bare issue identifiers like `MUL-123` / `TES-1` into canonical
 * mention links `[MUL-123](mention://issue/MUL-123)`, so the shared markdown
 * `a`/mention renderers can route them to a navigable issue chip.
 *
 * This module is intentionally PURE (packages/ui): it has no workspace or API
 * access. It only DETECTS candidate identifiers — whether one resolves to a
 * real issue in the current workspace is decided later, in the views layer,
 * via an exact-match lookup. A candidate that resolves to nothing renders as
 * plain text, so over-detection here is harmless (at most one deduped lookup).
 * We still skip code, existing links, URLs, and file/path tokens so non-prose
 * content is never rewritten.
 *
 * Bare identifiers are scoped to the CURRENT workspace (RUYI-705): `AC-1` in
 * a body is acceptance-criteria numbering, not a reference. Callers that know
 * the workspace's `issue_prefix` pass it via `opts.workspacePrefix` and
 * non-matching tokens stay plain text — same gate, same fall-through-on-
 * unknown semantics as core's `extractIssueReferences` (the tail-list
 * extractor). KEEP THE PREFIX GATE IN SYNC with the core copy: `packages/ui`
 * cannot import from `packages/core` (package boundary rules, root CLAUDE.md),
 * and the comparison lives in core's `identifierMatchesPrefix` — this file
 * keeps its own inline port. Mention-form links are never prefix-filtered;
 * they are not produced here anyway.
 *
 * NOTE: the `mention://issue/<id>` transport is reused for both real UUID
 * mentions and bare identifiers. Render sites tell them apart by the shape of
 * the id segment (`isIssueIdentifier`) — a UUID never matches the identifier
 * pattern, so the dispatch is unambiguous.
 */

// Detection form (global). Prefix is one uppercase letter followed by uppercase
// alphanumerics, then `-<number>`. Case-sensitive on purpose: lowercase
// `abc-1` in prose is almost never an issue reference, and matching it would
// linkify ordinary hyphenated words. The look-around excludes alphanumerics,
// `_`, and `-` on both sides so we only match standalone tokens.
const IDENTIFIER_RE = /(?<![A-Za-z0-9_-])([A-Z][A-Z0-9]*-\d+)(?![A-Za-z0-9_-])/g

// Anchored single-token form — used by render sites to distinguish a bare
// identifier (carried through `mention://issue/<identifier>`) from a real UUID.
export const ISSUE_IDENTIFIER_PATTERN = /^[A-Z][A-Z0-9]*-\d+$/

/**
 * True when `value` is a bare issue identifier (e.g. "MUL-123"). A UUID never
 * matches (lowercase hex, four dashes), so this cleanly separates autolinked
 * identifiers from real mention UUIDs at render time.
 */
export function isIssueIdentifier(value: string): boolean {
  return ISSUE_IDENTIFIER_PATTERN.test(value)
}

export interface PreprocessIssueIdentifiersOptions {
  /**
   * Current workspace's `issue_prefix` (`RUYI` for `RUYI-123`). Bare
   * identifiers carry the workspace's own prefix, so a differently-prefixed
   * token is not a reference to anything resolvable here and stays plain text
   * (RUYI-705). `null`/`undefined`/empty = unknown — fall through and rewrite
   * as before, matching core's extraction-side behaviour (identifierMatches
   * Prefix doc there has the full rationale).
   */
  workspacePrefix?: string | null
}

/**
 * Port of core's `identifierMatchesPrefix` (packages/ui cannot import from
 * packages/core) — see that function for the dash-boundary rationale. KEEP IN
 * SYNC with the core copy.
 */
function identifierMatchesPrefix(
  identifier: string,
  prefix: string | null | undefined,
): boolean {
  if (!prefix) return true
  return identifier.toUpperCase().startsWith(`${prefix.toUpperCase()}-`)
}

/**
 * Rewrite bare issue identifiers into canonical `mention://issue/<identifier>`
 * markdown links. Runs BEFORE `preprocessLinks`/`preprocessFileCards` so those
 * passes see the rewritten spans as existing markdown links and skip them.
 *
 * Skipped contexts (never rewritten):
 *   - fenced code blocks, inline code, and math (findCodeRanges)
 *   - existing markdown links / images (findMarkdownLinkRanges)
 *   - detected URLs / emails / file paths (detectLinks)
 *   - dotted filename tails like `ABC-123.ts` and path segments like `FOO-1/bar`
 *   - identifiers whose prefix cannot match `opts.workspacePrefix` (RUYI-705)
 */
export function preprocessIssueIdentifiers(
  text: string,
  opts?: PreprocessIssueIdentifiersOptions,
): string {
  // Cheap early-out: no `<UPPER>…-<DIGIT>` shape at all.
  if (!/[A-Z][A-Z0-9]*-\d/.test(text)) return text

  const workspacePrefix = opts?.workspacePrefix
  const codeRanges = findCodeRanges(text)
  const linkRanges = findMarkdownLinkRanges(text)
  const detectedLinks = detectLinks(text)

  IDENTIFIER_RE.lastIndex = 0
  let result = ''
  let lastIndex = 0
  let match: RegExpExecArray | null

  while ((match = IDENTIFIER_RE.exec(text)) !== null) {
    const identifier = match[1]
    if (!identifier) continue
    const start = match.index
    const end = start + identifier.length
    const range = { start, end }

    // Inside fenced/inline code or math.
    if (isInsideCode(start, codeRanges)) continue
    // Inside an existing markdown link/image (label OR destination).
    if (linkRanges.some((r) => rangesOverlap(range, r))) continue
    // Inside a detected URL / email / file path.
    if (detectedLinks.some((l) => rangesOverlap(range, l))) continue

    // Dotted continuation such as `ABC-123.ts` (filename) or `ABC-123.tar.gz`:
    // a `.` immediately followed by an alphanumeric means the token is part of
    // a larger dotted name. A trailing `.` before whitespace/EOL is a sentence
    // end ("see MUL-1.") and stays linkable.
    const after = text[end]
    if (after === '.' && /[A-Za-z0-9]/.test(text[end + 1] ?? '')) continue
    // Path segment such as `FOO-1/bar` or `foo/BAR-1` — a `/` on either side
    // signals a path rather than a standalone reference.
    if (after === '/' || text[start - 1] === '/') continue
    // Embedded in a dotted name on the left (`file.MUL-1`).
    if (text[start - 1] === '.') continue
    // Wrong workspace prefix (`AC-1` while this is `RUYI`) — not a reference,
    // leave it as plain text (RUYI-705). Applied after the shape skips, mirroring
    // the core extractor's ordering.
    if (!identifierMatchesPrefix(identifier, workspacePrefix)) continue

    result += text.slice(lastIndex, start)
    result += `[${identifier}](mention://issue/${identifier})`
    lastIndex = end
  }

  if (lastIndex === 0) return text
  result += text.slice(lastIndex)
  return result
}
