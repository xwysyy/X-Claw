# X-Claw Round 5 Resilience and Slimming Implementation Plan

> **For Claude / Codex:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 在不改变现有功能、CLI 命令面和 HTTP 接口语义的前提下，完成 Round 5 的残余 bug 清扫、继续精简大文件，并收敛 HTTPAPI / Session / Provider / Gateway 的边界。

**Architecture:** 本轮继续坚持“小步可回归、先 bug 后拆分、先收状态后拆职责”。优先修掉确定性的 hard-exit / 吞错 / 语义模糊问题，再做物理拆分与 helper 收口，最后做阶段门与全仓验证。

**Tech Stack:** Go、Cobra、现有 `pkg/agent` / `pkg/tools` / `pkg/httpapi` / `pkg/session` / `pkg/channels` / `pkg/providers` / `cmd/x-claw/internal/gateway` 运行时。

---

## 实施总规则

- 开始前先阅读：
  - `docs/plans/2026-03-08-round5-audit.md`
  - `docs/plans/2026-03-08-round5-requirements.md`
  - `docs/plans/PROGRESS-R5.md`
  - `docs/plans/PROGRESS-R4.md`
- 先把本文件再细分成任务清单写入 `docs/plans/PROGRESS-R5.md`
- 每完成一个任务，必须更新 `docs/plans/PROGRESS-R5.md`
- 每个 Task 都遵循：写测试 / 证明缺口 → 最小修复 → 跑通过 → 记录进度
- 物理拆分任务默认只做职责移动与 helper 收口，不混入新的行为变更
- 涉及不重叠写集合的任务可以并行，但必须先标明并行边界

---

## 本次执行任务清单（先拆分，再并行）

### 总协调规则

- 文档写集合由主控串行维护：
  - `docs/plans/2026-03-08-round5-implementation.md`
  - `docs/plans/PROGRESS-R5.md`
- 代码改动按三组并行：
  - Gateway / Channels
  - Providers / Config
  - Agent / Tools / HTTPAPI / Session
- 若发现隐藏依赖，优先完成前置 bug fix，再进入后续物理拆分

### 并行组 1：Gateway / Channels

1. `Task C.1`：继续拆 `pkg/channels/manager.go` / `manager_dispatch.go`
2. `Task C.2`：继续拆 `pkg/cron/service.go`
3. `Task C.3`：继续下沉 gateway runtime / reload / httpapi 装配 helper
4. 阶段内验证：完成后跑 `pkg/channels` / `pkg/cron` / `cmd/x-claw/internal/gateway` 定向测试

### 并行组 2：Providers / Config

1. `Task D.1`：收敛 provider fallback / protocol alias / config default 事实来源
2. `Task D.2`：继续拆 auth/provider interactive flow 与错误提示路径
3. 阶段内验证：完成后跑 `pkg/providers` / `pkg/config` / `pkg/auth` 定向测试

### 并行组 3：Agent / Tools / HTTPAPI / Session

1. `Task A.1`：移除 Agent 初始化路径的 `log.Fatalf`
2. `Task A.2`：修复 tool trace sync 吞错与 memory init 模糊失败
3. `Task A.3`：收敛 console auth / error surface / degraded session 语义
4. `Task B.1`：继续拆 `pkg/agent/loop.go`
5. `Task B.2`：继续拆 `pkg/agent/memory.go`
6. `Task B.3`：继续拆 `pkg/tools/filesystem.go` / `pkg/tools/shell*.go`
7. `Task B.4`：继续拆 `pkg/session/manager.go` / console helper
8. 阶段内验证：完成后跑 `pkg/agent` / `pkg/tools` / `pkg/httpapi` / `pkg/session` 定向测试

### 主控更新节奏

1. 任何组完成一个 Task 后，立即更新 `docs/plans/PROGRESS-R5.md`
2. 每个阶段结束后，补跑对应 Gate
3. 全部任务完成后，再统一补跑最终验证

---

### Task A.1: 移除 Agent 初始化路径的 `log.Fatalf`

**Files:**
- Modify: `pkg/agent/instance.go`
- Modify: `pkg/agent/instance_test.go`
- Update progress: `docs/plans/PROGRESS-R5.md`

**Step 1: 写失败测试**

- 构造一个会让 `tools.NewExecToolWithConfig(...)` 报错的配置
- 断言：
  - 不发生 hard-exit
  - `NewAgentInstance(...)` 或相关 helper 的失败路径可观测
  - 若采用降级策略，`exec` / `process` 工具被禁用但 agent 其余工具可用

**Step 2: 跑定向测试证明缺口**

Run: `go test ./pkg/agent -run 'NewAgentInstance|ExecTool' -count=1`

**Step 3: 最小修复**

- 去掉库层 `log.Fatalf`
- 改为：
  - 显式返回 error，或
  - 记录日志并优雅降级禁用对应工具
- 保持对外 API 行为尽可能稳定

**Step 4: 再跑测试**

Run: `go test ./pkg/agent -run 'NewAgentInstance|ExecTool' -count=1`

**Step 5: 记录进度**

- 更新 `docs/plans/PROGRESS-R5.md`

---

### Task A.2: 修复 tool trace sync 吞错与 memory init 模糊失败

**Files:**
- Modify: `pkg/tools/tool_trace.go`
- Modify: `pkg/agent/memory.go`
- Modify relevant tests under `pkg/tools` / `pkg/agent`
- Update progress: `docs/plans/PROGRESS-R5.md`

**Step 1: 写缺口测试 / 注入验证**

- 为 tool trace 写一个 sync failure 注入路径
- 为 memory store 初始化写不可创建目录场景验证
- 断言：
  - 失败可观测
  - 主流程行为不被意外打断

**Step 2: 跑定向测试**

Run: `go test ./pkg/tools ./pkg/agent -run 'Trace|MemoryStore|MemoryForSession' -count=1`

**Step 3: 最小修复**

- tool trace 的 `f.Sync()` 失败至少要记录 warn
- memory store 的初始化失败不能再 `_ = os.MkdirAll(...)`
- 保持成功路径行为不变

**Step 4: 再跑测试**

Run: `go test ./pkg/tools ./pkg/agent -run 'Trace|MemoryStore|MemoryForSession' -count=1`

**Step 5: 记录进度**

- 更新 `docs/plans/PROGRESS-R5.md`

---

### Task A.3: 收敛 console auth / error surface / degraded session 语义

**Files:**
- Modify: `pkg/httpapi/console_auth.go`
- Modify: `pkg/httpapi/console_notify.go`
- Modify: `pkg/httpapi/console_file.go`
- Modify: `pkg/httpapi/console_stream.go`
- Modify: `pkg/httpapi/console_sessions.go`
- Modify: `pkg/session/manager.go`
- Modify: `pkg/httpapi/console_test.go`
- Modify: `pkg/session/manager_test.go`
- Update progress: `docs/plans/PROGRESS-R5.md`

**Step 1: 写失败测试**

- 覆盖：
  - notify / console / resume 使用统一鉴权 helper
  - file/stream 不再直接回显内部错误细节
  - 损坏 JSONL / replay 失败时 session 语义明确

**Step 2: 跑定向测试**

Run: `go test ./pkg/httpapi ./pkg/session -run 'Console|Notify|Session|Replay' -count=1`

**Step 3: 最小修复**

- 统一鉴权 helper
- 统一文件/流错误面
- session replay failure 改为明确 degraded/skip 语义

**Step 4: 再跑测试**

Run: `go test ./pkg/httpapi ./pkg/session -run 'Console|Notify|Session|Replay' -count=1`

**Step 5: 记录进度**

- 更新 `docs/plans/PROGRESS-R5.md`

---

### Task B.1: 继续拆 `pkg/agent/loop.go`

**Files:**
- Modify: `pkg/agent/loop.go`
- Create/Modify: `pkg/agent/loop_*.go`
- Modify related tests under `pkg/agent`
- Update progress: `docs/plans/PROGRESS-R5.md`

**Step 1: 先只做职责切分，不改行为**

优先拆以下 2~3 组职责：

- inbound queue / bucket / steering
- outbound publish / response send
- MCP reload / shared tool refresh
- fallback / provider switch glue

**Step 2: 跑定向测试**

Run: `go test ./pkg/agent -run 'Loop|Fallback|Resume|Publish' -count=1`

**Step 3: 记录进度**

- 更新 `docs/plans/PROGRESS-R5.md`

---

### Task B.2: 继续拆 `pkg/agent/memory.go`

**Files:**
- Modify: `pkg/agent/memory.go`
- Create/Modify: `pkg/agent/memory_*.go`
- Modify related tests under `pkg/agent`
- Update progress: `docs/plans/PROGRESS-R5.md`

**Step 1: 先只做职责切分，不改行为**

优先拆以下 2~3 组职责：

- long-term / daily notes
- block parse / normalize / render
- organize writeback
- retrieval merge / hybrid scoring

**Step 2: 跑定向测试**

Run: `go test ./pkg/agent -run 'Memory|SearchRelevant|OrganizeWriteback' -count=1`

**Step 3: 记录进度**

- 更新 `docs/plans/PROGRESS-R5.md`

---

### Task B.3: 继续拆 `pkg/tools/filesystem.go` / `pkg/tools/shell*.go`

**Files:**
- Modify: `pkg/tools/filesystem.go`
- Modify/Create: `pkg/tools/filesystem_*.go`
- Modify: `pkg/tools/shell.go`
- Modify: `pkg/tools/shell_session.go`
- Modify/Create: `pkg/tools/shell_*.go`
- Modify related tests under `pkg/tools`
- Update progress: `docs/plans/PROGRESS-R5.md`

**Step 1: 先拆 helper，不改行为**

优先拆以下职责：

- filesystem 请求解析 / truncate 策略 / FS helper
- shell 参数解析 / backend 选择 / kill / poll / remove / write
- process manager 生命周期与 JSON 输出拼装

**Step 2: 跑定向测试**

Run: `go test ./pkg/tools -run 'Filesystem|Shell|Process|ExecuteToolCalls' -count=1`

**Step 3: 记录进度**

- 更新 `docs/plans/PROGRESS-R5.md`

---

### Task B.4: 继续拆 `pkg/session/manager.go` / console helper

**Files:**
- Modify: `pkg/session/manager.go`
- Create/Modify: `pkg/session/manager_*.go`
- Modify: `pkg/httpapi/console_sessions.go`
- Modify: `pkg/httpapi/console_fs.go`
- Modify related tests under `pkg/session` / `pkg/httpapi`
- Update progress: `docs/plans/PROGRESS-R5.md`

**Step 1: 先拆 replay / persist / snapshot / GC 逻辑**

**Step 2: 跑定向测试**

Run: `go test ./pkg/session ./pkg/httpapi -run 'Session|Replay|Console' -count=1`

**Step 3: 记录进度**

- 更新 `docs/plans/PROGRESS-R5.md`

---

### Task C.1: 继续拆 `pkg/channels/manager.go` / `manager_dispatch.go`

**Files:**
- Modify: `pkg/channels/manager.go`
- Modify: `pkg/channels/manager_dispatch.go`
- Create/Modify: `pkg/channels/manager_*.go`
- Modify: `pkg/channels/manager_test.go`
- Update progress: `docs/plans/PROGRESS-R5.md`

**Step 1: 先拆 lifecycle / worker map / dispatch / send helper**

**Step 2: 跑定向测试**

Run: `go test ./pkg/channels -count=1`

**Step 3: 记录进度**

- 更新 `docs/plans/PROGRESS-R5.md`

---

### Task C.2: 继续拆 `pkg/cron/service.go`

**Files:**
- Modify: `pkg/cron/service.go`
- Create/Modify: `pkg/cron/service_*.go`
- Modify: `pkg/cron/service_test.go`
- Modify: `pkg/cron/service_operable_test.go`
- Update progress: `docs/plans/PROGRESS-R5.md`

**Step 1: 先拆 store / scheduler / runner / state mutation helper**

**Step 2: 跑定向测试**

Run: `go test ./pkg/cron -count=1`

**Step 3: 记录进度**

- 更新 `docs/plans/PROGRESS-R5.md`

---

### Task C.3: 继续下沉 gateway runtime / reload / httpapi 装配 helper

**Files:**
- Modify: `cmd/x-claw/internal/gateway/*.go`
- Modify: `internal/gateway/*.go`
- Modify related tests under `cmd/x-claw/internal/gateway` / `internal/gateway`
- Update progress: `docs/plans/PROGRESS-R5.md`

**Step 1: 继续把 runtime/state/httpapi 装配收口到更小 helper**

**Step 2: 跑定向测试**

Run: `go test ./cmd/x-claw/internal/gateway ./internal/gateway -count=1`

**Step 3: 记录进度**

- 更新 `docs/plans/PROGRESS-R5.md`

---

### Task D.1: 收敛 provider fallback / protocol alias / config default 事实来源

**Files:**
- Modify: `pkg/providers/factory_provider.go`
- Modify: `pkg/providers/fallback.go`
- Modify: `pkg/agent/loop_fallback.go`
- Modify: `pkg/config/defaults.go`
- Modify related tests under `pkg/providers` / `pkg/agent` / `pkg/config`
- Update progress: `docs/plans/PROGRESS-R5.md`

**Step 1: 明确单一运行时主路径**

- protocol alias
- fallback candidate -> model config 映射
- default `api_base` / auth requirement

**Step 2: 跑定向测试**

Run: `go test ./pkg/providers ./pkg/agent ./pkg/config -run 'Factory|Fallback|Default|Provider' -count=1`

**Step 3: 记录进度**

- 更新 `docs/plans/PROGRESS-R5.md`

---

### Task D.2: 继续拆 auth/provider interactive flow 与错误提示路径

**Files:**
- Modify: `pkg/auth/oauth.go`
- Modify: `pkg/providers/antigravity_provider.go`
- Modify related tests under `pkg/auth` / `pkg/providers`
- Update progress: `docs/plans/PROGRESS-R5.md`

**Step 1: 把 interactive prompt / browser flow / token exchange / store usage 继续分离**

**Step 2: 跑定向测试**

Run: `go test ./pkg/auth ./pkg/providers -run 'OAuth|Auth|Credential|Antigravity' -count=1`

**Step 3: 记录进度**

- 更新 `docs/plans/PROGRESS-R5.md`

---

## 阶段 Gate 建议

### Gate A（Task A.1 ~ A.3 后）

Run:

```bash
go test ./pkg/agent ./pkg/tools ./pkg/httpapi ./pkg/session -count=1
go build -p 1 ./...
go vet ./...
```

### Gate B（Task B.1 ~ B.4 后）

Run:

```bash
go test ./pkg/agent ./pkg/tools ./pkg/httpapi ./pkg/session -count=1
go build -p 1 ./...
```

### Gate C（Task C.1 ~ C.3 后）

Run:

```bash
go test ./pkg/channels ./pkg/cron ./cmd/x-claw/internal/gateway ./internal/gateway -count=1
go build -p 1 ./...
go vet ./...
```

### Gate D（Task D.1 ~ D.2 后）

Run:

```bash
go test ./pkg/providers ./pkg/config ./pkg/auth ./pkg/agent -count=1
go build -p 1 ./...
```

### 最终验证

Run:

```bash
go build -p 1 ./...
go vet ./...
go test ./... -run '^$' -count=1
```

若环境对大包 / `-race` 不稳定，则：

- 保留 compile-only 全仓验证
- 对关键改动包做更小批次的定向测试
- 在 `docs/plans/PROGRESS-R5.md` 中明确记录替代验证和环境限制
