// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import MermaidDiagramDom from "./mermaid-diagram.dom";

const pdfPage = {
  getViewport: () => ({ width: 100, height: 120 }),
  render: () => ({ promise: Promise.resolve() }),
};

vi.mock("mermaid", () => ({
  default: {
    initialize: vi.fn(),
    render: vi.fn().mockResolvedValue({ svg: '<svg data-testid="diagram" />' }),
  },
}));

vi.mock("pdfjs-dist", () => ({
  GlobalWorkerOptions: { workerSrc: "" },
  getDocument: () => ({
    promise: Promise.resolve({ numPages: 1, getPage: async () => pdfPage }),
    destroy: vi.fn(),
  }),
}));

const colors = {
  primaryColor: "#fff",
  primaryBorderColor: "#000",
  primaryTextColor: "#111",
  lineColor: "#333",
  fontFamily: "inherit",
};

let root: Root;
let container: HTMLDivElement;

beforeEach(() => {
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  container = document.createElement("div");
  document.body.append(container);
  root = createRoot(container);
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue({
    ok: true,
    headers: { get: () => "1" },
    arrayBuffer: async () => new ArrayBuffer(1),
  }));
});

afterEach(async () => {
  await act(async () => root.unmount());
  container.remove();
  vi.unstubAllGlobals();
});

describe("MermaidDiagramDom", () => {
  it("keeps rendered PDF canvases when loading state changes to ready", async () => {
    await act(async () => {
      root.render(<MermaidDiagramDom kind="pdf" url="/document.pdf" />);
    });

    await act(async () => {
      await Promise.resolve();
    });

    expect(container.querySelector('[role="status"]')).toBeNull();
    expect(container.querySelector("canvas")).not.toBeNull();
    expect(container.querySelector("canvas")?.getAttribute("aria-label")).toBe("Page 1");
  });

  it("renders Mermaid SVG through a React DOM update", async () => {
    await act(async () => {
      root.render(
        <MermaidDiagramDom
          kind="mermaid"
          chart="flowchart LR\n  A --> B"
          colors={colors}
          mode="inline"
        />,
      );
    });

    expect(container.querySelector('svg[data-testid="diagram"]')).not.toBeNull();
  });
});
