"use client";

import { useState } from "react";
import { useModelTemplates, type ModelTemplate } from "@multica/core/computers";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import { Textarea } from "@multica/ui/components/ui/textarea";
import { Checkbox } from "@multica/ui/components/ui/checkbox";
import {
  Dialog,
  DialogTrigger,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogFooter,
  DialogClose,
} from "@multica/ui/components/ui/dialog";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@multica/ui/components/ui/table";
import { ModelChoice, ModelError, ModelStatus } from "../computers/model-config-controls";
import { useT } from "../i18n";

export function ModelTemplatePanel({ userId }: { userId: string }) {
  const { t } = useT("settings");
  const { templates, save } = useModelTemplates(userId);
  return (
    <section className="space-y-4">
      <h2 className="text-title font-semibold">
        {t(($) => $.model_configuration.templates_title)}
      </h2>
      <p className="text-caption text-muted-foreground">
        {t(($) => $.model_configuration.template_help)}
      </p>
      <ModelError error={templates.error} />
      {templates.isPending && (
        <p role="status">{t(($) => $.computers.loading)}</p>
      )}
      <div className="overflow-hidden rounded-lg border">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{t(($) => $.admin.runtimes_title)}</TableHead>
              <TableHead>{t(($) => $.model_configuration.files)}</TableHead>
              <TableHead>{t(($) => $.model_configuration.protocol)}</TableHead>
              <TableHead>{t(($) => $.model_configuration.revision)}</TableHead>
              <TableHead />
            </TableRow>
          </TableHeader>
          <TableBody>
            {templates.data?.map((template) => (
              <TableRow key={template.runtimeKind}>
                <TableCell className="font-medium">
                  {template.runtimeKind}
                </TableCell>
                <TableCell className="font-mono text-caption">
                  {template.files.map((f) => (
                    <p key={f.id}>{f.path}</p>
                  ))}
                </TableCell>
                <TableCell className="text-caption">
                  {template.supportedAPITypes.join(", ")}
                </TableCell>
                <TableCell>{template.revision}</TableCell>
                <TableCell className="max-w-xs whitespace-normal text-right">
                  {template.status === "unsupported" ? (
                    <ModelStatus status="unsupported" />
                  ) : (
                    <TemplateDialog
                      template={template}
                      save={async (draft) => {
                        try {
                          await save.mutateAsync(draft);
                        } finally {
                          save.reset();
                        }
                      }}
                    />
                  )}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>
    </section>
  );
}
function TemplateDialog({
  template,
  save,
}: {
  template: ModelTemplate;
  save: (draft: ModelTemplate) => Promise<void>;
}) {
  const { t } = useT("settings");
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  return (
    <Dialog
      open={open}
      onOpenChange={(value) => {
        if (!busy) setOpen(value);
      }}
    >
      <DialogTrigger render={<Button variant="outline" size="sm" />}>
        {t(($) => $.model_configuration.edit)}
      </DialogTrigger>
      <DialogContent
        className="flex max-h-[90dvh] flex-col sm:max-w-4xl"
        showCloseButton={!busy}
      >
        <DialogHeader>
          <DialogTitle>
            {template.runtimeKind} ·{" "}
            {t(($) => $.model_configuration.templates_title)}
          </DialogTitle>
          <DialogDescription>
            {t(($) => $.model_configuration.template_help)}
          </DialogDescription>
        </DialogHeader>
        {open && (
          <TemplateEditor
            key={template.runtimeKind}
            template={template}
            busy={busy}
            onSave={async (draft) => {
              setBusy(true);
              try {
                await save(draft);
                setOpen(false);
              } finally {
                setBusy(false);
              }
            }}
          />
        )}
      </DialogContent>
    </Dialog>
  );
}
function TemplateEditor({
  template,
  busy,
  onSave,
}: {
  template: ModelTemplate;
  busy: boolean;
  onSave: (draft: ModelTemplate) => Promise<void>;
}) {
  const { t } = useT("settings");
  const [draft, setDraft] = useState(template);
  const [bindings, setBindings] = useState(
    template.files.map((f) => JSON.stringify(f.bindings, null, 2)),
  );
  const [error, setError] = useState<unknown>(null);
  const patch = (
    index: number,
    value: Partial<ModelTemplate["files"][number]>,
  ) =>
    setDraft({
      ...draft,
      files: draft.files.map((f, i) => (i === index ? { ...f, ...value } : f)),
    });
  const submit = async () => {
    setError(null);
    try {
      const files = draft.files.map((f, i) => {
        const value: unknown = JSON.parse(bindings[i] ?? "{}");
        if (
          !value ||
          Array.isArray(value) ||
          typeof value !== "object" ||
          Object.values(value).some((v) => typeof v !== "string")
        )
          throw new Error(t(($) => $.model_configuration.bindings_help));
        return {
          ...f,
          bindings: Object.fromEntries(
            Object.entries(value).map(([k, v]) => [k, String(v)]),
          ),
        };
      });
      await onSave({ ...draft, files });
    } catch (err) {
      setError(
        err instanceof SyntaxError
          ? t(($) => $.model_configuration.bindings_help)
          : err,
      );
    }
  };
  return (
    <>
      <div className="min-h-0 space-y-5 overflow-y-auto px-1">
        <fieldset className="flex flex-wrap gap-4" disabled={busy}>
          <legend className="mb-2 font-medium">
            {t(($) => $.model_configuration.protocol)}
          </legend>
          {(
            [
              "anthropic-messages",
              "openai-responses",
              "openai-chat-completions",
            ] as const
          ).map((protocol) => (
            <label
              key={protocol}
              className="flex items-center gap-2 text-caption"
            >
              <Checkbox
                checked={draft.supportedAPITypes.includes(protocol)}
                onCheckedChange={(checked) =>
                  setDraft({
                    ...draft,
                    supportedAPITypes: checked
                      ? [...draft.supportedAPITypes, protocol]
                      : draft.supportedAPITypes.filter((p) => p !== protocol),
                  })
                }
              />
              {protocol}
            </label>
          ))}
        </fieldset>
        <label className="block space-y-1">
          <span>{t(($) => $.model_configuration.default_model)}</span>
          <Input
            disabled={busy}
            value={draft.defaultModel}
            onChange={(e) =>
              setDraft({ ...draft, defaultModel: e.target.value })
            }
          />
        </label>
        {draft.files.map((file, index) => (
          <fieldset
            key={file.id}
            className="space-y-3 rounded-lg border p-4"
            disabled={busy}
          >
            <legend className="px-1 font-medium">{file.id}</legend>
            <div className="grid gap-3 sm:grid-cols-[1fr_10rem]">
              <label className="space-y-1">
                <span>{t(($) => $.model_configuration.path)}</span>
                <Input
                  value={file.path}
                  onChange={(e) => patch(index, { path: e.target.value })}
                />
              </label>
              <ModelChoice
                label={t(($) => $.model_configuration.format)}
                value={file.format}
                disabled={busy}
                options={["json", "jsonc", "toml", "yaml"].map((value) => ({
                  value,
                  label: value,
                }))}
                onChange={(value) => {
                  if (
                    value === "json" ||
                    value === "jsonc" ||
                    value === "toml" ||
                    value === "yaml"
                  )
                    patch(index, { format: value });
                }}
              />
            </div>
            <label className="block space-y-1">
              <span>{t(($) => $.model_configuration.content)}</span>
              <Textarea
                value={file.content}
                spellCheck={false}
                className="min-h-52 font-mono text-caption"
                onChange={(e) => patch(index, { content: e.target.value })}
              />
            </label>
            <details>
              <summary className="cursor-pointer text-caption font-medium">
                {t(($) => $.model_configuration.bindings)}
              </summary>
              <p className="my-2 text-caption text-muted-foreground">
                {t(($) => $.model_configuration.bindings_help)}
              </p>
              <Textarea
                aria-label={`${file.id} ${t(($) => $.model_configuration.bindings)}`}
                value={bindings[index] ?? "{}"}
                spellCheck={false}
                className="min-h-28 font-mono text-caption"
                onChange={(e) =>
                  setBindings(
                    bindings.map((v, i) => (i === index ? e.target.value : v)),
                  )
                }
              />
            </details>
            <Button
              variant="outline"
              size="sm"
              disabled={busy || draft.files.length === 1}
              onClick={() => {
                setDraft({
                  ...draft,
                  files: draft.files.filter((_, i) => i !== index),
                });
                setBindings(bindings.filter((_, i) => i !== index));
              }}
            >
              {t(($) => $.model_configuration.remove)}
            </Button>
          </fieldset>
        ))}
        <Button
          variant="outline"
          disabled={busy || draft.files.length >= 16}
          onClick={() => {
            setDraft({
              ...draft,
              files: [
                ...draft.files,
                {
                  id: `file-${Date.now()}`,
                  path: "",
                  format: "json",
                  content: "{}",
                  bindings: {},
                },
              ],
            });
            setBindings([...bindings, "{}"]);
          }}
        >
          {t(($) => $.model_configuration.add_file)}
        </Button>
        <ModelError error={error} />
      </div>
      <DialogFooter>
        <DialogClose render={<Button variant="outline" disabled={busy} />}>
          {t(($) => $.model_configuration.close)}
        </DialogClose>
        <Button disabled={busy} aria-busy={busy} onClick={() => void submit()}>
          {t(($) => $.model_configuration.save)}
        </Button>
      </DialogFooter>
    </>
  );
}
