import type { App } from "electron";

export const DESKTOP_QA_APP_SUFFIX_ENV = "MULTICA_DESKTOP_APP_SUFFIX";
export const DESKTOP_QA_USER_DATA_DIR_ENV = "MULTICA_DESKTOP_USER_DATA_DIR";

export type DesktopQaIsolation = {
  appName: string;
  userDataPath: string;
};

type QaIsolationApp = Pick<App, "setName" | "setPath">;

export function configureDesktopQaIsolation(
  app: QaIsolationApp,
  environment: NodeJS.ProcessEnv = process.env,
): DesktopQaIsolation | null {
  const suffix = environment[DESKTOP_QA_APP_SUFFIX_ENV];
  const userDataPath = environment[DESKTOP_QA_USER_DATA_DIR_ENV];

  if (!suffix || !userDataPath) return null;

  const appName = `Multica QA ${suffix}`;
  app.setName(appName);
  app.setPath("userData", userDataPath);

  return { appName, userDataPath };
}
