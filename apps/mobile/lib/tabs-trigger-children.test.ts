// @vitest-environment node
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import ts from "typescript";
import { describe, expect, it } from "vitest";

interface TriggerViolation {
  file: string;
  line: number;
}

/**
 * Native RN renders nothing for a bare string child of a non-Text host
 * component: TabsTrigger (a Pressable-backed primitive) needs its label
 * wrapped in <Text> explicitly. Web DOM tolerates raw text nodes, so the
 * defect only surfaces on device — RUYI-346 QA round 2, P1. This sweep
 * encodes that invariant as a static source check; the jest-expo lane
 * can't catch it because component tests mock TabsTrigger entirely.
 *
 * Returns 1-based line numbers of offending direct children (non-whitespace
 * JSX text or JSX expressions not wrapped in an element) per <TabsTrigger>.
 */
export function findBareTabsTriggerChildren(source: string): number[] {
  const sf = ts.createSourceFile(
    "fixture.tsx",
    source,
    ts.ScriptTarget.Latest,
    true,
    ts.ScriptKind.TSX,
  );
  const lines: number[] = [];

  const visit = (node: ts.Node): void => {
    if (
      ts.isJsxOpeningElement(node) &&
      node.tagName.getText(sf) === "TabsTrigger" &&
      ts.isJsxElement(node.parent)
    ) {
      for (const child of node.parent.children) {
        const bare =
          (ts.isJsxText(child) && child.getText(sf).trim() !== "") ||
          ts.isJsxExpression(child);
        if (bare) {
          const line =
            sf.getLineAndCharacterOfPosition(child.getStart(sf)).line + 1;
          if (!lines.includes(line)) lines.push(line);
        }
      }
    }
    ts.forEachChild(node, visit);
  };
  ts.forEachChild(sf, visit);
  return lines;
}

const SKIP_DIRS = new Set([
  "node_modules",
  ".expo",
  ".expo-shared",
  "android",
  "ios",
  ".turbo",
  "dist",
]);

export function sweepMobileTsx(mobileRoot: string): TriggerViolation[] {
  const violations: TriggerViolation[] = [];
  const walk = (dir: string): void => {
    for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
      if (entry.isDirectory()) {
        if (!SKIP_DIRS.has(entry.name)) walk(path.join(dir, entry.name));
        continue;
      }
      if (!entry.name.endsWith(".tsx")) continue;
      const file = path.join(dir, entry.name);
      for (const line of findBareTabsTriggerChildren(
        fs.readFileSync(file, "utf8"),
      )) {
        violations.push({ file: path.relative(mobileRoot, file), line });
      }
    }
  };
  walk(mobileRoot);
  return violations;
}

const MOBILE_ROOT = path.resolve(
  path.dirname(fileURLToPath(import.meta.url)),
  "..",
);

describe("TabsTrigger children (native text rendering)", () => {
  describe("findBareTabsTriggerChildren", () => {
    it("flags bare string and expression children", () => {
      expect(
        findBareTabsTriggerChildren(
          '<TabsTrigger value="a">Label</TabsTrigger>',
        ),
      ).toEqual([1]);
      expect(
        findBareTabsTriggerChildren(
          '<TabsTrigger value="a">{t("x", "Y")}</TabsTrigger>',
        ),
      ).toEqual([1]);
    });

    it("accepts <Text>-wrapped children and whitespace-only text", () => {
      expect(
        findBareTabsTriggerChildren(
          '<TabsTrigger value="a">\n  <Text>Label</Text>\n</TabsTrigger>',
        ),
      ).toEqual([]);
      expect(
        findBareTabsTriggerChildren(
          '<TabsTrigger value="a">\n  <Text>{t("x", "Y")}</Text>\n</TabsTrigger>',
        ),
      ).toEqual([]);
    });

    it("accepts self-closing triggers without children", () => {
      expect(findBareTabsTriggerChildren('<TabsTrigger value="a" />')).toEqual(
        [],
      );
    });
  });

  it("finds zero bare-string TabsTrigger children across apps/mobile", () => {
    expect(sweepMobileTsx(MOBILE_ROOT)).toEqual([]);
  });
});
