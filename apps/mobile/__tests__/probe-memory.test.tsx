jest.mock("@react-native-async-storage/async-storage", () => ({
  __esModule: true,
  default: {
    getItem: jest.fn(async () => null),
    setItem: jest.fn(async () => undefined),
    removeItem: jest.fn(async () => undefined),
  },
}));

const asyncStorage = jest.requireMock("@react-native-async-storage/async-storage")
  .default as { getItem: jest.Mock };
const {
  useNewIssueDraftStore,
  useNewIssueLastAssigneeStore,
  seedDraftAssigneeFromMemory,
} = jest.requireActual<
  typeof import("@/data/stores/new-issue-draft-store")
>("@/data/stores/new-issue-draft-store");

const MEMORY_KEY = "multica_mobile_new_issue_last_assignee";

describe("probe", () => {
  it("memory seed mechanics", async () => {
    console.log("hydrated at start:", useNewIssueLastAssigneeStore.persist.hasHydrated());
    asyncStorage.getItem.mockImplementation(async (key: string) =>
      key === MEMORY_KEY
        ? JSON.stringify({
            state: {
              byServer: { "server-1": { "workspace-a": { type: "user", id: "user-1" } } },
            },
            version: 0,
          })
        : null,
    );
    const v0 = useNewIssueDraftStore.getState().assigneeVersion;
    const r = await seedDraftAssigneeFromMemory("server-1", "workspace-a", v0);
    console.log("seed result:", r);
    console.log("assignee:", useNewIssueDraftStore.getState().assignee);
    console.log("byServer:", JSON.stringify(useNewIssueLastAssigneeStore.getState().byServer));
    console.log("hydrated after:", useNewIssueLastAssigneeStore.persist.hasHydrated());
  });
});
