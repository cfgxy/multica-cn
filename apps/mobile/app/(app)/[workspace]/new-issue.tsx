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
 *
 * RUYI-624: the agent detail page's "+ Assign work" entry pre-seeds
 * `smartActor` in the draft store before pushing this screen — the web
 * counterpart opens quick-create with `initialMode="agent"` and a seeded
 * actor. The mount reset below would wipe that seed, so it is captured
 * first and re-applied after the reset; while a seed survives, Smart mode
 * is forced for this visit only (the remembered `lastMode` is untouched
 * and takes over as soon as the user touches the switch themselves).
 */
import { useEffect, useLayoutEffect, useState } from "react";
import { View } from "react-native";
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Text } from "@/components/ui/text";
import { ManualCreatePanel } from "@/components/issue/manual-create-panel";
import { QuickCreatePanel } from "@/components/issue/quick-create-panel";
import {
  seedDraftAssigneeFromMemory,
  useNewIssueDraftStore,
} from "@/data/stores/new-issue-draft-store";
import { useQuickCreatePrefsStore } from "@/data/stores/quick-create-prefs-store";
import { useWorkspaceStore } from "@/data/workspace-store";
import { useServerStore } from "@/data/server-store";
import { useT } from "@/lib/use-t";

export default function NewIssueModal() {
  const lastMode = useQuickCreatePrefsStore((s) => s.lastMode);
  const setLastMode = useQuickCreatePrefsStore((s) => s.setLastMode);
  const resetDraft = useNewIssueDraftStore((s) => s.reset);
  const { t } = useT("common");
  // RUYI-624: Smart forced only while a pre-navigation actor seed is in
  // effect; the first explicit tab touch hands control back to lastMode.
  const [smartForced, setSmartForced] = useState(false);
  const effectiveMode = smartForced ? "smart" : lastMode;

  // Reset before child passive effects seed Smart actor memory. Both panels
  // still share one draft for the rest of this visit. An actor seeded by the
  // assign-work entry survives the reset so the panel opens on that agent.
  useLayoutEffect(() => {
    const seededActor = useNewIssueDraftStore.getState().smartActor;
    resetDraft();
    if (seededActor) {
      useNewIssueDraftStore.getState().setSmartActor(seededActor);
      setSmartForced(true);
    }
  }, [resetDraft]);

  const handleModeChange = (v: string) => {
    setSmartForced(false);
    setLastMode(v as "smart" | "manual");
  };

  useEffect(() => {
    // RUYI-79 web parity: prefill the assignee with the last one submitted
    // from this server × workspace. The version guard prevents a delayed
    // AsyncStorage read from replacing a picker choice made after this reset.
    const assigneeVersion = useNewIssueDraftStore.getState().assigneeVersion;
    const { activeServerId } = useServerStore.getState();
    const slug = useWorkspaceStore.getState().currentWorkspaceSlug;
    if (activeServerId && slug) {
      void seedDraftAssigneeFromMemory(activeServerId, slug, assigneeVersion);
    }
    return () => {
      resetDraft();
    };
  }, [resetDraft]);

  return (
    <View className="flex-1 bg-background">
      {/* Mode switch — sticky above both mode bodies so the user can flip
          without scrolling. Keyboard avoidance stays inside each panel
          (behavior="padding" twice would double-offset). */}
      <View className="px-4 pt-3 pb-1">
        <Tabs
          value={effectiveMode}
          onValueChange={handleModeChange}
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
      {effectiveMode === "smart" ? <QuickCreatePanel /> : <ManualCreatePanel />}
    </View>
  );
}
