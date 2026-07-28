下面按依赖关系整理成可直接用于 GitHub Issues / Jira 的 RAG Backlog。工作量是相对估算，不代表排期。

状态约定：使用 Markdown 删除线的条目表示已经实现；保留原始内容用于追踪历史，不直接删除。

**P0：正确性与安全**

| ID | Backlog | 验收标准 | 依赖 | 工作量 |
|---|---|---|---|---|
| ~~RAG-001~~ | ~~统一 Retrieval Pipeline~~ | ~~HTTP API、Single、Multi、Loop 使用相同的 Embed、Recall、Rerank、Gate 流程；Runtime 不再直接调用 Store~~ | ~~无~~ | ~~M~~ |
| RAG-002 | 补齐 Pipeline 回归测试 | 同一查询经 API 和 Agent Runtime 返回一致结果；覆盖相关、无答案、Embedding 失败 | RAG-001 | M |
| RAG-003 | Workspace 强制隔离 | Conversation、Run、Document、Search 全链路携带 `workspace_id`；生产模式禁止无 Workspace 检索 | RAG-001 | L |
| RAG-004 | Chunk 级 ACL 前置过滤 | ACL/tenant filter 在 SQL 检索阶段执行；增加跨 Workspace 和无权限访问测试 | RAG-003、RAG-016B | L |
| ~~RAG-005~~ | ~~RAG Prompt Injection 防护~~ | ~~Context 明确标记为不可信资料；文档内容不能覆盖系统指令；记录检测和过滤原因~~ | ~~RAG-001~~ | ~~M~~ |
| RAG-031 | 语义 Prompt Injection 检测器 | 抽象 `PromptInjectionDetector`；保留规则检测器并增加可选语义分类器；默认以 shadow mode 运行；记录 detector/version、risk score、类别、建议动作、延迟和失败原因；语义检测失败时继续依赖规则与 untrusted boundary | RAG-005、RAG-007、RAG-009 | M |
| RAG-032 | Context-aware Injection Risk Aggregator | 综合规则命中、语义分数、文档来源、当前 Query、Agent 工具权限和运行模式，输出 Allow / Quarantine / Block；阈值通过 Golden Dataset 与消融评测确定；策略版本进入 Evaluation Run 和 Retrieval Trace | RAG-031、RAG-009、RAG-011 | M |

**P1：评测体系**

| ID | Backlog | 验收标准 | 依赖 | 工作量 |
|---|---|---|---|---|
| RAG-006 | 定义 Golden Dataset Schema | 支持 query、预期来源、answerable、forbidden sources、tags、版本信息 | 无 | S |
| RAG-007 | 建立 Golden Dataset v1 | 至少包含事实、改写、精确 ID、多跳、无答案、ACL、过期数据和注入攻击案例 | RAG-006 | M |
| RAG-008 | Dataset 版本管理 | Dataset 使用稳定 ID/version；已有版本不可被静默覆盖；变更有 changelog | RAG-006 | S |
| RAG-009 | 持久化 Evaluation Run | 保存 dataset、pipeline 配置、Embedding、Chunker、Top-K、逐题结果和运行时间 | RAG-007 | M |
| RAG-010 | 扩展离线评测指标 | 增加 MRR、NDCG、No-answer Precision/Recall、ACL 泄漏数和平均延迟 | RAG-009 | M |
| RAG-011 | Baseline 对比与消融 | 支持两个 Pipeline 版本使用同一 Dataset 对比；报告单变量变化 | RAG-009 | M |

**P1：检索质量**

| ID | Backlog | 验收标准 | 依赖 | 工作量 |
|---|---|---|---|---|
| ~~RAG-012~~ | ~~增加独立 Lexical Recall~~ | ~~关键词检索可以召回 Dense Top-K 中不存在的错误码、产品 ID 和专有名词~~ | ~~RAG-001~~ | ~~M~~ |
| ~~RAG-013~~ | ~~Dense + Lexical RRF 融合~~ | ~~两路候选独立召回；输出原始排名、RRF 分数和融合排名~~ | ~~RAG-012~~ | ~~M~~ |
| RAG-014 | Rerank 模块化 | 将当前启发式 Rerank 抽象为接口；支持配置版本和未来 Cross-Encoder | RAG-013 | M |
| RAG-015 | Relevance Gate 校准 | 阈值通过 Golden Dataset 确定；记录每个候选被保留或过滤的原因 | RAG-007、RAG-014 | M |

**P1：Context 与 Grounding**

| ID | Backlog | 验收标准 | 依赖 | 工作量 |
|---|---|---|---|---|
| ~~RAG-016A~~ | ~~Chunk Source Traceability（Chunk 来源追踪）~~ | ~~保存 `parent_id`、section path、offset、document version 和 content hash；字段可从 Ingest 传播至 Retrieval Trace~~ | ~~RAG-001~~ | ~~M~~ |
| RAG-016B | Chunk Scope 与 ACL | Chunk 关联 Workspace ownership 和 ACL metadata；定义权限策略，供检索前置过滤使用 | RAG-003、RAG-016A | M |
| RAG-017 | Parent-Child Retrieval | Child 用于检索；命中后按预算回填 Parent 或相邻 Chunk；多 Workspace 上线前必须补齐 Scope 过滤 | RAG-016A | L |
| RAG-018 | Context 去重与合并 | 相邻 Chunk 合并、重复来源去除、同文档结果聚合；不突破 Knowledge Token Budget | RAG-017 | M |
| RAG-019 | RAG 原生引用协议 | Context 分配 `[S1]` 等稳定来源 ID；结果返回结构化 `citations`；引用仅可指向本次选入 Context 的来源 | RAG-016A | L |
| RAG-020 | No-answer 与引用验证 | 证据不足时拒答；引用必须指向本次实际选入 Context 的 Chunk | RAG-015、RAG-019 | M |

**RAG-003 依赖边界**

- RAG-003 是 RAG-004、RAG-016B 和跨 Workspace 泄漏验证的硬依赖。
- RAG-016A、RAG-017、RAG-018、RAG-019、RAG-020 可以在 RAG-003 前实现和验证；它们属于溯源、Context Transformation、引用与 Grounding 能力，不依赖 Workspace 生命周期。
- 在 RAG-003、RAG-004、RAG-016B 完成前，RAG-017 至 RAG-020 只能声明为单 Workspace 模式可用，不得声明具备多租户安全保证。
- Golden Dataset 可以先纳入 ACL case 和泄漏指标的数据结构，但 ACL case 不作为发布通过条件；待 RAG-003、RAG-004、RAG-016B 完成后再启用其验收门禁。

**P2：平台与运维**

| ID | Backlog | 验收标准 | 依赖 | 工作量 |
|---|---|---|---|---|
| RAG-021 | Pipeline 全链路版本化 | 记录 Parser、Chunker、Embedding、Fusion、Reranker、Prompt、Context Builder 版本 | RAG-009 | M |
| RAG-022 | 分阶段 Retrieval Trace | 展示 Dense、Lexical、Fusion、Rerank、Gate、Context Selection 的耗时、排名和过滤原因 | RAG-013、RAG-021 | L |
| RAG-023 | 异步批量 Embedding | Ingest 改为任务化处理，支持批量、重试、状态查询和部分失败恢复 | RAG-021 | L |
| RAG-024 | 增量更新与幂等索引 | 根据 source/version/content hash 判断跳过、更新或重建；删除后向量完全清除 | RAG-023 | L |
| RAG-025 | Embedding Space 管理 | 移除业务层对 1536 维的固定假设；不同模型和维度使用隔离索引 | RAG-021 | L |
| RAG-026 | 敏感信息与 Trace 脱敏 | Query、Chunk 和 Metadata 按策略脱敏；敏感内容仅保存 hash 或摘要 | RAG-022 | M |

**P3：评测证明后再做**

| ID | Backlog | 启用条件 | 工作量 |
|---|---|---|---|
| RAG-027 | Query Rewrite / Route | Golden Dataset 证明简单检索在模糊、多跳问题上存在明确瓶颈 | M |
| RAG-028 | Cross-Encoder Reranker | 当前 Rerank 的 NDCG/MRR 无法满足目标，且延迟预算允许 | M |
| RAG-029 | Agentic RAG | 存在需要动态选择数据源或连续检索的真实案例 | L |
| RAG-030 | GraphRAG | Golden Dataset 中包含大量实体关系和全局关系查询 | XL |

**建议里程碑**
1. **M1：可信 Baseline**：完成 RAG-002、RAG-006 至 RAG-011，解决链路回归和可评测问题；ACL case 先进入 Dataset，但暂不作为发布门禁。
2. **M2：检索质量与 Grounding**：完成 RAG-014、RAG-015、RAG-016A、RAG-017 至 RAG-020；按单 Workspace 模式交付 Parent-Child、Context 合并、引用和拒答。
3. **并行安全轨道：多租户隔离**：按 RAG-003 → RAG-016B → RAG-004 推进；它不阻塞 M2 的算法实现，但必须在任何多租户发布前完成，并启用 ACL case 验收门禁。
4. **M3：平台化**：RAG-021 至 RAG-026，完成版本、Tracing、异步索引和运维能力。
5. **M4：高级能力**：仅根据评测结果选择 RAG-027 至 RAG-032，不默认全部实现；其中 RAG-031、RAG-032 属于安全增强，达到依赖条件后应优先于一般高级检索能力。
