/**
 * Status + Priority filter sheet for any issue list surface — My Issues
 * (`(tabs)/my-issues.tsx`) and workspace Issues (`more/issues.tsx`) both
 * mount it with their own view-store. Mirrors the Status / Priority
 * sub-menus of web's MyIssuesHeader (packages/views/my-issues/components/
 * my-issues-header.tsx:181-250) and the analogous controls in web's
 * IssuesHeader.
 *
 * State is passed in as props rather than read from a hard-coded store so
 * the same sheet can serve both surfaces without one page's filter state
 * leaking into the other. The two view stores have identical shape; the
 * sheet doesn't care which one the caller wired up.
 *
 * Modal + backdrop layout mirrors apps/mobile/components/issue/pickers/
 * status-picker-sheet.tsx (no new sheet lib).
 */
import { Modal, Pressable, ScrollView, View } from "react-native";
import type { IssuePriority, IssueStatus } from "@multica/core/types";
import { Text } from "@/components/ui/text";
import { Button } from "@/components/ui/button";
import { StatusIcon } from "@/components/ui/status-icon";
import { PriorityIcon } from "@/components/ui/priority-icon";
import { BOARD_STATUSES, STATUS_LABEL } from "@/lib/issue-status";
import { cn } from "@/lib/utils";

const ALL_STATUSES: IssueStatus[] = [...BOARD_STATUSES, "cancelled"];

// Mirrors PRIORITY_ORDER in packages/core/issues/config/priority.ts.
const PRIORITY_ORDER: IssuePriority[] = [
  "urgent",
  "high",
  "medium",
  "low",
  "none",
];

// TODO: consolidate to apps/mobile/lib/issue-priority.ts — this label map is
// duplicated in components/inbox/detail-label.tsx, components/issue/
// attribute-row.tsx, and lib/format-activity.ts. Out of scope for this PR.
const PRIORITY_LABEL: Record<IssuePriority, string> = {
  urgent: "Urgent",
  high: "High",
  medium: "Medium",
  low: "Low",
  none: "No priority",
};

interface Props {
  visible: boolean;
  onClose: () => void;
  statusFilters: IssueStatus[];
  priorityFilters: IssuePriority[];
  onToggleStatus: (status: IssueStatus) => void;
  onTogglePriority: (priority: IssuePriority) => void;
  onClearFilters: () => void;
}

export function IssueFilterSheet({
  visible,
  onClose,
  statusFilters,
  priorityFilters,
  onToggleStatus,
  onTogglePriority,
  onClearFilters,
}: Props) {
  const hasActive =
    statusFilters.length > 0 || priorityFilters.length > 0;

  return (
    <Modal
      visible={visible}
      transparent
      animationType="fade"
      onRequestClose={onClose}
    >
      <Pressable className="flex-1 bg-black/40" onPress={onClose}>
        <View className="flex-1 items-center justify-center px-6">
          <Pressable onPress={() => {}} className="w-full max-w-sm">
            <View className="bg-popover rounded-2xl overflow-hidden">
              <View className="px-4 py-3 border-b border-border">
                <Text className="text-base font-semibold text-foreground">
                  Filter
                </Text>
              </View>

              <ScrollView className="max-h-96">
                <SectionLabel>Status</SectionLabel>
                {ALL_STATUSES.map((status) => {
                  const checked = statusFilters.includes(status);
                  return (
                    <Pressable
                      key={status}
                      onPress={() => onToggleStatus(status)}
                      className={cn(
                        "flex-row items-center gap-3 px-4 py-2.5 active:bg-secondary",
                        checked && "bg-secondary/60",
                      )}
                    >
                      <StatusIcon status={status} size={16} />
                      <Text className="flex-1 text-sm text-foreground">
                        {STATUS_LABEL[status]}
                      </Text>
                      <CheckMark checked={checked} />
                    </Pressable>
                  );
                })}

                <SectionLabel>Priority</SectionLabel>
                {PRIORITY_ORDER.map((priority) => {
                  const checked = priorityFilters.includes(priority);
                  return (
                    <Pressable
                      key={priority}
                      onPress={() => onTogglePriority(priority)}
                      className={cn(
                        "flex-row items-center gap-3 px-4 py-2.5 active:bg-secondary",
                        checked && "bg-secondary/60",
                      )}
                    >
                      <PriorityIcon priority={priority} />
                      <Text className="flex-1 text-sm text-foreground">
                        {PRIORITY_LABEL[priority]}
                      </Text>
                      <CheckMark checked={checked} />
                    </Pressable>
                  );
                })}
              </ScrollView>

              <View className="flex-row items-center gap-2 px-3 py-2.5 border-t border-border">
                <Button
                  variant="outline"
                  size="sm"
                  onPress={onClearFilters}
                  disabled={!hasActive}
                  className={cn("flex-1", !hasActive && "opacity-50")}
                >
                  Reset
                </Button>
                <Button
                  variant="default"
                  size="sm"
                  onPress={onClose}
                  className="flex-1"
                >
                  Done
                </Button>
              </View>
            </View>
          </Pressable>
        </View>
      </Pressable>
    </Modal>
  );
}

function SectionLabel({ children }: { children: string }) {
  return (
    <View className="px-4 pt-3 pb-1.5">
      <Text className="text-xs uppercase tracking-wider text-muted-foreground font-medium">
        {children}
      </Text>
    </View>
  );
}

function CheckMark({ checked }: { checked: boolean }) {
  if (!checked) return null;
  return <Text className="text-sm text-primary font-semibold">✓</Text>;
}
