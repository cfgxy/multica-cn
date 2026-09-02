// Instance-level admin management types (RUYI-47). These mirror the
// /api/admin responses; parsing happens in api/schemas.ts, so the types
// here stay pure data shapes.
import type { User } from "../types";

export interface AdminUser {
  id: string;
  name: string;
  email: string;
  avatar_url: string | null;
  is_super_admin: boolean;
  /** ISO timestamp when the account was disabled; null = enabled. */
  disabled_at: string | null;
  workspace_count: number;
  created_at: string;
}

export interface AdminUserList {
  users: AdminUser[];
  total: number;
}

export interface AdminWorkspace {
  id: string;
  name: string;
  slug: string;
  /** Longest-standing owner-role member; null when the workspace has none. */
  owner_id: string | null;
  owner_name: string | null;
  owner_email: string | null;
  member_count: number;
  created_at: string;
}

export interface AdminWorkspaceList {
  workspaces: AdminWorkspace[];
  total: number;
}

/** Impersonation start/stop responses reuse the login shape. */
export interface ImpersonationResponse {
  token: string;
  user: User;
}
