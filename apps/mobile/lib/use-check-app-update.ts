/**
 * Shared "Check for Updates" interaction (RUYI-36).
 *
 * One hook, two entry points: the More-tab dropdown row and the settings
 * About section. Runs `checkAppUpdate` (anonymous GitHub Releases call —
 * see `lib/app-update.ts` for the serverless design and failure reasons)
 * and surfaces the outcome as a native `Alert.alert` in exactly three
 * states:
 *
 *   up_to_date        → informational alert, system OK button
 *   update_available  → confirm alert; "Download APK" opens the Release
 *                       asset URL in the system browser. The browser handles
 *                       the download + Android 8+ unknown-sources flow, so
 *                       this app needs no install permission of its own.
 *   failed            → degraded alert with a per-reason message; never
 *                       blocks or crashes the surrounding UI.
 *
 * The installed version comes from `Constants.expoConfig?.version` (the
 * `version` field in `app.config.ts`, baked into Android versionName by
 * prebuild). Read at call time, not module load, so tests and previews see
 * the current config.
 */
import { useCallback } from "react";
import { Alert, Linking } from "react-native";
import Constants from "expo-constants";
import {
  checkAppUpdate,
  type UpdateCheckOutcome,
} from "@/lib/app-update";
import { useT } from "@/lib/use-t";

export function useAppUpdate() {
  // useT 而非 useTranslation：lib/i18n-keys.test.ts 的 ns 绑定扫描只认
  // useT("ns")，绑错会把本文件的 t() 全部归到 common ns 报红。
  const { t } = useT("settings");

  const currentVersion = Constants.expoConfig?.version ?? "";

  const showOutcome = useCallback(
    (outcome: UpdateCheckOutcome) => {
      if (outcome.status === "up_to_date") {
        Alert.alert(
          t("mobile.update.up_to_date_title", "You're up to date"),
          t("mobile.update.up_to_date_body", {
            defaultValue: "Multica {{version}} is the latest version.",
            version: outcome.latestVersion,
          }),
        );
        return;
      }
      if (outcome.status === "update_available") {
        Alert.alert(
          t("mobile.update.available_title", "Update available"),
          t("mobile.update.available_body", {
            defaultValue:
              "Version {{version}} is available (installed: {{current}}).",
            version: outcome.latestVersion,
            current: currentVersion,
          }),
          [
            { text: t("common:cancel", "Cancel"), style: "cancel" },
            {
              text: t("mobile.update.download", "Download APK"),
              onPress: () => {
                void Linking.openURL(outcome.apkUrl).catch(() => {
                  // 直链打不开（无浏览器/URL 被拦）时降级到 Release 页。
                  if (outcome.releaseUrl) {
                    void Linking.openURL(outcome.releaseUrl).catch(() => {});
                  }
                });
              },
            },
          ],
        );
        return;
      }
      // 失败文案逐 reason 内联字面量（而非 key→文案映射表），让
      // lib/i18n-keys.test.ts 的静态扫描覆盖每一个调用点。
      let failureBody: string;
      switch (outcome.reason) {
        case "invalid_local_version":
          failureBody = t(
            "mobile.update.failed_invalid_local",
            "Couldn't read the installed version.",
          );
          break;
        case "network":
          failureBody = t(
            "mobile.update.failed_network",
            "No network connection. Try again later.",
          );
          break;
        case "rate_limited":
          failureBody = t(
            "mobile.update.failed_rate_limited",
            "GitHub is rate-limiting requests. Try again in a minute.",
          );
          break;
        case "no_release":
          failureBody = t(
            "mobile.update.failed_no_release",
            "No release has been published yet.",
          );
          break;
        case "no_apk_asset":
          failureBody = t(
            "mobile.update.failed_no_apk",
            "The latest release doesn't include an Android package yet.",
          );
          break;
        case "incomparable":
          failureBody = t(
            "mobile.update.failed_incomparable",
            "Couldn't compare the installed version with the latest release.",
          );
          break;
        case "unexpected":
          failureBody = t(
            "mobile.update.failed_unexpected",
            "Something went wrong. Try again later.",
          );
          break;
      }
      Alert.alert(
        t("mobile.update.failed_title", "Couldn't check for updates"),
        failureBody,
      );
    },
    [t, currentVersion],
  );

  const checkForUpdates = useCallback(() => {
    void checkAppUpdate({ localVersion: currentVersion }).then(showOutcome);
  }, [currentVersion, showOutcome]);

  return { currentVersion, checkForUpdates };
}
