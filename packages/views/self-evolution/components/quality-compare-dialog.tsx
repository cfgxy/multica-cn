"use client";

import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@multica/ui/components/ui/table";
import { PROMPT_QUALITY_DIMENSIONS, toMeasureView } from "@multica/core/self-evolution";
import type { MeasureView } from "@multica/core/self-evolution";
import type { PromptQualityVersionMeasures } from "@multica/core/types";
import { useT, useLocale } from "../../i18n";
import { formatMeasureDelta, formatMeasureValue } from "./quality-format";

/**
 * Two versions, seven dimensions, side by side (RUYI-184 UIUX §3.5).
 *
 * A side with no measurement prints its own state word, and the delta column
 * prints a dash: subtracting an absent measurement from a present one would
 * manufacture a trend out of one data point.
 */
export function QualityCompareDialog({
  left,
  right,
  open,
  onOpenChange,
}: {
  left: PromptQualityVersionMeasures | null;
  right: PromptQualityVersionMeasures | null;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const { t } = useT("self-evolution");
  const locale = useLocale();

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>{t(($) => $.quality.compare.title)}</DialogTitle>
          <DialogDescription>{t(($) => $.quality.compare.description)}</DialogDescription>
        </DialogHeader>

        {left === null || right === null ? null : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t(($) => $.quality.compare.dimension)}</TableHead>
                <TableHead className="text-right">
                  {t(($) => $.quality.timeline.version, { version: left.version })}
                </TableHead>
                <TableHead className="text-right">
                  {t(($) => $.quality.timeline.version, { version: right.version })}
                </TableHead>
                <TableHead className="text-right">{t(($) => $.quality.compare.delta)}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {PROMPT_QUALITY_DIMENSIONS.map((key) => {
                const a = toMeasureView(left.measures[key]);
                const b = toMeasureView(right.measures[key]);
                const aValue = a.state === "ok" ? a.value : null;
                const bValue = b.state === "ok" ? b.value : null;
                const delta = formatMeasureDelta(key, aValue, bValue, locale);
                return (
                  <TableRow key={key}>
                    <TableCell>{t(($) => $.quality.dimensions[key].label)}</TableCell>
                    <TableCell className="text-right tabular-nums">
                      {aValue === null
                        ? stateWord(t, a)
                        : formatMeasureValue(key, aValue, locale)}
                    </TableCell>
                    <TableCell className="text-right tabular-nums">
                      {bValue === null
                        ? stateWord(t, b)
                        : formatMeasureValue(key, bValue, locale)}
                    </TableCell>
                    <TableCell className="text-right tabular-nums">
                      {delta ?? t(($) => $.quality.compare.noDelta)}
                    </TableCell>
                  </TableRow>
                );
              })}
            </TableBody>
          </Table>
        )}
      </DialogContent>
    </Dialog>
  );
}

function stateWord(t: ReturnType<typeof useT<"self-evolution">>["t"], view: MeasureView): string {
  return view.state === "insufficient_sample"
    ? t(($) => $.quality.measure.insufficient)
    : t(($) => $.quality.measure.noData);
}
