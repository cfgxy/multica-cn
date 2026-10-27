/**
 * Cache-copy cleanup for dismissed shares (RUYI-463). When the user backs
 * out of the share landing page the copied files were never handed to a
 * composer, so nothing references them — delete the native copies instead
 * of waiting for the OS to reap our cache dir.
 *
 * Never call this once the files were enqueued into a composer: uploads read
 * from the same paths (same lifetime contract as expo-document-picker's
 * cache copies, which are also left for the OS to clear).
 */
import * as FileSystem from "expo-file-system";
import type { SharedFile } from "./share-payload";

export async function discardSharedFiles(files: SharedFile[]): Promise<void> {
  await Promise.allSettled(
    files.map((file) =>
      FileSystem.deleteAsync(file.uri.replace(/^file:\/\//, ""), { idempotent: true }),
    ),
  );
}
