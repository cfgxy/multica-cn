import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { act } from "react";
import type { StorageAdapter } from "@multica/core/types";
import {
  buildBuiltInServer,
  createServerStore,
  registerServerStore,
} from "@multica/core/servers";

const translations = {
  sidebar: {
    servers_label: "Servers",
    manage_servers: "Manage servers",
  },
  server: {
    built_in: "Built-in",
  },
};

vi.mock("@multica/views/i18n", () => ({
  useT: () => ({
    t: (selector: (resources: typeof translations) => string) =>
      selector(translations),
  }),
}));

// The group renders Base UI menu parts; plain wrappers keep the test
// focused on content and store routing (same approach as the sidebar test).
vi.mock("@multica/ui/components/ui/dropdown-menu", () => ({
  DropdownMenuGroup: ({ children, ...rest }: { children: React.ReactNode } & Record<string, unknown>) => (
    <div data-testid={rest["data-testid"] as string | undefined}>{children}</div>
  ),
  DropdownMenuItem: ({
    children,
    onClick,
  }: {
    children: React.ReactNode;
    onClick?: () => void;
  }) => (
    <button type="button" onClick={onClick}>
      {children}
    </button>
  ),
  DropdownMenuLabel: ({ children }: { children: React.ReactNode }) => (
    <div>{children}</div>
  ),
}));

import { useServerSwitcherStore } from "../stores/server-switcher-store";
import { ServerSwitcherGroup } from "./server-switcher-group";

const BUILT_IN = buildBuiltInServer("https://api.multica.example", undefined);

function makeStorage(initial: Record<string, string> = {}): StorageAdapter {
  const data = { ...initial };
  return {
    getItem: (k: string) => data[k] ?? null,
    setItem: (k: string, v: string) => {
      data[k] = v;
    },
    removeItem: (k: string) => {
      delete data[k];
    },
  };
}

function freshStore(initial: Record<string, string> = {}) {
  const storage = makeStorage(initial);
  const store = createServerStore({ storage, builtIn: BUILT_IN });
  registerServerStore(store);
  store.getState().hydrate();
  return store;
}

function resetSwitcherStore() {
  useServerSwitcherStore.setState({
    manageOpen: false,
    pendingSwitch: null,
    editingServerId: null,
  });
}

describe("ServerSwitcherGroup", () => {
  beforeEach(() => {
    resetSwitcherStore();
  });

  it("lists servers with the built-in badge and a manage entry", () => {
    const store = freshStore();
    store.getState().addServer({ name: "Home lab", apiUrl: "https://b.example.com", webUrl: null });

    render(<ServerSwitcherGroup />);

    expect(screen.getByText("Servers")).toBeInTheDocument();
    expect(screen.getByText(BUILT_IN.name)).toBeInTheDocument();
    expect(screen.getByText("Home lab")).toBeInTheDocument();
    expect(screen.getByText("Built-in")).toBeInTheDocument();
    expect(screen.getByText("Manage servers")).toBeInTheDocument();
    // The active entry (built-in, freshly hydrated) shows the check mark.
    expect(document.querySelector("svg.text-primary")).not.toBeNull();
  });

  it("routes clicks through the switcher store, never a direct switch", () => {
    freshStore();
    render(<ServerSwitcherGroup />);

    act(() => {
      screen.getByText("Manage servers").closest("button")!.click();
    });
    expect(useServerSwitcherStore.getState().manageOpen).toBe(true);
    expect(useServerSwitcherStore.getState().editingServerId).toBeNull();
  });

  it("renders nothing before hydration", () => {
    registerServerStore(
      createServerStore({ storage: makeStorage(), builtIn: BUILT_IN }),
    );
    const { container } = render(<ServerSwitcherGroup />);
    expect(container).toBeEmptyDOMElement();
  });
});
