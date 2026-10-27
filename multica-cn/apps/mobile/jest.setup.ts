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

// Reanimated worklets have no native side under jest-expo, so any suite
// whose import graph reaches an animated component dies at render time
// (RUYI-698: the SheetHeader component made every self-drawn sheet header
// an animated element). Globally fall back to an inert animated layer —
// native driver is exactly what jest cannot exercise; per-suite jest.mock
// declarations still take precedence over this.
jest.mock("react-native-reanimated", () => {
  const React = jest.requireActual("react") as typeof import("react");
  const { View } = jest.requireActual("react-native") as typeof import("react-native");
  // A real component with a displayName — NativeWind's css-interop wrapper
  // reads it for every animated JSX element (same shape as the per-suite
  // mock in agent-task-cancel-and-history.test.tsx).
  const AnimatedView = React.forwardRef(function AnimatedView(
    props: React.ComponentPropsWithoutRef<typeof View>,
    ref: React.Ref<React.ComponentRef<typeof View>>,
  ) {
    const { children, ...rest } = props;
    return React.createElement(View, { ...rest, ref }, children);
  });
  AnimatedView.displayName = "AnimatedView";
  return {
    __esModule: true,
    default: Object.assign(AnimatedView, {
      View: AnimatedView,
      create: () => AnimatedView,
    }),
    View: AnimatedView,
    useSharedValue: (initial: number) => ({ value: initial }),
    useAnimatedStyle: () => ({}),
    useAnimatedReaction: () => {},
    withRepeat: (animation: unknown) => animation,
    withTiming: (value: unknown) => value,
    runOnJS: (fn: unknown) => fn,
    // RUYI-694 merge union: the voice orb/overlay and the chat input area
    // also use the entering/exiting builders, derived values, springs and
    // easing curves — kept from the RUYI-694 side so one global fallback
    // serves both suites (per-suite jest.mock still takes precedence).
    FadeIn: { duration: () => ({}) },
    FadeOut: { duration: () => ({}) },
    SlideInDown: { duration: () => ({}) },
    SlideOutDown: { duration: () => ({}) },
    useDerivedValue: (fn: () => unknown) => ({ value: fn() }),
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
  };});
