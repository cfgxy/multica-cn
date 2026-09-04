import type { ExpoConfig, ConfigContext } from "expo/config";

/**
 * Dynamic Expo config — replaces app.json so we can read APP_ENV at runtime
 * and switch bundleIdentifier / display name for dev / staging / production.
 *
 * APP_ENV is set by package.json scripts:
 *   - dev          → APP_ENV unset (treated as "development")
 *   - dev:staging  → APP_ENV=staging
 *   - dev:prod     → APP_ENV=production (rare; usually only for EAS build)
 */
export default ({ config }: ConfigContext): ExpoConfig => {
  const env = process.env.APP_ENV ?? "development";
  const isProd = env === "production";
  const isStaging = env === "staging";

  return {
    ...config,
    name: isProd
      ? "Multica"
      : isStaging
        ? "Multica (Staging)"
        : "Multica (Dev)",
    slug: "multica-mobile",
    version: "0.1.0",
    orientation: "portrait",
    userInterfaceStyle: "automatic",
    scheme: "multica",
    // 1024x1024 source shared with the desktop client
    // (apps/desktop/build/icon.png). Expo prebuild generates every required
    // iOS icon size from this single PNG.
    icon: "./assets/icon.png",
    ios: {
      // Expo keeps the top-level portrait policy for iPhone while adding all
      // iPad orientations required for multitasking when tablet support is on.
      supportsTablet: true,
      // Pins DEVELOPMENT_TEAM on every prebuild. Leaving it unset is the normal
      // path — `expo run:ios` then resolves a signing identity from the Keychain
      // itself, which is right when the Apple ID owns exactly one team. With
      // several (a personal team plus an employer's) it takes the *first*
      // identity found whenever the terminal is non-interactive, writes that
      // choice into the generated ios/, and never clears it again: prebuild only
      // writes DEVELOPMENT_TEAM when a value is present, so a project pinned to
      // the wrong team stays wrong until ios/ is deleted. Setting this re-applies
      // the intended team on every `scripts/ios-run.sh` run, which also repairs
      // an already-mispinned checkout.
      appleTeamId: process.env.EXPO_APPLE_TEAM_ID,
      // 统一包名（Owner 2026-09-01 裁决）：所有环境共用 `ai.multica.mobile`，
      // 不再有 per-variant 身份分叉——变体只差服务器配置与显示名，同包名
      // 覆盖安装即切换。`EXPO_BUNDLE_IDENTIFIER` 是自建副本的单点出口
      // （Apple 只允许签名自己拥有的前缀，默认前缀被认领时换一个反向域
      // 名）；分环境包名废除后，环境变量"泄漏到其他变体"不再是问题。
      bundleIdentifier:
        process.env.EXPO_BUNDLE_IDENTIFIER ?? "ai.multica.mobile",
    },
    android: {
      // 与 iOS 同一套统一包名：环境变量为自建副本的单点覆盖出口。
      package: process.env.EXPO_ANDROID_PACKAGE ?? "ai.multica.mobile",
      // 复用与 iOS 相同的 1024 源图标;Android 12+ 实际展示的是
      // adaptiveIcon,monochromeImage 供主题图标(Material You)使用。
      adaptiveIcon: {
        foregroundImage: "./assets/adaptive-icon.png",
        backgroundColor: "#1C212C",
      },
    },
    plugins: [
      "expo-router",
      "expo-secure-store",
      // RUYI-37: 系统通知。plugin 注入 Android POST_NOTIFICATIONS 声明与
      // 本地通知所需 manifest 条目；无 FCM/远程推送（后续单）。
      "expo-notifications",
      "@react-native-community/datetimepicker",
      "react-native-enriched-markdown",
      [
        "expo-image-picker",
        {
          // iOS NSPhotoLibraryUsageDescription. Without this string in
          // Info.plist, calling launchImageLibraryAsync hard-crashes on
          // iOS 14+. Camera + microphone are disabled — we only ever read
          // from the existing photo library.
          photosPermission:
            "Allow Multica to access your photos to attach images to issues and comments.",
          cameraPermission: false,
          microphonePermission: false,
        },
      ],
      [
        "expo-build-properties",
        {
          ios: {
            buildReactNativeFromSource: true,
          },
          android: {
            // 自定义服务器地址由用户在运行时添加,无法用域名白名单覆盖。
            // 产品语义允许用户确认弱警告后访问 http 自建服务;显式开启
            // Android cleartext,HTTPS 仍由 TLS 校验保护。若产品以后改为
            // 禁止明文,删除此项并重新 prebuild/release 即可回退到 Android
            // 默认拒绝策略。
            usesCleartextTraffic: true,
          },
        },
      ],
      // android/ 是 prebuild 产物且被 gitignore,release 签名配置只能靠 plugin
      // 每次重新注入。凭据从构建机的 Gradle 属性读取,不进仓库。
      "./plugins/with-android-release-signing",
    ],
    extra: { APP_ENV: env },
  };
};
