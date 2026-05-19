/**
 * Workspace "Menu" sheet — presented as an iOS formSheet by the stack
 * (see app/(app)/[workspace]/_layout.tsx). Native drag handle, swipe-down
 * dismiss, native blur backdrop — all Apple-managed.
 *
 * Content lives in GlobalNavMenu; this file is just the route shell.
 */
import { GlobalNavMenu } from "@/components/nav/global-nav-menu";

export default function MoreScreen() {
  return <GlobalNavMenu />;
}
