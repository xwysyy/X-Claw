# X-Claw Round 5 重构需求文档

> 日期：2026-03-08
> 来源：`docs/plans/2026-03-08-round5-audit.md`
> 目标：把 Round 5 审查结论转化为可执行、可验证、可跟踪的需求清单。

---

## 一、目标与约束

## 1.1 目标

- 在**不改变现有功能、CLI 命令面、HTTP API 语义、会话格式**的前提下，继续压缩复杂度
- 修复仍可静态确认的残余 bug / 风险点
- 继续收敛 Agent / Tools / HTTPAPI / Session / Gateway / Provider 的运行时边界
- 补齐能证明本轮修复/拆分正确性的高价值回归测试

## 1.2 非目标

- 不重写 Agent 主循环语义
- 不修改 `gateway` / `agent` / `version` 的对外命令面
- 不改变 Provider / Session / HTTPAPI 的对外数据格式
- 不做大规模目录迁移（例如整包迁到 `internal`）
- 不引入新的 channel/provider 大功能

## 1.3 全局约束

- 所有重构默认必须保持行为不变，除非是修复明确 bug
- 优先做**小步、可验证、可回归**的增量修改
- 每一阶段结束后必须保持可编译、可测试
- 执行前要把任务拆分写入 `docs/plans/PROGRESS-R5.md`
- 每完成一个 Task，都必须更新 `docs/plans/PROGRESS-R5.md`

---

## 二、Bug 修复与风险收口需求

### REQ-R5-BUG-001：Agent 初始化路径不得在库层直接 `log.Fatalf`

- **来源**：BUG-R5-001
- **要求**：`pkg/agent/instance.go` 的 `buildBaseAgentToolRegistry(...)` 不得再直接终止进程
- **验收**：
  - exec tool 初始化失败时，错误路径对调用方可观测
  - 不因单个工具初始化失败而直接 `os.Exit`
  - 行为保持可预测：要么优雅降级禁用相关工具，要么明确返回错误

### REQ-R5-BUG-002：Tool trace 持久化失败不得静默吞掉

- **来源**：BUG-R5-002
- **要求**：`pkg/tools/tool_trace.go` 对 `f.Sync()` 失败必须有明确可观测路径
- **验收**：
  - 失败有 warn/error 日志
  - 不影响正常 tool 执行主流程
  - 有定向回归或单元级验证

### REQ-R5-BUG-003：MemoryStore 初始化失败不得被延后成模糊故障

- **来源**：BUG-R5-003
- **要求**：`pkg/agent/memory.go` 中的 memory 目录初始化失败必须被显式暴露
- **验收**：
  - 不再 `_ = os.MkdirAll(...)`
  - 调用链能得到清晰错误或至少清晰日志
  - 不改变 MemoryStore 读写成功路径行为

### REQ-R5-BUG-004：Console 文件/流接口应收敛内部错误暴露面

- **来源**：BUG-R5-004
- **要求**：`pkg/httpapi/console_file.go`、`pkg/httpapi/console_stream.go` 不应把内部系统错误细节直接回显给远端客户端
- **验收**：
  - HTTP 响应使用稳定可理解的通用错误
  - 详细错误写入日志
  - 不影响已有 authorized / loopback 使用方式

### REQ-R5-BUG-005：Session replay 失败时语义必须明确

- **来源**：BUG-R5-005
- **要求**：`pkg/session/manager.go` 在 JSONL replay 失败时不能留下“看起来有效、实际上历史不完整”的模糊 session
- **验收**：
  - replay 失败要么跳过加载，要么带明显 degraded 标记
  - console/session 读取路径对这种状态可观测
  - 有回归测试覆盖损坏/不完整 session 场景

---

## 三、结构精简与职责收敛需求

### REQ-R5-ARCH-001：继续拆分 `pkg/agent/loop.go`

- **来源**：代码规模热点
- **要求**：将主循环继续按职责物理拆分
- **验收**：
  - 至少把 queue / publish / MCP reload / batch execution / fallback 或相近职责中的 2~3 类下沉到独立文件
  - `loop.go` 明显缩小
  - 不改变主循环行为

### REQ-R5-ARCH-002：继续拆分 `pkg/agent/memory.go`

- **来源**：代码规模热点
- **要求**：将 memory block 操作、daily notes、organize writeback、hybrid retrieval 继续收口到更小文件
- **验收**：
  - `memory.go` 不再承担所有 memory 职责
  - vector / fts / block helpers 的边界更清楚
  - 现有 MemoryStore 行为不变

### REQ-R5-ARCH-003：继续拆分 `pkg/tools/filesystem.go` 与 `pkg/tools/shell*.go`

- **来源**：代码规模热点
- **要求**：将文件系统工具与 shell/process 工具的“请求解析 / 安全策略 / 执行 / 输出格式化 / 生命周期管理”进一步分离
- **验收**：
  - `filesystem.go` 与 shell 相关大文件进一步缩小
  - helper 与用户可见工具语义边界更清晰
  - 保持现有工具名称、参数和返回格式不变

### REQ-R5-ARCH-004：继续拆分 `pkg/session/manager.go`

- **来源**：代码规模热点
- **要求**：按“内存态管理 / JSONL replay / meta snapshot / legacy migration / GC / snapshot list”拆分 SessionManager
- **验收**：
  - manager 主文件职责更聚焦
  - replay / persist / snapshot / GC 不再混在一个文件内
  - 行为和持久化格式不变

### REQ-R5-ARCH-005：收敛 HTTPAPI 鉴权与 console helper

- **来源**：RISK-R5-001
- **要求**：console / notify / resume 等 HTTPAPI handler 使用统一鉴权 helper 与一致的错误面策略
- **验收**：
  - 不再存在等价但分散的 authorize 实现
  - 文件 helper / trace list helper / stream helper 边界进一步清晰
  - `pkg/httpapi` 的聚合测试可通过更稳定的窄批次命令验证

### REQ-R5-ARCH-006：继续收敛 Gateway / Channels / Cron 的生命周期职责

- **来源**：代码规模热点
- **要求**：继续把 `pkg/channels/manager.go`、`pkg/channels/manager_dispatch.go`、`pkg/cron/service.go` 分离成更明确的生命周期和执行职责
- **验收**：
  - channel manager 的 worker / dispatch / lifecycle 逻辑更清晰
  - cron service 的 store / schedule / execute / state mutate 更清晰
  - 不改变 Gateway 对外行为

### REQ-R5-ARCH-007：继续收敛 Provider / Config / Auth 的事实来源

- **来源**：边界收敛需要
- **要求**：继续让 provider fallback / protocol alias / auth credential / default config 以单一事实来源为准
- **验收**：
  - provider 选择链路更容易追踪
  - `config/defaults`、provider factory、fallback 之间不再出现双轨逻辑
  - interactive auth / store / error message 语义更清晰

---

## 四、测试与验证需求

### REQ-R5-TEST-001：每个 bug fix 必须有回归测试或等价验证

- `log.Fatalf` / init failure / degraded load / trace sync / console error surface 相关修改，必须至少补一个定向测试或等价的注入式验证

### REQ-R5-TEST-002：物理拆分后必须保持 compile-only 全仓验证通过

- 必须通过：
  - `go build -p 1 ./...`
  - `go vet ./...`
  - `go test ./... -run '^$' -count=1`

### REQ-R5-TEST-003：关键包需要有窄批次定向测试

- 至少覆盖：
  - `pkg/agent`
  - `pkg/tools`
  - `pkg/httpapi`
  - `pkg/session`
  - `pkg/channels` / `cmd/x-claw/internal/gateway`
  - `pkg/providers`

### REQ-R5-TEST-004：环境限制必须记录到进度文档

- 如果 `-race` 或大包聚合测试在当前环境触发 `SIGKILL(137)` 或 `exit -1`，必须记录：
  - 实际执行了什么
  - 为什么改用更小批次验证
  - 替代验证命令是什么

---

## 五、建议的执行顺序

建议下一轮按以下顺序推进：

1. 先做 Bug 修复与风险收口
2. 再做 Agent / Tools / Session / HTTPAPI 的物理拆分
3. 再做 Gateway / Channels / Cron 与 Providers / Config 的边界收敛
4. 最后统一做阶段门验证与全仓验证
