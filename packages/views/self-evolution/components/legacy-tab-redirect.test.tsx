// @vitest-environment jsdom

import { describe, expect, it, vi } from "vitest";
import { render, waitFor } from "@testing-library/react";
import { WorkspaceSlugProvider } from "@multica/core/paths";
import { NavigationProvider, type NavigationAdapter } from "../../navigation";
import { LegacySelfEvolutionTabRedirect } from "./legacy-tab-redirect";

function mount(destination: "versions" | "quiz" | "proposals", search: string, hash = "") {
  const replace = vi.fn();
  const adapter: NavigationAdapter = {
    push: vi.fn(),
    replace,
    back: vi.fn(),
    pathname: `/acme/self-evolution/${destination}`,
    searchParams: new URLSearchParams(search),
    hash,
    getShareableUrl: (path) => path,
  };
  render(
    <WorkspaceSlugProvider slug="acme">
      <NavigationProvider value={adapter}>
        <LegacySelfEvolutionTabRedirect destination={destination} />
      </NavigationProvider>
    </WorkspaceSlugProvider>,
  );
  return replace;
}

describe("LegacySelfEvolutionTabRedirect", () => {
  it.each([
    ["versions", "/acme/self-evolution/quality?days=90&tab=versions"],
    ["quiz", "/acme/self-evolution/quality?run=run-1&tab=quiz"],
    ["proposals", "/acme/self-evolution/clause-governance?run=run-1&tab=proposals"],
  ] as const)("replaces the legacy %s URL and preserves other query values", async (destination, expected) => {
    const replace = mount(destination, destination === "versions" ? "days=90" : "run=run-1");
    await waitFor(() => expect(replace).toHaveBeenCalledWith(expected));
  });

  it("preserves a URL fragment while replacing the legacy route", async () => {
    const replace = mount("quiz", "days=90", "#quality-status-scoring");
    await waitFor(() => expect(replace).toHaveBeenCalledWith(
      "/acme/self-evolution/quality?days=90&tab=quiz#quality-status-scoring",
    ));
  });
});
