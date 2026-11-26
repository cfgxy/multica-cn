"use client";

import { useState } from "react";
import { copyText } from "@multica/ui/lib/clipboard";
import {
  useRuntimeModelConfig,
  type ModelConfigAction,
  type ModelConfigInput,
  type ModelConfigResult,
} from "@multica/core/computers";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import { Textarea } from "@multica/ui/components/ui/textarea";
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
import { ModelChoice, ModelError, ModelStatus } from "./model-config-controls";
import { useT } from "../i18n";

export function RuntimeModelDialog({
  accountLabel,
  userId,
  bindingId,
  kind,
  label,
  disabled,
}: {
  accountLabel?: string;
  userId: string;
  bindingId: string;
  kind: string;
  label: string;
  disabled?: boolean;
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
      <DialogTrigger
        render={
          <Button
            type="button"
            variant="outline"
            size="sm"
            disabled={disabled}
          />
        }
      >
        {t(($) => $.model_configuration.configure)}
      </DialogTrigger>
      <DialogContent
        className="flex max-h-[90dvh] flex-col sm:max-w-4xl"
        showCloseButton={!busy}
      >
        <DialogHeader>
          <DialogTitle>
            {accountLabel ? `${accountLabel} · ` : ""}
            {label} · {t(($) => $.model_configuration.configure)}
          </DialogTitle>
          <DialogDescription>
            {t(($) => $.model_configuration.private_help)}
          </DialogDescription>
        </DialogHeader>
        {open && (
          <RuntimeModelEditor
            key={`${userId}:${bindingId}:${kind}`}
            userId={userId}
            bindingId={bindingId}
            kind={kind}
            busy={busy}
            setBusy={setBusy}
          />
        )}
      </DialogContent>
    </Dialog>
  );
}
function RuntimeModelEditor({
  userId,
  bindingId,
  kind,
  busy,
  setBusy,
}: {
  userId: string;
  bindingId: string;
  kind: string;
  busy: boolean;
  setBusy: (busy: boolean) => void;
}) {
  const { t } = useT("settings");
  const data = useRuntimeModelConfig(userId, bindingId, kind);
  const [copiedFile, setCopiedFile] = useState("");
  const [password, setPassword] = useState("");
  const [snapshot, setSnapshot] = useState<
    ModelConfigResult["snapshot"] | null
  >(null);
  const [files, setFiles] = useState<Record<string, string>>({});
  const [keyId, setKeyId] = useState<string | null>(null);
  const [modelId, setModelId] = useState("");
  const [apiType, setAPIType] = useState("");
  const [connection, setConnection] = useState<ModelConfigResult | null>(null);
  const [selection, setSelection] = useState<
    Pick<ModelConfigInput, "apiKeyId" | "modelId" | "apiType">
  >({});
  const [savedKeyId, setSavedKeyId] = useState("");
  const [testOperation, setTestOperation] = useState("");
  const [test, setTest] = useState<ModelConfigResult["test"]>();
  const [error, setError] = useState<unknown>(null);
  const [notice, setNotice] = useState("");
  const selectedKey = keyId ?? data.keys.data?.[0]?.id ?? "";
  const original = snapshot
    ? Object.fromEntries(
        Object.entries(snapshot.files).filter(
          (entry): entry is [string, string] => entry[1] !== null,
        ),
      )
    : {};
  const dirty =
    snapshot !== null &&
    (Object.keys(files).length !== Object.keys(original).length ||
      Object.entries(files).some(([path, value]) => value !== original[path]));
  const call = async (
    action: ModelConfigAction,
    input: ModelConfigInput = {},
  ) => {
    try {
      return await data.action.mutateAsync({
        action,
        input: action === "check" ? { files: input.files } : { password, expectedRevision: snapshot?.revision, ...input },
      });
    } finally {
      data.action.reset();
    }
  };
  const perform = async (fn: () => Promise<void>) => {
    if (busy) return;
    setBusy(true);
    setError(null);
    setNotice("");
    try {
      await fn();
    } catch (err) {
      setError(err);
    } finally {
      setBusy(false);
    }
  };
  const read = async () => {
    const result = await call("read");
    setSnapshot(result.snapshot);
    setFiles(
      Object.fromEntries(
        Object.entries(result.snapshot.files).filter(
          (entry): entry is [string, string] => entry[1] !== null,
        ),
      ),
    );
    setKeyId(result.apiKeyId || null);
    setSavedKeyId(result.apiKeyId);
    setModelId(result.modelId);
    setAPIType(result.apiType);
    setConnection(null);
    setSelection({});
    setTest(result.test);
    if (result.configStatus === "authorization_invalid")
      setNotice(t(($) => $.model_configuration.states.authorization_invalid));
  };
  const loadModels = async () => {
    const result = await call("connection", { apiKeyId: selectedKey });
    setConnection(result);
    const endpoint =
      result.endpoints.find((e) => e.apiType === apiType) ??
      result.endpoints[0];
    setAPIType(endpoint?.apiType ?? "");
    setModelId(
      result.models.some(
        (m) =>
          m.id === modelId && m.apiTypes.some((p) => p === endpoint?.apiType),
      )
        ? modelId
        : (endpoint?.defaultModel ?? ""),
    );
  };
  const preview = async () => {
    const result = await call("preview", {
      apiKeyId: selectedKey,
      modelId,
      apiType,
      files,
    });
    setFiles(
      Object.fromEntries(
        Object.entries(result.snapshot.files).filter(
          (entry): entry is [string, string] => entry[1] !== null,
        ),
      ),
    );
    setSelection({
      apiKeyId: result.apiKeyId,
      modelId: result.modelId,
      apiType: result.apiType,
    });
    setTest(undefined);
    setNotice(t(($) => $.model_configuration.preview_help));
  };
  const save = async () => {
    const result = await call("save", {
      files,
      ...selection,
      createOnly: snapshot
        ? Object.values(snapshot.files).every((v) => v === null)
        : false,
    });
    setSnapshot(result.snapshot);
    setSavedKeyId(result.apiKeyId);
    setTest(undefined);
    setNotice(t(($) => $.model_configuration.saved));
    return result.snapshot.revision;
  };
  const runTest = async () => {
    const revision = dirty ? await save() : snapshot?.revision;
    const result = await call("test", { expectedRevision: revision });
    setTest(result.test);
    setTestOperation(result.operationId);
    setNotice("");
  };
  const endpoints =
    connection?.apiKeyId === selectedKey ? connection.endpoints : [];
  const models =
    connection?.models.filter((m) => m.apiTypes.some((p) => p === apiType)) ??
    [];
  return (
    <>
      <div className="min-h-0 space-y-5 overflow-y-auto px-1">
        <div className="flex flex-wrap items-end gap-3">
          <label className="min-w-52 flex-1 space-y-1">
            <span>{t(($) => $.model_configuration.password)}</span>
            <Input
              type="password"
              autoComplete="off"
              value={password}
              disabled={busy}
              onChange={(e) => setPassword(e.target.value)}
            />
          </label>
          <Button
            variant="outline"
            disabled={busy || !password}
            aria-busy={busy}
            onClick={() => void perform(read)}
          >
            {snapshot
              ? t(($) => $.model_configuration.reload)
              : t(($) => $.model_configuration.read)}
          </Button>
        </div>
        <ModelError error={error} />
        <ModelError error={data.keys.error} />
        {notice && (
          <p role="status" className="text-caption">
            {notice}
          </p>
        )}
        {snapshot && (
          <>
            {data.keys.data?.length === 0 && (
              <p className="text-caption text-muted-foreground">
                {t(($) => $.model_configuration.no_keys)}
              </p>
            )}
            <p className="text-caption text-muted-foreground">
              {t(($) => $.model_configuration.current_key)}:{" "}
              {data.keys.data?.find((k) => k.id === savedKeyId)?.apiKeyName ||
                savedKeyId ||
                t(($) => $.model_configuration.custom_config)}
            </p>
            {!!data.keys.data?.length && (
              <div className="space-y-3 rounded-lg border p-4">
                <div className="flex flex-wrap items-end gap-3">
                  <div className="min-w-52 flex-1">
                    <ModelChoice
                      label={t(($) => $.model_configuration.key_name)}
                      value={selectedKey}
                      options={data.keys.data.map((k) => ({
                        value: k.id,
                        label: `${k.apiKeyName} #${k.id}`,
                      }))}
                      disabled={busy}
                      onChange={(value) => {
                        setKeyId(value);
                        setConnection(null);
                        setAPIType("");
                        setModelId("");
                      }}
                    />
                  </div>
                  <Button
                    variant="outline"
                    disabled={busy || !password || !selectedKey}
                    onClick={() => void perform(loadModels)}
                  >
                    {t(($) => $.model_configuration.load_models)}
                  </Button>
                </div>
                <p className="text-caption text-muted-foreground">
                  {t(($) => $.model_configuration.preview_help)}
                </p>
                {connection && (
                  <>
                    <div className="grid gap-3 sm:grid-cols-2">
                      <ModelChoice
                        label={t(($) => $.model_configuration.protocol)}
                        value={apiType}
                        disabled={busy}
                        options={endpoints.map((e) => ({
                          value: e.apiType,
                          label: e.apiType,
                        }))}
                        onChange={(value) => {
                          setAPIType(value);
                          setModelId(
                            endpoints.find((e) => e.apiType === value)
                              ?.defaultModel ?? "",
                          );
                        }}
                      />
                      <ModelChoice
                        label={t(($) => $.model_configuration.model)}
                        value={modelId}
                        disabled={busy}
                        options={models.map((m) => ({
                          value: m.id,
                          label:
                            m.name && m.name !== m.id
                              ? `${m.name} · ${m.id}`
                              : m.id,
                        }))}
                        onChange={setModelId}
                      />
                    </div>
                    <p className="break-all font-mono text-caption text-muted-foreground">
                      {endpoints.find((e) => e.apiType === apiType)?.baseURL}
                    </p>
                    <Button
                      variant="outline"
                      disabled={busy || !password || !modelId || !apiType}
                      onClick={() => void perform(preview)}
                    >
                      {t(($) => $.model_configuration.preview)}
                    </Button>
                  </>
                )}
              </div>
            )}
            <div className="flex justify-between gap-3">
              <h3 className="font-medium">
                {t(($) => $.model_configuration.files)}
              </h3>
              {dirty && (
                <span className="text-caption text-warning">
                  {t(($) => $.model_configuration.dirty)}
                </span>
              )}
            </div>
            {Object.keys(snapshot.files).map((path) => (
              <div key={path} className="space-y-2">
                <div className="flex items-center justify-between gap-3">
                  <label
                    htmlFor={`model-file-${kind}-${path}`}
                    className="break-all font-mono text-caption"
                  >
                    ~/{path}
                  </label>
                  <Button
                    type="button"
                    size="sm"
                    variant="outline"
                    disabled={files[path] === undefined}
                    onClick={() =>
                      void copyText(files[path] ?? "").then((ok) =>
                        setCopiedFile(ok ? path : ""),
                      )
                    }
                  >
                    {copiedFile === path
                      ? t(($) => $.model_configuration.copied)
                      : t(($) => $.model_configuration.copy_file)}
                  </Button>
                </div>
                {snapshot.files[path] === null && files[path] === undefined && (
                  <span className="block text-caption text-muted-foreground">
                    {t(($) => $.model_configuration.missing_file)}
                  </span>
                )}
                <Textarea
                  id={`model-file-${kind}-${path}`}
                  aria-label={path}
                  value={files[path] ?? ""}
                  disabled={busy}
                  spellCheck={false}
                  autoComplete="off"
                  className="min-h-52 font-mono text-caption"
                  onChange={(e) => {
                    setFiles({ ...files, [path]: e.target.value });
                    setSelection({});
                    setTest(undefined);
                    setCopiedFile("");
                    setNotice("");
                  }}
                />
              </div>
            ))}
            <p className="text-caption text-muted-foreground">
              {t(($) => $.model_configuration.cost_help)}
            </p>
            {test && (
              <div role="status" className="space-y-2 rounded-lg border p-3">
                <p className="text-caption">
                  <ModelStatus status={test.status} /> ·{" "}
                  {`${test.durationMs} ms`}
                  {test.model && ` · ${test.model}`}
                </p>
                {test.errorCode && (
                  <ModelError
                    error={Object.assign(new Error(test.errorCode), {
                      body: {
                        code: test.errorCode,
                        operation_id: testOperation,
                      },
                    })}
                  />
                )}
              </div>
            )}
          </>
        )}
      </div>
      <DialogFooter className="flex-wrap">
        <DialogClose render={<Button variant="outline" disabled={busy} />}>
          {t(($) => $.model_configuration.close)}
        </DialogClose>
        {snapshot && (
          <>
            <Button
              variant="outline"
              disabled={busy || !Object.keys(files).length}
              onClick={() =>
                void perform(async () => {
                  await call("check", { files });
                  setNotice(t(($) => $.model_configuration.valid));
                })
              }
            >
              {t(($) => $.model_configuration.validate)}
            </Button>
            <Button
              variant="outline"
              disabled={busy || !password || !dirty}
              onClick={() =>
                void perform(async () => {
                  await save();
                })
              }
            >
              {t(($) => $.model_configuration.save)}
            </Button>
            <Button
              disabled={busy || !password || !Object.keys(files).length}
              aria-busy={busy}
              onClick={() => void perform(runTest)}
            >
              {dirty
                ? t(($) => $.model_configuration.save_test)
                : t(($) => $.model_configuration.cli_test)}
            </Button>
          </>
        )}
      </DialogFooter>
    </>
  );
}
