"use dom";

/**
 * Mermaid diagram DOM component (RUYI-80) — the web side of mobile's hybrid
 * markdown renderer.
 *
 * Runs inside an Expo DOM component WebView (`expo/dom`, per the path-C
 * escape hatch reserved in `apps/mobile/docs/markdown-rendering-adr.md`):
 * mermaid needs a real DOM to lay out, and native/prose paths A/B can't
 * provide one. The RN host (`mermaid-diagram.tsx`) passes the chart text and
 * resolved theme colors as marshaled props — plain JSON across the bridge,
 * never HTML interpolation — so the untrusted chart string only ever meets
 * the DOM through `mermaid.render`.
 *
 * Security contract (mirrors `packages/views/editor/mermaid-diagram.tsx`):
 *   - `securityLevel: "strict"` via `buildMermaidConfig` — strips scripts,
 *     event handlers and HTML labels from the untrusted chart before
 *     anything reaches this document.
 *   - `htmlLabels: false` — labels are SVG <text>, so the injected string is
 *     pure SVG markup.
 *   - The WebView loads only this local DOM bundle; no remote origins.
 *
 * Inline mode scales the diagram to the phone column (CSS `max-width`),
 * matching the web inline frame contract (RUYI-80). Full mode draws at
 * natural size for the fullscreen viewer, where the WebView's own scrolling
 * pans wide diagrams.
 */
import { useEffect, useRef, useState } from "react";
import mermaid from "mermaid";
import { buildMermaidConfig, type MermaidThemeColors } from "./mermaid-config";

export type MermaidViewMode = "inline" | "full";

export interface MermaidDiagramDomProps {
  /** Raw mermaid chart source (untrusted — see security contract above). */
  chart: string;
  /** Theme tokens resolved by the RN host from `lib/theme.ts`. */
  colors: MermaidThemeColors;
  mode: MermaidViewMode;
}

// Mermaid requires a document-unique id per render; a module counter is
// enough because one DOM bundle instance renders one diagram at a time.
let renderSeq = 0;

const BASE_CSS = `
  html, body { margin: 0; padding: 0; background: transparent; }
  body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto,
         "PingFang SC", "Hiragino Sans GB", "Microsoft YaHei", sans-serif; }
  .wrap { display: flex; justify-content: center; }
`;

const INLINE_CSS = `${BASE_CSS}
  .wrap svg { max-width: 100%; height: auto; }
`;

const FULL_CSS = `${BASE_CSS}
  body { overflow: auto; }
  .wrap { min-width: fit-content; padding: 8px; }
  .wrap svg { max-width: none; }
`;

const ERROR_CSS = `
  .mmd-error { color: #b91c1c; font-size: 12px; margin: 0; padding: 8px;
               word-break: break-word; }
  .mmd-error pre { font-size: 11px; white-space: pre-wrap; margin: 4px 0 0;
                   color: #374151; }
`;

export default function MermaidDiagramDom({
  chart,
  colors,
  mode,
}: MermaidDiagramDomProps) {
  const hostRef = useRef<HTMLDivElement>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    const diagramId = `mmd-dom-${++renderSeq}`;

    async function renderDiagram() {
      try {
        mermaid.initialize(buildMermaidConfig(colors));
        const { svg } = await mermaid.render(diagramId, chart);
        if (cancelled || !hostRef.current) return;
        setError(null);
        // `svg` is pure SVG markup under the strict + htmlLabels:false config
        // above — the same trust level as the web renderer's srcDoc iframe.
        hostRef.current.innerHTML = svg;
      } catch (err) {
        if (!cancelled) {
          setError(err instanceof Error ? err.message : String(err));
        }
      }
    }

    void renderDiagram();

    return () => {
      cancelled = true;
    };
  }, [chart, colors, mode]);

  return (
    <>
      <style>{mode === "full" ? FULL_CSS : INLINE_CSS}</style>
      {error != null && <style>{ERROR_CSS}</style>}
      {error != null ? (
        <div className="mmd-error">
          <p>Unable to render Mermaid diagram.</p>
          <pre>{error}</pre>
        </div>
      ) : (
        <div className="wrap" ref={hostRef} />
      )}
    </>
  );
}
