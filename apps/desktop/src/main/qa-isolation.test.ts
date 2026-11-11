// @vitest-environment node
import { describe, expect, it, vi } from "vitest";
import {
  configureDesktopQaIsolation,
  DESKTOP_QA_APP_SUFFIX_ENV,
  DESKTOP_QA_USER_DATA_DIR_ENV,
} from "./qa-isolation";

function createApp() {
  return {
    setName: vi.fn(),
    setPath: vi.fn(),
  };
}

describe("configureDesktopQaIsolation", () => {
  it("uses a distinct app name and userData path when both QA variables are set", () => {
    const app = createApp();

    expect(
      configureDesktopQaIsolation(app, {
        [DESKTOP_QA_APP_SUFFIX_ENV]: "RUYI-895",
        [DESKTOP_QA_USER_DATA_DIR_ENV]: "C:\\Users\\cfgxy\\AppData\\Local\\Multica-QA-RUYI-895",
      }),
    ).toEqual({
      appName: "Multica QA RUYI-895",
      userDataPath: "C:\\Users\\cfgxy\\AppData\\Local\\Multica-QA-RUYI-895",
    });
    expect(app.setName).toHaveBeenCalledExactlyOnceWith("Multica QA RUYI-895");
    expect(app.setPath).toHaveBeenCalledExactlyOnceWith(
      "userData",
      "C:\\Users\\cfgxy\\AppData\\Local\\Multica-QA-RUYI-895",
    );
  });

  it.each([
    { [DESKTOP_QA_USER_DATA_DIR_ENV]: "C:\\qa-profile" },
    { [DESKTOP_QA_APP_SUFFIX_ENV]: "RUYI-895" },
    {
      [DESKTOP_QA_APP_SUFFIX_ENV]: "",
      [DESKTOP_QA_USER_DATA_DIR_ENV]: "C:\\qa-profile",
    },
    {
      [DESKTOP_QA_APP_SUFFIX_ENV]: "RUYI-895",
      [DESKTOP_QA_USER_DATA_DIR_ENV]: "",
    },
  ])("does nothing when a QA variable is missing or empty", (environment) => {
    const app = createApp();

    expect(configureDesktopQaIsolation(app, environment)).toBeNull();
    expect(app.setName).not.toHaveBeenCalled();
    expect(app.setPath).not.toHaveBeenCalled();
  });
});
