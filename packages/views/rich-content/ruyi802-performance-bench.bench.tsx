import { createHash } from "node:crypto";
import { writeFileSync } from "node:fs";
import { availableParallelism, cpus } from "node:os";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, it, vi } from "vitest";
import { NavigationProvider } from "../navigation/context";
import type { NavigationAdapter } from "../navigation/types";

const benchmarkMode = vi.hoisted(() => ({ current: "gate" as "baseline" | "gate" }));

vi.mock("react-markdown", async (importOriginal) => {
  const actual = await importOriginal<typeof import("react-markdown")>();
  const rehypeRaw = (await import("rehype-raw")).default;

  return {
    ...actual,
    default: (props: Parameters<typeof actual.default>[0]) => {
      const baseline = benchmarkMode.current === "baseline";
      return createElement(actual.default, {
        ...props,
        rehypePlugins: baseline
          ? [rehypeRaw, ...(props.rehypePlugins ?? []).slice(1)]
          : props.rehypePlugins,
      });
    },
  };
});

vi.mock(
  "../issues/hooks/use-issue-reference-resolutions",
  async (importOriginal) => ({
    ...(await importOriginal<
      typeof import("../issues/hooks/use-issue-reference-resolutions")
    >()),
    useIssueReferenceResolutions: (references: { form: string; ref: string }[]) =>
      new Map(references.map((ref) => [`${ref.form}:${ref.ref}`, null])),
  }),
);
vi.mock("../issues/hooks", () => ({ useResolveIssueIdentifier: () => null }));
vi.mock("../i18n", () => ({
  useT: () => ({ t: () => "References" }),
  useTimeAgo: () => "just now",
}));
vi.mock("@multica/core/paths", () => ({
  useCurrentWorkspace: () => ({ id: "ws-1", issue_prefix: "RUYI" }),
  useWorkspacePaths: () => ({ issueDetail: (id: string) => `/issues/${id}` }),
  useWorkspaceSlug: () => "bench",
}));
vi.mock("@multica/core/hooks", () => ({ useWorkspaceId: () => "ws-1" }));
vi.mock("@multica/core/issue-statuses/hooks", () => ({
  useIssueStatuses: () => ({ colorOf: () => "#123456" }),
}));
vi.mock("@multica/core/api", () => ({ api: {} }));
vi.mock("../issues/components/issue-mention-card", () => ({
  IssueMentionCard: ({ fallbackLabel }: { fallbackLabel?: string }) =>
    <span>{fallbackLabel}</span>,
}));
vi.mock("../issues/components/comment-mention-card", () => ({
  CommentMentionCard: ({ label }: { label?: string }) => <span>{label}</span>,
}));
vi.mock("../projects/components/project-mention-card", () => ({
  ProjectMentionCard: ({ label }: { label?: string }) => <span>{label}</span>,
}));
vi.mock("../editor/link-hover-card", () => ({
  useLinkHover: () => ({}),
  LinkHoverCard: () => null,
}));
vi.mock("./issue-reference-footer", async (importOriginal) => {
  if (process.env.RUYI802_OMIT_REFERENCE_TAIL === "1") {
    return { IssueReferenceTail: () => null };
  }
  return await importOriginal<typeof import("./issue-reference-footer")>();
});
vi.mock("./lazy-issue-reference-tail", async (importOriginal) => {
  if (process.env.RUYI802_OMIT_REFERENCE_TAIL === "1") {
    return { LazyIssueReferenceTail: () => null };
  }
  if (process.env.RUYI802_EAGER_REFERENCE_TAIL !== "1") {
    return await importOriginal<typeof import("./lazy-issue-reference-tail")>();
  }
  const { IssueReferenceTail } = await import("./issue-reference-footer");
  return { LazyIssueReferenceTail: IssueReferenceTail };
});

import { RichContent } from "./rich-content";

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

const adapter: NavigationAdapter = {
  push: () => {},
  replace: () => {},
  back: () => {},
  pathname: "/",
  searchParams: new URLSearchParams(),
  hash: "",
  getShareableUrl: (path) => `https://app.multica.ai${path}`,
};

function renderFixture(comments: string, mode: "baseline" | "gate" = "gate"): string {
  benchmarkMode.current = mode;
  return renderToStaticMarkup(
    <NavigationProvider value={adapter}>
      <main>{comments.split("\n\u0000\n").map((content, index) => (
        <RichContent key={index} content={content} />
      ))}</main>
    </NavigationProvider>,
  );
}

describe("RUYI-802 static render benchmark", () => {
  it("writes deterministic fixture identity and raw samples", () => {
    const fixture = JSON.stringify(fixtures);
    const fixtureSha256 = createHash("sha256").update(fixture).digest("hex");
    const warmups = Number(process.env.RUYI802_WARMUPS ?? 3);
    const repetitions = Number(process.env.RUYI802_REPETITIONS ?? 12);
    const samples: {
      scenario: string;
      iteration: number;
      ms: number;
      htmlBytes: number;
    }[] = [];
    const pairedSamples: {
      scenario: string;
      iteration: number;
      first: "baseline" | "gate";
      baselineMs: number;
      gateMs: number;
      baselineHtmlBytes: number;
      gateHtmlBytes: number;
      baselineHtmlSha256: string;
      gateHtmlSha256: string;
    }[] = [];
    const compareModes = process.env.RUYI802_PAIRED_COMPARE === "1";

    for (const scenario of fixtures) {
      const input = scenario.comments.join("\n\u0000\n");
      if (compareModes) {
        for (let i = 0; i < warmups; i++) {
          const first = i % 2 === 0 ? "baseline" : "gate";
          renderFixture(input, first);
          renderFixture(input, first === "baseline" ? "gate" : "baseline");
        }
        for (let i = 0; i < repetitions; i++) {
          const first = i % 2 === 0 ? "baseline" : "gate";
          const firstStart = performance.now();
          const firstHtml = renderFixture(input, first);
          const firstMs = Number((performance.now() - firstStart).toFixed(4));
          const secondMode = first === "baseline" ? "gate" : "baseline";
          const secondStart = performance.now();
          const secondHtml = renderFixture(input, secondMode);
          const secondMs = Number((performance.now() - secondStart).toFixed(4));
          const baselineIsFirst = first === "baseline";

          pairedSamples.push({
            scenario: scenario.name,
            iteration: i + 1,
            first,
            baselineMs: baselineIsFirst ? firstMs : secondMs,
            gateMs: baselineIsFirst ? secondMs : firstMs,
            baselineHtmlBytes: Buffer.byteLength(baselineIsFirst ? firstHtml : secondHtml),
            gateHtmlBytes: Buffer.byteLength(baselineIsFirst ? secondHtml : firstHtml),
            baselineHtmlSha256: createHash("sha256")
              .update(baselineIsFirst ? firstHtml : secondHtml)
              .digest("hex"),
            gateHtmlSha256: createHash("sha256")
              .update(baselineIsFirst ? secondHtml : firstHtml)
              .digest("hex"),
          });
        }
      } else {
        for (let i = 0; i < warmups; i++) renderFixture(input);
        for (let i = 0; i < repetitions; i++) {
          const start = performance.now();
          const html = renderFixture(input);
          samples.push({
            scenario: scenario.name,
            iteration: i + 1,
            ms: Number((performance.now() - start).toFixed(4)),
            htmlBytes: Buffer.byteLength(html),
          });
        }
      }
    }
    benchmarkMode.current = "gate";

    const result = {
      fixtureSha256,
      fixtureBytes: Buffer.byteLength(fixture),
      runtime: process.version,
      host: {
        platform: process.platform,
        arch: process.arch,
        cpuModel: cpus()[0]?.model ?? "unknown",
        logicalCpuCount: availableParallelism(),
      },
      omitReferenceTail: process.env.RUYI802_OMIT_REFERENCE_TAIL === "1",
      eagerReferenceTail: process.env.RUYI802_EAGER_REFERENCE_TAIL === "1",
      warmups,
      repetitions,
      paired: compareModes,
      ...(compareModes ? { pairedSamples } : { samples }),
    };
    const output = process.env.RUYI802_BENCH_OUT;
    if (!output) throw new Error("RUYI802_BENCH_OUT is required");
    writeFileSync(output, `${JSON.stringify(result, null, 2)}\n`);
  }, 120_000);
});
