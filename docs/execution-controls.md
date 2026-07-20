# AgentFlow 执行限制与资源控制

本文是 AgentFlow 中所有主要执行限制的总入口。遇到名称相似的配置时，
先判断它的 **scope（作用域）**、**unit（计数单位）** 和
**owner（唯一执行方）**，再决定应该调整哪一个参数。

本文聚焦会影响 Agent 执行准入、容量、资源消耗和停止条件的控制。搜索返回条数、
上传大小、Memory 置信度等业务参数仍在各自专题文档中维护，不属于本文的执行限制。

## 一分钟速查

| 问题 | 应查看的控制 | 不要混淆为 |
| --- | --- | --- |
| 同时运行的任务太多 | Run Admission | 单个 Run 的调用次数 |
| Provider 并发或 429 压力过大 | Model Request Limiter | Run Budget |
| 某个 Run 消耗过多调用、Token 或成本 | Run Budget | RPM/TPM |
| 单次 Prompt 装不进模型 Context Window | Context Assembly | 累计 Run Token |
| Autonomous 循环太久或输出膨胀 | Autonomous Loop Guards | Model Retry |
| Tool 卡住、并行不安全或结果太大 | Tool Execution Policy | Run 并发 |
| Verification 无限重试或证据过大 | Verification Contract/Boundary | Model Retry |
| 进程崩溃后 Run 长时间保持 running | Recovery Stale Threshold | Run Runtime Budget |

## 概念分类

| 类型 | 解决的问题 | 典型行为 |
| --- | --- | --- |
| Admission | 工作是否可以进入系统 | 拒绝、排队、返回 `Retry-After` |
| Concurrency | 同一时刻允许多少工作 | Semaphore / single-writer |
| Rate Limit | 一段时间内允许多少请求或输入 Token | Token bucket、等待或拒绝 |
| Retry Policy | 一次逻辑操作允许多少次物理尝试 | 分类、退避、停止重试 |
| Run Budget | 一个持久化 Run 最多消耗多少资源 | reservation、settlement、typed error |
| Capacity | 一次请求能否装进固定容量 | Context 裁剪、压缩、`max_tokens` |
| Timeout | 一个具体操作最多等待多久 | Context cancel / typed timeout |
| Loop Guard | Agent 循环何时必须停止 | iteration/output 上限 |
| Observability | 已经发生了什么 | Event、Trace、Episode；不负责拦截 |

## 控制边界总表

| 控制 | Scope | Unit | Owner | 是否持久化 |
| --- | --- | --- | --- | --- |
| Run Admission | 进程 + Conversation | active/queued Run | `concurrency.RunController` | 否 |
| Model Request Limiter | 进程 + API Key | 物理 HTTP 请求、近似输入 Token | `concurrency.ModelRequestLimiter` | 否 |
| Model Retry | 单个逻辑 Model Call | 物理 attempt | `openai.RetryPolicy` | 否 |
| Run Budget | 单个持久化 Run | 逻辑调用、实际 Token、Tool、active runtime、成本 | `budget.Tracker` + Usage Store | 是 |
| Context Assembly | 单个逻辑 Model Call | Context Token 容量 | `contextassembly.Assembler` | 配置随 Runtime Snapshot 冻结 |
| Autonomous Loop | 单个 Autonomous Run | iteration、累计输出字符 | Autonomous runtime | 配置随 Runtime Snapshot 冻结 |
| Tool Policy | 单次 Tool Call / Batch | timeout、bytes、并行组 | `tools.Executor` | 模型可见定义随 Snapshot 冻结；Binding policy 为进程配置 |
| Verification | 单个有 Contract 的 Run | verifier attempt、timeout、artifact | `verification.Engine` | Contract/Evidence 持久化 |
| Recovery | 进程启动扫描 | running 状态持续时间 | `recovery` | Run 状态已持久化，阈值不持久化 |
| Trace / Episode | 单个 Run | 已发生的事件与 usage projection | Event Store / report builder | 是，但不参与 enforcement |

## 1. Run Admission 与并发

```env
MAX_CONCURRENT_RUNS=8
RUN_QUEUE_SIZE=32
RUN_QUEUE_WAIT_TIMEOUT=30s
```

- `MAX_CONCURRENT_RUNS`：进程内同时执行的 Agent Run 数量。
- `RUN_QUEUE_SIZE`：active slots 之外允许等待的请求数量。
- `RUN_QUEUE_WAIT_TIMEOUT`：等待 conversation writer 或 active slot 的最长时间。
- 同一个 `conversation_id` 始终是 single-writer，即使全局仍有空闲 slot。
- 队列已满返回 `429`；等待超时返回 `503`，两者都带 `Retry-After`。

它不统计 Model Call，也不限制一个 Run 能运行多少步骤。Run 一旦取得 slot，
内部发出多少模型请求由后续控制负责。

## 2. Model Request Limiter

```env
MAX_CONCURRENT_MODEL_REQUESTS=8
MODEL_REQUESTS_PER_MINUTE=60
MODEL_TOKENS_PER_MINUTE=120000
```

- `MAX_CONCURRENT_MODEL_REQUESTS`：进程内正在进行的物理模型 HTTP 请求。
  Chat 和 Embedding 共用；流式响应在 body 关闭前持续占用 slot。
- `MODEL_REQUESTS_PER_MINUTE`：按 API Key 建立的请求 token bucket。
- `MODEL_TOKENS_PER_MINUTE`：按序列化请求大小估算的输入 Token bucket，
  不统计流式输出 Token。
- 每次 Retry 都是新的物理请求，因此重新占用 concurrency slot，并消耗
  RPM/TPM。
- API Key 为空时不建立 per-key RPM/TPM bucket；真实 HTTP 请求仍受全局
  concurrency 控制。

单个请求大于 TPM bucket 总容量时返回
`request_token_capacity_exceeded`。这不是 Run Budget 超限。

## 3. Model Retry 与请求超时

```env
OPENAI_REQUEST_TIMEOUT=5m
MODEL_RETRY_MAX_ATTEMPTS=3
MODEL_RETRY_BASE_DELAY=500ms
MODEL_RETRY_MAX_DELAY=5s
```

- `OPENAI_REQUEST_TIMEOUT`：单个物理 Provider 请求的 HTTP 超时。
- `MODEL_RETRY_MAX_ATTEMPTS` 包含第一次请求；设为 `1` 表示不重试。
- Base/Max Delay 控制指数退避；Max Delay 也限制 Provider `Retry-After`。
- 只有 transport、timeout、rate limit、Provider `5xx` 等可恢复错误会重试。
- Auth、quota、model-not-found、invalid request、context length、content policy
  等错误立即失败。
- Streaming 只允许在首个 delta 之前重试，避免重复输出。

**关键口径：**一次逻辑 Model Call 可以包含多次物理 attempt。Retry 每次计入
RPM/TPM，但整个 Retry Policy 只占用一次 Run Budget model-call reservation。

## 4. Run Budget 与 Usage Ledger

```env
RUN_MAX_MODEL_CALLS=32
RUN_MAX_PROMPT_TOKENS=200000
RUN_MAX_COMPLETION_TOKENS=50000
RUN_MAX_TOTAL_TOKENS=250000
RUN_MAX_TOOL_CALLS=50
RUN_MAX_RUNTIME=15m
RUN_MAX_ESTIMATED_COST_USD=0
MODEL_INPUT_COST_PER_MILLION_TOKENS_USD=0
MODEL_OUTPUT_COST_PER_MILLION_TOKENS_USD=0
```

Run Budget 是单个持久化 Run 的累计资源上限，并随 Runtime Snapshot 冻结。
修改环境变量只影响新 Run；Resume/Replay 使用原 Run 的 Budget。

| Dimension | 计数口径 |
| --- | --- |
| Model calls | 逻辑调用；Provider Retry 不增加 |
| Prompt tokens | reservation 先估算，settlement 使用 Provider usage |
| Completion tokens | Provider 输出 usage；同时用于计算每次请求的 `max_tokens` |
| Total tokens | Prompt + Completion 的累计值 |
| Tool calls | 已解析、参数合法并获准执行的 Tool；handler error/timeout 仍计数 |
| Runtime | 累计 `running` segment；queue 和 `waiting_for_user` 不计入 |
| Estimated cost | 按冻结价格计算的整数 microdollars |

数值 `0` 表示关闭对应 dimension。价格配置本身不是上限；只有
`RUN_MAX_ESTIMATED_COST_USD > 0` 时才执行成本限制。

Usage Ledger 是 enforcement authority。Trace Summary 和 Episode 也会展示调用和
Token，但只是事件投影，不能用于预算扣减。完整记账模型见
[Run Budget and Usage Ledger](run-budget.md)。

## 5. Context Assembly 与 Compaction

```env
MODEL_CONTEXT_WINDOW_TOKENS=128000
MODEL_OUTPUT_RESERVE_TOKENS=8192
CONTEXT_SAFETY_MARGIN_TOKENS=4096
CONTEXT_HISTORY_MAX_TOKENS=64000
CONTEXT_MEMORY_MAX_TOKENS=8000
CONTEXT_KNOWLEDGE_MAX_TOKENS=16000
CONTEXT_TOOL_RESULT_MAX_TOKENS=2000

CONTEXT_COMPACTION_MODE=auto
CONTEXT_COMPACTION_SOFT_THRESHOLD=0.70
CONTEXT_COMPACTION_HARD_THRESHOLD=0.85
CONTEXT_COMPACTION_RECENT_TOKENS=16000
CONTEXT_COMPACTION_SUMMARY_MAX_TOKENS=2000
CONTEXT_COMPACTION_TIMEOUT=45s
```

单次请求可用输入容量为：

```text
context window - output reserve - safety margin
```

History、Memory、Knowledge 和 Tool Result 的配置是各来源上限，不是累计 Run
Token Budget。Assembler 会生成 Context Manifest 解释选取、裁剪与 Token 估算。

`MODEL_OUTPUT_RESERVE_TOKENS` 与 Run Budget completion capacity 都会影响请求
`max_tokens`，最终取更严格值：前者保证单次请求装得下，后者保证累计 Run
不超预算，因此不是重复扣账。

Soft Compaction 在完成后异步执行，不影响已完成 Run；Hard Compaction 是当前
Turn 的前置工作，归入 Run Ledger 的 `compaction` purpose。更多算法说明见
[Context Management](context-management.md)。

## 6. Autonomous Loop Guards

```env
AUTONOMOUS_MAX_ITERATIONS=5
AUTONOMOUS_MAX_RUNTIME_SECONDS=300
AUTONOMOUS_MAX_OUTPUT_CHARS=60000
AUTONOMOUS_MAX_TOOL_CALLS=20
```

- Iteration 和 output characters 由 Autonomous loop 直接管理。
- 一个 iteration 通常包含 Observe/Plan/Act/Review/Decide，多次 Model Call；
  iteration 不能等同于 model-call count。
- 对新 Snapshot v5，Autonomous runtime/tool 配置和通用 Run Budget 在创建 Run
  时取更严格值，结果只写入 `RuntimeRunBudget`，由 Run Budget 单独执行。
- Snapshot v4 及更早版本按原协议恢复，不改变历史 Run 行为。

默认配置下，Autonomous 的有效 runtime/tool cap 是 `5m/20`，而不是通用
Run Budget 的 `15m/50`。

Run Budget 只限制数量，不判断 Agent 是否重复、振荡或没有进展。重复动作检测
属于尚未实现的 Progress Guard。

## 7. Tool Execution Policy

Tool Executor 当前使用代码级默认值，Binding 可以覆盖：

| Control | Default | Scope |
| --- | --- | --- |
| Execution timeout | 30s | 单次 Tool Call |
| Max result bytes | 20,000 bytes | 单次 Tool Result |
| Batch concurrency | 4 workers | 单个 Tool Batch，不是进程全局 |

Tool 的模型可见定义（name、description、parameters）随 Run 冻结；实际 handler、
timeout、结果大小和并发策略来自当前进程的 Binding。并发模式默认 `serial`；只有
明确声明 `read_only` 或可解析的 `keyed` group 才并行。结果过大时保留 UTF-8
安全预览、原始 byte count 和 truncation 标记。

Tool timeout 与 `RUN_MAX_RUNTIME` 不重复：前者限制一个 handler，后者限制整个
Run 的累计 active execution。

## 8. Completion Verification

Verification 只在请求附带 `completion_contract` 时启用。主要限制包括：

| Control | Default / Range | Scope |
| --- | --- | --- |
| Verifier timeout | 30s，允许 1ms-5m | 单个 Verifier |
| Policy max attempts | 2，允许 1-5 | 同一 Completion Contract |
| Max artifacts | 8 | 单条 Evidence |
| `VERIFICATION_MAX_ARTIFACT_BYTES` | 65,536 bytes | 单个持久化 Artifact |

Verifier attempt 不等于 Model Retry，也不增加 Run Budget model-call count；当前
内置 Verifier 不调用模型。Contract 和 policy 随 Run 冻结。

## 9. Recovery Threshold

```env
RECOVERY_STALE_RUN_TIMEOUT=60s
```

它只用于启动恢复：超过阈值、仍处于 `running` 且 heartbeat 过旧的 Run 会被
标记为 `failed_recoverable`。它不是一次 Run 的执行时间上限，也不会写入
Run Budget。

## Frozen 与 Live Policy

| Frozen with Run | Live process policy |
| --- | --- |
| Run Budget | Run/Model concurrency |
| Context Assembly/Compaction config | Queue size/wait timeout |
| Autonomous iteration/output config | RPM/TPM buckets |
| Provider/model identity | Retry/backoff and HTTP timeout |
| Tool name、description、parameters | Tool handler、timeout、结果大小和并发策略 |
| Completion Contract | Recovery stale threshold |

冻结项保证 Resume/Replay 不因配置变化而改变协议；live policy 保护当前进程和
Provider，因此可以在不修改历史 Run 的情况下动态调整部署容量。

## 常见混淆

1. **RPM 不是 model-call budget。** Retry 会增加 RPM，但不增加 Run model calls。
2. **TPM 不是累计 Run tokens。** TPM 是时间窗口内的近似输入；Run Ledger 使用
   reservation + Provider settlement，包含输入和输出。
3. **Context Window 不是成本预算。** 它只决定一次请求能否装下。
4. **Iteration 不是 Model Call。** 一个 Autonomous iteration 通常有多次调用。
5. **Timeout 不是 Runtime Budget。** 单次请求/Tool/Verifier timeout 和整个 Run
   的累计 active runtime 分属不同 scope。
6. **Trace 不是 Ledger。** Trace 用于 debug；Ledger 是预算 authority。
7. **配置可以组合，但 execution owner 必须唯一。** 同一资源的多个 policy input
   应在 Snapshot 创建时解析为一个有效值，不能在运行时维护两套 counter。

## 推荐调参顺序

1. 根据模型真实 Context Window 设置 Context Assembly。
2. 根据 Provider 配额设置 RPM/TPM 和 model-request concurrency。
3. 根据机器容量设置 Run concurrency 和 queue。
4. 根据产品成本与失败半径设置 Run Budget。
5. 设置 Autonomous iteration/output profile；runtime/tool cap 会折叠进 Run Budget。
6. 最后调整 Tool、Verification 和 Recovery 的操作级 timeout。

一次只调整一个层级，并从 `/api/runs/{id}/usage`、Replay 和 Run Events 验证实际
效果，避免同时改动多个相似参数后无法判断是哪一层生效。

## 新增限制时的维护检查表

新增任何 limit、timeout、quota 或 guard 前，必须回答：

1. Scope 是 request、attempt、model call、turn、run、conversation、API key
   还是 process？
2. Unit 是 count、token、byte、duration、cost 还是 concurrency slot？
3. 唯一 enforcement owner 是哪个 package？
4. 在 admission 前、执行中还是 settlement 后检查？
5. 配置是随 Run 冻结，还是 live deployment policy？
6. `0`、负数和缺省值分别代表什么？
7. 超限返回什么 typed error、HTTP/SSE 状态和 Run Event？
8. Replay/Usage API 如何解释它，测试如何证明没有双重计数？

同时更新本文、`.env.example`、`internal/config.Config` 注释和边界测试。若新控制
与现有控制作用于同一 resource，应先合并 policy 或明确上下级关系，而不是新增
第二个运行时 counter。
