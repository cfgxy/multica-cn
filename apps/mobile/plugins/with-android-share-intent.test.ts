import { describe, expect, it } from "vitest";
import * as shareIntentPlugin from "./with-android-share-intent";

/** 插件是 .js 无类型导出；测试侧给定结构签名，让中间值不再退化成 any。 */
const addShareIntentFilters = shareIntentPlugin.addShareIntentFilters as (
  manifest: ManifestDoc,
) => ManifestDoc;
const SHARE_INTENT_ACTIONS = shareIntentPlugin.SHARE_INTENT_ACTIONS as string[];

/** prebuild 解析后的 AndroidManifest XML 对象树的最小结构（属性挂 `$` 下）。 */
interface ManifestNode {
  $?: Record<string, string>;
  action?: ManifestNode[];
  category?: ManifestNode[];
  data?: ManifestNode[];
  "intent-filter"?: ManifestNode[];
  activity?: ManifestNode[];
}
interface ManifestDoc {
  manifest: { application?: ManifestNode[] };
}

/**
 * expo prebuild 解析后的 AndroidManifest 对象的最小形态（XML 对象树，
 * 属性挂在 `$` 下）。插件锚点打在 `.MainActivity` 上，与上游模板一致。
 */
function upstreamManifest(): ManifestDoc {
  return {
    manifest: {
      application: [
        {
          $: { "android:name": ".MainApplication" },
          activity: [
            {
              $: { "android:name": ".MainActivity", "android:exported": "true" },
              "intent-filter": [
                {
                  action: [
                    { $: { "android:name": "android.intent.action.MAIN" } },
                  ],
                  category: [
                    { $: { "android:name": "android.intent.category.LAUNCHER" } },
                  ],
                },
                {
                  action: [{ $: { "android:name": "android.intent.action.VIEW" } }],
                  category: [
                    { $: { "android:name": "android.intent.category.DEFAULT" } },
                    { $: { "android:name": "android.intent.category.BROWSABLE" } },
                  ],
                  data: [{ $: { "android:scheme": "multica" } }],
                },
              ],
            },
          ],
        },
      ],
    },
  };
}

function shareFilterOf(manifest: ManifestDoc) {
  const activity =
    manifest.manifest.application?.[0].activity?.find((a) =>
      (a.$?.["android:name"] ?? "").endsWith(".MainActivity"),
    ) ?? null;
  return (
    activity?.["intent-filter"]?.find((f) =>
      (f.action ?? []).some(
        (a) => a.$?.["android:name"] === "android.intent.action.SEND",
      ),
    ) ?? null
  );
}

describe("addShareIntentFilters", () => {
  it("给 MainActivity 追加 SEND + SEND_MULTIPLE 过滤器（DEFAULT + */*）", () => {
    const manifest = addShareIntentFilters(structuredClone(upstreamManifest()));
    const filter = shareFilterOf(manifest);
    expect(filter).not.toBeNull();
    expect(filter?.action?.map((a) => a.$?.["android:name"])).toEqual(
      SHARE_INTENT_ACTIONS,
    );
    expect(filter?.category?.map((c) => c.$?.["android:name"])).toEqual([
      "android.intent.category.DEFAULT",
    ]);
    expect(filter?.data?.map((d) => d.$?.["android:mimeType"])).toEqual(["*/*"]);
  });

  it("幂等：重复 prebuild 不产生第二个分享过滤器", () => {
    const once = addShareIntentFilters(structuredClone(upstreamManifest()));
    const twice = addShareIntentFilters(once);
    const filters =
      twice.manifest.application?.[0].activity?.[0]["intent-filter"] ?? [];
    const shareFilters = filters.filter((f) =>
      (f.action ?? []).some(
        (a) => a.$?.["android:name"] === "android.intent.action.SEND",
      ),
    );
    expect(shareFilters).toHaveLength(1);
    // 既有过滤器不被破坏
    expect(filters).toHaveLength(3);
  });

  it("找不到 MainActivity 时抛错（上游模板变更先红）", () => {
    const broken = {
      manifest: { application: [{ $: { "android:name": ".MainApplication" } }] },
    };
    expect(() => addShareIntentFilters(broken)).toThrow(/MainActivity/);
  });

  it("找不到 application 时抛错", () => {
    expect(() => addShareIntentFilters({ manifest: {} })).toThrow(/application/);
  });
});
