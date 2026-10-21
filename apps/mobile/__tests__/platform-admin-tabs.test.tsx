import { fireEvent, render } from "@testing-library/react-native";
import { PlatformAdminTabs } from "@/components/management/platform-admin-tabs";

jest.mock("@/components/ui/text", () => {
  const { Text } = jest.requireActual("react-native");
  return { Text };
});

const tabs = [
  { value: "users", label: "USERS" },
  { value: "workspaces", label: "WORKSPACES" },
  { value: "mcp", label: "MCP" },
  { value: "oauth", label: "OAUTH" },
  { value: "file_read_policy", label: "File policy" },
  { value: "audit", label: "Audit" },
] as const;

it("keeps the horizontal tab strip content-sized and selectable", async () => {
  const onSelect = jest.fn();
  const screen = await render(
    <PlatformAdminTabs tabs={tabs} selected="users" onSelect={onSelect} />,
  );

  const tabStrip = screen.getByTestId("platform-admin-tabs");
  expect(tabStrip.props.horizontal).toBe(true);
  expect(tabStrip.props.style).toEqual({ flexGrow: 0, flexShrink: 0 });
  expect(screen.getAllByRole("tab")).toHaveLength(6);
  expect(screen.getByRole("tab", { name: "USERS" }).props.accessibilityState).toEqual({ selected: true });

  fireEvent.press(screen.getByRole("tab", { name: "Audit" }));

  expect(onSelect).toHaveBeenCalledWith("audit");
});
