/**
 * Expo config plugin — 给 MainActivity 声明系统分享入口（RUYI-463）。
 *
 * 为什么必须是 plugin 而不是直接改 android/app/src/main/AndroidManifest.xml：
 * android/ 是 `expo prebuild` 的产物且被 .gitignore 忽略，手改的 intent-filter
 * 在下一次 prebuild（或换台机器 clone）时整个消失。写成 plugin 才能让
 * 「出现在系统分享面板」成为可复现的仓库行为（与 with-android-release-signing
 * 同一理由）。
 *
 * 注入一个 intent-filter 声明 SEND + SEND_MULTIPLE 两个 action，mimeType
 * 匹配任意类型 —— 文件类分享（图片/视频/任意文档）都会列出 Multica。纯
 * 文本分享（只有 EXTRA_TEXT、无 EXTRA_STREAM）也会命中过滤器，但原生模块
 * 对无流负载返回 null、不进落地页：文件为中心的语义下，点击无动作且无
 * 崩溃，文本注入属后续单。
 */
const { withAndroidManifest } = require("@expo/config-plugins");

const SHARE_INTENT_ACTIONS = [
  "android.intent.action.SEND",
  "android.intent.action.SEND_MULTIPLE",
];

/**
 * 幂等地把分享 intent-filter 追加到 `.MainActivity`。manifest 是 prebuild
 * 解析后的 XML 对象树，属性挂在 `$` 下。上游模板改动导致锚点失配时抛错
 * 而不是静默跳过 —— 与签名插件同一 fail-fast 约定。
 */
function addShareIntentFilters(manifest) {
  const applications = manifest?.manifest?.application;
  if (!Array.isArray(applications) || applications.length === 0) {
    throw new Error(
      "[with-android-share-intent] 未在 AndroidManifest 中找到 application 节点;" +
        "上游模板可能已变更,请更新本插件的锚点。",
    );
  }
  const application = applications[0];
  const activities = application.activity ?? [];
  const mainActivity = activities.find((a) =>
    (a.$?.["android:name"] ?? "").endsWith(".MainActivity"),
  );
  if (!mainActivity) {
    throw new Error(
      "[with-android-share-intent] 未在 AndroidManifest 中找到 .MainActivity;" +
        "上游模板可能已变更,请更新本插件的锚点。",
    );
  }
  const filters = mainActivity["intent-filter"] ?? [];
  const alreadyDeclared = filters.some((f) =>
    (f.action ?? []).some(
      (a) => a.$?.["android:name"] === "android.intent.action.SEND",
    ),
  );
  if (alreadyDeclared) return manifest;

  mainActivity["intent-filter"] = [
    ...filters,
    {
      action: SHARE_INTENT_ACTIONS.map((name) => ({
        $: { "android:name": name },
      })),
      category: [{ $: { "android:name": "android.intent.category.DEFAULT" } }],
      data: [{ $: { "android:mimeType": "*/*" } }],
    },
  ];
  return manifest;
}

module.exports = function withAndroidShareIntent(config) {
  return withAndroidManifest(config, (cfg) => {
    cfg.modResults = addShareIntentFilters(cfg.modResults);
    return cfg;
  });
};

module.exports.addShareIntentFilters = addShareIntentFilters;
module.exports.SHARE_INTENT_ACTIONS = SHARE_INTENT_ACTIONS;
