/**
 * Mobile auth store — Zustand. Logic mirrors packages/core/auth/store.ts:
 *   - Token written ONLY on successful verifyCode
 *   - 401 → clear token; non-401 (5xx / network blip) → preserve token so
 *     the next launch can retry
 *   - logout = clear token + clear in-memory user + setToken(null)
 *
 * NOT shared with web/desktop (per Sharing Principles in root CLAUDE.md).
 * Storage backend is expo-secure-store (mobile only); web uses HttpOnly
 * cookies, desktop uses localStorage via StorageAdapter.
 */
import { create } from "zustand";
import type { User } from "@multica/core/types";
import { api, ApiError } from "./api";
import {
  clearToken,
  getToken,
  migrateLegacySession,
  setToken,
} from "./secure-storage";
import { useWorkspaceStore } from "./workspace-store";
import { useServerStore } from "./server-store";
import { clearServerMemory } from "./stores/new-issue-draft-store";

interface AuthState {
  user: User | null;
  isLoading: boolean;
  initialize: () => Promise<void>;
  sendCode: (email: string) => Promise<void>;
  verifyCode: (email: string, code: string) => Promise<User>;
  logout: () => Promise<void>;
  /** Overwrite the in-memory user — call after PATCH /api/me so name/avatar
   *  edits land without a refetch. Server response is the source of truth. */
  setUser: (user: User) => void;
}

export const useAuthStore = create<AuthState>((set) => {
  // The original hydration chain, extracted so initialize() can own one
  // uniform failure boundary around it. Exits with isLoading:false at every
  // branch — the entry redirect depends on isLoading settling either way.
  const initializeFromStorage = async () => {
    // 服务器配置必须最先就绪 —— 下面的 api.getMe() 是首个网络请求,地址
    // 取自 server-store。晚一步就会打到内置默认服务器上(RUYI-4)。
    await useServerStore.getState().hydrate();
    const { activeServerId } = useServerStore.getState();

    // 会话按服务器分键存储。切换服务器 = 重跑 initialize():目标服务器有
    // 已存 token 则直接恢复会话(getMe 重建 user),没有则自然落到登录页。
    await migrateLegacySession(activeServerId);

    // Restore the persisted workspace slug alongside the auth token so the
    // entry redirect (app/index.tsx) can route directly to the last-used
    // workspace without flashing /select-workspace.
    await useWorkspaceStore.getState().restoreSlug();

    const token = await getToken(activeServerId);
    if (!token) {
      set({ isLoading: false });
      return;
    }
    api.setToken(token);
    try {
      const user = await api.getMe();
      set({ user, isLoading: false });
    } catch (err) {
      // Only clear token on a genuine 401. Network blips / 5xx keep the
      // token so the next launch (or a manual refresh) can retry.
      if (err instanceof ApiError && err.status === 401) {
        await clearToken(activeServerId);
        api.setToken(null);
      }
      set({ user: null, isLoading: false });
    }
  };

  return {
    user: null,
    isLoading: true,

    initialize: async () => {
      // Reset in-memory state first: initialize() runs both on cold start and
      // on server switch, where the previous server's user/slug must not flash
      // while the target server's session is being restored.
      set({ user: null, isLoading: true });

      // Storage/hydration failures must never wedge the boot spinner: the
      // entry redirect (app/index.tsx) waits on isLoading forever unless the
      // failure lands here. Degrade to signed-out state — the user reaches
      // /login and can retry, instead of a silent hang (RUYI-31).
      try {
        await initializeFromStorage();
      } catch (err) {
        console.error("[auth] initialize failed — continuing signed out:", err);
        set({ user: null, isLoading: false });
      }
    },

  sendCode: async (email) => {
    await api.sendCode(email);
  },

  verifyCode: async (email, code) => {
    const { token, user } = await api.verifyCode(email, code);
    const { activeServerId } = useServerStore.getState();
    await setToken(activeServerId, token);
    api.setToken(token);
    set({ user });
    return user;
  },

  logout: async () => {
    // Scoped to the active server: signing out of server A must not erase
    // the saved session of server B. The 401 path shares this — an expired
    // token only invalidates the server that rejected it.
    const { activeServerId } = useServerStore.getState();
    // RUYI-79: drop this account's last-assignee memory before the session
    // goes away, so the next login on this server entry never inherits the
    // previous account's pick (web draft-cleanup parity).
    clearServerMemory(activeServerId);
    await clearToken(activeServerId);
    api.setToken(null);
    set({ user: null });
  },

  setUser: (user) => set({ user }),
  };
});
