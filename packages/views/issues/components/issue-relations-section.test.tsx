import { describe, expect, it, vi, beforeEach } from "vitest";
import { fireEvent, render, screen, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";

import { I18nProvider } from "@multica/core/i18n/react";
import { paths, WorkspaceSlugProvider } from "@multica/core/paths";
import type { IssueRelationsResponse } from "@multica/core/types";
import { RESOURCES } from "../../test/i18n";
import { NavigationProvider, type NavigationAdapter } from "../../navigation";
import { IssueRelationsSection } from "./issue-relations-section";

const mocks = vi.hoisted(() => ({
  payload: null as IssueRelationsResponse | null,
  calls: [] as string[],
}));

vi.mock("@multica/core/issues/queries", () => ({
  issueRelationsOptions: (wsId: string, id: string) => ({
    queryKey: ["issues", wsId, "relations", id],
    queryFn: () => {
      mocks.calls.push(id);
      return Promise.resolve(mocks.payload);
    },
    refetchOnMount: "always" as const,
  }),
}));

function makeAdapter(
  overrides: Partial<NavigationAdapter> = {},
): NavigationAdapter {
  return {
    push: vi.fn(),
    replace: vi.fn(),
    back: vi.fn(),
    pathname: "/",
    searchParams: new URLSearchParams(),
    hash: "",
    getShareableUrl: (p) => p,
    ...overrides,
  };
}

function AllProviders({ children }: { children: ReactNode }) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  client.setQueryData(["workspaces", "list"], [
    { id: "ws-1", slug: "acme", name: "Acme" },
  ]);
  return (
    <I18nProvider locale="en" resources={RESOURCES}>
      <QueryClientProvider client={client}>
        <WorkspaceSlugProvider slug="acme">
          <NavigationProvider value={makeAdapter()}>{children}</NavigationProvider>
        </WorkspaceSlugProvider>
      </QueryClientProvider>
    </I18nProvider>
  );
}

const ref = (id: string, identifier: string, status: string) => ({
  id,
  identifier,
  title: `${identifier} title`,
  status,
});

function emptyRelations(): IssueRelationsResponse {
  return {
    issue_id: "issue-1",
    identifier: "MUL-1",
    revision: 1,
    parent: null,
    blocks: [],
    blocked_by: [],
    relates_to: [],
    supersedes: [],
    superseded_by: [],
  };
}

async function renderSection() {
  const result = render(<IssueRelationsSection issueId="issue-1" />, {
    wrapper: AllProviders,
  });
  // refetchOnMount:"always" fires a fetch on mount; wait for the fetch AND
  // the resulting React commit before asserting (findBy polls the DOM).
  await vi.waitFor(() => expect(mocks.calls).toContain("issue-1"));
  return result;
}

beforeEach(() => {
  mocks.calls = [];
  mocks.payload = emptyRelations();
});

describe("IssueRelationsSection", () => {
  it("hides itself entirely when all five edge groups are empty", async () => {
    const { container } = await renderSection();
    expect(container).toBeEmptyDOMElement();
  });

  it("renders only non-empty groups as collapsible rows and never duplicates the parent (it has its own section)", async () => {
    mocks.payload = {
      ...emptyRelations(),
      revision: 2,
      parent: ref("p-1", "MUL-9", "done"),
      blocks: [ref("b-1", "MUL-2", "todo")],
      blocked_by: [ref("bb-1", "MUL-3", "in_progress"), ref("bb-2", "MUL-4", "todo")],
    };

    const { container } = await renderSection();

    await screen.findByText("Relations");
    expect(screen.getByText("Blocks")).toBeTruthy();
    expect(screen.getByText("Blocked by")).toBeTruthy();
    // Empty groups don't occupy sidebar space…
    expect(screen.queryByText("Related")).toBeNull();
    expect(screen.queryByText("Supersedes")).toBeNull();
    // …and the parent keeps its dedicated section — no duplicate row here.
    expect(screen.queryByText("MUL-9")).toBeNull();

    const links = within(container).getAllByRole("link");
    expect(links).toHaveLength(3);
    expect(links[0]?.getAttribute("href")).toBe(
      paths.workspace("acme").issueDetail("b-1"),
    );
    expect(links[2]?.getAttribute("href")).toBe(
      paths.workspace("acme").issueDetail("bb-2"),
    );
  });

  it("collapses back to a bare header on toggle", async () => {
    mocks.payload = {
      ...emptyRelations(),
      blocks: [ref("b-1", "MUL-2", "todo")],
    };

    const { container } = await renderSection();

    expect(await within(container).findAllByRole("link")).toHaveLength(1);
    fireEvent.click(screen.getByText("Relations"));
    expect(within(container).queryAllByRole("link")).toHaveLength(0);
    expect(container.querySelector("button")).toBeTruthy();
  });
});
