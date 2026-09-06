import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import type { StorageAdapter } from "@multica/core/types";
import {
  BUILT_IN_SERVER_ID,
  buildBuiltInServer,
  createServerStore,
  registerServerStore,
  serverTokenKey,
} from "@multica/core/servers";

const mocks = vi.hoisted(() => ({
  toastError: vi.fn(),
  resetActiveServerSession: vi.fn(),
}));

// Same selector-style i18n stub as updates-settings-tab.test.tsx.
const translations = {
  server: {
    manage_title: "Manage servers",
    hint: "Select a server to connect to it.",
    built_in: "Built-in",
    add: "Add server",
    edit: "Edit",
    delete: "Delete",
    delete_title: "Delete server",
    delete_message: "Remove {{name}} from this device?",
    cancel: "Cancel",
    current: "Current",
    switch_confirm: "Switch",
    empty_title: "No other servers yet",
    empty_description: "Add a server to connect to another Multica instance.",
    form: {
      invalid_url: "Enter a valid http(s) address",
      duplicate: "This address is already saved",
      plain_http: "This address uses plain http.",
      name_label: "Name (optional)",
      name_placeholder: "e.g. Home lab",
      api_label: "Server address",
      api_placeholder: "https://api.example.com",
      advanced: "Advanced: web address",
      web_hint: "Web address",
      web_placeholder: "Leave empty to reuse the server address",
      test: "Test connection",
      testing: "Testing…",
      connected: "Connected",
      unreachable: "Couldn't reach this address.",
      save: "Save",
      saving: "Saving…",
      save_failed_message: "Could not save the server.",
      change_title: "Change server address?",
      change_message: "You'll be signed out.",
      cancel: "Cancel",
    },
  },
};

vi.mock("@multica/views/i18n", () => ({
  useT: () => ({
    t: (
      selector: (resources: typeof translations) => string,
      values?: Record<string, string>,
    ) => {
      const template = selector(translations);
      return Object.entries(values ?? {}).reduce(
        (result, [key, value]) => result.replace(`{{${key}}}`, value),
        template,
      );
    },
  }),
}));

vi.mock("sonner", () => ({ toast: { error: mocks.toastError, success: vi.fn() } }));

const authState = { user: { id: "user-1", name: "Tester", email: "t@example.com" } };
vi.mock("@multica/core/auth", () => ({
  useAuthStore: Object.assign(
    (selector: (s: typeof authState) => unknown) => selector(authState),
    { getState: () => authState },
  ),
}));

vi.mock("../platform/desktop-servers", async (importOriginal) => {
  const actual =
    await importOriginal<typeof import("../platform/desktop-servers")>();
  return {
    ...actual,
    resetActiveServerSessionStorage: mocks.resetActiveServerSession,
  };
});

const reload = vi.fn();
Object.defineProperty(window, "location", {
  value: { reload },
  writable: true,
});

import { useServerSwitcherStore } from "../stores/server-switcher-store";
import { ServerSettingsDialog } from "./server-settings-dialog";

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
  return { store, storage };
}

function renderDialog(editingServerId: string | null = null) {
  // The switcher store owns which screen (list vs form) is open.
  useServerSwitcherStore.getState().openManage(editingServerId);
  return render(<ServerSettingsDialog open onClose={vi.fn()} />);
}

function resetSwitcherStore() {
  useServerSwitcherStore.setState({
    manageOpen: false,
    pendingSwitch: null,
    editingServerId: null,
  });
}

describe("ServerSettingsDialog — list", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    reload.mockClear();
    resetSwitcherStore();
    freshStore();
  });

  it("shows the built-in entry without edit or delete affordances", () => {
    renderDialog();
    expect(screen.getByText(BUILT_IN.name)).toBeInTheDocument();
    expect(screen.getByText("Built-in")).toBeInTheDocument();
    expect(screen.queryByLabelText("Edit")).toBeNull();
    expect(screen.queryByLabelText("Delete")).toBeNull();
  });

  it("shows empty guidance when only the built-in entry exists", () => {
    renderDialog();
    expect(screen.getByTestId("server-list-empty")).toBeInTheDocument();
    expect(
      screen.getByText("Add a server to connect to another Multica instance."),
    ).toBeInTheDocument();
  });

  it("offers edit and delete for a non-active custom entry", () => {
    const { store } = freshStore();
    store.getState().addServer({ name: "Home lab", apiUrl: "https://home.example.com", webUrl: null });
    renderDialog();
    expect(screen.queryByTestId("server-list-empty")).toBeNull();
    expect(screen.getByLabelText("Edit")).toBeInTheDocument();
    expect(screen.getByLabelText("Delete")).toBeInTheDocument();
  });

  it("hides delete for the active custom entry", () => {
    const { store, storage } = freshStore();
    const entry = store.getState().addServer({ name: "Active lab", apiUrl: "https://active.example.com", webUrl: null });
    // Reflect a switch to the custom entry at the storage level, then rehydrate.
    storage.setItem(
      "multica_servers",
      JSON.stringify({
        version: 1,
        servers: [{ id: entry.id, name: entry.name, apiUrl: entry.apiUrl }],
        activeServerId: entry.id,
      }),
    );
    store.getState().hydrate();
    renderDialog();
    expect(screen.getByText("Current")).toBeInTheDocument();
    expect(screen.queryByLabelText("Delete")).toBeNull();
  });
});

describe("ServerSettingsDialog — create", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    reload.mockClear();
    resetSwitcherStore();
    freshStore();
  });

  it("blocks saving an invalid address and saves a valid one", async () => {
    const { store } = freshStore();
    renderDialog();
    fireEvent.click(screen.getByText("Add server"));

    const urlInput = await screen.findByLabelText("Server address");
    fireEvent.change(urlInput, { target: { value: "not a url" } });
    expect(screen.getByText("Enter a valid http(s) address")).toBeInTheDocument();
    expect(screen.getByText("Save")).toBeDisabled();

    fireEvent.change(urlInput, { target: { value: "https://home.example.com/" } });
    expect(screen.getByText("Save")).toBeEnabled();
    fireEvent.click(screen.getByText("Save"));

    await waitFor(() => {
      const custom = store.getState().servers.filter((s) => !s.builtIn);
      expect(custom).toHaveLength(1);
      expect(custom[0]?.apiUrl).toBe("https://home.example.com");
    });
    // A non-active entry edit never reloads the app.
    expect(reload).not.toHaveBeenCalled();
  });

  it("blocks a duplicate address", () => {
    const { store } = freshStore();
    store.getState().addServer({ name: "A", apiUrl: "https://a.example.com", webUrl: null });
    renderDialog();
    fireEvent.click(screen.getByText("Add server"));
    const urlInput = screen.getByLabelText("Server address");
    fireEvent.change(urlInput, { target: { value: "https://a.example.com" } });
    expect(screen.getByText("This address is already saved")).toBeInTheDocument();
    expect(screen.getByText("Save")).toBeDisabled();
  });
});

describe("ServerSettingsDialog — delete", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    reload.mockClear();
    resetSwitcherStore();
  });

  it("confirms, then removes the entry and its session snapshot", () => {
    const { store, storage } = freshStore({ [serverTokenKey("srv_x")]: "tok" });
    const entry = store.getState().addServer({ name: "Doomed", apiUrl: "https://doomed.example.com", webUrl: null });
    storage.setItem(serverTokenKey(entry.id), "tok");

    renderDialog();
    fireEvent.click(screen.getByLabelText("Delete"));
    // Confirm dialog names the target.
    expect(screen.getByText("Remove Doomed from this device?")).toBeInTheDocument();
    fireEvent.click(screen.getByText("Delete", { selector: "[data-slot=alert-dialog-action]" }));

    expect(storage.getItem(serverTokenKey(entry.id))).toBeNull();
    expect(store.getState().servers.map((s) => s.id)).toEqual([BUILT_IN_SERVER_ID]);
  });
});

describe("ServerSettingsDialog — active address change", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    reload.mockClear();
    resetSwitcherStore();
  });

  it("resets the session and reloads after saving an active address change", () => {
    const { store, storage } = freshStore();
    const entry = store.getState().addServer({ name: "Lab", apiUrl: "https://lab.example.com", webUrl: null });
    storage.setItem(
      "multica_servers",
      JSON.stringify({
        version: 1,
        servers: [{ id: entry.id, name: entry.name, apiUrl: entry.apiUrl }],
        activeServerId: entry.id,
      }),
    );
    store.getState().hydrate();

    renderDialog(entry.id);
    const urlInput = screen.getByLabelText("Server address");
    fireEvent.change(urlInput, { target: { value: "https://lab2.example.com" } });
    fireEvent.click(screen.getByText("Save"));
    // Signed-in save on an active address change confirms first (mobile parity).
    fireEvent.click(
      screen.getByText("Save", { selector: "[data-slot=alert-dialog-action]" }),
    );

    expect(mocks.resetActiveServerSession).toHaveBeenCalledTimes(1);
    expect(reload).toHaveBeenCalledTimes(1);
    expect(store.getState().servers.find((s) => s.id === entry.id)?.apiUrl).toBe(
      "https://lab2.example.com",
    );
  });

  it("reloads without session reset when only the name changes", () => {
    const { store, storage } = freshStore();
    const entry = store.getState().addServer({ name: "Lab", apiUrl: "https://lab.example.com", webUrl: null });
    storage.setItem(
      "multica_servers",
      JSON.stringify({
        version: 1,
        servers: [{ id: entry.id, name: entry.name, apiUrl: entry.apiUrl }],
        activeServerId: entry.id,
      }),
    );
    store.getState().hydrate();

    renderDialog(entry.id);
    const nameInput = screen.getByLabelText("Name (optional)");
    fireEvent.change(nameInput, { target: { value: "Renamed" } });
    fireEvent.click(screen.getByText("Save"));

    expect(mocks.resetActiveServerSession).not.toHaveBeenCalled();
    expect(reload).toHaveBeenCalledTimes(1);
    expect(store.getState().servers.find((s) => s.id === entry.id)?.name).toBe("Renamed");
  });
});
