/**
 * Manual create panel — the original new-issue form body, extracted
 * verbatim when the screen gained the smart-mode switch (RUYI-68). Layout
 * follows Apple Reminders / Linear iOS / Things 3: one vertical scrolling
 * form (title → description → property chips), no sticky bottom toolbar.
 *
 * Attachments live in the shared AttachmentZone above the description text.
 * Uploads never write Markdown into the description; submit binds completed
 * server ids directly and blocks while any upload is in flight.
 *
 * Mention pipeline shares `useMentionInput` with `issue/[id]/new-comment.tsx`
 * — both surfaces produce canonical `[@name](mention://type/id)` markdown
 * recognised by util.ParseMentions on the server.
 */
import { useCallback, useState } from "react";
import { Alert, ScrollView, TextInput } from "react-native";
import { Stack, router } from "expo-router";
import { SubmitIssueButton } from "@/components/issue/submit-issue-button";
import { CreateFormAttributeRow } from "@/components/issue/create-form-attribute-row";
import { AttachmentZone } from "@/components/issue/attachment-zone";
import { MentionSuggestionBar } from "@/components/issue/mention-suggestion-bar";
import { DescriptionField } from "@/components/issue/description-field";
import { MarkdownToolbar } from "@/components/editor/markdown-toolbar";
import { useFileAttach } from "@/components/editor/use-file-attach";
import { MOBILE_PLACEHOLDER_COLOR } from "@/components/ui/input-tokens";
import { KeyboardAvoidingView } from "react-native-keyboard-controller";
import { useCreateIssue } from "@/data/mutations/issues";
import {
  getNewIssueSubmissionContextGeneration,
  rememberLastAssigneeAfterSuccessfulCreate,
  useNewIssueDraftStore,
} from "@/data/stores/new-issue-draft-store";
import { useAuthStore } from "@/data/auth-store";
import { useWorkspaceStore } from "@/data/workspace-store";
import { useServerStore } from "@/data/server-store";
import { buildManualCreateContentFields } from "@/lib/attachment-zone";
import { useMentionInput } from "@/lib/use-mention-input";
import { useT } from "@/lib/use-t";

export function ManualCreatePanel() {
  const [title, setTitle] = useState("");
  const description = useMentionInput({ mentionMode: "chips" });
  // Attribute chips (status / priority / assignee / due date / project)
  // live in `useNewIssueDraftStore` so the new-issue-picker/* formSheet
  // routes can read and write the same values without a parent-child
  // React relationship. Reset ownership lives in the new-issue screen shell
  // (once per visit) so a smart↔manual mode flip doesn't wipe in-progress
  // picks — the shared-slot carry-over mirrors web's unified issue-create
  // draft.
  const status = useNewIssueDraftStore((s) => s.status);
  const priority = useNewIssueDraftStore((s) => s.priority);
  const assignee = useNewIssueDraftStore((s) => s.assignee);
  const dueDate = useNewIssueDraftStore((s) => s.dueDate);
  const project = useNewIssueDraftStore((s) => s.project);

  // Uploads run with no issue/comment context — the issue doesn't exist
  // yet; `attachment_ids` on the create request is what binds them
  // (api.uploadFile docstring, same flow as web).
  const {
    attachments,
    pickAndUploadImages,
    pickAndUploadFiles,
    removeAttachment,
    retryAttachment,
    uploading,
  } = useFileAttach();

  const createIssue = useCreateIssue();
  const isSubmitting = createIssue.isPending;

  const { t } = useT("common");
  const { t: tIssues } = useT("issues");
  // Same title key the workspace layout registered the screen with — now
  // owned here so the header follows the active mode.
  const { t: tModals } = useT("modals");

  // In-flight uploads block submit: submitting now would drop their
  // markdown inserts and strand the ids unbound (web MUL-3339 parity).
  const canSubmit =
    !isSubmitting && !uploading && title.trim().length > 0;

  const onSubmit = useCallback(async () => {
    const trimmedTitle = title.trim();
    if (trimmedTitle.length === 0 || uploading) return;
    const contentFields = buildManualCreateContentFields(
      description.serialize(),
      attachments,
    );
    // Capture the context that the request is sent from. The user can dismiss
    // this modal and switch account or workspace before its response arrives.
    const submittedServerId = useServerStore.getState().activeServerId;
    const submittedWorkspaceSlug = useWorkspaceStore.getState().currentWorkspaceSlug;
    const submittedUserId = useAuthStore.getState().user?.id;
    const submittedGeneration = getNewIssueSubmissionContextGeneration();
    try {
      await createIssue.mutateAsync({
        title: trimmedTitle,
        ...contentFields,
        status,
        priority,
        ...(assignee
          ? { assignee_type: assignee.type, assignee_id: assignee.id }
          : {}),
        ...(dueDate ? { due_date: dueDate } : {}),
        ...(project ? { project_id: project.id } : {}),
      });
      // RUYI-79 web parity (create-issue onAccepted): remember the SUBMITTED
      // assignee — not the live draft — only after the server accepted the
      // create. Unassigned is remembered as a value too.
      const { activeServerId } = useServerStore.getState();
      const workspaceSlug = useWorkspaceStore.getState().currentWorkspaceSlug;
      const userId = useAuthStore.getState().user?.id;
      if (submittedWorkspaceSlug && submittedUserId && workspaceSlug && userId) {
        rememberLastAssigneeAfterSuccessfulCreate(
          {
            serverId: submittedServerId,
            workspaceSlug: submittedWorkspaceSlug,
            userId: submittedUserId,
            generation: submittedGeneration,
          },
          {
            serverId: activeServerId,
            workspaceSlug,
            userId,
            generation: getNewIssueSubmissionContextGeneration(),
          },
          assignee ?? null,
        );
      }
      router.back();
    } catch (err) {
      Alert.alert(
        tIssues("table.quick_create_failed", "Failed to create issue"),
        err instanceof Error
          ? err.message
          : t("unknown_error", "Unknown error"),
      );
    }
  }, [
    title,
    description,
    attachments,
    uploading,
    status,
    priority,
    assignee,
    dueDate,
    project,
    createIssue,
    t,
    tIssues,
  ]);

  const headerRight = useCallback(
    () => (
      <SubmitIssueButton
        disabled={!canSubmit}
        loading={isSubmitting}
        onPress={onSubmit}
      />
    ),
    [canSubmit, isSubmitting, onSubmit],
  );

  return (
    <>
      <Stack.Screen
        options={{
          title: tModals("create_issue.sr_manual", "New Issue"),
          headerRight,
        }}
      />
      <KeyboardAvoidingView
        className="flex-1 bg-background"
        behavior="padding"
      >
        <ScrollView
          className="flex-1"
          contentContainerClassName="px-4 pt-4 pb-6 gap-4"
          keyboardShouldPersistTaps="handled"
        >
          <TextInput
            value={title}
            onChangeText={setTitle}
            placeholder={tIssues("detail.title_placeholder", "Issue title")}
            placeholderTextColor={MOBILE_PLACEHOLDER_COLOR}
            className="text-2xl font-semibold text-foreground py-2"
            autoFocus
            returnKeyType="next"
            editable={!isSubmitting}
          />
          <DescriptionField
            description={description}
            disabled={isSubmitting}
            leadingContent={
              <AttachmentZone
                mentions={description.markers}
                attachments={attachments}
                onRemoveMention={description.removeMention}
                onRemoveAttachment={removeAttachment}
                onRetryAttachment={retryAttachment}
                className="pt-2"
              />
            }
          />
          <MarkdownToolbar
            onAt={description.handlers.onAtButtonPress}
            onImage={pickAndUploadImages}
            onFile={pickAndUploadFiles}
            disabled={isSubmitting || uploading}
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
