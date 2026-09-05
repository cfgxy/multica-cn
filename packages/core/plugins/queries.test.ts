import { describe, expect, it } from "vitest";
import { marketplacePluginsOptions, pluginSurfaceLaunchOptions } from "./queries";

describe("marketplacePluginsOptions", () => {
  it("does not request the marketplace when its feature flag is off", () => {
    expect(marketplacePluginsOptions("workspace-1", false).enabled).toBe(false);
  });
});

describe("pluginSurfaceLaunchOptions", () => {
  it("uses a new cache entry when a mounted panel moves to another issue", () => {
    const issueOne = pluginSurfaceLaunchOptions(
      "workspace-1",
      "installation-1",
      "panel",
      "version-1",
      "frame-1",
      "issue-1",
    );
    const issueTwo = pluginSurfaceLaunchOptions(
      "workspace-1",
      "installation-1",
      "panel",
      "version-1",
      "frame-1",
      "issue-2",
    );

    expect(issueOne.queryKey).not.toEqual(issueTwo.queryKey);
    expect(issueOne.queryKey.at(-1)).toBe("issue-1");
    expect(issueTwo.queryKey.at(-1)).toBe("issue-2");
  });
});
