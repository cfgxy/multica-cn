// @vitest-environment jsdom

import { act, render, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { RichContentScrollRootProvider } from "./scroll-root";
import { IssueReferenceLayoutProvider } from "./issue-reference-layout";

vi.mock("./issue-reference-footer", () => ({
  useIssueReferences: (content: string) => content ? [{ form: "identifier", ref: content }] : [],
  IssueReferenceFooter: ({ references }: { references: { ref: string }[] }) => (
    <div data-reference-content="">{references[0]?.ref}</div>
  ),
}));

vi.mock("@multica/core/paths", () => ({
  useCurrentWorkspace: () => ({ id: "ws", issue_prefix: "RUYI" }),
}));

import { LazyIssueReferenceTail } from "./lazy-issue-reference-tail";

let observers: { callback: IntersectionObserverCallback; disconnect: ReturnType<typeof vi.fn> }[];

describe("LazyIssueReferenceTail", () => {
  beforeEach(() => {
    vi.unstubAllGlobals();
    observers = [];
    vi.stubGlobal("scrollBy", vi.fn());
    class Observer {
      disconnect = vi.fn();
      constructor(readonly callback: IntersectionObserverCallback) {
        observers.push(this);
      }
      observe() {}
      unobserve() {}
      takeRecords() { return []; }
    }
    vi.stubGlobal("IntersectionObserver", Observer);
  });

  it("mounts near the viewport and unmounts after leaving the prefetch range", () => {
    const { container } = render(<LazyIssueReferenceTail content="RUYI-1" />);
    const shell = container.querySelector("[data-issue-reference-tail-shell]")!;
    expect(container.querySelector("[data-reference-content]")).toBeNull();
    expect(observers).toHaveLength(1);
    expect(observers[0]!.callback).toBeTypeOf("function");

    act(() => {
      observers[0]!.callback(
        [{ isIntersecting: true, target: shell } as unknown as IntersectionObserverEntry],
        {} as IntersectionObserver,
      );
    });
    expect(container.querySelector("[data-reference-content]")?.textContent).toBe("RUYI-1");

    act(() => {
      observers[0]!.callback(
        [{ isIntersecting: false, target: shell } as unknown as IntersectionObserverEntry],
        {} as IntersectionObserver,
      );
    });
    expect(container.querySelector("[data-reference-content]")).toBeNull();
  });

  it("preserves measured space when a footer leaves the viewport", () => {
    let scrollTop = 900;
    vi.stubGlobal("scrollY", 900);
    const scrollBy = vi.fn((_x: number, y: number) => {
      scrollTop += y;
      vi.stubGlobal("scrollY", scrollTop);
    });
    vi.stubGlobal("scrollBy", scrollBy);
    const { container } = render(<LazyIssueReferenceTail content="RUYI-3" />);
    const shell = container.querySelector("[data-issue-reference-tail-shell]")!;
    vi.spyOn(shell, "getBoundingClientRect").mockReturnValue({
      top: -300,
      bottom: -300,
      left: 0,
      right: 0,
      width: 0,
      height: 0,
      x: 0,
      y: -300,
      toJSON: () => ({}),
    } as DOMRect);
    Object.defineProperty(shell, "scrollHeight", {
      configurable: true,
      get: () => container.querySelector("[data-reference-content]") ? 600 : Number.parseFloat((shell as HTMLElement).style.height) || 0,
    });

    act(() => {
      observers[0]!.callback(
        [{ isIntersecting: true, target: shell } as unknown as IntersectionObserverEntry],
        {} as IntersectionObserver,
      );
    });

    expect(scrollBy).toHaveBeenCalledWith(0, 600);
    expect(scrollTop).toBe(1500);
    expect(container.querySelector("[data-reference-content]")).not.toBeNull();

    act(() => {
      observers[0]!.callback(
        [{ isIntersecting: false, target: shell } as unknown as IntersectionObserverEntry],
        {} as IntersectionObserver,
      );
    });

    expect((shell as HTMLElement).style.height).toBe("600px");
    expect(scrollBy).toHaveBeenCalledTimes(1);
    expect(scrollTop).toBe(1500);
    expect(container.querySelector("[data-reference-content]")).toBeNull();
  });

  it.each([
    { rootType: "window", nativeAnchorDelta: 0 },
    { rootType: "window", nativeAnchorDelta: 200 },
    { rootType: "nested", nativeAnchorDelta: 0 },
    { rootType: "nested", nativeAnchorDelta: 200 },
  ] as const)(
    "keeps the find selection at its original top for $rootType scrolling with $nativeAnchorDelta px native anchoring",
    ({ rootType, nativeAnchorDelta }) => {
      const initialTop = 300;
      const initialScrollTop = 900;
      let scrollTop = initialScrollTop;
      let appliedNativeAnchor = false;
      const selectedText = document.createTextNode("FIND TARGET");
      const containerRef: { current: HTMLElement | null } = { current: null };
      const range = {
        startContainer: selectedText,
        cloneRange() { return this; },
        getBoundingClientRect: () => {
          const mounted = Boolean(
            containerRef.current?.querySelector("[data-reference-content]"),
          );
          if (mounted && !appliedNativeAnchor) {
            scrollTop += nativeAnchorDelta;
            appliedNativeAnchor = true;
          }
          return {
            top: initialTop + (mounted ? 100 : 0) - (scrollTop - initialScrollTop),
          };
        },
        toString: () => "FIND TARGET",
      } as unknown as Range;
      const selection = {
        isCollapsed: false,
        toString: () => "FIND TARGET",
        getRangeAt: () => range,
      } as unknown as Selection;
      vi.stubGlobal("getSelection", () => selection);

      const scrollRoot = document.createElement("div");
      Object.defineProperty(scrollRoot, "scrollTop", {
        configurable: true,
        get: () => scrollTop,
        set: (value: number) => { scrollTop = value; },
      });
      const scrollBy = vi.fn((_x: number, y: number) => {
        scrollTop += y;
        vi.stubGlobal("scrollY", scrollTop);
      });
      vi.stubGlobal("scrollY", scrollTop);
      vi.stubGlobal("scrollBy", scrollBy);

      const content = (
        <>
          <span />
          <LazyIssueReferenceTail content="RUYI-8" />
        </>
      );
      const rendered = rootType === "nested"
        ? render(
            <RichContentScrollRootProvider scrollRoot={scrollRoot}>
              {content}
            </RichContentScrollRootProvider>,
          )
        : render(content);
      containerRef.current = rendered.container;
      const { container } = rendered;
      container.querySelector("span")!.appendChild(selectedText);
      const shell = container.querySelector("[data-issue-reference-tail-shell]")!;
      vi.spyOn(shell, "getBoundingClientRect").mockReturnValue({
        top: -300,
        bottom: -300,
        left: 0,
        right: 0,
        width: 0,
        height: 0,
        x: 0,
        y: -300,
        toJSON: () => ({}),
      } as DOMRect);
      Object.defineProperty(shell, "scrollHeight", {
        configurable: true,
        get: () => container.querySelector("[data-reference-content]") ? 100 : 0,
      });

      act(() => {
        observers[0]!.callback(
          [{ isIntersecting: true, target: shell } as unknown as IntersectionObserverEntry],
          {} as IntersectionObserver,
        );
      });

      expect(range.getBoundingClientRect().top).toBe(initialTop);
      expect(scrollTop).toBe(initialScrollTop + 100);
      if (rootType === "nested") {
        expect(scrollBy).not.toHaveBeenCalled();
      } else {
        expect(scrollBy).toHaveBeenCalledWith(0, nativeAnchorDelta === 0 ? 100 : -100);
      }
    },
  );

  it("compensates the combined height of tails mounted above a shared find hit", () => {
    let scrollTop = 900;
    const scrollRoot = document.createElement("div");
    Object.defineProperty(scrollRoot, "scrollTop", {
      configurable: true,
      get: () => scrollTop,
      set: (value: number) => { scrollTop = value; },
    });
    const { container } = render(
      <RichContentScrollRootProvider scrollRoot={scrollRoot}>
        <div data-find-hit="" />
        <LazyIssueReferenceTail content="RUYI-4" />
        <LazyIssueReferenceTail content="RUYI-5" />
      </RichContentScrollRootProvider>,
    );
    const shells = [...container.querySelectorAll<HTMLElement>("[data-issue-reference-tail-shell]")];
    const findHit = container.querySelector<HTMLElement>("[data-find-hit]")!;
    vi.spyOn(findHit, "getBoundingClientRect").mockImplementation(() => ({
      top: 2000 + container.querySelectorAll("[data-reference-content]").length * 100 - scrollTop,
      bottom: 2020 + container.querySelectorAll("[data-reference-content]").length * 100 - scrollTop,
      left: 0,
      right: 20,
      width: 20,
      height: 20,
      x: 0,
      y: 2000 + container.querySelectorAll("[data-reference-content]").length * 100 - scrollTop,
      toJSON: () => ({}),
    } as DOMRect));
    for (const shell of shells) {
      vi.spyOn(shell, "getBoundingClientRect").mockReturnValue({
        top: -300,
        bottom: -300,
        left: 0,
        right: 0,
        width: 0,
        height: 0,
        x: 0,
        y: -300,
        toJSON: () => ({}),
      } as DOMRect);
    }
    for (const shell of shells) {
      Object.defineProperty(shell, "scrollHeight", {
        configurable: true,
        get: () => shell.querySelector("[data-reference-content]") ? 100 : 0,
      });
    }
    const initialHitTop = findHit.getBoundingClientRect().top;

    act(() => {
      for (const observer of observers) {
        const shell = observer === observers[0] ? shells[0]! : shells[1]!;
        observer.callback(
          [{ isIntersecting: true, target: shell } as unknown as IntersectionObserverEntry],
          {} as IntersectionObserver,
        );
      }
    });

    expect(scrollTop).toBe(1100);
    expect(findHit.getBoundingClientRect().top).toBe(initialHitTop);
  });

  it("does not double-compensate native anchoring shared by multiple tails", () => {
    let scrollTop = 900;
    const scrollRoot = document.createElement("div");
    Object.defineProperty(scrollRoot, "scrollTop", {
      configurable: true,
      get: () => scrollTop,
      set: (value: number) => { scrollTop = value; },
    });
    const { container } = render(
      <RichContentScrollRootProvider scrollRoot={scrollRoot}>
        <div data-find-hit="" />
        <LazyIssueReferenceTail content="RUYI-6" />
        <LazyIssueReferenceTail content="RUYI-7" />
      </RichContentScrollRootProvider>,
    );
    const shells = [...container.querySelectorAll<HTMLElement>("[data-issue-reference-tail-shell]")];
    const findHit = container.querySelector<HTMLElement>("[data-find-hit]")!;
    vi.spyOn(findHit, "getBoundingClientRect").mockImplementation(() => ({
      top: 2000 + container.querySelectorAll("[data-reference-content]").length * 100 - scrollTop,
      bottom: 2020 + container.querySelectorAll("[data-reference-content]").length * 100 - scrollTop,
      left: 0,
      right: 20,
      width: 20,
      height: 20,
      x: 0,
      y: 2000 + container.querySelectorAll("[data-reference-content]").length * 100 - scrollTop,
      toJSON: () => ({}),
    } as DOMRect));
    for (const shell of shells) {
      vi.spyOn(shell, "getBoundingClientRect").mockReturnValue({
        top: -300,
        bottom: -300,
        left: 0,
        right: 0,
        width: 0,
        height: 0,
        x: 0,
        y: -300,
        toJSON: () => ({}),
      } as DOMRect);
      Object.defineProperty(shell, "scrollHeight", {
        configurable: true,
        get: () => shell.querySelector("[data-reference-content]") ? 100 : 0,
      });
    }
    const initialHitTop = findHit.getBoundingClientRect().top;

    act(() => {
      for (const [index, observer] of observers.entries()) {
        observer.callback(
          [{ isIntersecting: true, target: shells[index]! } as unknown as IntersectionObserverEntry],
          {} as IntersectionObserver,
        );
      }
      // Model the browser preserving the hit after both 100px footers grow.
      scrollTop += 200;
    });

    expect(scrollTop).toBe(1100);
    expect(findHit.getBoundingClientRect().top).toBe(initialHitTop);
  });

  it("reuses Activity measurements after virtual row remount, but not after content, width or page changes", () => {
    let show = true;
    let content = "RUYI-10";
    let page = "page-a";
    let width = 640;
    const view = () => (
      <IssueReferenceLayoutProvider key={page}>
        {show ? <LazyIssueReferenceTail content={content} /> : null}
      </IssueReferenceLayoutProvider>
    );
    const { container, rerender } = render(view());
    let shell = container.querySelector<HTMLElement>("[data-issue-reference-tail-shell]")!;
    Object.defineProperty(shell, "clientWidth", { configurable: true, get: () => width });
    Object.defineProperty(shell, "scrollHeight", { configurable: true, get: () => 120 });
    act(() => observers.at(-1)!.callback(
      [{ isIntersecting: true, target: shell } as unknown as IntersectionObserverEntry], {} as IntersectionObserver,
    ));
    show = false;
    rerender(view());
    const widthSpy = vi.spyOn(HTMLElement.prototype, "clientWidth", "get").mockImplementation(() => width);
    show = true;
    rerender(view());
    shell = container.querySelector<HTMLElement>("[data-issue-reference-tail-shell]")!;
    expect(shell.style.height).toBe("120px");
    expect(shell.querySelector("[data-reference-content]")).toBeNull();

    content = "RUYI-11";
    rerender(view());
    expect(shell.style.height).toBe("");
    expect(shell.querySelector("[data-issue-reference-placeholder]")).not.toBeNull();
    content = "RUYI-10";
    rerender(view());
    expect(shell.style.height).toBe("120px");
    show = false;
    rerender(view());
    width = 320;
    show = true;
    rerender(view());
    shell = container.querySelector<HTMLElement>("[data-issue-reference-tail-shell]")!;
    expect(shell.style.height).toBe("");
    width = 640;
    page = "page-b";
    rerender(view());
    shell = container.querySelector<HTMLElement>("[data-issue-reference-tail-shell]")!;
    expect(shell.style.height).toBe("");
    widthSpy.mockRestore();
  });

  it("updates the reserved space when asynchronously resolved content changes height", () => {
    const resizeCallbacks: ResizeObserverCallback[] = [];
    vi.stubGlobal("ResizeObserver", class {
      constructor(callback: ResizeObserverCallback) { resizeCallbacks.push(callback); }
      observe() {}
      disconnect() {}
    });
    const { container } = render(<LazyIssueReferenceTail content="RUYI-12" />);
    const shell = container.querySelector<HTMLElement>("[data-issue-reference-tail-shell]")!;
    let height = 80;
    Object.defineProperty(shell, "scrollHeight", { get: () => height });
    act(() => observers[0]!.callback(
      [{ isIntersecting: true, target: shell } as unknown as IntersectionObserverEntry], {} as IntersectionObserver,
    ));
    height = 140;
    act(() => resizeCallbacks.at(-1)!([], {} as ResizeObserver));
    act(() => observers[0]!.callback(
      [{ isIntersecting: false, target: shell } as unknown as IntersectionObserverEntry], {} as IntersectionObserver,
    ));
    expect(shell.style.height).toBe("140px");
  });

  it("invalidates an offscreen measurement when typography changes", async () => {
    const { container } = render(<LazyIssueReferenceTail content="RUYI-13" />);
    const shell = container.querySelector<HTMLElement>("[data-issue-reference-tail-shell]")!;
    Object.defineProperty(shell, "scrollHeight", { get: () => 80 });
    act(() => observers[0]!.callback(
      [{ isIntersecting: true, target: shell } as unknown as IntersectionObserverEntry], {} as IntersectionObserver,
    ));
    act(() => observers[0]!.callback(
      [{ isIntersecting: false, target: shell } as unknown as IntersectionObserverEntry], {} as IntersectionObserver,
    ));
    expect(shell.style.height).toBe("80px");
    document.documentElement.style.fontSize = "20px";
    await waitFor(() => expect(shell.style.height).toBe(""));
    document.documentElement.style.removeProperty("font-size");
  });

  it("does not reserve space for content without references", () => {
    const { container } = render(<LazyIssueReferenceTail content="" />);
    expect(container.querySelector("[data-issue-reference-placeholder]")).toBeNull();
  });

  it("falls back to mounting when IntersectionObserver is unavailable", async () => {
    vi.stubGlobal("IntersectionObserver", undefined);
    const { findByText } = render(<LazyIssueReferenceTail content="RUYI-2" />);
    expect(await findByText("RUYI-2")).toBeTruthy();
  });
});
