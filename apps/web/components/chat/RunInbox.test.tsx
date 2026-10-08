import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { RunInbox } from "./RunInbox";
import * as api from "../../lib/run-input-api";

vi.mock("../../lib/run-input-api", () => ({ listRunInputs: vi.fn(), enqueueRunInput: vi.fn(), withdrawRunInput: vi.fn(), startFollowup: vi.fn() }));
afterEach(() => { cleanup(); vi.resetAllMocks(); });

it("uses explicit actions, preserves idempotency on retry, and restores receipts", async () => {
 vi.mocked(api.listRunInputs).mockResolvedValue([]);
 vi.mocked(api.enqueueRunInput).mockRejectedValueOnce(new Error("Connection lost")).mockResolvedValueOnce({ id:"input-one" } as api.RunInput);
 const onSubmitted=vi.fn();
 render(<RunInbox conversationId="conv-one" runId="run-one" mode="single" agentId="agent-one" input="Be concise" busy canStartFollowup={false} onSubmitted={onSubmitted} onRunAvailable={vi.fn()} />);
 fireEvent.click(screen.getByRole("button",{name:"Steer current run"}));
 await screen.findByRole("alert");
 expect(onSubmitted).not.toHaveBeenCalled();
 await waitFor(()=>expect((screen.getByRole("button",{name:"Steer current run"}) as HTMLButtonElement).disabled).toBe(false));
 fireEvent.click(screen.getByRole("button",{name:"Steer current run"}));
 await waitFor(()=>expect(onSubmitted).toHaveBeenCalledOnce());
 const calls=vi.mocked(api.enqueueRunInput).mock.calls;
 expect(calls[0][1]).toEqual(calls[1][1]);
 expect(calls[0][1].kind).toBe("steer");
});

it("does not clear the new conversation's draft after a stale submission", async () => {
 vi.mocked(api.listRunInputs).mockResolvedValue([]);
 let resolve!:(value:api.RunInput)=>void;
 vi.mocked(api.enqueueRunInput).mockImplementation(()=>new Promise(r=>{resolve=r;}));
 const onSubmitted=vi.fn(); const props={conversationId:"conv-one",runId:"run-one",mode:"single" as const,agentId:"agent-one",input:"Task",busy:true,canStartFollowup:false,onSubmitted,onRunAvailable:vi.fn()};
 const view=render(<RunInbox {...props} />);
 fireEvent.click(screen.getByRole("button",{name:"Queue follow-up"}));
 view.rerender(<RunInbox {...props} conversationId="conv-two" />);
 resolve({id:"old"} as api.RunInput);
 await waitFor(()=>expect(api.enqueueRunInput).toHaveBeenCalledOnce());
 expect(onSubmitted).not.toHaveBeenCalled();
});

it("preserves text typed while an input submission is pending", async () => {
 vi.mocked(api.listRunInputs).mockResolvedValue([]);
 let resolve!:(value:api.RunInput)=>void;
 vi.mocked(api.enqueueRunInput).mockImplementation(()=>new Promise(r=>{resolve=r;}));
 const onSubmitted=vi.fn();
 const props={conversationId:"conv-one",runId:"run-one",mode:"single" as const,agentId:"agent-one",input:"Original task",busy:true,canStartFollowup:false,onSubmitted,onRunAvailable:vi.fn()};
 const view=render(<RunInbox {...props} />);
 fireEvent.click(screen.getByRole("button",{name:"Queue follow-up"}));
 view.rerender(<RunInbox {...props} input="New draft" />);
 resolve({id:"input-one"} as api.RunInput);
 await waitFor(()=>expect(api.listRunInputs).toHaveBeenCalledTimes(2));
 expect(onSubmitted).not.toHaveBeenCalled();
});

it("does not offer to start a follow-up while the current run needs approval or recovery", async () => {
 vi.mocked(api.listRunInputs).mockResolvedValue([{id:"input-one",kind:"follow_up",status:"queued",content:"Next task"} as api.RunInput]);
 render(<RunInbox conversationId="conv-one" runId="run-one" mode="single" agentId="agent-one" input="" busy={false} canStartFollowup={false} onSubmitted={vi.fn()} onRunAvailable={vi.fn()} />);
 await screen.findByText("Next task");
 expect(screen.queryByRole("button",{name:"Start next follow-up"})).toBeNull();
});
