"use client";

import { createContext, useContext, useState, type ReactNode } from "react";

type TailMeasurement = { layout: string; height: number };
type TailMeasurements = Map<string, TailMeasurement>;
const IssueReferenceLayoutContext = createContext<TailMeasurements | null>(null);

/** Geometry only: lifetime is one Activity page, never a second server-data cache. */
export function IssueReferenceLayoutProvider({ children }: { children: ReactNode }) {
  const [measurements] = useState<TailMeasurements>(() => new Map());
  return (
    <IssueReferenceLayoutContext.Provider value={measurements}>
      {children}
    </IssueReferenceLayoutContext.Provider>
  );
}

export function useIssueReferenceMeasurements(): TailMeasurements | null {
  return useContext(IssueReferenceLayoutContext);
}

export function saveTailMeasurement(
  measurements: TailMeasurements,
  key: string,
  measurement: TailMeasurement,
): void {
  measurements.delete(key);
  measurements.set(key, measurement);
  // Virtualized rows may be revisited, but an Activity page must stay bounded.
  if (measurements.size > 1024) measurements.delete(measurements.keys().next().value!);
}
