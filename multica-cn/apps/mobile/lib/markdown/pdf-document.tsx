import type { DOMProps } from "expo/dom";
import MermaidDiagramDom, { type ReaderDocumentDomProps } from "./mermaid-diagram.dom";

type DomPdfProps = Extract<ReaderDocumentDomProps, { kind: "pdf" }> & { dom?: DOMProps };

export function PdfDocument({ data, url }: { data?: string; url?: string }) {
  const props: DomPdfProps = {
    kind: "pdf",
    data,
    url,
    dom: {
      useExpoDOMWebView: true,
      matchContents: true,
      style: { width: "100%" },
    },
  };
  return <MermaidDiagramDom {...props} />;
}
