import { describe, expect, it, afterEach } from "vitest";
import type { ConfigContext } from "expo/config";
import appConfig from "./app.config";

// expo ConfigContext 必需字段（projectRoot/staticConfigPath/packageJsonPath）
// 在 app.config.ts 内未被读取，测试只需类型合法的最小构造。
const minimalContext = (): ConfigContext => ({
  projectRoot: process.cwd(),
  staticConfigPath: null,
  packageJsonPath: null,
  config: {},
});

// RUYI-573：MULTICA_APP_VERSION 构建期注入——日常开发构建把
// X.Y.Z-dev.YYYYMMDD-N 烘焙进 versionName 与 expo-constants；未注入时必须
// 保持正式基线版本，保证本地/EAS/正式 tag 通道行为不变。
describe("app.config version injection", () => {
  const ORIGINAL = process.env.MULTICA_APP_VERSION;

  afterEach(() => {
    if (ORIGINAL === undefined) {
      delete process.env.MULTICA_APP_VERSION;
    } else {
      process.env.MULTICA_APP_VERSION = ORIGINAL;
    }
  });

  it("falls back to the committed release version when unset", () => {
    delete process.env.MULTICA_APP_VERSION;
    expect(appConfig(minimalContext()).version).toBe("0.2.0");
  });

  it("injects the daily dev version when MULTICA_APP_VERSION is set", () => {
    process.env.MULTICA_APP_VERSION = "0.2.0-dev.20261008-3";
    expect(appConfig(minimalContext()).version).toBe("0.2.0-dev.20261008-3");
  });
});
