import "../global.css";

import { useEffect, useRef } from "react";
import { Stack, router } from "expo-router";
import { StatusBar } from "expo-status-bar";
import { GestureHandlerRootView } from "react-native-gesture-handler";
import { SafeAreaProvider } from "react-native-safe-area-context";
import { KeyboardProvider } from "react-native-keyboard-controller";
import { QueryClientProvider, useQueryClient } from "@tanstack/react-query";
import { ThemeProvider } from "@react-navigation/native";
import { PortalHost } from "@rn-primitives/portal";
import { I18nextProvider } from "react-i18next";
import i18n from "i18next";
import { api } from "@/data/api";
import { queryClient } from "@/data/query-client";
import { useAuthStore } from "@/data/auth-store";
import { useWorkspaceStore } from "@/data/workspace-store";
import { LightboxProvider, prewarmHighlighter } from "@/lib/markdown";
import { NAV_THEME } from "@/lib/theme";
import { useColorScheme } from "@/lib/use-color-scheme";
import { initI18n } from "@/lib/i18n";

// 在模块顶层初始化 i18n（同步，initAsync: false）。
// 必须在 I18nextProvider 渲染之前调用一次。
initI18n();

// Kick off Shiki highlighter init at module load — fires once per process,
// finishes before the user navigates to any screen with a code block. If
// init fails (engine unavailable) the highlighter falls back to plain
// text; nothing here is allowed to throw.
prewarmHighlighter();

function AuthInitializer({ children }: { children: React.ReactNode }) {
  const initialize = useAuthStore((s) => s.initialize);
  const qc = useQueryClient();
  // Idempotent guard: 401 on multiple in-flight requests would otherwise
  // logout/navigate repeatedly during the same session-expire moment.
  const signingOutRef = useRef(false);

  useEffect(() => {
    // Wire 401 handling onto the shared ApiClient singleton. Must be set
    // before any request fires — initialize() below kicks off the first
    // getMe() call, so do this synchronously first.
    api.setOptions({
      onUnauthorized: () => {
        if (signingOutRef.current) return;
        signingOutRef.current = true;
        void (async () => {
          await useAuthStore.getState().logout();
          await useWorkspaceStore.getState().clear();
          qc.clear();
          router.replace("/login");
          // Reset on next tick so a fresh session can hit 401 again later
          // without being silently swallowed.
          setTimeout(() => {
            signingOutRef.current = false;
          }, 0);
        })();
      },
    });
    initialize();
  }, [initialize, qc]);

  return <>{children}</>;
}

export default function RootLayout() {
  const { colorScheme, isDarkColorScheme } = useColorScheme();
  return (
    <GestureHandlerRootView style={{ flex: 1 }}>
      <SafeAreaProvider>
        {/* RN 0.83 起 Android 强制 edge-to-edge（窗口不再随键盘 resize），
            keyboard-controller 计算键盘动画时须把状态栏/导航栏视为
            translucent，否则 KeyboardStickyView 等 offset 偏一个系统栏
            高度。回退：删去这两个 prop 即恢复旧行为。RUYI-30。 */}
        <KeyboardProvider
          statusBarTranslucent
          navigationBarTranslucent
        >
          <QueryClientProvider client={queryClient}>
            <ThemeProvider value={NAV_THEME[colorScheme]}>
              <I18nextProvider i18n={i18n}>
              <AuthInitializer>
                <LightboxProvider>
                  <StatusBar style={isDarkColorScheme ? "light" : "dark"} />
                  <Stack screenOptions={{ headerShown: false }}>
                    <Stack.Screen name="index" />
                    <Stack.Screen name="(auth)" />
                    <Stack.Screen name="(app)" />
                    {/* 登录前后都可达 —— 未登录用户连自建后端是核心场景。 */}
                    <Stack.Screen name="server-settings" />
                  </Stack>
                  <PortalHost />
                </LightboxProvider>
              </AuthInitializer>
              </I18nextProvider>
            </ThemeProvider>
          </QueryClientProvider>
        </KeyboardProvider>
      </SafeAreaProvider>
    </GestureHandlerRootView>
  );
}
