"use client";

import { useState, useEffect, useRef } from "react";
import { clientErrorMessage, errorCode } from "@multica/core/api";
import { useAuthStore } from "@multica/core/auth";
import {
  usePersonalComputerSettings,
  useComputerCredentials,
  type ComputerSettings,
  type ComputerBinding,
} from "@multica/core/computers";
import { Button } from "@multica/ui/components/ui/button";
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
import { Input } from "@multica/ui/components/ui/input";
import { Textarea } from "@multica/ui/components/ui/textarea";
import { LinuxPasswordInput } from "../../computers/linux-password-input";
import { useT } from "../../i18n";
import { SettingsTab } from "./settings-layout";

const fields: (keyof ComputerSettings)[] = [
  "git_name",
  "git_email",
  "gitlab_url",
  "gitlab_token",
  "git_ssh_key",
  "git_known_hosts",
  "model_env",
  "multica_pat",
];
const secrets = new Set<keyof ComputerSettings>([
  "gitlab_token",
  "git_ssh_key",
  "model_env",
  "multica_pat",
]);
const multiline = new Set<keyof ComputerSettings>([
  "git_ssh_key",
  "git_known_hosts",
  "model_env",
]);

const emptySettings: ComputerSettings = {
  git_name: "",
  git_email: "",
  gitlab_url: "",
  gitlab_token: "",
  git_ssh_key: "",
  git_known_hosts: "",
  model_env: "",
  multica_pat: "",
};

export function CredentialsTab() {
  const userId = useAuthStore((s) => s.user?.id ?? "");
  const { t } = useT("settings");
  return (
    <SettingsTab title={t(($) => $.credential_page.title)}>
      <p>{t(($) => $.linux_user_pages.template_help)}</p>
      <PersonalTemplate key={userId} userId={userId} />
    </SettingsTab>
  );
}

function PersonalTemplate({ userId }: { userId: string }) {
  const { t } = useT("settings");
  const { settings } = usePersonalComputerSettings(userId);
  if (settings.error)
    return (
      <p role="alert" className="text-destructive">
        {settings.error.message}
      </p>
    );
  if (!settings.data)
    return <p role="status">{t(($) => $.computers.loading)}</p>;
  return (
    <CredentialEditor
      userId={userId}
      initialSettings={settings.data.settings}
    />
  );
}

export function AccountCredentials({
  userId,
  binding,
  onDirtyChange,
}: {
  userId: string;
  binding: ComputerBinding;
  onDirtyChange?: (dirty: boolean) => void;
}) {
  return (
    <CredentialEditor
      key={`${userId}:${binding.id}`}
      userId={userId}
      binding={binding}
      onDirtyChange={onDirtyChange}
    />
  );
}

function CredentialEditor({
  userId,
  binding,
  onDirtyChange,
  initialSettings,
}: {
  userId: string;
  binding?: ComputerBinding;
  onDirtyChange?: (dirty: boolean) => void;
  initialSettings?: ComputerSettings;
}) {
  const { t } = useT("settings");
  const transfer = useComputerCredentials(userId);
  const [draft, setDraft] = useState<ComputerSettings | null>(null);
  const [reveal, setReveal] = useState(false);
  const [importConfirmed, setImportConfirmed] = useState(false);
  const [replaceConfirmed, setReplaceConfirmed] = useState(false);
  const [notice, setNotice] = useState("");
  const [error, setError] = useState("");
  const alive = useRef(true);
  const dirty = draft !== null;
  useEffect(() => {
    onDirtyChange?.(dirty);
  }, [dirty, onDirtyChange]);
  const resetRead = transfer.read.reset;
  const resetWrite = transfer.write.reset;
  const resetTemplate = transfer.loadTemplate.reset;
  const resetSave = transfer.saveTemplate.reset;
  useEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
      onDirtyChange?.(false);
      resetRead();
      resetWrite();
      resetTemplate();
      resetSave();
    };
  }, [onDirtyChange, resetRead, resetWrite, resetTemplate, resetSave]);
  useEffect(() => {
    if (!dirty) return;
    const warn = (event: BeforeUnloadEvent) => {
      event.preventDefault();
      event.returnValue = "";
    };
    window.addEventListener("beforeunload", warn);
    return () => window.removeEventListener("beforeunload", warn);
  }, [dirty]);
  const settings = draft ?? (binding ? emptySettings : initialSettings);
  const busy =
    transfer.saveTemplate.isPending ||
    transfer.read.isPending ||
    transfer.write.isPending ||
    transfer.loadTemplate.isPending;
  const blocked =
    !!binding &&
    (!binding.verified ||
      binding.account_state === "missing" ||
      binding.operation_busy ||
      ["running", "interrupted"].includes(binding.state));
  const labels = {
    git_name: t(($) => $.computers.git_name),
    git_email: t(($) => $.computers.git_email),
    gitlab_url: t(($) => $.computers.gitlab_url),
    gitlab_token: t(($) => $.computers.gitlab_token),
    git_ssh_key: t(($) => $.computers.ssh_key),
    git_known_hosts: t(($) => $.computers.known_hosts),
    model_env: t(($) => $.computers.model_keys),
    multica_pat: t(($) => $.computers.multica_pat),
  };
  const help = t(($) => $.credential_page.field_help, { returnObjects: true });
  async function perform(
    kind: "save" | "read" | "write" | "load",
    password = "",
  ) {
    if (busy) return false;
    if (binding && kind === "save" && !importConfirmed) return false;
    if ((kind === "read" || kind === "load") && dirty && !replaceConfirmed)
      return false;
    const current = () => alive.current;
    setError("");
    setNotice("");
    try {
      if (kind === "read" && binding) {
        const result = await transfer.read.mutateAsync({
          id: binding.id,
          password,
        });
        if (!current()) return false;
        setDraft(result.settings);
        setReveal(false);
        setImportConfirmed(false);
        setReplaceConfirmed(false);
        setNotice(t(($) => $.credential_page.read_done));
      } else if (kind === "load") {
        const result = await transfer.loadTemplate.mutateAsync();
        if (!current()) return false;
        setDraft(result.settings);
        setReveal(false);
        setImportConfirmed(false);
        setReplaceConfirmed(false);
      } else if (kind === "write" && binding && settings) {
        await transfer.write.mutateAsync({
          id: binding.id,
          password,
          settings,
        });
        if (!current()) return false;
        setDraft(null);
        setImportConfirmed(false);
        setNotice(t(($) => $.credential_page.write_done));
      } else if (kind === "save" && settings) {
        await transfer.saveTemplate.mutateAsync(settings);
        if (!current()) return false;
        if (!binding) setDraft(null);
        setImportConfirmed(false);
        setNotice(t(($) => $.credential_page.saved));
      }
      return true;
    } catch (err) {
      if (!current()) return false;
      const code = errorCode(err);
      const messages = t(($) => $.linux_user.errors, { returnObjects: true });
      const connectionErrors = t(($) => $.workspace_linux_users.errors, {
        returnObjects: true,
      });
      const message =
        code && Object.hasOwn(messages, code)
          ? messages[code as keyof typeof messages]
          : code && Object.hasOwn(connectionErrors, code)
            ? connectionErrors[code as keyof typeof connectionErrors]
            : (clientErrorMessage(err) ?? t(($) => $.computers.failed));
      if (kind === "read" || kind === "write") throw new Error(message);
      setError(message);
      return false;
    } finally {
      if (current()) {
        transfer.read.reset();
        transfer.write.reset();
        transfer.loadTemplate.reset();
        transfer.saveTemplate.reset();
      }
    }
  }
  return (
    <div className="space-y-4">
      {error && (
        <p role="alert" className="text-destructive">
          {error}
        </p>
      )}
      {notice && <p role="status">{notice}</p>}
      {binding && (
        <div className="space-y-3">
          <p>
            {t(($) => $.credential_page.target)}: {binding.username}
          </p>
          <p className="text-caption text-muted-foreground">
            {t(($) => $.credential_page.transfer_help)}
          </p>
          {dirty && (
            <label className="flex gap-2">
              <input
                type="checkbox"
                checked={replaceConfirmed}
                onChange={(event) => setReplaceConfirmed(event.target.checked)}
              />
              <span>{t(($) => $.linux_user_pages.replace_draft)}</span>
            </label>
          )}
          <div className="flex flex-wrap gap-2">
            <CredentialPasswordAction
              title={t(($) => $.credential_page.read)}
              username={binding.username}
              disabled={busy || blocked || (dirty && !replaceConfirmed)}
              variant="outline"
              onConfirm={(password) => perform("read", password)}
            />
            <Button
              variant="outline"
              disabled={busy || (dirty && !replaceConfirmed)}
              onClick={() => void perform("load")}
            >
              {t(($) => $.linux_user_pages.load_template)}
            </Button>
          </div>
          {blocked && <p>{t(($) => $.linux_user_pages.configure_first)}</p>}
        </div>
      )}
      {settings && (
        <form
          className="space-y-4"
          onSubmit={(event) => {
            event.preventDefault();
            if (!binding) void perform("save");
          }}
        >
          {fields.map((field) => {
            const hidden = secrets.has(field) && !reveal;
            const props = {
              id: `credential-${field}`,
              "aria-describedby": `credential-help-${field}`,
              autoComplete: "off",
              value: hidden && multiline.has(field) ? "" : settings[field],
              disabled: busy,
              onChange: (
                event: React.ChangeEvent<
                  HTMLInputElement | HTMLTextAreaElement
                >,
              ) => setDraft({ ...settings, [field]: event.target.value }),
            };
            return (
              <div key={field} className="space-y-1">
                <label htmlFor={`credential-${field}`}>{labels[field]}</label>
                {multiline.has(field) ? (
                  <Textarea
                    {...props}
                    readOnly={hidden}
                    placeholder={
                      hidden ? t(($) => $.computers.hidden) : undefined
                    }
                  />
                ) : (
                  <Input {...props} type={hidden ? "password" : "text"} />
                )}
                <p
                  id={`credential-help-${field}`}
                  className="text-sm text-muted-foreground"
                >
                  {help[field]}
                </p>
              </div>
            );
          })}

          <div className="flex flex-wrap gap-2">
            {binding ? (
              <CredentialPasswordAction
                title={t(($) => $.credential_page.write)}
                username={binding.username}
                disabled={busy || blocked || !dirty}
                onConfirm={(password) => perform("write", password)}
              />
            ) : (
              <Button type="submit" disabled={busy}>
                {t(($) => $.linux_user_pages.save_template)}
              </Button>
            )}
            <Button
              type="button"
              variant="outline"
              onClick={() => setReveal(!reveal)}
            >
              {reveal
                ? t(($) => $.computers.hide)
                : t(($) => $.computers.reveal)}
            </Button>
          </div>
          {binding && (
            <details className="space-y-2">
              <summary>{t(($) => $.linux_user_pages.save_template)}</summary>
              <label className="flex gap-2">
                <input
                  type="checkbox"
                  checked={importConfirmed}
                  disabled={busy}
                  onChange={(event) => setImportConfirmed(event.target.checked)}
                />
                <span>{t(($) => $.credential_page.confirm_import)}</span>
              </label>
              <Button
                type="button"
                variant="outline"
                disabled={busy || !dirty || !importConfirmed}
                onClick={() => void perform("save")}
              >
                {t(($) => $.linux_user_pages.save_template)}
              </Button>
            </details>
          )}
        </form>
      )}
      {binding && (
        <p className="text-caption text-muted-foreground">
          {t(($) => $.credential_page.locations)}
        </p>
      )}
    </div>
  );
}

function CredentialPasswordAction({
  title,
  username,
  disabled,
  variant,
  onConfirm,
}: {
  title: string;
  username: string;
  disabled: boolean;
  variant?: "outline";
  onConfirm: (password: string) => Promise<boolean>;
}) {
  const { t } = useT("settings");
  const [open, setOpen] = useState(false);
  const submitting = useRef(false);
  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!submitting.current) setOpen(next);
      }}
    >
      <DialogTrigger
        render={<Button type="button" variant={variant} disabled={disabled} />}
      >
        {title}
      </DialogTrigger>
      {open && (
        <CredentialPasswordConfirmation
          title={title}
          description={t(($) => $.credential_page.authenticate, { username })}
          onConfirm={async (password) => {
            submitting.current = true;
            try {
              return await onConfirm(password);
            } finally {
              submitting.current = false;
            }
          }}
          onDone={() => setOpen(false)}
        />
      )}
    </Dialog>
  );
}

// Unmounting the dialog (including account/section navigation) drops the password.
function CredentialPasswordConfirmation({
  title,
  description,
  onConfirm,
  onDone,
}: {
  title: string;
  description: string;
  onConfirm: (password: string) => Promise<boolean>;
  onDone: () => void;
}) {
  const { t } = useT("settings");
  const [password, setPassword] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState("");
  const alive = useRef(true);
  useEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
    };
  }, []);
  return (
    <DialogContent showCloseButton={!submitting}>
      <form
        onSubmit={async (event) => {
          event.preventDefault();
          event.stopPropagation();
          if (!password || submitting) return;
          setSubmitting(true);
          setError("");
          try {
            if ((await onConfirm(password)) && alive.current) onDone();
          } catch (err) {
            if (alive.current) setError((err as Error).message);
          } finally {
            if (alive.current) {
              setPassword("");
              setSubmitting(false);
            }
          }
        }}
        className="space-y-4"
      >
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription>{description}</DialogDescription>
        </DialogHeader>
        <LinuxPasswordInput
          autoComplete="off"
          value={password}
          disabled={submitting}
          onChange={(event) => setPassword(event.target.value)}
        />
        {error && (
          <p role="alert" className="text-destructive">
            {error}
          </p>
        )}
        <DialogFooter>
          <DialogClose
            render={
              <Button type="button" variant="outline" disabled={submitting} />
            }
          >
            {t(($) => $.linux_user.cancel)}
          </DialogClose>
          <Button type="submit" disabled={!password || submitting}>
            {title}
          </Button>
        </DialogFooter>
      </form>
    </DialogContent>
  );
}
