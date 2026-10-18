// @vitest-environment node
import { createHash } from "node:crypto";
import { writeFileSync } from "node:fs";
import { describe, it } from "vitest";
import {
  dedupeIssueReferences,
  extractIssueReferences,
} from "@multica/core/markdown";

const UUID = (n: number) =>
  `00000000-0000-4000-8000-${String(n).padStart(12, "0")}`;
const fixtures = [
  ...[25, 100, 500].map((count) => ({
    name: `plain_${count}`,
    comments: Array.from({ length: count }, (_, i) =>
      `Comment ${i + 1}: stable text with **bold** and a short sentence.`,
    ),
  })),
  {
    name: "same_reference_100",
    comments: Array.from({ length: 100 }, (_, i) =>
      `Comment ${i + 1}: [RUYI-42](mention://issue/${UUID(42)})`,
    ),
  },
  {
    name: "different_references_100",
    comments: Array.from({ length: 100 }, (_, i) =>
      `Comment ${i + 1}: [RUYI-${i + 1}](mention://issue/${UUID(i + 1)})`,
    ),
  },
  {
    name: "markdown_blockquote_100",
    comments: Array.from({ length: 100 }, (_, i) =>
      `Comment ${i + 1}: **formatted** text\n\n> Quoted sentence ${i + 1}.\n\n- first item\n- second item`,
    ),
  },
];

describe("RUYI-802 reference extraction benchmark", () => {
  it("writes deterministic fixture identity and raw samples", () => {
    const fixture = JSON.stringify(fixtures);
    const fixtureSha256 = createHash("sha256").update(fixture).digest("hex");
    const warmups = Number(process.env.RUYI802_WARMUPS ?? 8);
    const repetitions = Number(process.env.RUYI802_REPETITIONS ?? 30);
    const samples: { scenario: string; iteration: number; ms: number; references: number }[] = [];

    for (const scenario of fixtures) {
      for (let i = 0; i < warmups; i++) {
        for (const content of scenario.comments) {
          dedupeIssueReferences(extractIssueReferences(content, { workspacePrefix: "RUYI" }));
        }
      }
      for (let i = 0; i < repetitions; i++) {
        const start = performance.now();
        let referenceCount = 0;
        for (const content of scenario.comments) {
          referenceCount += dedupeIssueReferences(
            extractIssueReferences(content, { workspacePrefix: "RUYI" }),
          ).length;
        }
        samples.push({
          scenario: scenario.name,
          iteration: i + 1,
          ms: Number((performance.now() - start).toFixed(4)),
          references: referenceCount,
        });
      }
    }

    const output = process.env.RUYI802_BENCH_OUT;
    if (!output) throw new Error("RUYI802_BENCH_OUT is required");
    writeFileSync(output, JSON.stringify({ fixtureSha256, warmups, repetitions, samples }, null, 2) + "\n");
  }, 120_000);
});
