import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ClarificationPanel } from "./ClarificationPanel";
import type { Clarification } from "../../lib/clarification-api";

afterEach(cleanup);
const question: Clarification = {id:"q-1",run_id:"r-1",conversation_id:"c-1",turn_id:"t-1",version:1,status:"pending",created_at:"2026-10-12T00:00:00Z",expires_at:"2026-10-13T00:00:00Z",questions:[{id:"audience",question:"Who is the audience?",options:["Engineers","Managers"]}]};
describe("durable clarification",()=>{
 it("continues a claimed answer only when the Run is recoverable",()=>{
  const resume=vi.fn();const receipt={...question,status:"applied" as const,answers:{audience:"Engineers"}};
  render(<ClarificationPanel items={[receipt]} recoverable onResume={resume}/>);
  fireEvent.click(screen.getByRole("button",{name:"Continue run"}));
  expect(resume).toHaveBeenCalledWith(receipt,{audience:"Engineers"});
  expect(screen.queryByRole("textbox")).toBeNull();
 });
 it("keeps free text available with options and submits the question identity",()=>{
  const resume=vi.fn();render(<ClarificationPanel items={[question]} busy={false} onResume={resume}/>);
  fireEvent.click(screen.getByRole("radio",{name:"Engineers"}));
  fireEvent.change(screen.getByLabelText("Your answer"),{target:{value:"Engineering leads"}});
  fireEvent.click(screen.getByRole("button",{name:"Answer and continue"}));
  expect(resume).toHaveBeenCalledWith(question, {audience:"Engineering leads"});
 });
 it("offers retry of a durable answer without collecting it again",()=>{
  render(<ClarificationPanel items={[{...question,status:"answered",answers:{audience:"Engineers"}}]} busy={false} onResume={vi.fn()}/>);
  expect(screen.getByText("Answer saved")).toBeTruthy();
  expect(screen.getByRole("button",{name:"Continue run"})).toBeTruthy();
  expect(screen.queryByRole("textbox")).toBeNull();
 });
 it("never enables expired or canceled questions and preserves readable evidence",()=>{
  render(<ClarificationPanel items={[{...question,status:"expired"}]} busy={false} onResume={vi.fn()}/>);
  expect(screen.getByText("Who is the audience?")).toBeTruthy();
  expect(screen.queryByRole("button",{name:"Answer and continue"})).toBeNull();
 });
});
