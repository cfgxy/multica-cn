/**
 * Expanded timeline for one agent run (RUYI-33) — the mobile counterpart of
 * web's `AgentTranscriptDialog` step list, rebuilt on RN primitives.
 *
 * Rows come pre-redacted and pre-paired from `lib/run-detail.ts`
 * (buildRunStepViews): tool_use/tool_result pairs fold into one call row
 * (buildSteps), every summary/detail/copy field already passed
 * redactSecrets, and long bodies are clamped. The component renders — it
 * does not transform transcript data, so no display exit can bypass the
 * redaction layer.
 *
 * Reading hierarchy mirrors web's trace-event-presenter contract:
 * agent text and errors read inline; tool calls collapse to
 * provider-native label + most-informative arg, and open into a detail
 * body (diff / file write / multi-file patch / JSON fallback) plus the
 * paired result. thinking is italic and muted.
 */
import { useState } from "react";
import { View } from "react-native";
import { Ionicons } from "@expo/vector-icons";
import * as Clipboard from "expo-clipboard";
import type {
  DiffLineView,
  RunCallStepView,
  RunDetailBody,
  RunStepView,
} from "@/lib/run-detail";
import { Text } from "@/components/ui/text";
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from "@/components/ui/collapsible";
import { useT } from "@/lib/use-t";
import { useColorScheme } from "@/lib/use-color-scheme";
import { THEME } from "@/lib/theme";

const MUTED_ICON = "#a1a1aa";

export function RunDetailTimeline({ views }: { views: RunStepView[] }) {
  return (
    <View className="gap-0.5">
      {views.map((view) => (
        <StepRow key={view.key} view={view} />
      ))}
    </View>
  );
}

function StepRow({ view }: { view: RunStepView }) {
  switch (view.kind) {
    case "call":
      return <CallRow view={view} />;
    case "thinking":
      return <MessageRow view={view} italic />;
    case "error":
      return <MessageRow view={view} error />;
    default:
      return <MessageRow view={view} />;
  }
}

/** Row header shared by every kind: icon + label + summary + clock. */
function RowHeader({
  icon,
  iconColor,
  label,
  labelMono,
  summary,
  summaryMono,
  clockLabel,
  error,
}: {
  icon: keyof typeof Ionicons.glyphMap;
  iconColor: string;
  label: string;
  labelMono?: boolean;
  summary: string;
  summaryMono?: boolean;
  clockLabel?: string;
  error?: boolean;
}) {
  return (
    <>
      <Ionicons name={icon} size={12} color={iconColor} style={{ marginTop: 2 }} />
      <Text
        className={`text-xs ${labelMono ? "font-mono font-medium" : "font-medium"} ${
          error ? "text-destructive" : "text-foreground"
        }`}
      >
        {label}
      </Text>
      {summary ? (
        <Text
          className={`flex-1 text-xs ${summaryMono ? "font-mono" : ""} ${
            error ? "text-destructive" : "text-muted-foreground"
          }`}
          numberOfLines={1}
        >
          {summary}
        </Text>
      ) : (
        <View className="flex-1" />
      )}
      {clockLabel ? (
        <Text className="ml-1 text-[10px] tabular-nums text-muted-foreground/70">
          {clockLabel}
        </Text>
      ) : null}
    </>
  );
}

function CallRow({ view }: { view: RunStepView & RunCallStepView }) {
  const { t } = useT("issues");
  const result = view.result;
  const hasBody = !!view.detail || !!result;
  if (!hasBody) {
    return (
      <View className="py-0.5 flex-row items-start gap-1.5">
        <RowHeader
          icon="terminal-outline"
          iconColor={MUTED_ICON}
          label={view.label}
          labelMono
          summary={view.summary}
          summaryMono
          clockLabel={view.clockLabel}
        />
      </View>
    );
  }
  return (
    <Collapsible>
      <CollapsibleTrigger asChild>
        <View className="py-0.5 flex-row items-start gap-1.5 active:opacity-70">
          <RowHeader
            icon="terminal-outline"
            iconColor={MUTED_ICON}
            label={view.label}
            labelMono
            summary={view.summary}
            summaryMono
            clockLabel={view.clockLabel}
          />
        </View>
      </CollapsibleTrigger>
      <CollapsibleContent>
        <View className="ml-4 mt-1 gap-1.5">
          {view.detail ? <DetailBody detail={view.detail} /> : null}
          {result ? (
            <View className="rounded bg-muted/40 px-2 py-1.5">
              <Text className="mb-0.5 text-[10px] uppercase tracking-wide text-muted-foreground/70">
                {t("mobile.run_detail.result_label", "Result")}
              </Text>
              <MonoBlock
                text={result.body}
                onCopy={() => void Clipboard.setStringAsync(result.body)}
                copyLabel={t("mobile.run_detail.copy", "Copy")}
              />
              {result.truncated ? <TruncatedNote /> : null}
            </View>
          ) : null}
        </View>
      </CollapsibleContent>
    </Collapsible>
  );
}

function MessageRow({
  view,
  italic,
  error,
}: {
  view: Extract<RunStepView, { kind: "text" | "thinking" | "error" }>;
  italic?: boolean;
  error?: boolean;
}) {
  const icon = error ? "alert-circle" : italic ? "bulb-outline" : "chatbubble-ellipses-outline";
  const iconColor = error ? "#dc2626" : MUTED_ICON;
  if (!view.body.body) {
    return (
      <View className="py-0.5 flex-row items-start gap-1.5">
        <RowHeader
          icon={icon}
          iconColor={iconColor}
          label={view.label}
          summary=""
          clockLabel={view.clockLabel}
          error={error}
        />
      </View>
    );
  }
  return (
    <Collapsible>
      <CollapsibleTrigger asChild>
        <View className="py-0.5 flex-row items-start gap-1.5 active:opacity-70">
          <RowHeader
            icon={icon}
            iconColor={iconColor}
            label={view.label}
            summary={view.summary}
            clockLabel={view.clockLabel}
            error={error}
          />
        </View>
      </CollapsibleTrigger>
      <CollapsibleContent>
        <View className="ml-4 mt-1 rounded bg-muted/40 px-2 py-1.5">
          <Text
            className={`text-xs ${italic ? "italic text-muted-foreground" : error ? "text-destructive" : "text-foreground"}`}
          >
            {view.body.body}
          </Text>
          {view.body.truncated ? <TruncatedNote /> : null}
        </View>
      </CollapsibleContent>
    </Collapsible>
  );
}

// ─── Detail bodies ──────────────────────────────────────────────────────────

function DetailBody({ detail }: { detail: RunDetailBody }) {
  switch (detail.variant) {
    case "diff":
      return <DiffBlock path={detail.path} lines={detail.lines} />;
    case "file":
      return (
        <View className="rounded bg-muted/40 px-2 py-1.5">
          <BlockPath path={detail.path} meta={`${detail.lineCount} lines`} />
          <Text className="font-mono text-xs text-foreground">{detail.text}</Text>
        </View>
      );
    case "patch":
      return (
        <View className="gap-1.5">
          {detail.files.map((file) => (
            <View key={file.path} className="rounded bg-muted/40 px-2 py-1.5">
              <BlockPath
                path={file.movePath ? `${file.path} → ${file.movePath}` : file.path}
                meta={file.changeKind}
              />
              <PatchFileBody body={file.body} />
              {file.truncated ? <TruncatedNote /> : null}
            </View>
          ))}
          {detail.truncated ? <TruncatedNote /> : null}
        </View>
      );
    case "text":
      return (
        <View className="rounded bg-muted/40 px-2 py-1.5">
          <MonoBlock text={detail.text} />
        </View>
      );
  }
}

function PatchFileBody({
  body,
}: {
  body: Extract<RunDetailBody, { variant: "patch" }>["files"][number]["body"];
}) {
  switch (body.kind) {
    case "diff":
      return <DiffLines lines={body.lines} />;
    case "file":
      return <Text className="font-mono text-xs text-foreground">{body.text}</Text>;
    default:
      return null;
  }
}

function BlockPath({ path, meta }: { path: string; meta?: string }) {
  return (
    <View className="mb-1 flex-row items-center gap-1.5">
      <Text className="flex-1 font-mono text-[11px] text-muted-foreground" numberOfLines={2}>
        {path}
      </Text>
      {meta ? (
        <Text className="text-[10px] uppercase tracking-wide text-muted-foreground/70">
          {meta}
        </Text>
      ) : null}
    </View>
  );
}

const ADD = "#16a34a";
const REMOVE = "#dc2626";

function DiffBlock({
  path,
  lines,
}: {
  path: string;
  lines: Extract<RunDetailBody, { variant: "diff" }>["lines"];
}) {
  return (
    <View className="rounded bg-muted/40 px-2 py-1.5">
      <BlockPath path={path} />
      <DiffLines lines={lines} />
    </View>
  );
}

function DiffLines({ lines }: { lines: DiffLineView[] }) {
  const { colorScheme } = useColorScheme();
  const palette = THEME[colorScheme];
  return (
    <View>
      {lines.map((line, i) => {
        if (line.kind === "gap") {
          return (
            <Text key={i} className="font-mono text-xs text-muted-foreground/60">
              {line.hidden ? `⋯ ${line.hidden}` : "⋯"}
            </Text>
          );
        }
        const color =
          line.kind === "add"
            ? ADD
            : line.kind === "remove"
              ? REMOVE
              : palette.foreground;
        return (
          <Text key={i} className="font-mono text-xs" style={{ color }}>
            {line.kind === "add" ? "+" : line.kind === "remove" ? "-" : " "}
            {line.text}
          </Text>
        );
      })}
    </View>
  );
}

function MonoBlock({ text, onCopy, copyLabel }: { text: string; onCopy?: () => void; copyLabel?: string }) {
  const { t } = useT("issues");
  const [copied, setCopied] = useState(false);
  return (
    <View className="gap-1">
      <Text className="font-mono text-xs text-foreground">{text}</Text>
      {onCopy ? (
        <Text
          accessibilityRole="button"
          onPress={() => {
            onCopy();
            setCopied(true);
          }}
          className="self-start text-[11px] font-medium text-brand active:opacity-70"
        >
          {copied ? t("mobile.run_detail.copied", "Copied") : (copyLabel ?? t("mobile.run_detail.copy", "Copy"))}
        </Text>
      ) : null}
    </View>
  );
}

function TruncatedNote() {
  const { t } = useT("issues");
  return (
    <Text className="mt-1 text-[10px] italic text-muted-foreground/70">
      {t("mobile.run_detail.truncated", "… (truncated)")}
    </Text>
  );
}
