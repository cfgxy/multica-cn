/**
 * New issue creation screen — smart/manual mode shell (RUYI-68).
 *
 * Web's create-issue dialog has two modes: agent quick-create (one-line
 * prompt, server derives title/description) and the manual form, with the
 * last-used mode remembered (`create-mode-store`, default "agent"). Mobile
 * mirrors that split as an in-screen segmented switch (RNR Tabs) instead of
 * two modal registry entries — a phone screen can't show both bodies at
 * once, and the switch sits where the user's thumb already is. Each mode
 * owns its header title + submit button via its own `Stack.Screen`.
 *
 * Mode preference is session-scoped on mobile (quick-create-prefs-store,
 * in-memory per the mobile store convention); web persists it to
 * localStorage — documented divergence, semantics identical within a
 * session.
 *
 * Manual mode: `ManualCreatePanel` (the original form, extracted verbatim).
 * Smart mode: `QuickCreatePanel` (web AgentCreatePanel counterpart).
 */
import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { View } from "react-native";
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Text } from "@/components/ui/text";
import { ManualCreatePanel } from "@/components/issue/manual-create-panel";
import { QuickCreatePanel } from "@/components/issue/quick-create-panel";
import type { QuickCreateEditSeed } from "@/lib/quick-create-edit";
import {
  seedDraftAssigneeFromMemory,
  useNewIssueDraftStore,
} from "@/data/stores/new-issue-draft-store";
import { takeNewIssuePrefill } from "@/data/stores/new-issue-prefill-store";
import { useQuickCreatePrefsStore } from "@/data/stores/quick-create-prefs-store";
import { useWorkspaceStore } from "@/data/workspace-store";
import { useServerStore } from "@/data/server-store";
import { useT } from "@/lib/use-t";

export default function NewIssueModal() {
  const lastMode = useQuickCreatePrefsStore((s) => s.lastMode);
  const setLastMode = useQuickCreatePrefsStore((s) => s.setLastMode);
  const resetDraft = useNewIssueDraftStore((s) => s.reset);
  const { t } = useT("common");
  // RUYI-605: one-shot seed from the inbox quick-create outcome detail
  // ("edit in the full form"). Mirrors web's create-issue registry entry:
  // that open lands in the manual form (`initialMode="manual"`) without
  // touching the remembered mode preference, so the override below applies
  // to this visit only and the first tab switch falls back to `lastMode`.
  const [prefill, setPrefill] = useState<QuickCreateEditSeed | null>(null);
  const [manualModeOverride, setManualModeOverride] = useState(false);
  const prefillTakenRef = useRef(false);

  // Reset before child passive effects seed Smart actor memory. Both panels
  // still share one draft for the rest of this visit.
  useLayoutEffect(() => {
    resetDraft();
  }, [resetDraft]);

  // Consume the prefill AFTER the reset above (layout effects run in
  // declaration order) so the seed lands in a clean draft, and BEFORE paint
  // so the manual panel with the seeded description is the first frame.
  useLayoutEffect(() => {
    const taken = takeNewIssuePrefill();
    if (!taken) return;
    if (taken.agentId) {
      prefillTakenRef.current = true;
      useNewIssueDraftStore.getState().setAssignee({
        type: "agent",
        id: taken.agentId,
      });
    }
    setPrefill(taken);
    setManualModeOverride(true);
  }, []);

  useEffect(() => {
    // RUYI-79 web parity: prefill the assignee with the last one submitted
    // from this server × workspace. The version guard prevents a delayed
    // AsyncStorage read from replacing a picker choice made after this reset.
    // An inbox prefill (above) skips the memory seed — the explicit agent
    // candidate is the user's recovery path, not a remembered default.
    if (!prefillTakenRef.current) {
      const assigneeVersion = useNewIssueDraftStore.getState().assigneeVersion;
      const { activeServerId } = useServerStore.getState();
      const slug = useWorkspaceStore.getState().currentWorkspaceSlug;
      if (activeServerId && slug) {
        void seedDraftAssigneeFromMemory(activeServerId, slug, assigneeVersion);
      }
    }
    return () => {
      resetDraft();
    };
  }, [resetDraft]);

  const mode = manualModeOverride ? "manual" : lastMode;

  return (
    <View className="flex-1 bg-background">
      {/* Mode switch — sticky above both mode bodies so the user can flip
          without scrolling. Keyboard avoidance stays inside each panel
          (behavior="padding" twice would double-offset). */}
      <View className="px-4 pt-3 pb-1">
        <Tabs
          value={mode}
          onValueChange={(v) => {
            setManualModeOverride(false);
            setLastMode(v as "smart" | "manual");
          }}
        >
          <TabsList className="w-full">
            <TabsTrigger value="smart" className="flex-1">
              <Text>{t("mobile.create_issue.mode_smart", "Smart")}</Text>
            </TabsTrigger>
            <TabsTrigger value="manual" className="flex-1">
              <Text>{t("mobile.create_issue.mode_manual", "Manual")}</Text>
            </TabsTrigger>
          </TabsList>
        </Tabs>
      </View>
      {mode === "smart" ? (
        <QuickCreatePanel />
      ) : (
        <ManualCreatePanel initialDescription={prefill?.description} />
      )}
    </View>
  );
}
