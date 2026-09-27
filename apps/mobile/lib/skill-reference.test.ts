// @vitest-environment node
import { describe, expect, it } from "vitest";
import { skillTriggerFromInput, insertSkillReference, rankSkills } from "./skill-reference";

describe("mobile skill references", () => {
  it("ranks matches by name before description and preserves order within a tier", () => {
    const skills = [
      { name: "examiner", description: "review" },
      { name: "Review", description: "" },
      { name: "reviewer", description: "" },
      { name: "code-review", description: "" },
    ];
    expect(rankSkills(skills, "review").map((skill) => skill.name)).toEqual(["Review", "reviewer", "code-review", "examiner"]);
  });
  it("opens for a newly typed slash at a word boundary, not a paste or path", () => {
    expect(skillTriggerFromInput("hello ", "hello /", 6, true)).toEqual({ start: 6, query: "" });
    expect(skillTriggerFromInput("", "/", 0, false)).toBeNull();
    expect(skillTriggerFromInput("path", "path/", 4, true)).toBeNull();
  });

  it("replaces the token with a safe platform reference that round-trips", () => {
    expect(insertSkillReference("use /rev now", { start: 4, query: "rev" }, { id: "s1", name: "Review [PR]" }))
      .toBe("use [/Review \\[PR\\]](slash://skill/s1) now");
    expect(insertSkillReference("/", { start: 0, query: "" }, { id: "s2", name: "a(b)\\" }))
      .toBe("[/a\\(b\\)\\\\](slash://skill/s2) ");
    expect(insertSkillReference("unchanged", { start: 0, query: "rev" }, { id: "s1", name: "Review" }))
      .toBe("unchanged");
  });
});
