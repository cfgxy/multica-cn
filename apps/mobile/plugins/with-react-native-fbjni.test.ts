import { describe, expect, it } from "vitest";
import plugin from "./with-react-native-fbjni.js";

const { pinReactNativeFbjni } = plugin as {
  pinReactNativeFbjni: (contents: string) => string;
};

const UPSTREAM = `dependencies {
    implementation("com.facebook.react:react-android")
    implementation("com.facebook.react:hermes-android")
}
`;

describe("pinReactNativeFbjni", () => {
  it("把 fbjni 固定到 React Native 0.83.6 的版本，覆盖原生模块的动态依赖", () => {
    const out = pinReactNativeFbjni(UPSTREAM);

    expect(out).toContain('implementation("com.facebook.fbjni:fbjni")');
    expect(out).toContain('strictly("0.7.0")');
    expect(out).toContain('implementation("com.facebook.react:react-android")');
  });

  it("重复预构建不会重复添加约束", () => {
    const once = pinReactNativeFbjni(UPSTREAM);
    expect(pinReactNativeFbjni(once)).toBe(once);
    expect(once.match(/strictly\("0\.7\.0"\)/g)).toHaveLength(1);
  });

  it("上游模板缺少 dependencies 时停止预构建", () => {
    expect(() => pinReactNativeFbjni("android { }\n")).toThrow(/dependencies/);
  });
});
