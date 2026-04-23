"use client";

import { useEffect, useId, useState } from "react";
import { useTheme } from "next-themes";

/**
 * Client-side Mermaid diagram renderer.
 *
 * Dynamic-imports the mermaid package so it's only loaded on pages that
 * actually use it (~400 KB). Re-renders when the page theme flips.
 *
 * Usage in .mdx:
 *
 *   import { Mermaid } from "@/components/mermaid";
 *
 *   <Mermaid chart={`
 *     graph LR
 *       A[User] --> B[Server]
 *   `} />
 */
export function Mermaid({ chart }: { chart: string }) {
  const reactId = useId();
  const { resolvedTheme } = useTheme();
  const [svg, setSvg] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;

    void import("mermaid").then(({ default: mermaid }) => {
      mermaid.initialize({
        startOnLoad: false,
        theme: resolvedTheme === "dark" ? "dark" : "default",
        securityLevel: "strict",
        fontFamily: "inherit",
      });

      // mermaid requires a DOM-valid id; useId returns ":r0:" which isn't.
      const domId = `mermaid-${reactId.replace(/:/g, "")}`;

      mermaid
        .render(domId, chart.trim())
        .then((result) => {
          if (!cancelled) {
            setSvg(result.svg);
            setError(null);
          }
        })
        .catch((err: unknown) => {
          if (!cancelled) {
            setError(err instanceof Error ? err.message : String(err));
            setSvg(null);
          }
        });
    });

    return () => {
      cancelled = true;
    };
  }, [chart, reactId, resolvedTheme]);

  if (error) {
    return (
      <pre className="my-4 rounded-md border border-destructive/40 bg-destructive/10 p-3 text-sm text-destructive">
        Mermaid error: {error}
      </pre>
    );
  }

  if (!svg) {
    return (
      <div className="my-4 text-sm text-muted-foreground">
        Rendering diagram…
      </div>
    );
  }

  return (
    <div
      className="my-4 flex justify-center overflow-x-auto"
      dangerouslySetInnerHTML={{ __html: svg }}
    />
  );
}
