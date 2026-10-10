import { AppState, Platform } from "react-native";

import { api } from "./api";

// RUYI-567: forward real AppState transitions into the API client so
// fetchRaw can re-check its 30s deadline on foreground entry — the
// setTimeout inside fetchRaw is paused by RN while the app is backgrounded
// (Timing module on host pause) and cannot fire until the app returns.
// Imported once for its side effect in app/_layout.tsx, next to the
// onUnauthorized wiring.
// RUYI-576: 同一缝线注入 Platform.OS，让 X-Client-OS 上报真实运行平台，
// 替代曾经的 "ios" 硬编码（Android 设备流量曾被服务端记为 iOS）。
api.setOptions({
  clientOS: Platform.OS,
  subscribeAppState: (listener) => {
    const subscription = AppState.addEventListener("change", listener);
    return () => subscription.remove();
  },
});
