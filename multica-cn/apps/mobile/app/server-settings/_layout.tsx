import { Stack } from "expo-router";

/**
 * 服务器设置分组。刻意不在 (app) / (auth) 之内 —— 未登录用户连自建后端
 * 是核心场景,登录前必须可达,因此这里没有任何 auth 守卫(RUYI-4)。
 *
 * `headerShown: false` + 屏内自绘 `<Header />`,与同样登录前可达的
 * (auth) 分组保持同一套写法。原先用的是原生 Stack header(裸 `<Stack />`),
 * 但在 Android 上实测 header 内容整块压在状态栏下面 —— 返回箭头压系统
 * 图标、标题压时间、右上「+」压电池(RUYI-25 批次 13 截图)。原生 header
 * 的 top inset 在 react-native-screens 4.23 里由 CustomToolbar 自行处理,
 * `topInsetEnabled` prop 在 Android 侧已被忽略(ScreenStackHeaderConfigViewManager
 * 里只 logNotAvailable),JS 侧没有可靠的开关能纠正它。(auth) 的
 * `headerShown: false` + SafeAreaView 写法是本仓库里唯一有 Android 实测
 * 证据的可行路径,这里对齐它。
 *
 * 返回按钮改由 `<Header left>` 自绘;Android 的系统返回键/返回手势不依赖
 * header,行为不变。
 */
export default function ServerSettingsLayout() {
  return <Stack screenOptions={{ headerShown: false }} />;
}
