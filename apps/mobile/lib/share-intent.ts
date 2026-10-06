/**
 * Typed wrapper over the local `modules/share-intent` native module
 * (Android only, RUYI-463). The module copies share streams into our cache
 * dir and hands back { id, files }; all validation lives in
 * lib/share-payload.ts so this file stays a thin bridge shim.
 */
import { NativeModule, requireNativeModule } from "expo-modules-core";
import type { RawShareFile } from "./share-payload";

/** Payload shape produced by ShareIntentModule.kt — `files` is re-validated
 *  by normalizeSharePayload before anything downstream trusts it. */
export interface RawShareIntentPayload {
  id: string;
  files: RawShareFile[];
}

declare class ShareIntentNativeModule extends NativeModule<{
  onShareIntent: (payload: RawShareIntentPayload) => void;
}> {
  /** Cold start: share payload from the activity's launch intent, or a
   *  buffered payload that raced ahead of the JS subscription. Resolves
   *  null when there is nothing to land on. */
  getInitialShare(): Promise<RawShareIntentPayload | null>;
}

export default requireNativeModule<ShareIntentNativeModule>("ShareIntent");
