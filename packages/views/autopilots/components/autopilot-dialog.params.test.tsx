import { useImperativeHandle, useRef, useState } from "react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderWithI18n } from "../../test/i18n";

// Regression cover for the RUYI-641 dry-run review: editing an autopilot used
// to PATCH the templates and PUT the parameter set as two separate calls, so
// "add a parameter and reference it in the title template" in one save was
// rejected against the stale set (and the reverse order broke deletions the
// same way). The save must be one PATCH whose params key appears only when
// the set actually changed, and the dedicated params-PUT must stay silent.

const mockUpdateAutopilot = vi.hoisted(() => vi.fn());
const mockReplaceParams = vi.hoisted(() => vi.fn());

vi.mock("@multica/core/hooks", () => ({ useWorkspaceId: () => "ws-test" }));
vi.mock("@multica/core/paths", () => ({ useCurrentWorkspace: () => ({ name: "Acme" }) }));

vi.mock("@multica/core/workspace/queries", () => ({
  agentListOptions: (wsId: string) => ({
    queryKey: ["agents", wsId],
    queryFn: async () => [
      {
        id: "agent-1",
        name: "Scout",
        description: "Researches things",
        archived_at: null,
        runtime_id: "runtime-1",
      },
    ],
  }),
  squadListOptions: (wsId: string) => ({
    queryKey: ["squads", wsId],
    queryFn: async () => [],
  }),
}));

vi.mock("@multica/core/projects/queries", () => ({
  projectListOptions: (wsId: string) => ({
    queryKey: ["projects", wsId],
    queryFn: async () => [{ id: "proj-1", title: "Fleet", icon: null }],
  }),
}));

vi.mock("@multica/core/autopilots/queries", () => ({
  autopilotParamsOptions: () => ({
    queryKey: ["autopilot-params"],
    queryFn: async () => ({
      params: [
        {
          id: "p-1",
          name: "city",
          param_type: "string",
          required: true,
          default_value: null,
          enum_options: null,
        },
      ],
    }),
    retry: false,
  }),
  cronPreviewOptions: (wsId: string, expr: string, tz: string) => ({
    queryKey: ["cron-preview", wsId, expr, tz],
    queryFn: async () => ({ next_runs: ["2126-07-14T01:00:00Z"] }),
    retry: false,
  }),
}));

vi.mock("@multica/core/autopilots/mutations", () => ({
  useCreateAutopilot: () => ({ mutateAsync: vi.fn() }),
  useCreateAutopilotTrigger: () => ({ mutateAsync: vi.fn().mockResolvedValue({ id: "trg-new" }) }),
  useUpdateAutopilot: () => ({ mutateAsync: mockUpdateAutopilot }),
  useUpdateAutopilotTrigger: () => ({ mutateAsync: vi.fn() }),
  useReplaceAutopilotParams: () => ({ mutateAsync: mockReplaceParams }),
}));

vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: vi.fn() } }));

vi.mock("../../editor", () => ({
  TitleEditor: ({ ref, defaultValue, placeholder, onChange, onSubmit }: any) => {
    const [value, setValue] = useState(defaultValue ?? "");
    const inputRef = useRef<HTMLInputElement>(null);
    useImperativeHandle(ref, () => ({
      getText: () => value,
      focus: () => inputRef.current?.focus(),
      focusAtCoords: () => inputRef.current?.focus(),
    }));
    return (
      <input
        ref={inputRef}
        aria-label="title"
        value={value}
        placeholder={placeholder}
        onChange={(e) => {
          setValue(e.target.value);
          onChange?.(e.target.value);
        }}
        onKeyDown={(e) => {
          if (e.key === "Enter") onSubmit?.();
        }}
      />
    );
  },
  ContentEditor: ({ placeholder }: any) => <textarea aria-label="runbook" placeholder={placeholder} />,
}));

vi.mock("../../common/actor-avatar", () => ({
  ActorAvatar: ({ actorId }: { actorId: string }) => <span data-testid="actor-avatar">{actorId}</span>,
}));

vi.mock("./subscriber-multi-select", () => ({
  SubscriberMultiSelect: () => <div data-testid="subscriber-multi-select" />,
}));

vi.mock("../../projects/components/project-picker", () => ({
  ProjectPicker: ({ triggerRender }: { triggerRender: React.ReactElement }) => (
    <div>{triggerRender}</div>
  ),
}));

vi.mock("./pickers/timezone-picker", () => ({
  TimezonePicker: ({ value }: { value: string }) => <div data-testid="timezone-picker">{value}</div>,
}));

import { AutopilotDialog } from "./autopilot-dialog";

const AUTOPILOT_ID = "ap-1";

function renderEditDialog() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return renderWithI18n(
    <QueryClientProvider client={qc}>
      <AutopilotDialog
        mode="edit"
        open
        onOpenChange={vi.fn()}
        autopilotId={AUTOPILOT_ID}
        initial={{
          title: "Push fleet repo to GitHub",
          description: "",
          project_id: null,
          assignee_type: "agent",
          assignee_id: "agent-1",
          execution_mode: "run_only",
          subscriber_user_ids: [],
        }}
        triggers={[]}
        collaborators={[]}
        canManageAccess={false}
      />
    </QueryClientProvider>,
  );
}

const saveButton = () => screen.getByRole("button", { name: "Save" });
// The stored "city" row loads async and the freshly added row shares its
// name-input label, so queries are findAll*.
const findParamNameInputs = () => screen.findAllByLabelText("Name (e.g. city)");

describe("AutopilotDialog params save semantics", () => {
  beforeEach(() => {
    mockUpdateAutopilot.mockReset().mockResolvedValue({ id: AUTOPILOT_ID });
    mockReplaceParams.mockReset().mockResolvedValue({});
  });

  it("sends added parameters inside the same PATCH as the templates", async () => {
    const user = userEvent.setup();
    renderEditDialog();

    // The stored set loaded before the user edits.
    const stored = await findParamNameInputs();
    expect(stored[0]!).toHaveValue("city");

    await user.click(screen.getByRole("button", { name: "Add parameter" }));
    const inputs = await findParamNameInputs();
    expect(inputs).toHaveLength(2);
    await user.type(inputs[1]!, "region");
    await user.click(saveButton());

    await waitFor(() => expect(mockUpdateAutopilot).toHaveBeenCalledTimes(1));
    const payload = mockUpdateAutopilot.mock.calls[0]?.[0];
    expect(payload.id).toBe(AUTOPILOT_ID);
    expect(payload.params).toEqual([
      { name: "city", param_type: "string", required: true },
      { name: "region", param_type: "string" },
    ]);
    // The old split call must be gone entirely — params ride with the PATCH.
    expect(mockReplaceParams).not.toHaveBeenCalled();
  });

  it("omits the params key when the set is untouched", async () => {
    const user = userEvent.setup();
    renderEditDialog();

    expect(await findParamNameInputs()).toBeTruthy();
    await user.click(saveButton());

    await waitFor(() => expect(mockUpdateAutopilot).toHaveBeenCalledTimes(1));
    const payload = mockUpdateAutopilot.mock.calls[0]?.[0];
    // Key absent = server keeps the stored set (tri-state, like event_filters).
    expect(payload.params).toBeUndefined();
    expect(mockReplaceParams).not.toHaveBeenCalled();
  });
});
