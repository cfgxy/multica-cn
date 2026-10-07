import { isBackendAuthPath } from "../config/runtime-urls";

/**
 * Navigate to a sanitized post-auth `next` target.
 *
 * Backend-owned auth paths (`/auth/**` minus the frontend callback pages) are
 * redirect-chain endpoints: the OAuth authorize step answers 302 on to the
 * connecting client's callback, or parks the request behind the consent
 * screen (RUYI-420). Only the browser itself can follow that chain — a
 * client-side router transition fetches the URL as an RSC payload, the fetch
 * consumes the 302, and the user is stranded on the login page with no error
 * (RUYI-526). Those targets get a full-page navigation; every other same-origin
 * `next` keeps the soft in-app navigation.
 */
export function navigateToNextUrl(
  nextUrl: string,
  router: { push: (url: string) => void; replace: (url: string) => void },
  method: "push" | "replace" = "push",
): void {
  const pathname = nextUrl.split(/[?#]/)[0] ?? nextUrl;
  if (isBackendAuthPath(pathname)) {
    window.location.href = nextUrl;
    return;
  }
  router[method](nextUrl);
}
