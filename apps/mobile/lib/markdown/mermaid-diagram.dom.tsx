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
import * as pdfjs from "pdfjs-dist";
import { buildMermaidConfig, type MermaidThemeColors } from "./mermaid-config";

export type MermaidViewMode = "inline" | "full";

const MAX_PDF_BYTES = 20 * 1024 * 1024;
const MAX_PDF_PAGES = 100;

interface MermaidDiagramDomProps {
  kind?: "mermaid";
  /** Raw mermaid chart source (untrusted — see security contract above). */
  chart: string;
  /** Theme tokens resolved by the RN host from `lib/theme.ts`. */
  colors: MermaidThemeColors;
  mode: MermaidViewMode;
}

interface PdfDocumentDomProps {
  kind: "pdf";
  url: string;
}

export type ReaderDocumentDomProps = MermaidDiagramDomProps | PdfDocumentDomProps;

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

export default function MermaidDiagramDom(props: ReaderDocumentDomProps) {
  const hostRef = useRef<HTMLDivElement>(null);
  const [error, setError] = useState<string | null>(null);
  const [diagramSvg, setDiagramSvg] = useState("");
  const [pdfState, setPdfState] = useState<"loading" | "ready" | "error" | "large">("loading");
  const kind = props.kind ?? "mermaid";
  const url = props.kind === "pdf" ? props.url : "";
  const chart = props.kind === "pdf" ? "" : props.chart;
  const colors = props.kind === "pdf" ? null : props.colors;
  const mode = props.kind === "pdf" ? null : props.mode;

  useEffect(() => {
    let cancelled = false;
    let loadingTask: ReturnType<typeof pdfjs.getDocument> | null = null;
    const controller = new AbortController();
    if (kind === "pdf") setPdfState("loading");

    async function render() {
      try {
        if (kind === "pdf") {
          const response = await fetch(url, { signal: controller.signal });
          if (!response.ok) throw new Error("PDF request failed");
          const declaredLength = Number(response.headers.get("content-length"));
          if (Number.isFinite(declaredLength) && declaredLength > MAX_PDF_BYTES) {
            if (!cancelled) setPdfState("large");
            return;
          }
          const bytes = new Uint8Array(await response.arrayBuffer());
          if (bytes.byteLength > MAX_PDF_BYTES) {
            if (!cancelled) setPdfState("large");
            return;
          }

          pdfjs.GlobalWorkerOptions.workerSrc = new URL(
            "pdfjs-dist/build/pdf.worker.min.mjs",
            import.meta.url,
          ).toString();
          loadingTask = pdfjs.getDocument({ data: bytes });
          const document = await loadingTask.promise;
          if (document.numPages > MAX_PDF_PAGES) {
            if (!cancelled) setPdfState("large");
            return;
          }
          if (cancelled || !hostRef.current) return;
          hostRef.current.replaceChildren();
          for (let pageNumber = 1; pageNumber <= document.numPages; pageNumber += 1) {
            const page = await document.getPage(pageNumber);
            if (cancelled || !hostRef.current) return;
            const natural = page.getViewport({ scale: 1 });
            const scale = Math.min(1.5, window.innerWidth / natural.width);
            const viewport = page.getViewport({ scale });
            const canvas = window.document.createElement("canvas");
            canvas.width = Math.ceil(viewport.width);
            canvas.height = Math.ceil(viewport.height);
            canvas.setAttribute("aria-label", `Page ${pageNumber}`);
            canvas.style.cssText = "display:block;width:100%;height:auto;margin:0 auto 12px;";
            hostRef.current.append(canvas);
            await page.render({ canvas, viewport }).promise;
          }
          if (!cancelled) setPdfState("ready");
          return;
        }

        const diagramId = `mmd-dom-${++renderSeq}`;
        mermaid.initialize(buildMermaidConfig(colors!));
        const { svg } = await mermaid.render(diagramId, chart);
        if (cancelled || !hostRef.current) return;
        setError(null);
        // `svg` is pure SVG markup under the strict + htmlLabels:false config
        // above. Rendering it through React lets Expo DOM observe the updated
        // content size and resize its native WebView host.
        setDiagramSvg(svg);
      } catch (err) {
        if (!cancelled) {
          if (kind === "pdf") setPdfState("error");
          else setError(err instanceof Error ? err.message : String(err));
        }
      }
    }

    void render();

    return () => {
      cancelled = true;
      controller.abort();
      void loadingTask?.destroy();
    };
  }, [
    kind,
    url,
    chart,
    colors,
    mode,
  ]);

  if (kind === "pdf") {
    return (
      <div style={{ minHeight: "100%", padding: 12, background: "#f3f4f6" }}>
        <div ref={hostRef} />
        {pdfState === "loading" ? <p role="status">Loading PDF…</p> : null}
        {pdfState === "error" ? <p role="alert">PDF preview is unavailable. Download the file to continue.</p> : null}
        {pdfState === "large" ? <p role="alert">This PDF is too large to preview. Download the file to continue.</p> : null}
      </div>
    );
  }

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
        <div className="wrap" ref={hostRef} dangerouslySetInnerHTML={{ __html: diagramSvg }} />
      )}
    </>
  );
}
