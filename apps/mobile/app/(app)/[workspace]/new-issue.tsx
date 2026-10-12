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
  // Two this-visit mode overrides, neither touching the remembered lastMode:
  // RUYI-605 — one-shot seed from the inbox quick-create outcome detail
  // ("edit in the full form", mirroring web's create-issue registry entry
  // that opens the manual form without changing the mode preference);
  // RUYI-624 — Smart forced while a pre-navigation assign-work actor seed is
  // in effect. The two enter from different screens, but if both seeds are
  // ever present the inbox prefill wins and forces Manual: its payload is a
  // full description only the manual panel can render, and its explicit
  // setAssignee below subsumes RUYI-624's manual-landing actor carry (that
  // carry itself guards on `!assignee`). The first explicit tab switch
  // clears both flags and lastMode takes over.
  const [prefill, setPrefill] = useState<QuickCreateEditSeed | null>(null);
  const [manualModeOverride, setManualModeOverride] = useState(false);
  const prefillTakenRef = useRef(false);
  const [smartForced, setSmartForced] = useState(false);
  const effectiveMode = manualModeOverride
    ? "manual"
    : smartForced
      ? "smart"
      : lastMode;

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
    // Precedence (see above): a taken prefill displaces the assign-work
    // smart force for this visit — a smartForced left set by the mount
    // effect would linger dormant behind the manual override.
    setSmartForced(false);
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

  const handleModeChange = (v: string) => {
    // RUYI-624: web `switchToManual` parity. On a seeded (assign-work)
    // visit, landing on Manual — after the CLI version gate blocked Smart
    // or by explicit tap — carries the actor into the manual assignee slot
    // when the user hasn't picked one (a landed memory backfill counts as
    // picked). Unseeded visits stay memory-backfill-only: smartActor there
    // is the panel's first-visible fallback, not an assignment intent.
    // Going through setAssignee bumps assigneeVersion, so a still-pending
    // RUYI-79 memory read can't replace the seed after the fact.
    if (v === "manual" && smartForced) {
      const { smartActor, assignee, setAssignee } =
        useNewIssueDraftStore.getState();
      if (smartActor && !assignee) {
        setAssignee({ type: smartActor.type, id: smartActor.id });
      }
    }
    // First explicit tab switch ends both this-visit overrides; the
    // just-recorded lastMode takes over.
    setSmartForced(false);
    setManualModeOverride(false);
    setLastMode(v as "smart" | "manual");
  };

  return (
    <View className="flex-1 bg-background">
      {/* Mode switch — sticky above both mode bodies so the user can flip
          without scrolling. Keyboard avoidance stays inside each panel
          (behavior="padding" twice would double-offset). */}
      <View className="px-4 pt-3 pb-1">
        <Tabs value={effectiveMode} onValueChange={handleModeChange}>
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
      {effectiveMode === "smart" ? (
        <QuickCreatePanel />
      ) : (
        <ManualCreatePanel
          // The seed lands via a REMOUNT, not a prop update: when lastMode is
          // already "manual" the panel mounts on the first render, where the
          // prefill state is still null, and useMentionInput's mount-only
          // useState(initialText) would ignore the later prop. Keying on the
          // taken seed re-creates the panel in the same pre-paint commit with
          // the seed as its initial description; seedless visits keep the
          // stable "blank" key and never remount.
          key={prefill ? "prefilled" : "blank"}
          initialDescription={prefill?.description}
        />
      )}
    </View>
  );
}
