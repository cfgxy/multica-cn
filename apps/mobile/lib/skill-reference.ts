export interface SkillToken {
  start: number;
  query: string;
}

export interface SkillReference {
  id: string;
  name: string;
}

export function rankSkills<T extends { name: string; description?: string }>(skills: T[], query: string): T[] {
  const q = query.trim().toLowerCase();
  if (!q) return skills;
  const rank = (skill: T) => {
    const name = skill.name.toLowerCase();
    if (name === q) return 0;
    if (name.startsWith(q)) return 1;
    if (name.includes(q)) return 2;
    if ((skill.description ?? "").toLowerCase().includes(q)) return 3;
    return 4;
  };
  return skills.map((skill) => ({ skill, tier: rank(skill) }))
    .filter(({ tier }) => tier < 4)
    .sort((a, b) => a.tier - b.tier)
    .map(({ skill }) => skill);
}

export function skillTriggerFromInput(
  prev: string,
  next: string,
  cursor: number,
  typedSlash: boolean,
): SkillToken | null {
  if (!typedSlash || next.length !== prev.length + 1 || next[cursor] !== "/") return null;
  if (next !== prev.slice(0, cursor) + "/" + prev.slice(cursor)) return null;
  if (cursor > 0 && !/\s/.test(next[cursor - 1]!)) return null;
  return { start: cursor, query: "" };
}

export function serializeSkillReference(skill: SkillReference): string {
  const label = skill.name.replace(/[\\[\]()]/g, "\\$&");
  return `[/` + label + `](slash://skill/${skill.id})`;
}

export function insertSkillReference(text: string, token: SkillToken, skill: SkillReference): string {
  const expected = `/${token.query}`;
  if (text.slice(token.start, token.start + expected.length) !== expected) return text;
  const before = text.slice(0, token.start);
  const after = text.slice(token.start + expected.length);
  return before + serializeSkillReference(skill) + (after.startsWith(" ") ? "" : " ") + after;
}
