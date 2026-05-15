/**
 * iOS pageSheet for viewing agent runs on the current issue. Uses the
 * shared `<SheetShell>` primitive (components/ui/sheet-shell.tsx) which
 * encapsulates pageSheet + safe-area + close-button.
 *
 * See `apps/mobile/CLAUDE.md` Lesson #6 for the rationale on choosing
 * pageSheet over the project's older transparent-fade Modal pattern.
 *
 * Two sections: Active (queued/dispatched/running, created_at desc) and
 * Past (failed → cancelled → completed, completed_at desc within each).
 * Empty sections hide entirely.
 *
 * Past-row tap is a no-op in v1 — transcript drilldown is deferred.
 */
import { useMemo } from "react";
import { ScrollView, View } from "react-native";
import type { AgentTask } from "@multica/core/types";
import { Text } from "@/components/ui/text";
import { SheetShell } from "@/components/ui/sheet-shell";
import { RunRow } from "./run-row";

interface Props {
  visible: boolean;
  onClose: () => void;
  issueId: string;
  activeTasks: AgentTask[];
  pastTasks: AgentTask[];
}

const PAST_STATUS_ORDER: Record<AgentTask["status"], number> = {
  failed: 0,
  cancelled: 1,
  completed: 2,
  // Active statuses don't appear in the Past section but the type demands
  // exhaustive keys; assign anything beyond the terminal three.
  queued: 99,
  dispatched: 99,
  running: 99,
};

export function RunsSheet({
  visible,
  onClose,
  issueId,
  activeTasks,
  pastTasks,
}: Props) {
  const active = useMemo(
    () =>
      [...activeTasks].sort((a, b) =>
        (b.created_at ?? "").localeCompare(a.created_at ?? ""),
      ),
    [activeTasks],
  );

  const past = useMemo(() => {
    return [...pastTasks].sort((a, b) => {
      const ord = PAST_STATUS_ORDER[a.status] - PAST_STATUS_ORDER[b.status];
      if (ord !== 0) return ord;
      // Within a status group: newest completion first.
      return (b.completed_at ?? "").localeCompare(a.completed_at ?? "");
    });
  }, [pastTasks]);

  return (
    <SheetShell visible={visible} onClose={onClose} title="Agent Runs">
      <ScrollView showsVerticalScrollIndicator={false}>
        <View className="px-4 gap-3 pb-4">
          {active.length > 0 ? (
            <Section title="Active">
              {active.map((task) => (
                <RunRow key={task.id} task={task} issueId={issueId} />
              ))}
            </Section>
          ) : null}
          {past.length > 0 ? (
            <Section title="Past">
              {past.map((task) => (
                <RunRow key={task.id} task={task} issueId={issueId} />
              ))}
            </Section>
          ) : null}
        </View>
      </ScrollView>
    </SheetShell>
  );
}

function Section({
  title,
  children,
}: {
  title: string;
  children: React.ReactNode;
}) {
  return (
    <View className="gap-1">
      <Text className="text-[11px] font-medium text-muted-foreground uppercase tracking-wide">
        {title}
      </Text>
      <View>{children}</View>
    </View>
  );
}
