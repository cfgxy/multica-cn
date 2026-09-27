import { create } from "zustand";
import type { SkillReference, SkillToken } from "@/lib/skill-reference";

interface SkillDraftState {
  selected: SkillReference | null;
  token: SkillToken | null;
  setSelected: (selected: SkillReference | null) => void;
  setToken: (token: SkillToken | null) => void;
  clear: () => void;
}

export const useSkillDraftStore = create<SkillDraftState>((set) => ({
  selected: null,
  token: null,
  setSelected: (selected) => set({ selected }),
  setToken: (token) => set({ token }),
  clear: () => set({ selected: null, token: null }),
}));
