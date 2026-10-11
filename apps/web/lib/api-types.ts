import type { components } from "./api-contract.gen";

export type ContractSchemas = components["schemas"];
// The client defaults nullable Go slices to arrays without repeating DTO fields.
type ArrayDefaults<T> = { [K in keyof T]: T[K] extends unknown[] | null ? NonNullable<T[K]> : T[K] };


export type APIHealth = ContractSchemas["HealthResponse"];
export type ChatRequest = ContractSchemas["ChatRequest"];
export type AgentConfigInput = ContractSchemas["AgentConfigRequest"];
export type Conversation = ContractSchemas["Conversation"];
type APIMessage = ContractSchemas["Message"];
export type Message = Omit<APIMessage, "workspace_id"> & { workspace_id?: string };
export type RunEvent = ContractSchemas["RunEvent"];
export type PartialOutput = ContractSchemas["PartialOutput"];
export type ToolProgress = ContractSchemas["ToolProgress"];
export type ChatEvent = Exclude<ContractSchemas["ChatStreamEvent"], RunEvent>;
export type AgentInfo = ContractSchemas["Agent"];
export type AgentRoutingRequirements = ContractSchemas["AgentRoutingRequirements"];
export type ToolInfo = ContractSchemas["ToolInfo"];
export type SkillInfo = ContractSchemas["SkillMetadata"];
export type SkillEvidence = ContractSchemas["SkillEvidence"];
export type ChatMode = ContractSchemas["ChatMode"];
export type RunInfo = ContractSchemas["Run"];
export type OperatorAttentionItem = ContractSchemas["OperatorAttentionItem"];
export type CollaborationStepInfo = ContractSchemas["CollaborationStep"];
export type RunTraceSummary = ContractSchemas["RunTraceSummary"];
export type RunUsageTotals = ContractSchemas["RunUsageTotals"];
export type RunUsageEntry = ContractSchemas["RunUsageEntry"];
export type RunUsageLedger = ArrayDefaults<ContractSchemas["RunUsageLedger"]>;

export type RuntimeInvariantFailure = ContractSchemas["RuntimeInvariantFailure"];
export type RunProjectionSnapshot = Omit<ArrayDefaults<ContractSchemas["RunProjectionSnapshot"]>, "run" | "usage"> & {
  run: ArrayDefaults<ContractSchemas["RunProjection"]>;
  usage: Omit<ContractSchemas["UsageProjection"], "ledger"> & { ledger: RunUsageLedger };
};
export type ModelRequestEnvelope = ContractSchemas["ModelRequestEnvelope"];
export type ModelRequestCapture = ContractSchemas["ModelRequestCapture"];
export type ModelRequestDebugResponse = ContractSchemas["ModelRequestDebugResponse"];
export type TaskItemStatus = ContractSchemas["TaskItemStatus"];
export type TaskBlockerStatus = ContractSchemas["TaskBlockerStatus"];
export type TaskState = ContractSchemas["TaskState"];

// UI commands narrow the generated wire shape with required-by-operation fields.
type TaskCommand<K extends ContractSchemas["TaskStateOperation"]["type"], F extends keyof ContractSchemas["TaskStateOperation"] = never> =
  { type: K } & Required<Pick<ContractSchemas["TaskStateOperation"], F>>;
export type TaskStateOperation =
  | TaskCommand<"set_goal", "goal">
  | TaskCommand<"clear_goal">
  | TaskCommand<"upsert_task", "task">
  | TaskCommand<"set_task_status", "task_id" | "task_status">
  | TaskCommand<"remove_task", "task_id">
  | TaskCommand<"add_decision", "decision">
  | TaskCommand<"upsert_constraint", "constraint">
  | TaskCommand<"remove_constraint", "constraint_id">
  | TaskCommand<"upsert_blocker", "blocker">
  | TaskCommand<"resolve_blocker", "blocker_id">
  | TaskCommand<"remove_blocker", "blocker_id">
  | TaskCommand<"add_artifact_ref", "artifact_ref">
  | TaskCommand<"remove_artifact_ref", "artifact_ref">;
export type TaskStatePatch = Omit<ContractSchemas["TaskStatePatch"], "operations"> & { operations: TaskStateOperation[] };
export type TaskStateRevision = ContractSchemas["TaskStateRevision"];
export type ToolArtifact = ContractSchemas["ToolArtifact"];
export type ToolEffectReconciliationAction = ContractSchemas["ToolEffectReconciliationAction"];
export type ToolEffect = ContractSchemas["ToolEffect"];
export type RecoveryEvidence = ContractSchemas["RecoveryEvidence"];
export type RecoveryAction = ContractSchemas["RecoveryAction"];
export type RecoverySummary = ArrayDefaults<ContractSchemas["RecoverySummary"]>;
export type RunReplay = Omit<ArrayDefaults<ContractSchemas["RunReplay"]>, "messages" | "projection" | "usage_ledger" | "recovery_summary"> & {
  messages: Message[];
  projection: RunProjectionSnapshot;
  usage_ledger: RunUsageLedger;
  recovery_summary?: RecoverySummary;
};
export type EpisodeReport = Omit<ArrayDefaults<ContractSchemas["EpisodeReport"]>, "messages" | "retrievals" | "verification"> & {
  messages: Message[];
  retrievals: ArrayDefaults<ContractSchemas["EpisodeRetrievals"]>;
  verification: ArrayDefaults<ContractSchemas["EpisodeVerification"]>;
};
