// @vitest-environment node

/**
 * RUYI-132 regression guard for the enriched-markdown native height contract.
 *
 * The direct cause of overlap is not our React Native layout code. Android
 * parses GitHub-flavored markdown asynchronously, then must write the final
 * segment height to `MeasurementStore` and notify the Fabric shadow node. When
 * that height differs from the initial shadow measurement, Yoga does not lay
 * out the siblings after `<Markdown>` again, including `CommentAttachmentList`.
 *
 * Version 1.0.2 introduced the Text-path notification, but Multica comments
 * select the GitHub container. The container must emit the same notification
 * after ordinary asynchronous segment reconciliation.
 *
 * These assertions read installed native sources and repository configuration;
 * they deliberately use no mocks:
 *
 *   1. The installed package must remain pinned to the reviewed version.
 *   2. App prose must select the GitHub container, which retains its Fabric
 *      state wrapper and notifies it after asynchronous segment changes.
 *   3. The container shadow node must dirty its layout when the counter changes.
 *   4. app.config.ts must not register the removed Expo config plugin.
 *   5. Both workspace installation and app native builds must disable the
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

describe("enriched-markdown 原生高度回传契约", () => {
  it("安装的版本保持为已审查的 1.0.2", () => {
    const installed = JSON.parse(
      readFileSync(enrichedPackageJsonPath(), "utf8"),
    ) as { version: string };

    expect(installed.version).toBe("1.0.2");
  });

  it("评论 prose 选择 GitHub Fabric 容器", () => {
    const markdown = readFileSync(path.join(APP_ROOT, "lib/markdown/markdown.tsx"), "utf8");
    const nativeWrapper = readNativeSource("src/native", "EnrichedMarkdownText.tsx");

    expect(markdown).toMatch(
      /<EnrichedMarkdownText[\s\S]*?flavor="github"[\s\S]*?markdown=\{seg\.content\}/,
    );
    expect(nativeWrapper).toMatch(
      /if \(flavor === 'github'\) \{\s*return <EnrichedMarkdownNativeComponent \{\.\.\.sharedProps\} \/>;\s*\}/,
    );
  });

  it("GitHub 容器管理器保留 Fabric state wrapper", () => {
    const manager = readNativeSource(
      "android/src/main/java/com/swmansion/enriched/markdown",
      "EnrichedMarkdownManager.kt",
    );

    expect(manager).toMatch(
      /override fun updateState\([\s\S]*?view\.stateWrapper = stateWrapper[\s\S]*?return super\.updateState\(view, props, stateWrapper\)/,
    );
  });

  it("异步 GitHub 段落应用通过 Fabric state 触发容器重新测量", () => {
    const container = readNativeSource(
      "android/src/main/java/com/swmansion/enriched/markdown",
      "EnrichedMarkdown.kt",
    );
    const notification = container.match(
      /private fun notifyHeightChanged\(\) \{([\s\S]*?)\n    \}/,
    )?.[1];

    expect(notification).toEqual(expect.any(String));
    expect(notification ?? "").toMatch(
      /MeasurementStore\.invalidate\(id\)[\s\S]*?requestLayout\(\)[\s\S]*?val wrapper = stateWrapper \?: return[\s\S]*?state\.putInt\("forceHeightRecalculationCounter", \+\+forceHeightRecalculationCounter\)[\s\S]*?wrapper\.updateState\(state\)/,
    );
    expect(container).toMatch(
      /if \(forceHeight \|\| topologyChanged \|\| heightBefore != heightAfter\) \{\s*notifyHeightChanged\(\)\s*\}/,
    );
    expect(container).toMatch(
      /fun onImageLayoutChanged\(\) \{[\s\S]*?notifyHeightChanged\(\)/,
    );
  });

  it("GitHub 容器 shadow node 在状态计数变化时重新布局", () => {
    const shadowNode = readNativeSource(
      "android/src/main/jni/react/renderer/components/EnrichedMarkdownTextSpec",
      "MarkdownContainerShadowNode.cpp",
    );

    expect(shadowNode).toMatch(
      /void MarkdownContainerShadowNode::dirtyLayoutIfNeeded\(\) \{\s*const auto state = this->getStateData\(\);\s*const auto counter = state\.getForceHeightRecalculationCounter\(\);\s*if \(forceHeightRecalculationCounter_ != counter\) \{\s*forceHeightRecalculationCounter_ = counter;\s*dirtyLayout\(\);\s*\}\s*\}/,
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
