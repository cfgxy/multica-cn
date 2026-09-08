/**
 * Line-level diff for the prompt apply confirmation (RUYI-100).
 *
 * Written here rather than pulled in as a dependency: the only diff this
 * product renders is two prompt texts side by side, and a prompt is short
 * enough that a plain LCS table is the right amount of machinery.
 *
 * The guard below matters more than the algorithm. An LCS table is O(n*m) in
 * both time and memory, and a prompt has no enforced upper length — a pair of
 * very long texts would freeze the tab that is asking the user to confirm an
 * overwrite. Past the cap the diff degrades to "everything was replaced",
 * which is honest and still lets the user see both texts.
 */

export type PromptDiffKind = "context" | "added" | "removed";

export interface PromptDiffLine {
  kind: PromptDiffKind;
  text: string;
  /** 1-based line number in the current text; null on an added line. */
  currentLine: number | null;
  /** 1-based line number in the incoming text; null on a removed line. */
  incomingLine: number | null;
}

export interface PromptDiffResult {
  lines: PromptDiffLine[];
  added: number;
  removed: number;
  /** True when the texts were too large to compare line by line. */
  truncated: boolean;
}

/**
 * Above this many lines on either side the LCS table is abandoned. 4000 lines
 * against 4000 lines is a 16M-cell table — already far past any prompt a human
 * wrote, and the point where the browser starts to feel it.
 */
const MAX_DIFF_LINES = 4000;

function splitLines(text: string): string[] {
  if (text === "") return [];
  // Normalise CRLF: a prompt that made a round trip through a Windows editor
  // must not read as "every line changed".
  return text.replace(/\r\n/g, "\n").split("\n");
}

export function diffPromptText(
  current: string,
  incoming: string,
): PromptDiffResult {
  const a = splitLines(current);
  const b = splitLines(incoming);

  if (a.length > MAX_DIFF_LINES || b.length > MAX_DIFF_LINES) {
    const lines: PromptDiffLine[] = [
      ...a.map((text, index) => ({
        kind: "removed" as const,
        text,
        currentLine: index + 1,
        incomingLine: null,
      })),
      ...b.map((text, index) => ({
        kind: "added" as const,
        text,
        currentLine: null,
        incomingLine: index + 1,
      })),
    ];
    return { lines, added: b.length, removed: a.length, truncated: true };
  }

  // lcs[i][j] = length of the longest common subsequence of a[i:] and b[j:].
  const lcs: number[][] = Array.from({ length: a.length + 1 }, () =>
    new Array<number>(b.length + 1).fill(0),
  );
  for (let i = a.length - 1; i >= 0; i--) {
    for (let j = b.length - 1; j >= 0; j--) {
      lcs[i]![j] =
        a[i] === b[j]
          ? lcs[i + 1]![j + 1]! + 1
          : Math.max(lcs[i + 1]![j]!, lcs[i]![j + 1]!);
    }
  }

  const lines: PromptDiffLine[] = [];
  let added = 0;
  let removed = 0;
  let i = 0;
  let j = 0;
  while (i < a.length && j < b.length) {
    if (a[i] === b[j]) {
      lines.push({
        kind: "context",
        text: a[i]!,
        currentLine: i + 1,
        incomingLine: j + 1,
      });
      i++;
      j++;
    } else if (lcs[i + 1]![j]! >= lcs[i]![j + 1]!) {
      lines.push({
        kind: "removed",
        text: a[i]!,
        currentLine: i + 1,
        incomingLine: null,
      });
      removed++;
      i++;
    } else {
      lines.push({
        kind: "added",
        text: b[j]!,
        currentLine: null,
        incomingLine: j + 1,
      });
      added++;
      j++;
    }
  }
  for (; i < a.length; i++) {
    lines.push({
      kind: "removed",
      text: a[i]!,
      currentLine: i + 1,
      incomingLine: null,
    });
    removed++;
  }
  for (; j < b.length; j++) {
    lines.push({
      kind: "added",
      text: b[j]!,
      currentLine: null,
      incomingLine: j + 1,
    });
    added++;
  }

  return { lines, added, removed, truncated: false };
}
