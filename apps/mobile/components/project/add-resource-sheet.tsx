/**
 * Attach a GitHub repo to a project. v1 only supports `github_repo` resource
 * type — server will accept the JSON ref `{ url }`. Optional label so the
 * row in the list reads as something the user picked rather than the raw URL.
 *
 * Minimal client-side validation: the URL must look like
 * `https://github.com/owner/repo`. Anything else surfaces a Submit error
 * from the server (real validation lives there).
 *
 * Container: iOS pageSheet via shared `<SheetShell>` (CLAUDE.md Lesson #6).
 * Primary action ("Attach") lives in the header's rightAction slot — iOS
 * convention for confirm-style sheets (Mail, Messages). X closes (replaces
 * the old explicit Cancel button).
 */
import { useState } from "react";
import { View } from "react-native";
import type { CreateProjectResourceRequest } from "@multica/core/types";
import { Text } from "@/components/ui/text";
import { Button } from "@/components/ui/button";
import { TextField } from "@/components/ui/text-field";
import { SheetShell } from "@/components/ui/sheet-shell";

interface Props {
  visible: boolean;
  onSubmit: (body: CreateProjectResourceRequest) => void;
  onClose: () => void;
  submitting?: boolean;
}

// Loose prefix match — accepts `owner/repo`, `owner/repo.git`,
// `owner/repo/tree/main`, etc. Server is the canonical validator
// (validateAndNormalizeResourceRef on the Go side); we only gate the
// Attach button on "this looks like a GitHub repo URL at all".
const GITHUB_PATTERN = /^https:\/\/github\.com\/[\w.-]+\/[\w.-]+(\/|$)/i;

export function AddResourceSheet({
  visible,
  onSubmit,
  onClose,
  submitting,
}: Props) {
  const [url, setUrl] = useState("");
  const [label, setLabel] = useState("");

  const reset = () => {
    setUrl("");
    setLabel("");
  };

  const close = () => {
    reset();
    onClose();
  };

  const valid = GITHUB_PATTERN.test(url.trim());

  const submit = () => {
    if (!valid || submitting) return;
    onSubmit({
      resource_type: "github_repo",
      resource_ref: { url: url.trim() },
      label: label.trim() || undefined,
    });
    reset();
  };

  return (
    <SheetShell
      visible={visible}
      onClose={close}
      title="Attach repository"
      rightAction={
        <Button
          size="sm"
          onPress={submit}
          disabled={!valid || submitting}
          className={!valid || submitting ? "opacity-50" : undefined}
        >
          <Text>{submitting ? "Attaching…" : "Attach"}</Text>
        </Button>
      }
    >
      <View className="px-4 pt-4 gap-4">
        <View className="gap-1">
          <Text className="text-xs text-muted-foreground">Repository URL</Text>
          <TextField
            value={url}
            onChangeText={setUrl}
            placeholder="https://github.com/owner/repo"
            autoCapitalize="none"
            autoCorrect={false}
            keyboardType="url"
            autoFocus
          />
        </View>
        <View className="gap-1">
          <Text className="text-xs text-muted-foreground">
            Label (optional)
          </Text>
          <TextField
            value={label}
            onChangeText={setLabel}
            placeholder="e.g. Backend"
          />
        </View>
      </View>
    </SheetShell>
  );
}
