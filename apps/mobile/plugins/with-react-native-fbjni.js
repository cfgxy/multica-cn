const { withAppBuildGradle } = require("@expo/config-plugins");

const MARKER = 'strictly("0.7.0")';
const FBJNI_CONSTRAINT = `
    constraints {
        implementation("com.facebook.fbjni:fbjni") {
            version { strictly("0.7.0") }
        }
    }`;

function pinReactNativeFbjni(contents) {
  if (contents.includes(MARKER)) return contents;

  const dependencies = /^dependencies\s*\{/m;
  if (!dependencies.test(contents)) {
    throw new Error("[with-react-native-fbjni] Missing dependencies block in app/build.gradle");
  }

  return contents.replace(dependencies, (match) => `${match}${FBJNI_CONSTRAINT}`);
}

module.exports = function withReactNativeFbjni(config) {
  return withAppBuildGradle(config, (cfg) => {
    if (cfg.modResults.language !== "groovy") {
      throw new Error("[with-react-native-fbjni] Expected Groovy app/build.gradle");
    }
    cfg.modResults.contents = pinReactNativeFbjni(cfg.modResults.contents);
    return cfg;
  });
};

module.exports.pinReactNativeFbjni = pinReactNativeFbjni;
