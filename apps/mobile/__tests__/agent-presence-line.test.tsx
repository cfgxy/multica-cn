// @ts-nocheck
import React from "react";
import { render, screen } from "@testing-library/react-native";
import { AgentPresenceLine } from "@/components/agents/agent-presence-line";

jest.mock("@/lib/use-t", () => ({
  useT: () => ({
    t: (key, options = {}) => {
      if (key === "presence.running_count") return `${options.count} running`;
      if (key === "presence.running_ratio") {
        return `${options.running}/${options.capacity} running`;
      }
      return options.defaultValue ?? key;
    },
  }),
}));

describe("AgentPresenceLine", () => {
  it("shows only the running count for an unlimited agent", () => {
    render(
      <AgentPresenceLine
        detail={{
          availability: "online",
          workload: "working",
          runningCount: 1,
          queuedCount: 0,
          capacity: 0,
        }}
      />,
    );

    expect(screen.getByText("1 running")).toBeTruthy();
    expect(screen.queryByText("1/0 running")).toBeNull();
  });
});
