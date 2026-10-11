globalThis.IS_REACT_ACT_ENVIRONMENT = true;
global.IS_REACT_ACT_ENVIRONMENT = true;

// AsyncStorage's native module is null under jest-expo, so any suite whose
// import graph reaches data/server-store dies at import time (RUYI-626: the
// voice runtime screens gained a direct data/api import, dragging
// server-store into suites that mock the queries layer). Mock it globally
// with the package's official in-memory mock; per-suite jest.mock
// declarations still take precedence over this.
jest.mock("@react-native-async-storage/async-storage", () =>
  require("@react-native-async-storage/async-storage/jest/async-storage-mock"),
);

// server-store's module scope builds the built-in server entry from
// EXPO_PUBLIC_API_URL and fail-closes when it is absent (same RUYI-626
// import-graph exposure as above). Jest never talks to the API, so any
// well-formed placeholder keeps the module loadable; an explicitly set
// value always wins.
process.env.EXPO_PUBLIC_API_URL ??= "http://localhost:21801";

// RUYI-694: the voice orb/overlay surfaces (and the chat input area)
// import react-native-reanimated, whose worklets runtime needs the native
// counterpart jest doesn't have (WorkletsError at import time — even the
// package's official /mock entry loads it on reanimated 4.x). Provide the
// minimal JS surface the component tree uses: entering/exiting builders
// become no-ops, shared values stay plain objects. Per-suite jest.mock
// declarations still take precedence over this.
jest.mock("react-native-reanimated", () => {
  const { View } = require("react-native");
  return {
    __esModule: true,
    default: { View },
    View,
    FadeIn: { duration: () => ({}) },
    FadeOut: { duration: () => ({}) },
    SlideInDown: { duration: () => ({}) },
    SlideOutDown: { duration: () => ({}) },
    useSharedValue: (initial: unknown) => ({ value: initial }),
    useAnimatedStyle: () => ({}),
    useDerivedValue: (fn: () => unknown) => ({ value: fn() }),
    withRepeat: () => 0,
    withTiming: () => 0,
    withDelay: () => 0,
    withSpring: () => 0,
    cancelAnimation: () => {},
    Easing: {
      linear: (v: unknown) => v,
      ease: (v: unknown) => v,
      in: (e: unknown) => e,
      out: (e: unknown) => e,
      inOut: (e: unknown) => e,
    },
    runOnJS: (fn: unknown) => fn,
  };
});
