import React from "react";
import { render } from "@testing-library/react-native";

/**
 * RUYI-416: entering/exiting text-selection mode must REMOUNT the markdown
 * pipeline instead of flipping `selectable` on the live native views.
 *
 * Flipping in place drives Android's TextView through
 * setTextIsSelectable(false→true) on an already-laid-out view; on that path
 * the selection pipeline can come up in the "select all, no handles" state
 * instead of word selection at the press point. Remounting makes selectable
 * a creation-time property — the same steady state as reader surfaces
 * (issue description), where native long-press word selection works.
 *
 * The Markdown module is mocked with mount/unmount counters so the
 * assertions observe real React remount semantics (key change ⇒ unmount +
 * mount), not implementation details of the wrapper.
 */

// jest.mock factories are hoisted above module scope and may only reference
// out-of-scope variables whose name starts with "mock" — hence the holder.
const mockCounts = globalThis as unknown as {
  __ruyi416MdCounts?: { mounts: number; unmounts: number };
  __ruyi416MdProps?: Record<string, unknown>[];
};

jest.mock("@/lib/markdown", () => {
  const React = require("react");
  return {
    Markdown: (props: Record<string, unknown>) => {
      React.useEffect(() => {
        mockCounts.__ruyi416MdCounts!.mounts += 1;
        return () => {
          mockCounts.__ruyi416MdCounts!.unmounts += 1;
        };
      }, []);
      mockCounts.__ruyi416MdProps!.push(props);
      return null;
    },
  };
});

import { SelectableMarkdown } from "@/components/ui/selectable-markdown";

function resetCounters() {
  mockCounts.__ruyi416MdCounts = { mounts: 0, unmounts: 0 };
  mockCounts.__ruyi416MdProps = [];
}

describe("SelectableMarkdown remount contract (RUYI-416)", () => {
  beforeEach(resetCounters);

  it("remounts Markdown when selection mode flips on and off", async () => {
    const screen = await render(
      <SelectableMarkdown content="hello" selectable={false} />,
    );
    expect(mockCounts.__ruyi416MdCounts!.mounts).toBe(1);

    await screen.rerender(<SelectableMarkdown content="hello" selectable={true} />);
    expect(mockCounts.__ruyi416MdCounts!.unmounts).toBe(1);
    expect(mockCounts.__ruyi416MdCounts!.mounts).toBe(2);

    await screen.rerender(<SelectableMarkdown content="hello" selectable={false} />);
    expect(mockCounts.__ruyi416MdCounts!.unmounts).toBe(2);
    expect(mockCounts.__ruyi416MdCounts!.mounts).toBe(3);
  });

  it("does NOT remount when unrelated props change", async () => {
    const screen = await render(
      <SelectableMarkdown content="hello" selectable={false} />,
    );
    await screen.rerender(<SelectableMarkdown content="edited" selectable={false} />);
    expect(mockCounts.__ruyi416MdCounts!.mounts).toBe(1);
    expect(mockCounts.__ruyi416MdCounts!.unmounts).toBe(0);
  });

  it("passes content/attachments/compact through with the flip's target state", async () => {
    const attachments = [{ id: "att-1" }];
    const screen = await render(
      <SelectableMarkdown
        content="hello"
        attachments={attachments as never}
        selectable={false}
        compact
      />,
    );
    await screen.rerender(
      <SelectableMarkdown
        content="hello"
        attachments={attachments as never}
        selectable={true}
        compact
      />,
    );
    const last = mockCounts.__ruyi416MdProps!.at(-1)!;
    expect(last.content).toBe("hello");
    expect(last.attachments).toBe(attachments);
    expect(last.selectable).toBe(true);
    expect(last.compact).toBe(true);
  });
});
