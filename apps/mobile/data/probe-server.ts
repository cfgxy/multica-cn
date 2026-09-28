import { SERVER_PROBE_PATH, interpretProbeResponse } from "./server-config";

export const STARTUP_PROBE_TIMEOUT_MS = 3_000;

/** Status checks do not carry the session token. */
export async function probeServer(apiUrl: string, signal: AbortSignal): Promise<boolean> {
  const controller = new AbortController();
  const abort = () => controller.abort();
  signal.addEventListener("abort", abort, { once: true });
  if (signal.aborted) abort();
  let timer: ReturnType<typeof setTimeout> | undefined;
  try {
    return await Promise.race([
      fetch(`${apiUrl}${SERVER_PROBE_PATH}`, {
        signal: controller.signal,
        credentials: "omit",
      }).then(
        (response) => interpretProbeResponse(response.status, response.headers.get("content-type")),
        () => false,
      ),
      new Promise<boolean>((resolve) => {
        timer = setTimeout(() => {
          abort();
          resolve(false);
        }, STARTUP_PROBE_TIMEOUT_MS);
      }),
    ]);
  } finally {
    clearTimeout(timer);
    signal.removeEventListener("abort", abort);
  }
}
