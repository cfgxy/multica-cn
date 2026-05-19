/**
 * Due-date picker. Wraps `@react-native-community/datetimepicker` (native
 * UIDatePicker on iOS, Material spinner on Android). Two affordances:
 *   - "Done" — sends the currently displayed date as ISO 8601 / RFC 3339
 *   - "Clear" — sends null (only shown when value is set)
 *
 * Container: iOS pageSheet via shared `<SheetShell>`. UIDatePicker
 * `display="inline"` is sized for sheet-body use (Apple Calendar /
 * Reminders sheets use the same component) — the earlier centered
 * transparent card cramped it. Done / Cancel / Clear sit in the
 * `rightAction` slot of the sheet header so the body is just the picker.
 *
 * Backend (`server/internal/handler/issue.go` CreateIssue / UpdateIssue)
 * parses with `time.Parse(time.RFC3339, ...)` — strict. Mirrors web's
 * `packages/views/issues/components/pickers/due-date-picker.tsx:57` which
 * sends `d.toISOString()`.
 *
 * Note: full ISO means UTC. Users in negative or large positive offsets
 * may see a one-day shift after round-trip (e.g. local "May 14" stored as
 * "2026-05-13T16:00:00Z" for UTC+8 if backend truncates day). This
 * matches web's behavior — diverging here would break parity.
 */
import { useState, useEffect } from "react";
import { Pressable, View } from "react-native";
import DateTimePicker from "@react-native-community/datetimepicker";
import { Text } from "@/components/ui/text";
import { SheetShell } from "@/components/ui/sheet-shell";

interface Props {
  visible: boolean;
  value: string | null;
  onChange: (next: string | null) => void;
  onClose: () => void;
}

function isoToDate(iso: string | null): Date {
  if (!iso) return new Date();
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? new Date() : d;
}

function dateToIso(d: Date): string {
  return d.toISOString();
}

export function DueDatePickerSheet({
  visible,
  value,
  onChange,
  onClose,
}: Props) {
  const [draft, setDraft] = useState<Date>(() => isoToDate(value));

  // Reset draft to incoming value when sheet (re)opens.
  useEffect(() => {
    if (visible) setDraft(isoToDate(value));
  }, [visible, value]);

  const submit = () => {
    onChange(dateToIso(draft));
    onClose();
  };
  const clear = () => {
    onChange(null);
    onClose();
  };

  const headerActions = (
    <View className="flex-row items-center gap-1">
      {value ? (
        <Pressable
          onPress={clear}
          hitSlop={6}
          className="px-2 py-1 rounded-md active:bg-secondary"
        >
          <Text className="text-sm text-destructive">Clear</Text>
        </Pressable>
      ) : null}
      <Pressable
        onPress={submit}
        hitSlop={6}
        className="px-2 py-1 rounded-md active:bg-secondary"
      >
        <Text className="text-sm font-medium text-primary">Done</Text>
      </Pressable>
    </View>
  );

  return (
    <SheetShell
      visible={visible}
      onClose={onClose}
      title="Due date"
      rightAction={headerActions}
    >
      <View className="flex-1 items-center pt-2">
        <DateTimePicker
          value={draft}
          mode="date"
          display="inline"
          onChange={(_event, selected) => {
            if (selected) setDraft(selected);
          }}
        />
      </View>
    </SheetShell>
  );
}
