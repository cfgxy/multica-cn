/**
 * Promote CommonMark soft breaks to hard breaks in prose — the string-domain
 * port of web's remark-breaks plugin (packages/ui/markdown/Markdown.tsx).
 *
 * CommonMark folds a single `\n` inside a paragraph into a space, and md4c
 * (react-native-enriched-markdown's native parser) is spec-compliant with no
 * `breaks` option on its prop surface (`md4cFlags` only carries underline /
 * latexMath). Web never folds because remark-breaks upgrades every
 * paragraph-internal soft break to a hard one — so multi-line comments and
 * numbered agent output collapse into one merged line on mobile only
 * (RUYI-235). The upgrade form is `"  \n"`, the canonical hard-break syntax
 * preprocess.ts already uses for `<br>`.
 *
 * Scope: applied to prose segments only (split-markdown keeps top-level
 * code / table / mermaid out of the prose buffer). Fenced code that survives
 * inside prose — nested in a list or quote — is skipped by the line scanner,
 * and so are separators adjacent to a fence line: those newlines are block
 * structure, not paragraph text, exactly what remark-breaks leaves alone.
 *
 * Idempotent: lines already ending in a hard break (2+ trailing spaces or a
 * backslash) and blank-line boundaries are left untouched, so preprocess's
 * `<br>` → `"  \n"` output passes through unchanged and re-running the
 * transform is a no-op.
 */

// Hard break already present: 2+ trailing spaces/tabs, or a backslash.
const ALREADY_HARD_BREAK = /[ \t]{2,}$|\\$/;
// Fence opener/closer: 3+ backticks or tildes. Any indentation counts —
// fences nested in a list item are indented past CommonMark's 3-space limit.
const FENCE_LINE = /^(`{3,}|~{3,})/;

function isBlank(line: string): boolean {
  return line.trim().length === 0;
}

export function promoteSoftBreaks(input: string): string {
  if (!input.includes("\n")) return input;

  const lines = input.split("\n");
  const out: string[] = [];
  let inFence = false;

  for (let i = 0; i < lines.length; i++) {
    const line = lines[i]!;
    if (FENCE_LINE.test(line.trimStart())) inFence = !inFence;
    out.push(line);
    if (i === lines.length - 1) break;

    const next = lines[i + 1]!;
    if (inFence) continue;
    if (FENCE_LINE.test(line.trimStart()) || FENCE_LINE.test(next.trimStart())) {
      continue;
    }
    if (isBlank(line) || isBlank(next)) continue;
    if (ALREADY_HARD_BREAK.test(line)) continue;
    // Append to the line itself: the two spaces must precede the `\n` that
    // join() will re-insert, producing the "line  \nnext" hard-break form.
    out[out.length - 1] += "  ";
  }
  return out.join("\n");
}
