// @vitest-environment node
import { readFileSync, readdirSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

// 根布局的每个 <Stack.Screen name> 必须命中 expo-router 从文件系统生成的
// 一个真实根级路由名，否则 expo-router 的 getSortedChildren
// （build/useScreens.js）在根 Stack 每次渲染时都告警：
// [Layout children]: No route named "X" exists in nested children:
// （冷启动启动窗内根布局多次渲染，告警随之重复出现，RUYI-346 第四轮）。
//
// 路由生成规则（本测试只需覆盖根布局这一层）：
// - 带 `_layout.tsx` 的目录各自成节点，节点名 = 目录名（分组保留括号）；
// - 不带 `_layout.tsx` 的目录被扁平化：目录名并入路由路径
//   （app/servers/select.tsx → 路由名 "servers/select"，不存在裸 "servers"）；
// - `index` 文件归并为父路径本身；`_`/`+` 前缀条目不是路由。
const appDir = fileURLToPath(new URL("../app", import.meta.url));
const rootLayoutPath = fileURLToPath(new URL("../app/_layout.tsx", import.meta.url));

const ROUTE_EXT = /\.(tsx|ts|jsx|js)$/;

function hasLayout(dir: string): boolean {
  return readdirSync(dir, { withFileTypes: true }).some(
    (entry) => entry.isFile() && (entry.name === "_layout.tsx" || entry.name === "_layout.js"),
  );
}

function collectRouteNames(dir: string, segments: string[], into: Set<string>): void {
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    if (entry.name.startsWith("_") || entry.name.startsWith("+")) continue;
    if (entry.isFile()) {
      if (!ROUTE_EXT.test(entry.name)) continue;
      const base = entry.name.replace(ROUTE_EXT, "");
      if (base === "index") {
        into.add(segments.length ? segments.join("/") : "index");
      } else {
        into.add([...segments, base].join("/"));
      }
    } else if (entry.isDirectory()) {
      if (hasLayout(`${dir}/${entry.name}`)) {
        into.add([...segments, entry.name].join("/"));
      } else {
        collectRouteNames(`${dir}/${entry.name}`, [...segments, entry.name], into);
      }
    }
  }
}

function declaredRootScreenNames(source: string): string[] {
  return [...source.matchAll(/<Stack\.Screen\s+name="([^"]+)"/g)].map((match) => match[1]);
}

describe("root layout Stack.Screen declarations", () => {
  it("只声明文件系统真实生成的根级路由名", () => {
    const generated = new Set<string>();
    collectRouteNames(appDir, [], generated);
    const declared = declaredRootScreenNames(readFileSync(rootLayoutPath, "utf8"));
    expect(declared.length).toBeGreaterThan(0);
    for (const name of declared) {
      expect(generated, `Stack.Screen name="${name}" 不匹配任何根级路由`).toContain(name);
    }
  });
});
