// @ts-nocheck
import React from "react";
import { Linking } from "react-native";
import { fireEvent, render, screen } from "@testing-library/react-native";
import IntegrationsPage from "@/app/(app)/[workspace]/more/integrations";
import WebOnlyGuidePage from "@/app/(app)/[workspace]/more/web-only/[feature]";

const mockQueryResults = new Map();

jest.mock("expo-router", () => ({
  Stack: { Screen: () => null },
  useLocalSearchParams: () => ({ feature: "billing" }),
}));

jest.mock("@tanstack/react-query", () => ({
  useQuery: (options) => mockQueryResults.get(JSON.stringify(options.queryKey)),
}));

jest.mock("@/data/queries/integrations", () => ({
  larkInstallationsOptions: (wsId) => ({ queryKey: ["integrations", wsId ?? "_", "installations", "lark"] }),
  slackInstallationsOptions: (wsId) => ({ queryKey: ["integrations", wsId ?? "_", "installations", "slack"] }),
  dingTalkInstallationsOptions: (wsId) => ({ queryKey: ["integrations", wsId ?? "_", "installations", "dingtalk"] }),
  wecomInstallationsOptions: (wsId) => ({ queryKey: ["integrations", wsId ?? "_", "installations", "wecom"] }),
  telegramInstallationsOptions: (wsId) => ({ queryKey: ["integrations", wsId ?? "_", "installations", "telegram"] }),
}));

jest.mock("@/data/queries/composio", () => ({
  composioConnectionsOptions: () => ({ queryKey: ["composio", "connections"] }),
}));

jest.mock("@/data/server-store", () => ({ getWebUrl: () => "http://dev1-web.example/" }));
jest.mock("@/data/workspace-store", () => ({
  useWorkspaceStore: (selector) => selector({ currentWorkspaceId: "ws-1", currentWorkspaceSlug: "acme" }),
}));
jest.mock("@/lib/use-t", () => ({ useT: () => ({ t: (_key, fallback) => fallback }) }));
jest.mock("@/components/ui/text", () => {
  const { Text } = jest.requireActual("react-native");
  return { Text };
});

const queryKey = (provider) => JSON.stringify(["integrations", "ws-1", "installations", provider]);
const resolvedQuery = (data) => ({ data, isLoading: false, isError: false, isRefetching: false, refetch: jest.fn() });

beforeEach(() => {
  jest.clearAllMocks();
  mockQueryResults.clear();
  for (const provider of ["lark", "slack", "dingtalk", "wecom", "telegram"]) {
    mockQueryResults.set(queryKey(provider), resolvedQuery({ installations: [] }));
  }
  mockQueryResults.set(JSON.stringify(["composio", "connections"]), resolvedQuery([]));
});

describe("mobile integration pages", () => {
  it("does not present a failed Composio request as an empty connection list", async () => {
    mockQueryResults.set(JSON.stringify(["composio", "connections"]), {
      data: undefined,
      isLoading: false,
      isError: true,
      isRefetching: false,
      refetch: jest.fn(),
    });

    await render(<IntegrationsPage />);

    expect(screen.getAllByText("Could not load. Tap to retry.")).toHaveLength(1);
    expect(screen.getAllByText("No connected accounts")).toHaveLength(5);
  });

  it("does not show empty states while connection lists are loading", async () => {
    for (const provider of ["lark", "slack", "dingtalk", "wecom", "telegram"]) {
      mockQueryResults.set(queryKey(provider), {
        data: undefined,
        isLoading: true,
        isError: false,
        isRefetching: false,
        refetch: jest.fn(),
      });
    }
    mockQueryResults.set(JSON.stringify(["composio", "connections"]), {
      data: undefined,
      isLoading: true,
      isError: false,
      isRefetching: false,
      refetch: jest.fn(),
    });

    await render(<IntegrationsPage />);

    expect(screen.queryByText("No connected accounts")).toBeNull();
  });

  it("opens web-only billing on the currently selected server", async () => {
    const openURL = jest.spyOn(Linking, "openURL").mockResolvedValue();
    await render(<WebOnlyGuidePage />);

    fireEvent.press(screen.getByRole("link", { name: "Open web app" }));

    expect(openURL).toHaveBeenCalledWith("http://dev1-web.example/acme/billing");
    openURL.mockRestore();
  });

  it("opens integration settings on the currently selected server", async () => {
    const openURL = jest.spyOn(Linking, "openURL").mockResolvedValue();
    await render(<IntegrationsPage />);

    fireEvent.press(screen.getByRole("link", { name: "Open integration settings on web" }));

    expect(openURL).toHaveBeenCalledWith("http://dev1-web.example/acme/settings?tab=integrations");
    openURL.mockRestore();
  });
});
