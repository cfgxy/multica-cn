/**
 * Pure picker body for due-date. The caller (a formSheet route) renders the
 * Done / Clear actions in its own header area — this body only handles the
 * picker + the local draft state.
 *
 * due_date is a calendar day (date-only "YYYY-MM-DD", no time/timezone — see
 * @multica/core/issues/date and GH #3618). Mirrors web's
 * packages/views/issues/components/pickers/due-date-picker.tsx: read the stored
 * day into a local-midnight Date for the spinner, write back the picked local
 * day as a date-only string.
 *
 * Platform split (RUYI-480): `display="inline"` and the embedded-picker view
 * are iOS-only. On Android the same declarative component renders no inline
 * content at all (null) — the native dialog it presents on mount is a separate
 * window, so the formSheet body behind it stays blank, and cancelling drops
 * straight into a re-present loop (baseline behavior, verified on device).
 * Rendering the iOS-only component unconditionally therefore left Android
 * users a blank sheet. So:
 *  - iOS keeps the embedded UIDatePicker, as before.
 *  - Android renders real inline content (the drafted day + a dialog trigger)
 *    and presents the native Material dialog imperatively with display
 *    "default". The dialog auto-opens once on mount to keep the interaction
 *    chain that shipped (sheet open → calendar pops); re-opening is manual —
 *    keying the dialog to every draft change would re-pop it after each OK.
 */
import {
  useCallback,
  useEffect,
  useRef,
  useState,
  useImperativeHandle,
  forwardRef,
} from "react";
import { Platform, Pressable, View } from "react-native";
import DateTimePicker, {
  DateTimePickerAndroid,
} from "@react-native-community/datetimepicker";
import {
  toDateOnly,
  dateOnlyToLocalDate,
  formatDateOnly,
} from "@multica/core/issues/date";
import { displayLocale } from "@/lib/display-locale";
import { Text } from "@/components/ui/text";
import { useT } from "@/lib/use-t";

interface Props {
  value: string | null;
}

export interface DueDatePickerBodyHandle {
  /** Returns the currently-displayed day as a date-only "YYYY-MM-DD" string. */
  getIso: () => string;
}

function toLocalDay(value: string | null): Date {
  return dateOnlyToLocalDate(value) ?? new Date();
}

export const DueDatePickerBody = forwardRef<DueDatePickerBodyHandle, Props>(
  function DueDatePickerBody({ value }, ref) {
    const { t } = useT("issues");
    const [draft, setDraft] = useState<Date>(() => toLocalDay(value));
    const draftRef = useRef(draft);
    draftRef.current = draft;

    useEffect(() => {
      setDraft(toLocalDay(value));
    }, [value]);

    const openAndroidDialog = useCallback(() => {
      DateTimePickerAndroid.open({
        value: draftRef.current,
        mode: "date",
        display: "default",
        onChange: (_event, selected) => {
          if (selected) setDraft(selected);
        },
      });
    }, []);

    useEffect(() => {
      if (Platform.OS !== "android") return;
      openAndroidDialog();
      // Open exactly once per mount: firing on every draft change would
      // re-present the dialog right after the user confirms a pick.
      // eslint-disable-next-line react-hooks/exhaustive-deps
    }, []);

    useImperativeHandle(ref, () => ({
      getIso: () => toDateOnly(draft),
    }));

    if (Platform.OS === "android") {
      return (
        <View className="flex-1 justify-center px-4 pb-6">
          <Pressable
            testID="due-date-dialog-trigger"
            onPress={openAndroidDialog}
            className="flex-row items-center justify-between rounded-lg border border-border bg-background px-4 py-3 active:bg-secondary"
          >
            <Text className="text-base text-foreground">
              {formatDateOnly(
                toDateOnly(draft),
                { year: "numeric", month: "short", day: "numeric" },
                displayLocale(),
              )}
            </Text>
            <Text className="text-sm text-muted-foreground">
              {t("mobile.picker.change_date", "Change")}
            </Text>
          </Pressable>
        </View>
      );
    }

    return (
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
    );
  },
);
