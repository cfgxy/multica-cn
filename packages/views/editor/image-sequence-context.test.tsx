import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  act,
  fireEvent,
  render as rtlRender,
  screen,
} from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { useState, type ReactElement } from "react";
import type { Attachment } from "@multica/core/types";
import { collectImageSequence } from "@multica/core/attachments/image-sequence";

const { downloadMock, getBaseUrlMock, toastErrorMock } = vi.hoisted(() => ({
  downloadMock: vi.fn(),
  getBaseUrlMock: vi.fn(() => ""),
  toastErrorMock: vi.fn(),
}));

vi.mock("../platform", () => ({ openExternal: vi.fn() }));

vi.mock("@multica/core/api", () => ({
  api: { getBaseUrl: getBaseUrlMock, getAttachmentTextContent: vi.fn() },
  PreviewTooLargeError: class extends Error {},
  PreviewUnsupportedError: class extends Error {},
}));

vi.mock("./use-download-attachment", () => ({
  useDownloadAttachment: () => downloadMock,
}));

vi.mock("../navigation", () => ({
  useNavigation: () => ({
    push: vi.fn(),
    replace: vi.fn(),
    back: vi.fn(),
    pathname: "/acme/issues",
    searchParams: new URLSearchParams(),
    hash: "",
    getShareableUrl: (p: string) => `https://app.example${p}`,
  }),
}));

vi.mock("./readonly-content", () => ({
  ReadonlyContent: () => null,
}));

vi.mock("sonner", () => ({ toast: { error: toastErrorMock } }));

const STRINGS: Record<string, Record<string, string>> = {
  image: {
    download: "Download",
    canvas_label: "Image canvas",
    previous: "Previous image",
    next: "Next image",
    sequence_position: "{{index}} / {{total}}",
    unavailable: "That image is no longer available — skipped it.",
    skipped_notice:
      "The image you clicked couldn't be loaded — showing the next available one.",
    unavailable_notice:
      "This image couldn't be loaded. It may have been deleted or you may no longer have access.",
    loading_notice: "Loading the image you selected — still showing the previous one.",
  },
  canvas: {
    zoom_in: "Zoom in",
    zoom_out: "Zoom out",
    zoom_fit: "Fit to view",
    zoom_actual: "Actual size",
  },
  attachment: {
    close: "Close",
    preview_unsupported: "This file type can't be previewed.",
    open_in_new_tab: "Open in new tab",
  },
};

vi.mock("../i18n", () => ({
  useT: () => ({
    t: (
      sel: (s: Record<string, Record<string, string>>) => string,
      params?: Record<string, string | number>,
    ) => {
      const raw = sel(STRINGS);
      if (!params) return raw;
      return raw.replace(/\{\{(\w+)\}\}/g, (_, k: string) => String(params[k]));
    },
  }),
}));

import {
  ImageSequenceProvider,
  useImageSequencePreview,
} from "./image-sequence-context";

function render(ui: ReactElement) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  });
  return rtlRender(<QueryClientProvider client={qc}>{ui}</QueryClientProvider>);
}

const UUID = (n: number) =>
  `${String(n).repeat(8)}-${String(n).repeat(4)}-4${String(n).repeat(3)}-8${String(n).repeat(3)}-${String(n).repeat(12)}`;

function imageAttachment(n: number): Attachment {
  const id = UUID(n);
  return {
    id,
    workspace_id: "ws-1",
    issue_id: null,
    comment_id: null,
    chat_session_id: null,
    chat_message_id: null,
    uploader_type: "member",
    uploader_id: "u-1",
    filename: `shot-${n}.png`,
    url: `https://cdn.example.test/${id}.png`,
    download_url: `https://cdn.example.test/${id}.png?Signature=s`,
    markdown_url: `https://cdn.example.test/${id}.png`,
    content_type: "image/png",
    size_bytes: 10,
    created_at: "2026-08-05T00:00:00Z",
  };
}

const THREE = [imageAttachment(1), imageAttachment(2), imageAttachment(3)];

// Each attachment lands in its own block, mirroring three comments each
// carrying one screenshot.
function sequenceOf(attachments: Attachment[]) {
  return collectImageSequence(attachments.map((a) => ({ attachments: [a] })));
}

function Opener({ openKey }: { openKey: string }) {
  const sequence = useImageSequencePreview();
  return (
    <button type="button" onClick={() => sequence.openAt(openKey)}>
      open
    </button>
  );
}

// The "X / Y" readout is the only place the modal prints a bare count.
function expectCounter(text: string) {
  expect(screen.getByText(text)).toBeInTheDocument();
}

function prevButton(): HTMLButtonElement {
  return screen.getByRole("button", { name: "Previous image" });
}
function nextButton(): HTMLButtonElement {
  return screen.getByRole("button", { name: "Next image" });
}

beforeEach(() => {
  vi.clearAllMocks();
});

// jsdom's HTMLImageElement has no `decode()`, so `useSettledImageURL` swaps the
// canvas synchronously and the decode window under test never exists. This
// installs a probe whose decode() is resolved by hand, one deferred per URL, so
// a test can inspect the panel BEFORE the target frame lands.
function installControllableDecode() {
  const pending = new Map<string, { resolve: () => void; reject: () => void }>();
  const RealImage = window.Image;
  class ProbeImage {
    #src = "";
    set src(value: string) {
      this.#src = value;
    }
    get src() {
      return this.#src;
    }
    decode(): Promise<void> {
      const url = this.#src;
      return new Promise<void>((resolve, reject) => {
        pending.set(url, { resolve: () => resolve(), reject: () => reject(new Error("decode failed")) });
      });
    }
  }
  // @ts-expect-error test double, only `src` + `decode` are exercised
  window.Image = ProbeImage;
  // Settling is async: the hook's state update rides the decode promise's
  // continuation, so the awaiting `act` has to let the microtask queue drain.
  const take = (url: string) => {
    const d = pending.get(url);
    if (!d) throw new Error(`no pending decode for ${url}`);
    pending.delete(url);
    return d;
  };
  return {
    async settle(url: string) {
      const d = take(url);
      await act(async () => {
        d.resolve();
      });
    },
    async fail(url: string) {
      const d = take(url);
      await act(async () => {
        d.reject();
      });
    },
    restore() {
      window.Image = RealImage;
    },
  };
}

// The stable endpoint the preview asks for — `stablePreviewImageURL` rewrites
// the record's expiring signature to this shape.
const stableUrl = (att: Attachment) => `/api/attachments/${att.id}/download`;

function canvasSrc(): string {
  return screen.getByRole("dialog").querySelector("img")!.getAttribute("src")!;
}

function dialogLabel(): string {
  return screen.getByRole("dialog").getAttribute("aria-label")!;
}

describe("ImageSequenceProvider", () => {
  it("opens at the clicked image's real position and reports X / Y", () => {
    render(
      <ImageSequenceProvider items={sequenceOf(THREE)}>
        <Opener openKey={THREE[1]!.id} />
      </ImageSequenceProvider>,
    );

    act(() => {
      fireEvent.click(screen.getByText("open"));
    });

    expectCounter("2 / 3");
    expect(screen.getByRole("dialog")).toHaveAttribute(
      "aria-label",
      "shot-2.png",
    );
  });

  it("walks forward and back without wrapping, disabling at each end", () => {
    render(
      <ImageSequenceProvider items={sequenceOf(THREE)}>
        <Opener openKey={THREE[0]!.id} />
      </ImageSequenceProvider>,
    );
    act(() => {
      fireEvent.click(screen.getByText("open"));
    });

    // First image: no previous.
    expectCounter("1 / 3");
    expect(prevButton()).toBeDisabled();
    expect(nextButton()).not.toBeDisabled();

    act(() => {
      fireEvent.click(nextButton());
    });
    expectCounter("2 / 3");
    expect(prevButton()).not.toBeDisabled();

    act(() => {
      fireEvent.click(nextButton());
    });
    // Last image: no next, and clicking it cannot wrap to the first.
    expectCounter("3 / 3");
    expect(nextButton()).toBeDisabled();

    act(() => {
      fireEvent.click(prevButton());
    });
    expectCounter("2 / 3");
  });

  it("moves with the left / right arrow keys", () => {
    render(
      <ImageSequenceProvider items={sequenceOf(THREE)}>
        <Opener openKey={THREE[0]!.id} />
      </ImageSequenceProvider>,
    );
    act(() => {
      fireEvent.click(screen.getByText("open"));
    });

    act(() => {
      fireEvent.keyDown(document, { key: "ArrowRight" });
    });
    expectCounter("2 / 3");

    act(() => {
      fireEvent.keyDown(document, { key: "ArrowLeft" });
    });
    expectCounter("1 / 3");

    // At the first image ArrowLeft is inert rather than wrapping.
    act(() => {
      fireEvent.keyDown(document, { key: "ArrowLeft" });
    });
    expectCounter("1 / 3");
  });

  it("freezes the sequence at open time so later images can't shift the index", () => {
    function Harness() {
      const [items, setItems] = useState(sequenceOf(THREE));
      return (
        <ImageSequenceProvider items={items}>
          <Opener openKey={THREE[2]!.id} />
          <button
            type="button"
            onClick={() =>
              setItems(sequenceOf([imageAttachment(9), ...THREE]))
            }
          >
            grow
          </button>
        </ImageSequenceProvider>
      );
    }
    render(<Harness />);

    act(() => {
      fireEvent.click(screen.getByText("open"));
    });
    expectCounter("3 / 3");

    // A comment lands while the preview is open.
    act(() => {
      fireEvent.click(screen.getByText("grow"));
    });
    expectCounter("3 / 3");
  });

  it("skips a broken image and says so", () => {
    render(
      <ImageSequenceProvider items={sequenceOf(THREE)}>
        <Opener openKey={THREE[0]!.id} />
      </ImageSequenceProvider>,
    );
    act(() => {
      fireEvent.click(screen.getByText("open"));
    });
    act(() => {
      fireEvent.click(nextButton());
    });
    expectCounter("2 / 3");

    act(() => {
      fireEvent.error(screen.getByRole("dialog").querySelector("img")!);
    });

    // Kept moving the way the reader was going, and the dead frame is now out
    // of the walk in both directions.
    expectCounter("3 / 3");
    expect(toastErrorMock).toHaveBeenCalledTimes(1);
    act(() => {
      fireEvent.click(prevButton());
    });
    expectCounter("1 / 3");
  });

  // RUYI-103 A3. Swapping the frame without saying so is exactly what reads as
  // "I clicked image 2 and the viewer opened image 3".
  it("says on the canvas that the frame shown is not the one clicked", () => {
    render(
      <ImageSequenceProvider items={sequenceOf(THREE)}>
        <Opener openKey={THREE[1]!.id} />
      </ImageSequenceProvider>,
    );
    act(() => {
      fireEvent.click(screen.getByText("open"));
    });
    expect(screen.queryByRole("status")).toBeNull();

    act(() => {
      fireEvent.error(screen.getByRole("dialog").querySelector("img")!);
    });

    expectCounter("3 / 3");
    expect(screen.getByRole("status")).toHaveTextContent(
      "showing the next available one",
    );

    // A deliberate move means the reader is looking at what they asked for
    // again — the banner must not linger over an image that loaded fine.
    act(() => {
      fireEvent.click(prevButton());
    });
    expectCounter("1 / 3");
    expect(screen.queryByRole("status")).toBeNull();
  });

  // RUYI-103 A3 / C2. When nothing loadable is left the viewer stays put
  // rather than silently substituting some other image, and says why.
  it("keeps the broken frame and explains when the whole sequence is dead", () => {
    render(
      <ImageSequenceProvider items={sequenceOf(THREE)}>
        <Opener openKey={THREE[0]!.id} />
      </ImageSequenceProvider>,
    );
    act(() => {
      fireEvent.click(screen.getByText("open"));
    });

    const img = () => screen.getByRole("dialog").querySelector("img")!;
    act(() => {
      fireEvent.error(img());
    });
    act(() => {
      fireEvent.error(img());
    });
    act(() => {
      fireEvent.error(img());
    });

    expect(screen.getByRole("status")).toHaveTextContent("couldn't be loaded");
    expect(prevButton()).toBeDisabled();
    expect(nextButton()).toBeDisabled();
  });

  // RUYI-103 B1 / B2. One click can cascade through a run of dead frames;
  // each one used to queue its own identical toast.
  it("raises one failure toast per user action, not one per dead frame", () => {
    render(
      <ImageSequenceProvider items={sequenceOf(THREE)}>
        <Opener openKey={THREE[0]!.id} />
      </ImageSequenceProvider>,
    );
    act(() => {
      fireEvent.click(screen.getByText("open"));
    });

    const img = () => screen.getByRole("dialog").querySelector("img")!;
    act(() => {
      fireEvent.error(img());
    });
    act(() => {
      fireEvent.error(img());
    });
    act(() => {
      fireEvent.error(img());
    });

    expect(toastErrorMock).toHaveBeenCalledTimes(1);
  });

  // ...but the next deliberate action is a new event and does get its own
  // toast, otherwise a reader who navigates on would never be told again.
  it("re-arms the toast after the reader moves deliberately", () => {
    const FIVE = [
      imageAttachment(1),
      imageAttachment(2),
      imageAttachment(3),
      imageAttachment(4),
      imageAttachment(5),
    ];
    render(
      <ImageSequenceProvider items={sequenceOf(FIVE)}>
        <Opener openKey={FIVE[0]!.id} />
      </ImageSequenceProvider>,
    );
    act(() => {
      fireEvent.click(screen.getByText("open"));
    });

    const img = () => screen.getByRole("dialog").querySelector("img")!;
    act(() => {
      fireEvent.error(img());
    });
    expectCounter("2 / 5");
    expect(toastErrorMock).toHaveBeenCalledTimes(1);

    act(() => {
      fireEvent.click(nextButton());
    });
    expectCounter("3 / 5");
    act(() => {
      fireEvent.error(img());
    });
    expect(toastErrorMock).toHaveBeenCalledTimes(2);
  });

  // RUYI-103 A2. `useSettledImageURL` deliberately holds the previous frame on
  // the canvas until the target decodes; the header meanwhile already reads the
  // target's filename and "x / N". Silently, that window IS "new name + old
  // picture" — the reported defect, just narrower than the expiry case.
  describe("while the next image is still decoding", () => {
    let decode: ReturnType<typeof installControllableDecode>;

    beforeEach(() => {
      decode = installControllableDecode();
    });
    afterEach(() => {
      decode.restore();
    });

    it("says the canvas is still on the previous frame until the target decodes", async () => {
      render(
        <ImageSequenceProvider items={sequenceOf(THREE)}>
          <Opener openKey={THREE[0]!.id} />
        </ImageSequenceProvider>,
      );
      act(() => {
        fireEvent.click(screen.getByText("open"));
      });

      // Opening settles synchronously — the first frame IS the target.
      expectCounter("1 / 3");
      expect(canvasSrc()).toBe(stableUrl(THREE[0]!));
      expect(screen.queryByRole("status")).toBeNull();

      act(() => {
        fireEvent.click(nextButton());
      });

      // Before the decode resolves: header has moved, canvas has not, and the
      // banner is what keeps the two from contradicting each other.
      expectCounter("2 / 3");
      expect(dialogLabel()).toBe("shot-2.png");
      expect(canvasSrc()).toBe(stableUrl(THREE[0]!));
      expect(screen.getByRole("status")).toHaveTextContent(
        "still showing the previous one",
      );

      await decode.settle(stableUrl(THREE[1]!));

      // After: identity, title, counter and notice all agree again.
      expectCounter("2 / 3");
      expect(dialogLabel()).toBe("shot-2.png");
      expect(canvasSrc()).toBe(stableUrl(THREE[1]!));
      expect(screen.queryByRole("status")).toBeNull();
    });

    it("replaces the loading notice with the substitution notice when the target fails", async () => {
      render(
        <ImageSequenceProvider items={sequenceOf(THREE)}>
          <Opener openKey={THREE[0]!.id} />
        </ImageSequenceProvider>,
      );
      act(() => {
        fireEvent.click(screen.getByText("open"));
      });
      act(() => {
        fireEvent.click(nextButton());
      });
      expect(screen.getByRole("status")).toHaveTextContent(
        "still showing the previous one",
      );

      // The frame the reader asked for is gone: the viewer skips on, and the
      // banner must switch from "loading" to "this is not what you clicked"
      // rather than staying on a promise that will never resolve.
      await decode.fail(stableUrl(THREE[1]!));

      expectCounter("3 / 3");
      expect(screen.getByRole("status")).toHaveTextContent(
        "showing the next available one",
      );
      expect(toastErrorMock).toHaveBeenCalledTimes(1);
    });

    // RUYI-103 C2. A stale signature, a deleted object and a failed re-sign all
    // arrive here as a rejected decode. Whatever the cause, the canvas must not
    // end up quietly showing a different image than the header names.
    it("never leaves a stale frame under a new title when every frame fails", async () => {
      render(
        <ImageSequenceProvider items={sequenceOf(THREE)}>
          <Opener openKey={THREE[0]!.id} />
        </ImageSequenceProvider>,
      );
      act(() => {
        fireEvent.click(screen.getByText("open"));
      });
      act(() => {
        fireEvent.click(nextButton());
      });

      await decode.fail(stableUrl(THREE[1]!));
      expectCounter("3 / 3");
      await decode.fail(stableUrl(THREE[2]!));

      // Nothing loadable is left. The viewer stays put and says so; it does not
      // wander back to image 1 while the header claims image 3.
      expect(screen.getByRole("status")).toHaveTextContent("couldn't be loaded");
      expect(prevButton()).toBeDisabled();
      expect(nextButton()).toBeDisabled();
      // One user action (the click on "next") — one toast, however many frames
      // died behind it.
      expect(toastErrorMock).toHaveBeenCalledTimes(1);
    });
  });

  it("reports false for an image the surface does not know", () => {
    const seen: boolean[] = [];
    function Probe() {
      const sequence = useImageSequencePreview();
      return (
        <button
          type="button"
          onClick={() => seen.push(sequence.openAt("https://cdn/unknown.png"))}
        >
          try
        </button>
      );
    }
    render(
      <ImageSequenceProvider items={sequenceOf(THREE)}>
        <Probe />
      </ImageSequenceProvider>,
    );
    act(() => {
      fireEvent.click(screen.getByText("try"));
    });
    expect(seen).toEqual([false]);
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("leaves a lone image with no sequence chrome", () => {
    render(
      <ImageSequenceProvider items={sequenceOf([THREE[0]!])}>
        <Opener openKey={THREE[0]!.id} />
      </ImageSequenceProvider>,
    );
    act(() => {
      fireEvent.click(screen.getByText("open"));
    });
    expect(screen.getByRole("dialog")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Next image" })).toBeNull();
  });
});

describe("useImageSequencePreview without a provider", () => {
  it("reports false so the caller can fall back to a single preview", () => {
    const seen: boolean[] = [];
    function Probe() {
      const sequence = useImageSequencePreview();
      return (
        <button type="button" onClick={() => seen.push(sequence.openAt("k"))}>
          try
        </button>
      );
    }
    render(<Probe />);
    act(() => {
      fireEvent.click(screen.getByText("try"));
    });
    expect(seen).toEqual([false]);
  });
});
