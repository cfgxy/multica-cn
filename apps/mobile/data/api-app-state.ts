import { AppState } from "react-native";

import { api } from "./api";

// RUYI-567: forward real AppState transitions into the API client so
// fetchRaw can re-check its 30s deadline on foreground entry — the
// setTimeout inside fetchRaw is paused by RN while the app is backgrounded
// (Timing module on host pause) and cannot fire until the app returns.
// Imported once for its side effect in app/_layout.tsx, next to the
// onUnauthorized wiring.
api.setOptions({
  subscribeAppState: (listener) => {
    const subscription = AppState.addEventListener("change", listener);
    return () => subscription.remove();
  },
});
