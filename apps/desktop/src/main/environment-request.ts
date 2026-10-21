export interface EnvironmentProfile {
  name: string;
  port: number;
}

export interface EnvironmentRequestDependencies {
  ensureActiveProfile: () => Promise<EnvironmentProfile | null>;
  fetchHealthAtPort: (port: number) => Promise<{ status?: string } | null>;
  readProfileConfig: (profile: string) => Promise<{ token?: unknown }>;
  fetch: typeof globalThis.fetch;
}

export function createEnvironmentRequest({
  ensureActiveProfile,
  fetchHealthAtPort,
  readProfileConfig,
  fetch,
}: EnvironmentRequestDependencies) {
  return async (path: string, method = "GET", body?: unknown): Promise<unknown> => {
    const active = await ensureActiveProfile();
    if (!active) throw new Error("Desktop daemon profile is not ready");
    const health = await fetchHealthAtPort(active.port);
    if (health?.status !== "running") throw new Error("Desktop daemon is not running");
    const config = await readProfileConfig(active.name);
    const token = typeof config.token === "string" ? config.token : "";
    if (!token) throw new Error("Desktop daemon is not authenticated");
    const response = await fetch(`http://127.0.0.1:${active.port}${path}`, {
      method,
      headers: {
        Authorization: `Bearer ${token}`,
        "X-Multica-Profile": active.name,
        ...(body === undefined ? {} : { "Content-Type": "application/json" }),
      },
      body: body === undefined ? undefined : JSON.stringify(body),
      signal: AbortSignal.timeout(30_000),
    });
    if (!response.ok) {
      throw new Error((await response.text()).slice(0, 500) || `Daemon request failed (${response.status})`);
    }
    return response.json();
  };
}
