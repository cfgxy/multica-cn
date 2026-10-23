import type { DOMProps } from "expo/dom";
import MermaidDiagramDom, { type ReaderDocumentDomProps } from "./mermaid-diagram.dom";

type DomPdfProps = Extract<ReaderDocumentDomProps, { kind: "pdf" }> & { dom?: DOMProps };

export function PdfDocument({ url }: { url: string }) {
  const props: DomPdfProps = {
    kind: "pdf",
    url,
    dom: {
      useExpoDOMWebView: true,
      matchContents: true,
      style: { width: "100%" },
    },
  };
  return <MermaidDiagramDom {...props} />;
}
