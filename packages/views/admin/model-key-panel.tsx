"use client";

import { useState } from "react";
import {
  modelRuntimeKinds,
  useModelKeyAdmin,
  type ModelKey,
} from "@multica/core/computers";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import { Checkbox } from "@multica/ui/components/ui/checkbox";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@multica/ui/components/ui/table";
import {
  ModelChoice,
  ModelError,
  ModelStatus,
} from "../computers/model-config-controls";
import { useT } from "../i18n";
import { useDebouncedValue } from "../common/use-debounced-value";

export function ModelKeyPanel({ userId }: { userId: string }) {
  const { t } = useT("settings");
  const [search, setSearch] = useState("");
  const [runtime, setRuntime] = useState("");
  const [page, setPage] = useState(1);
  const [userSearch, setUserSearch] = useState("");
  const [target, setTarget] = useState("");
  const [draft, setDraft] = useState<ModelKey[] | null>(null);
  const [baseURL, setBaseURL] = useState<string | null>(null);
  const [sourceRevision, setSourceRevision] = useState<number | null>(null);
  const [token, setToken] = useState("");
  const [error, setError] = useState<unknown>(null);
  const [notice, setNotice] = useState("");
  const debouncedSearch = useDebouncedValue(search, 300);
  const debouncedUserSearch = useDebouncedValue(userSearch, 300);
  const data = useModelKeyAdmin(
    userId,
    debouncedSearch,
    runtime,
    page,
    debouncedUserSearch,
    target,
  );
  const selected = draft ?? data.grants.data ?? [];
  const busy =
    data.saveSource.isPending ||
    data.refresh.isPending ||
    data.saveGrants.isPending;
  const perform = async (fn: () => Promise<unknown>, done?: () => void) => {
    setError(null);
    setNotice("");
    try {
      await fn();
      done?.();
      setNotice(t(($) => $.model_configuration.saved));
    } catch (err) {
      setError(err);
    }
  };
  const toggle = (key: ModelKey, checked: boolean) =>
    setDraft(
      checked ? [...selected, key] : selected.filter((k) => k.id !== key.id),
    );
  return (
    <div className="space-y-8">
      <section
        className="space-y-4"
        aria-label={t(($) => $.model_configuration.source)}
      >
        <h2 className="text-title font-semibold">
          {t(($) => $.model_configuration.source)}
        </h2>
        <p className="text-caption text-muted-foreground">
          {t(($) => $.model_configuration.source_help)}
        </p>
        <form
          className="grid gap-4 sm:grid-cols-2"
          onSubmit={(e) => {
            e.preventDefault();
            void perform(async () => {
              try {
                await data.saveSource.mutateAsync({
                  baseURL: baseURL ?? data.source.data?.baseURL ?? "",
                  directoryToken: token,
                  revision: sourceRevision ?? data.source.data?.revision ?? 0,
                });
                setToken("");
                setBaseURL(null);
                setSourceRevision(null);
              } finally {
                data.saveSource.reset();
              }
            });
          }}
        >
          <label className="space-y-1">
            <span>{t(($) => $.model_configuration.base_url)}</span>
            <Input
              required
              type="url"
              value={baseURL ?? data.source.data?.baseURL ?? ""}
              placeholder="https://sub2api.example.com"
              onChange={(e) => {
                setBaseURL(e.target.value);
                setSourceRevision(
                  (revision) => revision ?? data.source.data?.revision ?? 0,
                );
              }}
              disabled={busy}
            />
          </label>
          <label className="space-y-1">
            <span>{t(($) => $.model_configuration.directory_token)}</span>
            <Input
              required
              type="password"
              autoComplete="new-password"
              value={token}
              onChange={(e) => {
                setToken(e.target.value);
                setSourceRevision(
                  (revision) => revision ?? data.source.data?.revision ?? 0,
                );
              }}
              placeholder={t(($) => $.model_configuration.credential_hint)}
              disabled={busy}
            />
          </label>
          <div className="flex flex-wrap items-center gap-3 sm:col-span-2">
            <Button
              type="submit"
              disabled={
                busy || data.source.isPending || !!data.source.error || !token
              }
            >
              {t(($) => $.model_configuration.save)}
            </Button>
            <Button
              type="button"
              variant="outline"
              disabled={busy || !data.source.data?.configured}
              onClick={() => void perform(() => data.refresh.mutateAsync())}
            >
              {t(($) => $.model_configuration.refresh)}
            </Button>
            {data.source.data && (
              <span className="text-caption">
                <ModelStatus status={data.source.data.status} /> ·{" "}
                {data.source.data.directoryCount}
                {data.source.data.lastCheckedAt &&
                  ` · ${new Date(data.source.data.lastCheckedAt).toLocaleString()}`}
              </span>
            )}
          </div>
        </form>
        <ModelError
          error={data.source.error || error || data.source.data?.lastErrorCode}
        />
        {notice && (
          <p role="status" className="text-caption text-success">
            {notice}
          </p>
        )}
      </section>
      <section
        className="space-y-4"
        aria-label={t(($) => $.model_configuration.users)}
      >
        <h2 className="text-title font-semibold">
          {t(($) => $.model_configuration.users)}
        </h2>
        <Input
          aria-label={t(($) => $.model_configuration.search_users)}
          placeholder={t(($) => $.model_configuration.search_users)}
          value={userSearch}
          onChange={(e) => setUserSearch(e.target.value)}
        />
        <ModelError error={data.users.error} />
        <div className="max-h-64 overflow-auto rounded-lg border">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t(($) => $.model_configuration.users)}</TableHead>
                <TableHead className="text-right">
                  {t(($) => $.model_configuration.assign)}
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {data.users.data?.map((u) => (
                <TableRow
                  key={u.id}
                  data-state={u.id === target ? "selected" : undefined}
                >
                  <TableCell>
                    <p className="font-medium">{u.name}</p>
                    <p className="text-caption text-muted-foreground">
                      {u.email}
                    </p>
                  </TableCell>
                  <TableCell className="text-right">
                    <Button
                      variant={u.id === target ? "secondary" : "outline"}
                      size="sm"
                      disabled={busy}
                      onClick={() => {
                        setTarget(u.id);
                        setDraft(null);
                        setError(null);
                        setNotice("");
                      }}
                    >
                      {t(($) => $.model_configuration.assign)}
                    </Button>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
        {target && (
          <div key={target} className="space-y-4 rounded-lg border p-4">
            <h3 className="font-semibold">
              {data.users.data?.find((u) => u.id === target)?.name ?? target} ·{" "}
              {t(($) => $.model_configuration.selected)}
            </h3>
            <p className="text-caption text-muted-foreground">
              {t(($) => $.model_configuration.grant_help)}
            </p>
            <ModelError error={data.grants.error} />
            <Table>
              <TableBody>
                {selected.map((key, index) => (
                  <TableRow key={key.id}>
                    <TableCell>{index + 1}</TableCell>
                    <TableCell>
                      {key.apiKeyName}{" "}
                      <span className="text-caption text-muted-foreground">
                        #{key.id}
                      </span>
                    </TableCell>
                    <TableCell>{key.allowedRuntimes.join(", ")}</TableCell>
                    <TableCell>
                      <ModelStatus status={key.status} />
                    </TableCell>
                    <TableCell className="text-right">
                      <div className="flex justify-end gap-2">
                        <Button
                          variant="outline"
                          size="sm"
                          disabled={busy || index === 0}
                          onClick={() => {
                            const next = [...selected];
                            [next[index - 1], next[index]] = [
                              next[index]!,
                              next[index - 1]!,
                            ];
                            setDraft(next);
                          }}
                        >
                          {t(($) => $.model_configuration.move_up)}
                        </Button>
                        <Button
                          variant="outline"
                          size="sm"
                          disabled={busy}
                          onClick={() => toggle(key, false)}
                        >
                          {t(($) => $.model_configuration.remove)}
                        </Button>
                      </div>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
            <div className="grid items-end gap-3 sm:grid-cols-2">
              <Input
                aria-label={t(($) => $.model_configuration.search_keys)}
                placeholder={t(($) => $.model_configuration.search_keys)}
                value={search}
                onChange={(e) => {
                  setSearch(e.target.value);
                  setPage(1);
                }}
              />
              <ModelChoice
                label={t(($) => $.model_configuration.allowed_runtimes)}
                value={runtime}
                options={[
                  {
                    value: "",
                    label: t(($) => $.model_configuration.all_runtimes),
                  },
                  ...modelRuntimeKinds.map((kind) => ({
                    value: kind,
                    label: kind,
                  })),
                ]}
                onChange={(value) => {
                  setRuntime(value);
                  setPage(1);
                }}
              />
            </div>
            <ModelError error={data.directory.error} />
            <div className="overflow-hidden rounded-lg border">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead className="w-10" />
                    <TableHead>
                      {t(($) => $.model_configuration.key_name)}
                    </TableHead>
                    <TableHead>
                      {t(($) => $.model_configuration.allowed_runtimes)}
                    </TableHead>
                    <TableHead>
                      {t(($) => $.model_configuration.status)}
                    </TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {data.directory.data?.items.map((key) => (
                    <TableRow key={key.id}>
                      <TableCell>
                        <Checkbox
                          aria-label={`${key.apiKeyName} #${key.id}`}
                          checked={selected.some((k) => k.id === key.id)}
                          disabled={
                            busy ||
                            data.grants.isPending ||
                            !!data.grants.error ||
                            key.status !== "enabled" ||
                            !key.allowedRuntimes.some((k) =>
                              modelRuntimeKinds.some((kind) => kind === k),
                            )
                          }
                          onCheckedChange={(checked) => toggle(key, checked)}
                        />
                      </TableCell>
                      <TableCell>
                        {key.apiKeyName}{" "}
                        <span className="text-caption text-muted-foreground">
                          #{key.id}
                        </span>
                      </TableCell>
                      <TableCell>
                        {key.allowedRuntimes.join(", ") || "—"}
                      </TableCell>
                      <TableCell>
                        <ModelStatus status={key.status} />
                      </TableCell>
                    </TableRow>
                  ))}
                  {data.directory.data?.items.length === 0 && (
                    <TableRow>
                      <TableCell colSpan={4}>
                        {t(($) => $.model_configuration.empty)}
                      </TableCell>
                    </TableRow>
                  )}
                </TableBody>
              </Table>
            </div>
            <div className="flex flex-wrap justify-between gap-3">
              <div className="flex items-center gap-3">
                <Button
                  variant="outline"
                  size="sm"
                  disabled={page === 1 || data.directory.isFetching}
                  onClick={() => setPage(page - 1)}
                >
                  {t(($) => $.model_configuration.previous)}
                </Button>
                <span>{page}</span>
                <Button
                  variant="outline"
                  size="sm"
                  disabled={
                    !data.directory.data?.hasMore || data.directory.isFetching
                  }
                  onClick={() => setPage(page + 1)}
                >
                  {t(($) => $.model_configuration.next)}
                </Button>
              </div>
              <Button
                disabled={busy || draft === null || !!data.grants.error}
                onClick={() =>
                  void perform(
                    () =>
                      data.saveGrants.mutateAsync(selected.map((k) => k.id)),
                    () => setDraft(null),
                  )
                }
              >
                {t(($) => $.model_configuration.save)}
              </Button>
            </div>
          </div>
        )}
      </section>
    </div>
  );
}
