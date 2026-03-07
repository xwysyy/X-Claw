# X-Claw Round 4 重构需求文档

> 日期：2026-03-08
> 来源：`docs/plans/2026-03-08-round4-audit.md`
> 目标：将 Round 4 审查结论转化为可执行、可验证、可跟踪的需求清单。

---

## 一、目标与约束

## 1.1 目标

- 在**不改变外部功能与核心使用方式**的前提下，修复本轮确认的残余 bug
- 继续降低运行时复杂度与长期运行风险
- 为后续重构建立更清晰的 runtime / provider / tools / channels 边界
- 补齐高价值回归测试与 race 级验证

## 1.2 非目标

- 不新增 channel/provider 大功能
- 不重写 agent 主循环语义
- 不调整 CLI 主命令面（仍保持 `gateway` / `agent` / `version`）
- 不做大规模目录迁移（例如 `pkg` 整体迁移到 `internal`）
- 不改变 HTTP API、消息格式、会话格式、存储布局的对外语义

## 1.3 全局约束

- 所有重构必须保持现有行为不变，除非是修复明确 bug
- 优先做**小步、可回归、可验证**的改动
- 每个阶段结束必须保留可编译、可测试状态
- 当前工作树非干净状态，执行时不得覆盖现有未提交改动
- 开发进度必须记录到 `docs/plans/PROGRESS-R4.md`

---

## 二、P0 Bug 修复需求

### REQ-R4-BUG-001：`StartAll` 必须真实反映启动结果

- **来源**：BUG-R4-001
- **要求**：`pkg/channels/manager.go` 中 `StartAll` 不能在“0 个 channel 成功启动”时返回成功
- **验收**：
  - 所有 channel 启动失败时返回错误
  - `healthServer` 不能被置为 ready
  - 保留已有部分启动成功时的当前行为

### REQ-R4-BUG-002：`StopAll` 后发送路径必须安全

- **来源**：BUG-R4-002
- **要求**：`StopAll` 后再走 `SendToChannel` 不得 panic
- **验收**：
  - 不向 closed queue 发送
  - 返回明确错误或走安全丢弃策略
  - reload / shutdown 路径下均不产生 panic

### REQ-R4-BUG-003：reload 必须具备原子切换或显式回滚语义

- **来源**：BUG-R4-003
- **要求**：`cmd/x-claw/internal/gateway/reload.go` 必须先完成新 runtime 预热，再切换旧 runtime
- **验收**：
  - 新 manager 启动失败时，旧 manager 保持可用
  - `svc.cfg` 与 `agentLoop` 不得在切换失败后停留在半更新状态
  - reload 失败必须留下明确日志

### REQ-R4-BUG-004：本地 HTTP provider 默认 base 语义必须正确

- **来源**：BUG-R4-004
- **要求**：`ollama`、`vllm`、允许默认本地地址的 `litellm` 场景不能被错误要求 `api_key`
- **验收**：
  - 若协议有默认 `api_base`，则校验逻辑基于“补全后的有效配置”判断
  - 现有远端 provider 的要求不被放松

### REQ-R4-BUG-005：用户错误提示必须匹配当前 CLI 实际能力

- **来源**：BUG-R4-005
- **要求**：所有 provider/OAuth 相关报错不得再提示执行不存在的 `x-claw auth login`
- **验收**：
  - 错误提示引用真实可行的操作路径
  - 同类 provider 的提示文案保持一致

### REQ-R4-BUG-006：`ContextBuilder` 必须具备并发安全保证

- **来源**：BUG-R4-006
- **要求**：`SetRuntimeSettings`、`SetWebEvidenceMode`、`SetToolsRegistry` 与构建消息/提示词逻辑并发执行时不得有 data race
- **验收**：
  - `go test -race` 覆盖相关场景通过
  - 不引入明显性能退化

### REQ-R4-BUG-007：scoped memory cache 必须具备回收策略

- **来源**：BUG-R4-007
- **要求**：`ContextBuilder.memoryScopes` 必须有 TTL、LRU 或容量上限中的至少一种
- **验收**：
  - 大量 session/channel/chat 场景下不会无界增长
  - 回收后功能不变，可按需重建 `MemoryStore`

### REQ-R4-BUG-008：后台 shell session 表必须具备回收策略

- **来源**：BUG-R4-008
- **要求**：`pkg/tools/shell_session.go` 中后台会话元数据不能无界增长
- **验收**：
  - 已完成/失败/被杀死会话可被自动或显式回收
  - 回收策略可测试、可预测

### REQ-R4-BUG-009：ToolRegistry 必须防御 nil `ToolResult`

- **来源**：BUG-R4-009
- **要求**：`ToolRegistry.ExecuteWithContext` 对异常 tool 返回 `nil` 时必须返回标准错误结果而非 panic
- **验收**：
  - 增加单测覆盖
  - 调用链保持可观测

### REQ-R4-BUG-010：trace / heartbeat / startup 失败必须可观测

- **来源**：BUG-R4-010
- **要求**：运行时 best-effort 路径的失败不能被静默吞掉或误报成功
- **验收**：
  - `loop_trace` 的落盘同步失败可记录
  - `heartbeat` 的消息发送失败可记录
  - gateway startup 日志不再在失败后打印成功信息
  - `setupCronTool` 不再 `log.Fatalf`

### REQ-R4-BUG-011：日志流 follow 必须正确处理轮转与首尾失败

- **来源**：BUG-R4-011
- **要求**：`console_stream.go` 的 follow 模式需对 truncate / rotate / 首次 tail 失败做出可预测处理
- **验收**：
  - 至少覆盖 truncate 与 rename+new file 两类场景
  - 首次 tail 失败不会留下“无错误、半连接”的不可观测状态

---

## 三、架构与代码精简需求

### REQ-R4-ARCH-001：提取共享 runtime bootstrap

- **来源**：ARCH-R4-001
- **要求**：抽离 `agent` / `gateway` 入口共有的装配流程
- **验收**：
  - 入口层只保留参数与生命周期控制
  - 共享 bootstrap 成为单一事实来源

### REQ-R4-ARCH-002：统一 provider 选择与默认 base/协议映射

- **来源**：ARCH-R4-002
- **要求**：收敛 `legacy_provider` / `factory_provider` / `factory_selection` / `loop_fallback` 的选择与映射逻辑
- **验收**：
  - 运行时路径唯一清晰
  - 遗留路径要么退役、要么明确标注 legacy-only

### REQ-R4-ARCH-003：继续拆解 Gateway 组合根职责

- **来源**：ARCH-R4-003
- **要求**：`cmd/x-claw/internal/gateway/helpers.go` 继续拆为 bootstrap/runtime/reload/shutdown 等职责单元
- **验收**：
  - 文件与函数职责清晰
  - 不改变 Gateway 命令对外行为

### REQ-R4-ARCH-004：继续拆解 `ContextBuilder` / `AgentInstance` / `filesystem` 工具文件

- **来源**：ARCH-R4-004
- **要求**：以“职责收敛”为目标，而非仅仅挪代码
- **验收**：
  - `pkg/agent/context.go` 拆成更清晰的 prompt/cache/memory/pruning 单元
  - `pkg/agent/instance.go` 的装配逻辑职责下降
  - `pkg/tools/filesystem.go` 按工具行为与底层 fs helper 拆开

### REQ-R4-ARCH-005：HTTPAPI 鉴权与文件读取 helper 必须收敛

- **来源**：ARCH-R4-005
- **要求**：统一 notify/resume/console 鉴权 helper 与 tail/stream/session 文件读取 helper
- **验收**：
  - 多入口共享同一套 helper
  - 认证与只读文件访问逻辑表驱动或集中化

### REQ-R4-ARCH-006：Channels interaction / dispatch 必须进一步分层

- **来源**：ARCH-R4-006
- **要求**：typing/reaction/placeholder / worker dispatch 不再继续扩大 `Manager` 的直接职责
- **验收**：
  - 至少抽出一个 interaction coordinator 或等价子模块
  - dispatcher 背压语义更清楚

### REQ-R4-ARCH-007：遗留 `toolloop.go` 必须退役或对齐主循环

- **来源**：ARCH-R4-007
- **要求**：不得继续保留“看似可用、实则与主循环逻辑分叉”的僵尸实现
- **验收**：
  - 要么删除并清理引用
  - 要么明确复用主循环的 loop detection / tool execution 语义

---

## 四、测试与验证需求

### REQ-R4-TEST-001：新增 `ContextBuilder` race 回归

- 覆盖：并发设置 runtime/web/tools 与 BuildMessages/BuildSystemPrompt

### REQ-R4-TEST-002：新增 channel 启动/停止/发送安全回归

- 覆盖：
  - `StartAll` 全失败
  - `StopAll` 后发送
  - 慢 channel 背压

### REQ-R4-TEST-003：新增 reload 回滚回归

- 覆盖：新 manager 启动失败、旧状态保留、ready/route 不污染

### REQ-R4-TEST-004：新增 provider factory 默认 base 回归

- 覆盖：`ollama` / `vllm` / `litellm` 的本地默认地址场景

### REQ-R4-TEST-005：新增 shell session GC 回归

- 覆盖：completed / failed / killed / clear / remove / capacity

### REQ-R4-TEST-006：新增 stream follow 轮转回归

- 覆盖：truncate、rename+new file、首次 tail 失败、客户端取消

### REQ-R4-TEST-007：新增 nil `ToolResult` 防御回归

- 覆盖：mock tool 返回 nil 时的错误路径

---

## 五、优先级与阶段建议

## 5.1 阶段优先级

### Phase A：P0 Bug 与运行时安全

- REQ-R4-BUG-001
- REQ-R4-BUG-002
- REQ-R4-BUG-003
- REQ-R4-BUG-004
- REQ-R4-BUG-005
- REQ-R4-BUG-006
- REQ-R4-BUG-009

### Phase B：P1 长跑稳定性与可观测性

- REQ-R4-BUG-007
- REQ-R4-BUG-008
- REQ-R4-BUG-010
- REQ-R4-BUG-011

### Phase C：P1/P2 结构瘦身

- REQ-R4-ARCH-001
- REQ-R4-ARCH-002
- REQ-R4-ARCH-003
- REQ-R4-ARCH-004
- REQ-R4-ARCH-005
- REQ-R4-ARCH-006
- REQ-R4-ARCH-007

### Phase D：验证补齐

- REQ-R4-TEST-001 ~ REQ-R4-TEST-007

## 5.2 并行原则

适合并行的前提：

- 修改文件集合不重叠
- 不共享同一条关键控制流
- 不互相依赖未落地前置重构

推荐并行域：

- Gateway/Channels/HTTPAPI
- Providers/Config/CLI bootstrap
- Agent/Tools/stability

---

## 六、验收总标准

当以下条件全部满足时，Round 4 可视为完成：

1. P0 / P1 bug 均有代码与测试落地
2. 不存在已知“全部启动失败仍 ready”“关闭后发送 panic”“reload 半切换”问题
3. Provider 默认 base 与错误提示语义正确
4. `ContextBuilder` 并发测试通过，race 不报错
5. scoped memory 与 shell sessions 具备边界控制
6. `ToolRegistry` nil result 不 panic
7. Gateway / Agent 命令入口仍保持原有外部行为
8. `docs/plans/PROGRESS-R4.md` 记录完整

