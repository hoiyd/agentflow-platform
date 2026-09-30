import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { SkillEvidencePanel } from "./SkillEvidence";

afterEach(cleanup);

it("keeps ordinary runs quiet and distinguishes binding from observed inclusion", () => {
 const inspect=vi.fn();
 const view=render(<SkillEvidencePanel items={[]} onInspectEvent={inspect} />);
 expect(screen.queryByText(/Skill evidence/)).toBeNull();
 view.rerender(<SkillEvidencePanel items={[{name:"method",hash:"hash",agent_id:"agent",bound:true,instructions:"not_observed",activation:"not_observed",resources:[],failures:[]}]} onInspectEvent={inspect} />);
 const details=view.container.querySelector("details")!;
 expect(details.open).toBe(false);
 fireEvent.click(screen.getByText(/Skill evidence/));
 expect(screen.getByText("Instructions not observed")).toBeTruthy();
 expect(screen.getByText("Origin not observed")).toBeTruthy();
 expect(screen.getByText("Bound")).toBeTruthy();
 expect(screen.queryByRole("button",{name:/Inspect input/})).toBeNull();
});

it("shows explicit input evidence, resource ranges and typed failures without prompt content", () => {
 const inspect=vi.fn();
 const view=render(<SkillEvidencePanel items={[{name:"method",hash:"package-hash",agent_id:"agent",stage_id:"worker",bound:true,instructions:"included",activation:"explicit",first_sequence:3,manifest_id:"manifest",request_id:"request",event_id:"input",estimated_tokens:12,resources:[{path:"references/check.md",hash:"resource-hash",offset:0,next_offset:8,total_bytes:16,event_id:"read",sequence:4}],failures:[{tool:"skill_read",code:"execution_failed",event_id:"failed",sequence:5}]}]} onInspectEvent={inspect} />);
 fireEvent.click(screen.getByText(/Skill evidence/));
 expect(screen.getByText("Explicit invocation")).toBeTruthy();
 expect(screen.getByText("Instructions included")).toBeTruthy();
 expect(screen.getByText(/0–8 \/ 16 bytes/)).toBeTruthy();
 expect(screen.getByText("execution_failed")).toBeTruthy();
 fireEvent.click(screen.getByRole("button",{name:"Inspect input · event 3"}));
 expect(inspect).toHaveBeenLastCalledWith("input");
 fireEvent.click(screen.getByRole("button",{name:"Inspect failure · event 5"}));
 expect(inspect).toHaveBeenLastCalledWith("failed");
 expect(view.container.textContent).not.toContain("instructions body");
});
