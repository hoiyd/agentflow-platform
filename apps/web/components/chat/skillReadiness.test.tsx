import { expect, it } from "vitest";
import { skillReadiness } from "./skillReadiness";

it("review rejection cannot look ready even with no Tool dependencies", () => {
  const result = skillReadiness(
    { name: "report-method", description: "Write a report", hash: "hash", review_reason: "skill_review_changed" },
    { tools: [], skills: ["report-method"] }
  );
  expect(result.ready).toBe(false);
  expect(result.reason).toContain("changed");
});
