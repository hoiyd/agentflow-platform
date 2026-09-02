# Bounded Child Run Delegation

Multi-Agent 的 Worker 不再直接使用父 Run 执行。Router 选中 Agent 后，父
Stage 创建一个持久化的 Child Run；Planner、Router、Reviewer 和 Finalizer
仍保留在父 Run 中。

```text
Parent Run
  Planner -> Router -> Worker delegation -> Reviewer -> Finalizer
                         |
                         `-> isolated Child Run -> Worker Turn / Tools
```

## Isolation contract

Child Run 冻结并独立持有：

- selected Agent 的 system prompt、native execution protocol 和显式 Tool allowlist；
- 父 Run 已冻结的 provider/model identity；
- 独立 Run Budget、timeout、Context Assembly 配置和 Usage Ledger；
- `delegation_id`、`parent_run_id`、`parent_turn_id`、父 Stage 和 depth。

Child 的 timeout、summary cap、definition source 和 Budget 在父 Multi-Agent
Run 创建时先冻结为 `child_run_policy`。因此父 Run 等待 plan approval 期间的
环境配置变化不会改变之后创建的 Child。全局/单 Parent 并发上限属于进程级
admission policy，不进入 Snapshot。

Child 只能取得“selected Agent allowlist”和“父 Snapshot 中已冻结 Tool”
的交集。父 Run 候选 Agent 的 Tool 并集不会被继承，`update_task_state` 也不
会进入 Child。首版 depth 固定为 `1`，Child 不能继续创建孙 Run。

Child 使用显式 task 作为输入，不自动读取父 Conversation 的原始历史、
Context Compaction 或 durable Task State。Agent 自身显式允许的 Memory / RAG
retrieval 仍可在 Child Context 中执行，并记录在 Child Trace。

## Bounds and backpressure

```env
MAX_CONCURRENT_CHILD_RUNS=2
MAX_CHILD_RUNS_PER_PARENT=1
CHILD_RUN_TIMEOUT=2m
CHILD_RUN_SUMMARY_MAX_CHARACTERS=4000
CHILD_RUN_MAX_MODEL_CALLS=8
CHILD_RUN_MAX_TOTAL_TOKENS=12000
CHILD_RUN_MAX_TOOL_CALLS=8
```

Child admission 是独立的无队列控制器。达到全局或单 Parent 上限时立即返回
可重试的 typed capacity error，不占用顶层 `RUN_QUEUE_SIZE`。模型 HTTP 请求
仍受全局 Model Request Limiter 控制。

Child 的 Budget 独立记账，不消耗父 Run 的 model/tool/token 计数。它仍受
进程级模型并发和 RPM/TPM 约束。heartbeat 每 15 秒刷新；父 Run 取消会通过
运行中 registry 传播给 Child context。

## Result boundary

完整 Worker 输出只保存在 Child 自己的 Stage、Trace 和 Replay。父 Run 只
接收：

- 字符数受限的 `summary`；
- `run://<child-run>/stages/<worker-stage>` 引用；
- 完整输出的 SHA-256、byte size 和 truncated 标记。

当前 `run://` 引用是 Trace-backed artifact reference，而不是通用 Artifact
Store。H-01 引入通用 Artifact 后，可以替换其物理存储，但不需要改变
Delegation contract。

## Durability and recovery

File Store 和 Postgres 都会原子创建 Child Run 与 `RunDelegation`。父 Run 的
Replay 返回 `child_delegations`，Child Replay 返回 `parent_delegation`。

启动恢复先由 H-08 将 stale Child Run 修复为 `failed_recoverable`，再对仍处于
`created/running` 的 Delegation 做幂等 reconciliation：

- Child 已完成：从 durable Child Stage 重建 bounded summary 和 output ref；
- Child failed：关闭 Delegation 并失败父 Worker Stage；
- Child failed_recoverable：将 Delegation 标记为 `blocked`，并记录
  `block_reason=child_recovery_required`；父 Run Resume 使用同一 Child Run、
  冻结 Snapshot 和 Delegation 继续执行；
- Child canceled：关闭 Delegation 并失败父 Worker Stage；
- Child 仍有效运行或 queued：保持原状态，不猜测结果。

reconciliation 会补写 synthetic typed `delegation.completed/blocked/failed/canceled`
事件。它不会自动重放未知副作用；外部 Tool 的安全恢复仍由 H-08 Tool Effect
Journal 负责。

## Typed lifecycle

父 Run 持久化以下事件：

- `delegation.created`
- `delegation.started`
- `delegation.completed`
- `delegation.failed`
- `delegation.canceled`

这些事件只携带 bounded summary、引用和诊断元数据，不携带完整 Child 输出。
