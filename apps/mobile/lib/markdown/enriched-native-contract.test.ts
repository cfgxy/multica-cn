// @vitest-environment node

/**
 * RUYI-132 regression guard for the enriched-markdown native height contract.
 *
 * The direct cause of overlap is not our React Native layout code. Android
 * 0.6.0 parses markdown asynchronously, then writes the final Spannable height
 * to `MeasurementStore` without notifying the Fabric shadow node. When that
 * height differs from the initial shadow measurement, Yoga does not lay out the
 * siblings after `<Markdown>` again, including `CommentAttachmentList`.
 *
 * Version 1.0.0 introduced the missing Text path. Versions 0.7.0-0.7.4 keep
 * the 0.6.0 implementation, so any version below 1.0.0 can reproduce the bug.
 *
 * These assertions read installed native sources and repository configuration;
 * they deliberately use no mocks:
 *
 *   1. The installed package must be >= 1.0.0.
 *   2. Android must persist the state wrapper, update its height counter, and
 *      dirty the shadow-node layout when the counter changes.
 *   3. app.config.ts must not register the removed Expo config plugin.
 *   4. Both workspace installation and app native builds must disable the
 *      library highlighter because Multica supplies Shiki code blocks itself.
 */

import { existsSync, readFileSync } from "node:fs";
import path from "node:path";
import { createRequire } from "node:module";
import { describe, expect, it } from "vitest";

const APP_ROOT = path.resolve(__dirname, "../..");
const WORKSPACE_ROOT = path.resolve(APP_ROOT, "../..");
const requireFromApp = createRequire(path.join(APP_ROOT, "package.json"));

const PKG = "react-native-enriched-markdown";

function enrichedPackageJsonPath(): string {
  return requireFromApp.resolve(`${PKG}/package.json`);
}

function nativeSourcePath(...parts: string[]): string {
  return path.join(path.dirname(enrichedPackageJsonPath()), ...parts);
}

function readNativeSource(...parts: string[]): string {
  const sourcePath = nativeSourcePath(...parts);

  expect(
    existsSync(sourcePath),
    `未找到 ${sourcePath}；上游若移动了该文件，请重新核对高度回传链路是否仍然成立`,
  ).toBe(true);

  return readFileSync(sourcePath, "utf8");
}

function parseMajor(version: string): number {
  const major = Number.parseInt(version.split(".")[0] ?? "", 10);
  expect(Number.isNaN(major), `无法解析 ${PKG} 版本号：${version}`).toBe(false);
  return major;
}

describe("enriched-markdown 原生高度回传契约", () => {
  it("安装的版本带有 Text 分支的高度回传修复（>= 1.0.0）", () => {
    const installed = JSON.parse(
      readFileSync(enrichedPackageJsonPath(), "utf8"),
    ) as { version: string };

    expect(parseMajor(installed.version)).toBeGreaterThanOrEqual(1);
  });

  it("Android invalidateLayout() 将高度变化依次写回 shadow node", () => {
    const source = readNativeSource(
      "android/src/main/java/com/swmansion/enriched/markdown",
      "EnrichedMarkdownTextLayoutManager.kt",
    );

    // 0.6.0 discards the store result. The full sequence must increment state
    // only for a changed height and send that state through the Fabric wrapper.
    expect(source).toMatch(
      /val heightChanged = MeasurementStore\.store\([\s\S]*?\)\s*if \(!heightChanged\) return\s*val stateWrapper = view\.stateWrapper \?: return\s*val state = Arguments\.createMap\(\)\s*state\.putInt\("forceHeightRecalculationCounter", \+\+forceHeightRecalculationCounter\)\s*stateWrapper\.updateState\(state\)/,
    );
  });

  it("Android manager and shadow node complete the height recalculation chain", () => {
    const manager = readNativeSource(
      "android/src/main/java/com/swmansion/enriched/markdown",
      "EnrichedMarkdownTextManager.kt",
    );
    const shadowNode = readNativeSource(
      "android/src/main/jni/react/renderer/components/EnrichedMarkdownTextSpec",
      "MarkdownTextShadowNode.cpp",
    );

    expect(manager).toMatch(/view\.stateWrapper\s*=\s*stateWrapper/);
    expect(shadowNode).toMatch(
      /void MarkdownTextShadowNode::dirtyLayoutIfNeeded\(\) \{\s*const auto state = this->getStateData\(\);\s*const auto counter = state\.getForceHeightRecalculationCounter\(\);\s*if \(forceHeightRecalculationCounter_ != counter\) \{\s*forceHeightRecalculationCounter_ = counter;\s*dirtyLayout\(\);\s*\}\s*\}/,
    );
  });

  it("app.config.ts 不再注册已被移除的 Expo config plugin", () => {
    const appConfig = readFileSync(path.join(APP_ROOT, "app.config.ts"), "utf8");

    // The package name can appear in a comment; only plugin array entries are forbidden.
    const pluginEntry = new RegExp(`^\\s*(?:\\[\\s*)?["']${PKG}["']`, "m");
    expect(pluginEntry.test(appConfig)).toBe(false);
  });

  it("安装和原生构建都从 package.json 读取关闭代码高亮的配置", () => {
    for (const packageJson of [
      path.join(WORKSPACE_ROOT, "package.json"),
      path.join(APP_ROOT, "package.json"),
    ]) {
      const pkg = JSON.parse(readFileSync(packageJson, "utf8")) as Record<string, unknown>;
      const block = pkg["enriched-markdown"];

      expect(typeof block).toBe("object");
      expect(block).not.toBeNull();
      expect((block as Record<string, unknown>).enableCodeHighlight).toBe(false);
    }
  });
});
