/**
 * GFM 表格（RUYI-72 手机端）。
 *
 * 表格原本整块交给 enriched-markdown 的原生 table 渲染，宽表格在手机竖屏
 * 下只能横向滚动，尾部列被藏起来。enriched 不允许为任何叶子节点注入 React
 * （见 `split-markdown.ts` 顶部的 maintainer 立场），所以行上挂不了手势——
 * 只能像 code / image / mermaid 一样把 table 拆出来自研。
 *
 *   ┌─────────────────────────────┐
 *   │ 姓名   │ 角色   │ …         │  ← 表头行，随内容横向滚动
 *   ├─────────────────────────────┤
 *   │ 甲     │ 开发   │ …         │  ← 点击任意数据行 → 行详情面板
 *   └─────────────────────────────┘
 *
 * 行详情面板把该行重新表达成「表头 → 单元格」的字段列表，覆盖全部列，
 * 底部上一行/下一行翻行，首尾禁用不回绕——与 web 的行详情同一产品语义，
 * 共用 `editor` 命名空间下的 `table.row_detail.*` 文案。
 *
 * 与 web 的有意差异（apps/mobile/CLAUDE.md §Behavioral parity 要求写明）：
 *
 *   1. **载体**：web 是居中 Dialog + ←/→ 键盘翻页；手机没有键盘，用 RN
 *      `<Modal>` 底部面板 + 两枚翻行按钮，与既有 `components/ui/
 *      action-sheet.tsx` 的 Android 面板同构（Principle 1「existing pattern
 *      first」）。面板高度不设固定像素：外层 `justify-end` + 面板
 *      `flexShrink`，内容少时贴内容、内容多时最多吃满安全区内的可用高度，
 *      不会在小屏上裁切。
 *   2. **单元格富文本**：网格内的单元格走 `stripMarkdown` 显示纯文本（表格
 *      格子窄，每格塞一个 enriched 原生实例既慢又会把行高撑乱），面板里的
 *      字段值才用 `EnrichedMarkdownText` 渲染完整 markdown。字段值里的链接
 *      走调用方传下来的同一个 `onLinkPress`，与散文里的 http(s) /
 *      `mention://` / 附件语义完全一致（决策见 `link-route.ts`）。
 */
import { useCallback, useMemo, useState } from "react";
import { Modal, Pressable, ScrollView, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { EnrichedMarkdownText } from "react-native-enriched-markdown";
import Svg, { Path } from "react-native-svg";
import { Text } from "@/components/ui/text";
import { THEME } from "@/lib/theme";
import { useColorScheme } from "@/lib/use-color-scheme";
import { useT } from "@/lib/use-t";
import { stripMarkdown } from "@/lib/strip-markdown";
import { useMarkdownStyle } from "./markdown-style";
import { buildTableRowFields, stepTableRow } from "./table-row-detail";
import type { MarkdownTableCell } from "./split-markdown";

/** 单列最小宽度：窄于此中文两字就要折行，表格会被撑成竖条。 */
const MIN_COLUMN_WIDTH = 96;

/** 面板顶部至少留出的可点击遮罩高度（安全区之外再留一段，避免面板顶到
 *  刘海下沿看不出还能点空白关闭）。 */
const MIN_SCRIM_HEIGHT = 24;

type LinkPressHandler = (event: { url: string }) => void;

interface Props {
  headers: MarkdownTableCell[];
  rows: MarkdownTableCell[][];
  align: (("left" | "center" | "right") | null)[];
  /** 与散文实例共用的链接处理器，保证表格内链接行为不发生分叉。 */
  onLinkPress?: LinkPressHandler;
  /** 同 `Markdown` 的 `selectable`：宿主自带 onLongPress 时需关掉原生选择。 */
  selectable?: boolean;
}

export function TableBlock({
  headers,
  rows,
  align,
  onLinkPress,
  selectable = true,
}: Props) {
  const { t } = useT("editor");
  const { isDarkColorScheme } = useColorScheme();
  const theme = isDarkColorScheme ? THEME.dark : THEME.light;
  const [openRow, setOpenRow] = useState<number | null>(null);

  const fields = useMemo(
    () =>
      buildTableRowFields({
        headers,
        rows,
        unnamedColumnLabel: (index) =>
          t("table.row_detail.unnamed_column", "Column {{index}}", { index }),
      }),
    [headers, rows, t],
  );

  const textAlign = (column: number) => align[column] ?? "left";

  return (
    <View>
      <ScrollView
        horizontal
        showsHorizontalScrollIndicator={false}
        // RN 横向 ScrollView 默认 flexGrow:1，在可回收的 FlashList 单元格里
        // 会让子节点撑破单元格高度——与 code-block.tsx 同样的规避。
        style={{ flexGrow: 0 }}
      >
        <View
          className="rounded-lg border border-border overflow-hidden"
          style={{ borderColor: theme.border }}
        >
          <View className="flex-row" style={{ backgroundColor: theme.surface2 }}>
            {headers.map((header, column) => (
              <View
                key={column}
                className="px-2.5 py-1.5"
                style={{ minWidth: MIN_COLUMN_WIDTH }}
              >
                <Text
                  className="text-sm font-semibold text-foreground"
                  style={{ textAlign: textAlign(column) }}
                >
                  {stripMarkdown(header.text)}
                </Text>
              </View>
            ))}
          </View>
          {rows.map((row, index) => (
            <Pressable
              key={index}
              onPress={() => setOpenRow(index)}
              accessibilityRole="button"
              accessibilityLabel={t(
                "table.row_detail.position",
                "Row {{index}} of {{total}}",
                { index: index + 1, total: rows.length },
              )}
              className="flex-row active:bg-secondary"
              style={{ borderTopWidth: 1, borderTopColor: theme.border }}
            >
              {row.map((cell, column) => (
                <View
                  key={column}
                  className="px-2.5 py-1.5"
                  style={{ minWidth: MIN_COLUMN_WIDTH }}
                >
                  <Text
                    className="text-sm text-foreground"
                    style={{ textAlign: textAlign(column) }}
                    // 网格只负责定位行，长内容截断即可——完整内容在面板里。
                    numberOfLines={2}
                  >
                    {stripMarkdown(cell.text)}
                  </Text>
                </View>
              ))}
            </Pressable>
          ))}
        </View>
      </ScrollView>
      <TableRowDetailSheet
        fields={fields}
        row={openRow}
        onRowChange={setOpenRow}
        onClose={() => setOpenRow(null)}
        onLinkPress={onLinkPress}
        selectable={selectable}
      />
    </View>
  );
}

function TableRowDetailSheet({
  fields,
  row,
  onRowChange,
  onClose,
  onLinkPress,
  selectable,
}: {
  fields: { label: string; value: string }[][];
  /** null = 关闭；非空即当前显示的行下标。 */
  row: number | null;
  onRowChange: (row: number) => void;
  onClose: () => void;
  onLinkPress?: LinkPressHandler;
  selectable: boolean;
}) {
  const { t } = useT("editor");
  const insets = useSafeAreaInsets();
  const { isDarkColorScheme } = useColorScheme();
  const theme = isDarkColorScheme ? THEME.dark : THEME.light;
  const markdownStyle = useMarkdownStyle();

  const total = fields.length;
  const step = useCallback(
    (delta: number) => {
      if (row === null) return;
      onRowChange(stepTableRow(row, delta, total));
    },
    [row, total, onRowChange],
  );

  if (row === null) return null;
  const rowFields = fields[row] ?? [];

  return (
    <Modal visible transparent animationType="fade" onRequestClose={onClose}>
      {/* 高度全部交给 flex：遮罩 flexGrow 吃掉多余空间，面板 flexShrink 在
          内容超高时被压到「安全区内的可用高度」，因此既不留白也不裁切。 */}
      <View className="flex-1 justify-end">
        <Pressable
          className="flex-1 bg-black/40"
          style={{ minHeight: insets.top + MIN_SCRIM_HEIGHT }}
          onPress={onClose}
        />
        <View
          className="rounded-t-2xl overflow-hidden"
          style={{ backgroundColor: theme.background, flexShrink: 1 }}
        >
          <View
            className="px-4 py-3"
            style={{ borderBottomWidth: 1, borderBottomColor: theme.border }}
          >
            <Text className="text-sm font-semibold text-foreground text-center">
              {t("table.row_detail.position", "Row {{index}} of {{total}}", {
                index: row + 1,
                total,
              })}
            </Text>
          </View>
          <ScrollView style={{ flexShrink: 1 }}>
            {rowFields.map((field, column) => (
              <View
                key={column}
                className="px-4 py-2.5"
                style={
                  column > 0
                    ? { borderTopWidth: 1, borderTopColor: theme.border }
                    : undefined
                }
              >
                <Text className="text-xs text-muted-foreground mb-1">
                  {stripMarkdown(field.label)}
                </Text>
                {field.value.trim() === "" ? (
                  <Text className="text-sm text-muted-foreground">—</Text>
                ) : (
                  <EnrichedMarkdownText
                    flavor="github"
                    markdown={field.value}
                    markdownStyle={markdownStyle}
                    onLinkPress={onLinkPress}
                    selectable={selectable}
                  />
                )}
              </View>
            ))}
          </ScrollView>
          <View
            className="flex-row items-center justify-between px-4 py-3"
            style={{ borderTopWidth: 1, borderTopColor: theme.border }}
          >
            <StepButton
              label={t("table.row_detail.prev_row", "Previous row")}
              direction="prev"
              disabled={row === 0}
              onPress={() => step(-1)}
              color={theme.foreground}
              mutedColor={theme.mutedForeground}
            />
            <StepButton
              label={t("table.row_detail.next_row", "Next row")}
              direction="next"
              disabled={row === total - 1}
              onPress={() => step(1)}
              color={theme.foreground}
              mutedColor={theme.mutedForeground}
            />
          </View>
          <View style={{ height: insets.bottom }} />
        </View>
      </View>
    </Modal>
  );
}

function StepButton({
  label,
  direction,
  disabled,
  onPress,
  color,
  mutedColor,
}: {
  label: string;
  direction: "prev" | "next";
  disabled: boolean;
  onPress: () => void;
  color: string;
  mutedColor: string;
}) {
  // 首尾置灰而非回绕：标题里的「第 N / M 行」若无提示地跳回首行，用户读不
  // 出发生了什么（与 web dialog 的 disabled 处理一致）。
  const tint = disabled ? mutedColor : color;
  const chevron = <Chevron direction={direction} color={tint} />;
  return (
    <Pressable
      onPress={onPress}
      disabled={disabled}
      hitSlop={8}
      accessibilityRole="button"
      accessibilityState={{ disabled }}
      accessibilityLabel={label}
      className="flex-row items-center gap-1 px-3 py-1.5"
    >
      {direction === "prev" ? chevron : null}
      <Text className="text-sm" style={{ color: tint }}>
        {label}
      </Text>
      {direction === "next" ? chevron : null}
    </Pressable>
  );
}

function Chevron({
  direction,
  color,
}: {
  direction: "prev" | "next";
  color: string;
}) {
  // react-native-svg 的图元不吃 NativeWind className，颜色由父级从 THEME 传入
  // （与 code-block.tsx 的图标同一处理）。
  return (
    <Svg width={14} height={14} viewBox="0 0 16 16" fill="none">
      <Path
        d={direction === "prev" ? "M10 3.5L5.5 8L10 12.5" : "M6 3.5L10.5 8L6 12.5"}
        stroke={color}
        strokeWidth={1.6}
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </Svg>
  );
}
