/**
 * The shared issue-limit recovery presentation (RUYI-605 delta) — mobile's
 * counterpart of desktop's `IssueLimitUpgradeDialog`
 * (packages/views/modals/issue-limit-upgrade-dialog.tsx). These cases pin
 * the card's copy/action mapping over `resolveBillingRecovery`'s states: the
 * action set is authored by Cloud's availableActions (checkout → Upgrade
 * to Pro, portal → Open Billing Portal, purchaseSeats → View Billing;
 * empty → contact-admin copy with no action), billing-disabled beats any
 * action, and an unloadable summary falls back to billing_unavailable —
 * which keeps the View Billing action, same as desktop. The dialog is
 * dismissible, and every billing action opens the workspace
 * billing-settings page on the web (the mobile adaptation the quota-notice
 * block already uses).
 */
import React from "react";
import { Linking } from "react-native";
import {
  fireEvent,
  render,
  screen,
} from "@testing-library/react-native";

const mockOnClose = jest.fn();
let mockBillingFlag = true;
let mockSummary:
  | { availableActions: { checkout: boolean; portal: boolean; purchaseSeats: boolean } }
  | undefined;
let mockSummaryFetching = false;

jest.mock("@tanstack/react-query", () => ({
  useQuery: ({ queryKey }: { queryKey: string[] }) => {
    switch (queryKey[0]) {
      case "config":
        return {
          data: {
            feature_flags: { billing_workspace_subscriptions: mockBillingFlag },
          },
          isFetching: false,
        };
      case "workspace-subscriptions":
        return { data: mockSummary, isFetching: mockSummaryFetching, error: null };
      default:
        throw new Error(`Unexpected query key: ${queryKey[0]}`);
    }
  },
}));

// Option factories only — the query layer itself is the useQuery mock
// above, so no queryFn (and no @/data/api → AsyncStorage import chain).
jest.mock("@/data/queries/billing", () => ({
  appConfigOptions: () => ({ queryKey: ["config"], enabled: false }),
  workspaceSubscriptionSummaryOptions: () => ({
    queryKey: ["workspace-subscriptions", "ws-1", "summary"],
    enabled: false,
  }),
}));

jest.mock("@/data/workspace-store", () => {
  const { create } = jest.requireActual<typeof import("zustand")>("zustand");
  return {
    useWorkspaceStore: create(() => ({
      currentWorkspaceId: "ws-1",
      currentWorkspaceSlug: "ws",
    })),
  };
});

jest.mock("@/lib/use-t", () => ({
  useT: () => ({
    t: (_key: string, fallback?: string) => fallback ?? _key,
  }),
}));

jest.mock("@/components/ui/text", () => {
  const { Text } = jest.requireActual<typeof import("react-native")>(
    "react-native",
  );
  return { Text };
});

jest.mock("@/components/ui/button", () => {
  const { Pressable } = jest.requireActual<typeof import("react-native")>(
    "react-native",
  );
  return {
    Button: (props: React.ComponentProps<typeof Pressable>) => (
      <Pressable {...props} />
    ),
  };
});

import { IssueLimitRecoveryCard } from "@/components/billing/issue-limit-recovery";

const BILLING_URL = "https://web.example/ws/settings?tab=billing";

function mockOpenURL() {
  return jest
    .spyOn(Linking, "openURL")
    .mockImplementation(() => Promise.resolve());
}

beforeEach(() => {
  jest.clearAllMocks();
  mockBillingFlag = true;
  mockSummary = undefined;
  mockSummaryFetching = false;
  process.env.EXPO_PUBLIC_WEB_URL = "https://web.example";
});

afterEach(() => {
  delete process.env.EXPO_PUBLIC_WEB_URL;
});

describe("IssueLimitRecoveryCard — availableActions branches", () => {
  it("checkout authorizes the upgrade action and opens the web billing tab", async () => {
    mockSummary = { availableActions: { checkout: true, portal: false, purchaseSeats: false } };
    const openURL = mockOpenURL();
    await render(<IssueLimitRecoveryCard onClose={mockOnClose} />);

    expect(
      screen.getByText("This workspace has reached its issue limit"),
    ).toBeTruthy();
    expect(
      screen.getByText("Upgrade to Pro to keep creating issues."),
    ).toBeTruthy();
    fireEvent.press(screen.getByText("Upgrade to Pro"));
    expect(openURL).toHaveBeenCalledWith(BILLING_URL);
    expect(mockOnClose).toHaveBeenCalled();
  });

  it("portal authorizes the portal action with the same web billing hand-off", async () => {
    mockSummary = { availableActions: { checkout: false, portal: true, purchaseSeats: false } };
    const openURL = mockOpenURL();
    await render(<IssueLimitRecoveryCard onClose={mockOnClose} />);

    expect(
      screen.getByText(
        "Open Billing Portal to manage this workspace's subscription and restore issue creation.",
      ),
    ).toBeTruthy();
    fireEvent.press(screen.getByText("Open Billing Portal"));
    expect(openURL).toHaveBeenCalledWith(BILLING_URL);
    expect(mockOnClose).toHaveBeenCalled();
  });

  it("purchaseSeats authorizes the View Billing action", async () => {
    mockSummary = { availableActions: { checkout: false, portal: false, purchaseSeats: true } };
    await render(<IssueLimitRecoveryCard onClose={mockOnClose} />);

    expect(
      screen.getByText(
        "Open Billing to see the upgrade options available for this workspace.",
      ),
    ).toBeTruthy();
    expect(screen.getByText("View Billing")).toBeTruthy();
  });

  it("an explicit empty action set renders the contact-admin copy with no action", async () => {
    mockSummary = { availableActions: { checkout: false, portal: false, purchaseSeats: false } };
    await render(<IssueLimitRecoveryCard onClose={mockOnClose} />);

    expect(
      screen.getByText("Ask a workspace owner or admin to upgrade to Pro."),
    ).toBeTruthy();
    expect(screen.queryByText("Upgrade to Pro")).toBeNull();
    expect(screen.queryByText("Open Billing Portal")).toBeNull();
    expect(screen.queryByText("View Billing")).toBeNull();
  });
});

describe("IssueLimitRecoveryCard — state gating and fallbacks", () => {
  it("billing-disabled beats any availableActions copy and drops the action", async () => {
    mockBillingFlag = false;
    mockSummary = { availableActions: { checkout: true, portal: true, purchaseSeats: true } };
    await render(<IssueLimitRecoveryCard onClose={mockOnClose} />);

    expect(
      screen.getByText(
        "Delete an existing issue to free space, or contact your workspace administrator.",
      ),
    ).toBeTruthy();
    expect(screen.queryByText("Upgrade to Pro")).toBeNull();
  });

  it("an unloadable summary falls back to billing_unavailable and keeps View Billing", async () => {
    mockSummary = undefined;
    mockSummaryFetching = false;
    await render(<IssueLimitRecoveryCard onClose={mockOnClose} />);

    expect(
      screen.getByText(
        "Billing actions could not be loaded. Open Billing to try again.",
      ),
    ).toBeTruthy();
    expect(screen.getByText("View Billing")).toBeTruthy();
  });

  it("an in-flight summary renders the checking copy with no action yet", async () => {
    mockSummary = undefined;
    mockSummaryFetching = true;
    await render(<IssueLimitRecoveryCard onClose={mockOnClose} />);

    expect(
      screen.getByText(
        "Checking the billing actions available for this workspace…",
      ),
    ).toBeTruthy();
    expect(screen.queryByText("View Billing")).toBeNull();
  });

  it("drops the billing action when no web URL is configured, keeping the dialog dismissible", async () => {
    delete process.env.EXPO_PUBLIC_WEB_URL;
    mockSummary = { availableActions: { checkout: true, portal: false, purchaseSeats: false } };
    const openURL = mockOpenURL();
    await render(<IssueLimitRecoveryCard onClose={mockOnClose} />);

    expect(
      screen.getByText("Upgrade to Pro to keep creating issues."),
    ).toBeTruthy();
    expect(screen.queryByText("Upgrade to Pro")).toBeNull();
    fireEvent.press(screen.getByText("Close"));
    expect(openURL).not.toHaveBeenCalled();
    expect(mockOnClose).toHaveBeenCalled();
  });

  it("Close dismisses without opening any URL", async () => {
    mockSummary = { availableActions: { checkout: true, portal: false, purchaseSeats: false } };
    const openURL = mockOpenURL();
    await render(<IssueLimitRecoveryCard onClose={mockOnClose} />);

    fireEvent.press(screen.getByText("Close"));
    expect(mockOnClose).toHaveBeenCalled();
    expect(openURL).not.toHaveBeenCalled();
  });
});
