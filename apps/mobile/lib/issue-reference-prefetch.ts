export function commentIdsInPrefetchRange<T extends { entry: { id: string; type?: string } }>(
  rows: readonly T[],
  visibleIndexes: readonly (number | null)[],
  distance = 2,
): Set<string> {
  const indexes = new Set<number>();
  for (const visibleIndex of visibleIndexes) {
    if (visibleIndex == null) continue;
    for (let offset = -distance; offset <= distance; offset++) {
      indexes.add(visibleIndex + offset);
    }
  }

  const ids = new Set<string>();
  for (const index of indexes) {
    const row = rows[index];
    if (row?.entry.type === "comment") ids.add(row.entry.id);
  }
  return ids;
}
