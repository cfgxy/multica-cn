import { describe, expect, it, vi } from "vitest";
import { render } from "@testing-library/react";
import { Markdown } from "@multica/ui/markdown";
import { ReadonlyContent } from "./readonly-content";

// RUYI-705: RichContent now reads the workspace prefix; null = unknown =
// fall-through, which is what these fixtures were written against.
vi.mock("@multica/core/paths", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@multica/core/paths")>()),
  useCurrentWorkspace: () => null,
}));

// Prose with two dollar amounts and `~` (approximately) markers.
const FINANCE_TEXT =
  "Revenue ≈ $120/mo gross (~$85 net of fees), 12 active subscriptions";

describe("dollar amounts in markdown", () => {
  it("Markdown renders $ amounts as plain text, not inline math", () => {
    const { container } = render(<Markdown>{FINANCE_TEXT}</Markdown>);
    expect(container.querySelector(".katex")).toBeNull();
    expect(container.textContent).toContain(
      "$120/mo gross (~$85 net of fees)",
    );
  });

  it("ReadonlyContent renders $ amounts as plain text, not inline math", () => {
    const { container } = render(<ReadonlyContent content={FINANCE_TEXT} />);
    expect(container.querySelector(".katex")).toBeNull();
    expect(container.textContent).toContain(
      "$120/mo gross (~$85 net of fees)",
    );
  });

  it("renders single-dollar inline math but leaves code and currency prose alone", () => {
    const { container } = render(
      <ReadonlyContent content={'$E=mc^2$ and `$x^2$` cost $120/mo gross (~$85 net of fees)'} />,
    );

    expect(container.querySelectorAll(".katex")).toHaveLength(1);
    expect(container.querySelector("code")?.textContent).toBe("$x^2$");
    expect(container.textContent).toContain("$120/mo gross (~$85 net of fees)");
  });

  it("still renders explicit $$ display math", () => {
    const { container } = render(<Markdown>{"$$\nE = mc^2\n$$"}</Markdown>);
    expect(container.querySelector(".katex")).not.toBeNull();
  });
});
