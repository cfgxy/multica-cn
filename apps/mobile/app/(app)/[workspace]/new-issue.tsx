/**
 * New issue creation modal — manual only.
 *
 * Layout follows Apple Reminders / Linear iOS / Things 3: one vertical
 * scrolling form (title → description → property chips), no sticky bottom
 * toolbar. Property chips are part of the form, not pinned above keyboard.
 * MentionSuggestionBar floats above keyboard only when the user is mid-@.
 *
 * No markdown toolbar / upload buttons in v1: mobile users creating an
 * issue rarely format markdown, and attachment upload is deferred to a
 * later release (see plan-issue-majestic-rabin.md "skip uploads").
 *
 * Mention pipeline shares `useMentionInput` with `issue/[id]/new-comment.tsx`
 * — both surfaces produce canonical `[@name](mention://type/id)` markdown
 * recognised by util.ParseMentions on the server.
 */
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  Alert,
  KeyboardAvoidingView,
  Platform,
  ScrollView,
  TextInput,
  View,
} from "react-native";
import { Stack, router, useLocalSearchParams } from "expo-router";
import { Text } from "@/components/ui/text";
import { SubmitIssueButton } from "@/components/issue/submit-issue-button";
import { CreateFormAttributeRow } from "@/components/issue/create-form-attribute-row";
import { MentionSuggestionBar } from "@/components/issue/mention-suggestion-bar";
import { AutosizeTextArea } from "@/components/ui/autosize-textarea";
import {
  MIN_BODY_INPUT_HEIGHT_PX,
  MOBILE_PLACEHOLDER_COLOR,
} from "@/components/ui/input-tokens";
import { cn } from "@/lib/utils";
import { useCreateIssue } from "@/data/mutations/issues";
import { useNewIssueDraftStore } from "@/data/stores/new-issue-draft-store";
import {
  useMentionInput,
  type UseMentionInputReturn,
} from "@/lib/use-mention-input";

export default function NewIssueModal() {
  // Route params support "create from comment" flows from
  // comment-action-sheet.tsx:
  //   - seed_content: comment body that prefills the description (quoted)
  //   - seed_actor:   comment author (rendered in the attribution line)
  //   - seed_source_issue: identifier (MUL-NN) for attribution
  //   - parent_id:    when set, the new issue is created as a sub-issue
  //                   (mobile UI shows a hint banner; backend accepts via
  //                   CreateIssueRequest.parent_issue_id)
  const {
    seed_content: seedContent,
    seed_actor: seedActor,
    seed_source_issue: seedSourceIssue,
    parent_id: parentId,
  } = useLocalSearchParams<{
    seed_content?: string;
    seed_actor?: string;
    seed_source_issue?: string;
    parent_id?: string;
  }>();

  const [title, setTitle] = useState("");
  const description = useMentionInput();
  // Attribute chips (status / priority / assignee / due date / project)
  // live in `useNewIssueDraftStore` so the new-issue-picker/* formSheet
  // routes can read and write the same values without a parent-child
  // React relationship. The store is reset on mount + on unmount so
  // re-opening the new-issue modal starts clean.
  const status = useNewIssueDraftStore((s) => s.status);
  const priority = useNewIssueDraftStore((s) => s.priority);
  const assignee = useNewIssueDraftStore((s) => s.assignee);
  const dueDate = useNewIssueDraftStore((s) => s.dueDate);
  const project = useNewIssueDraftStore((s) => s.project);
  const resetDraft = useNewIssueDraftStore((s) => s.reset);

  useEffect(() => {
    resetDraft();
    return () => {
      resetDraft();
    };
  }, [resetDraft]);

  // Prefill description once on mount when "from comment" params are set.
  // Format mirrors the canonical GitHub-style attribution quote: an
  // attribution line, a blank line, then the original body as a markdown
  // blockquote. seededRef guards against re-seeding after the user edits.
  const seededRef = useRef(false);
  useEffect(() => {
    if (seededRef.current || !seedContent) return;
    const attribution = seedSourceIssue
      ? `> From ${seedActor ?? "a comment"} on ${seedSourceIssue}`
      : `> From ${seedActor ?? "a comment"}`;
    const quoted = seedContent
      .split("\n")
      .map((line) => `> ${line}`)
      .join("\n");
    description.setText(`${attribution}\n>\n${quoted}\n\n`);
    seededRef.current = true;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [seedContent, seedActor, seedSourceIssue]);

  const isSubIssue = !!parentId;

  const createIssue = useCreateIssue();
  const isSubmitting = createIssue.isPending;

  const canSubmit = !isSubmitting && title.trim().length > 0;

  const onSubmit = useCallback(async () => {
    const trimmedTitle = title.trim();
    if (trimmedTitle.length === 0) return;
    const finalDescription = description.serialize().trim();
    try {
      await createIssue.mutateAsync({
        title: trimmedTitle,
        description: finalDescription || undefined,
        status,
        priority,
        ...(assignee
          ? { assignee_type: assignee.type, assignee_id: assignee.id }
          : {}),
        ...(dueDate ? { due_date: dueDate } : {}),
        ...(project ? { project_id: project.id } : {}),
        ...(parentId ? { parent_issue_id: parentId } : {}),
      });
      router.back();
    } catch (err) {
      Alert.alert(
        "Failed to create issue",
        err instanceof Error ? err.message : "Unknown error",
      );
    }
  }, [
    title,
    description,
    status,
    priority,
    assignee,
    dueDate,
    project,
    parentId,
    createIssue,
  ]);

  const headerRight = useMemo(() => {
    function HeaderRight() {
      return (
        <SubmitIssueButton
          disabled={!canSubmit}
          loading={isSubmitting}
          onPress={onSubmit}
        />
      );
    }
    return HeaderRight;
  }, [canSubmit, isSubmitting, onSubmit]);

  return (
    <>
      <Stack.Screen options={{ headerRight }} />
      <KeyboardAvoidingView
        className="flex-1 bg-background"
        behavior={Platform.OS === "ios" ? "padding" : undefined}
      >
        <ScrollView
          className="flex-1"
          contentContainerClassName="px-4 pt-4 pb-6 gap-4"
          keyboardShouldPersistTaps="handled"
        >
          {isSubIssue ? (
            <View className="rounded-lg bg-secondary px-3 py-2">
              <Text className="text-xs text-muted-foreground">
                Sub-issue
                {seedSourceIssue ? (
                  <Text className="text-foreground font-medium">
                    {" "}
                    of {seedSourceIssue}
                  </Text>
                ) : null}
              </Text>
            </View>
          ) : null}
          <TextInput
            value={title}
            onChangeText={setTitle}
            placeholder="Issue title"
            placeholderTextColor={MOBILE_PLACEHOLDER_COLOR}
            className="text-2xl font-semibold text-foreground py-2"
            autoFocus
            returnKeyType="next"
            editable={!isSubmitting}
          />
          <DescriptionField
            description={description}
            disabled={isSubmitting}
          />
          <CreateFormAttributeRow />
        </ScrollView>

        {/* Mention suggestions float above the keyboard only when the user
            types `@`. Self-hides via `if (!visible) return null` so it
            doesn't take space at rest. */}
        <MentionSuggestionBar {...description.suggestionBar} />
      </KeyboardAvoidingView>
    </>
  );
}

/** Description field with a focus-tinted rounded-2xl container — the
 *  same "write markdown body" treatment used by the comment modal
 *  (issue/[id]/new-comment.tsx) so the two surfaces feel consistent. */
function DescriptionField({
  description,
  disabled,
}: {
  description: UseMentionInputReturn;
  disabled: boolean;
}) {
  const [focused, setFocused] = useState(false);
  return (
    <View
      className={cn(
        "rounded-2xl border px-3",
        focused
          ? "border-primary/30 bg-secondary"
          : "border-transparent bg-secondary/40",
      )}
    >
      <AutosizeTextArea
        value={description.text}
        onChangeText={description.handlers.onChangeText}
        selection={description.selection}
        onSelectionChange={description.handlers.onSelectionChange}
        onFocus={() => setFocused(true)}
        onBlur={() => setFocused(false)}
        placeholder="Description… (type @ to mention)"
        className="py-2"
        minHeight={MIN_BODY_INPUT_HEIGHT_PX}
        editable={!disabled}
      />
    </View>
  );
}
