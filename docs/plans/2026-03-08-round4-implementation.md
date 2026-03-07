# X-Claw Round 4 Residual Hardening and Simplification Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 在不改变现有功能、CLI 命令面和 HTTP 接口语义的前提下，完成 Round 4 残余 bug 清扫、长跑稳定性修复和关键职责收敛。

**Architecture:** 本轮不做大迁移，只围绕现有运行时做“小步可回归”的修复与拆分。优先清理确定性 bug，再补长跑型状态回收，最后收敛 runtime / provider / tools / channels 的职责边界。

**Tech Stack:** Go、Cobra、现有 `pkg/agent` / `pkg/tools` / `pkg/channels` / `pkg/providers` / `pkg/httpapi` / `cmd/x-claw/internal/gateway` 运行时。

---

## 实施总规则

- 开始前先阅读：
  - `docs/plans/2026-03-08-round4-audit.md`
  - `docs/plans/2026-03-08-round4-requirements.md`
  - `docs/plans/PROGRESS-R4.md`
  - `docs/plans/PROGRESS-R3.md`
- 当前工作树不是干净状态；**不要覆盖、不要回滚现有未提交改动**
- 每完成一个任务，必须更新 `docs/plans/PROGRESS-R4.md`
- 每个 Task 都遵循：写测试 → 跑失败/证明缺口 → 最小修复 → 跑通过 → 记录进度
- 涉及不重叠写集合的任务可以并行，但必须先标明并行边界

---

## 本次执行任务清单（先拆分，再并行）

### 总协调规则

- 文档类写集合由主控串行维护：`docs/plans/2026-03-08-round4-implementation.md`、`docs/plans/PROGRESS-R4.md`
- 代码改动按三组并行，避免交叉写同一文件；若发现任务间存在隐藏依赖，先完成前置任务再进入后置任务
- 每组内部严格按“先 bug / 稳定性，后收敛 / 拆分”的顺序推进，避免边修边拆导致回归面扩大

### 并行组 1：Gateway / Channels

1. `Task A.1`：修复 `channels.StartAll` 假成功与 ready 误报
2. `Task A.2`：修复 `StopAll` 后发送 panic 风险
3. `Task A.3`：让 Gateway reload 具备原子切换 / 回滚语义，并修复 startup 误报
4. `Task C.1`：抽取共享 runtime bootstrap，并继续拆 Gateway 组合根（依赖 A.3 稳定后的 gateway/runtime 边界）
5. 阶段内验证：先完成 `Gate A` 中 `pkg/channels` / `cmd/x-claw/internal/gateway` 部分，再在 `Task C.1` 完成后补跑 `Gate C` 中 `cmd/x-claw/...` 部分

### 并行组 2：Providers / Config

1. `Task A.4`：修复 provider factory 默认 base 逻辑与错误提示
2. `Task C.2`：收敛 provider 选择边界，冻结或退役遗留选择路径（依赖 A.4 的默认 base / credential 语义先稳定）
3. 阶段内验证：先完成 `Gate A` 中 `pkg/providers` 部分，再在 `Task C.2` 完成后补跑 `Gate C` 中 `pkg/providers` / `pkg/agent` provider 相关定向测试

### 并行组 3：Agent / Tools

1. `Task B.1`：修复 `ContextBuilder` 并发安全问题
2. `Task B.2`：为 `memoryScopes` 增加边界控制（依赖 B.1 的并发同步策略已稳定）
3. `Task B.3`：为后台 shell session 表增加回收策略
4. `Task B.4`：为 `ToolRegistry` 增加 nil `ToolResult` 防御，并让旁路通知更可观测
5. `Task B.5`：补足 trace / heartbeat / stream follow 的可观测性与健壮性
6. `Task C.3`：继续拆 `ContextBuilder` / `AgentInstance` / `filesystem` 并退役 `toolloop.go`（依赖 B.1/B.2/B.3/B.4 已把行为面先稳住）
7. `Task C.4`：收敛 HTTPAPI 鉴权与文件 helper（依赖 B.5 对 `console_stream` 的行为修复已落地）
8. 阶段内验证：先完成 `Gate B`，再在 `Task C.3` / `Task C.4` 完成后补跑 `Gate C` 中 `pkg/agent` / `pkg/tools` / `pkg/httpapi` 部分

### 主控更新节奏

1. 任何组完成一个 Task 后，立即更新 `docs/plans/PROGRESS-R4.md`
2. 先记录该 Task 的完成时间、改动摘要、验证命令与结果，再继续下一个 Task
3. 三组任务全部完成后，统一补跑阶段 Gate 与最终验证，并把受环境限制的命令与替代验证写入 `docs/plans/PROGRESS-R4.md`

---

### Task A.1: 修复 `channels.StartAll` 假成功与 ready 误报

**Files:**
- Modify: `pkg/channels/manager.go`
- Modify: `pkg/channels/manager_test.go`
- Update progress: `docs/plans/PROGRESS-R4.md`

**Step 1: 写失败测试**

- 为 `StartAll` 增加一个“所有 mock channel 都返回启动失败”的测试
- 断言：
  - `StartAll(...)` 返回 error
  - `healthServer` 未 ready
  - 不创建可用 worker

**Step 2: 运行定向测试确认缺口存在**

Run: `go test ./pkg/channels -run 'StartAll|Ready' -count=1`

**Step 3: 最小修复**

- 在 `StartAll` 内统计成功启动数
- 若成功启动数为 0：
  - 返回明确错误
  - 保证不置 ready
- 保持“部分成功、部分失败”的当前容忍语义

**Step 4: 重新运行测试**

Run: `go test ./pkg/channels -run 'StartAll|Ready' -count=1`

**Step 5: 记录进度**

- 在 `docs/plans/PROGRESS-R4.md` 勾选 Task A.1
- 写入时间、修改摘要、验证命令

---

### Task A.2: 修复 `StopAll` 后发送 panic 风险，并明确队列关闭语义

**Files:**
- Modify: `pkg/channels/manager.go`
- Modify: `pkg/channels/manager_dispatch.go`
- Modify: `pkg/channels/manager_test.go`
- Update progress: `docs/plans/PROGRESS-R4.md`

**Step 1: 写失败测试**

- 新增“`StopAll` 后 `SendToChannel` 不 panic”的测试
- 新增“dispatcher 在 manager 停止后不会再向 closed queue 写入”的测试

**Step 2: 运行定向测试**

Run: `go test ./pkg/channels -run 'StopAll|SendToChannel|Dispatcher' -count=1`

**Step 3: 最小修复**

- 方案优先级：
  1. 停止时移除/失效化 `workers` 映射中的可发送引用
  2. `SendToChannel` 对 stopped/closed worker 做明确判定
  3. dispatcher 入队前检查 manager/worker 状态
- 不改变现有发送接口签名

**Step 4: 重新运行测试**

Run: `go test ./pkg/channels -run 'StopAll|SendToChannel|Dispatcher' -count=1`

**Step 5: 记录进度**

- 更新 `docs/plans/PROGRESS-R4.md`

---

### Task A.3: 让 Gateway reload 具备原子切换 / 回滚语义

**Files:**
- Modify: `cmd/x-claw/internal/gateway/reload.go`
- Modify: `cmd/x-claw/internal/gateway/helpers.go`
- Modify or Create: `cmd/x-claw/internal/gateway/reload_test.go`
- Update progress: `docs/plans/PROGRESS-R4.md`

**Step 1: 写失败测试**

- 新增“新 channel manager 启动失败时，旧 manager 仍保持可用”的测试
- 断言：
  - reload 返回 error
  - 旧配置与旧 manager 不被污染

**Step 2: 跑测试确认行为缺口**

Run: `go test ./cmd/x-claw/internal/gateway -run 'Reload|reload' -count=1`

**Step 3: 最小修复**

- 新 config / new manager 必须先完成预热
- 只有在新 manager 可用后，才切换 `svc.cfg` / `agentLoop.SetConfig(...)` / `channelManager`
- 若切换中途失败：
  - 保持旧 runtime 不动
  - 记录明确日志

**Step 4: 顺手修复 startup 误报与 `log.Fatalf`**

- `setupCronTool` 改为向上返回 error
- `runGateway` 中 `cronService.Start()` / `heartbeatService.Start()` 失败时，不再打印“started”

**Step 5: 运行测试**

Run: `go test ./cmd/x-claw/internal/gateway -count=1`

**Step 6: 记录进度**

- 更新 `docs/plans/PROGRESS-R4.md`

---

### Task A.4: 修复 provider factory 默认 base 逻辑与错误提示

**Files:**
- Modify: `pkg/providers/factory_provider.go`
- Modify: `pkg/providers/legacy_provider.go`（如需要）
- Modify: `pkg/providers/claude_provider.go`
- Modify: `pkg/providers/codex_provider.go`
- Modify: `pkg/providers/antigravity_provider.go`
- Modify or Create: `pkg/providers/factory_provider_test.go`
- Update progress: `docs/plans/PROGRESS-R4.md`

**Step 1: 补失败测试**

- 新增：`ollama` / `vllm` / `litellm` 在无 `api_key`、无显式 `api_base` 但存在默认 `api_base` 时的测试
- 新增：provider credential 缺失时错误提示不再提不存在的 `x-claw auth login`

**Step 2: 运行定向测试**

Run: `go test ./pkg/providers -run 'Factory|DefaultAPIBase|Credential' -count=1`

**Step 3: 最小修复**

- 校验顺序改为：
  1. 先确定 protocol
  2. 先补默认 `api_base`
  3. 再基于“最终有效配置”判断是否需要 key
- 错误提示统一改成当前真实可行的指引

**Step 4: 重新运行测试**

Run: `go test ./pkg/providers -run 'Factory|DefaultAPIBase|Credential' -count=1`

**Step 5: 记录进度**

- 更新 `docs/plans/PROGRESS-R4.md`

---

### Task B.1: 修复 `ContextBuilder` 并发安全问题

**Files:**
- Modify: `pkg/agent/context.go`
- Modify: `pkg/agent/context_test.go`
- Update progress: `docs/plans/PROGRESS-R4.md`

**Step 1: 写 race 导向测试**

- 并发调用：
  - `SetRuntimeSettings`
  - `SetWebEvidenceMode`
  - `SetToolsRegistry`
  - `BuildMessagesForSession*`
  - `BuildSystemPromptWithCache`

**Step 2: 用 race 运行测试**

Run: `go test -race ./pkg/agent -run 'Context|BuildMessages|SystemPrompt' -count=1`

**Step 3: 最小修复**

- 给共享可变字段引入明确同步策略：
  - `RWMutex`
  - 或不可变快照 + 原子替换
- 保证读取路径不会拿到半更新状态

**Step 4: 再跑 race 测试**

Run: `go test -race ./pkg/agent -run 'Context|BuildMessages|SystemPrompt' -count=1`

**Step 5: 记录进度**

- 更新 `docs/plans/PROGRESS-R4.md`

---

### Task B.2: 为 `memoryScopes` 增加边界控制

**Files:**
- Modify: `pkg/agent/context.go`
- Modify: `pkg/agent/context_test.go`
- Update progress: `docs/plans/PROGRESS-R4.md`

**Step 1: 写回收测试**

- 构造大量不同 scope
- 断言缓存数量不会无限增长
- 断言被回收后再次访问仍可重建，行为不变

**Step 2: 运行定向测试**

Run: `go test ./pkg/agent -run 'MemoryForSession|Context' -count=1`

**Step 3: 最小实现**

- 采用 TTL、LRU 或最大条目数中的一种
- 保持 `MemoryForSession(...)` 调用方行为不变

**Step 4: 跑测试**

Run: `go test ./pkg/agent -run 'MemoryForSession|Context' -count=1`

**Step 5: 记录进度**

- 更新 `docs/plans/PROGRESS-R4.md`

---

### Task B.3: 为后台 shell session 表增加回收策略

**Files:**
- Modify: `pkg/tools/shell_session.go`
- Create or Modify: `pkg/tools/shell_session_test.go`
- Update progress: `docs/plans/PROGRESS-R4.md`

**Step 1: 写失败/缺口测试**

- 覆盖：completed、failed、killed、clear、remove、容量上限

**Step 2: 运行定向测试**

Run: `go test ./pkg/tools -run 'ShellSession|ProcessManager|ExecBackground' -count=1`

**Step 3: 最小修复**

- 增加 completed/failed session 的自动回收或容量回收
- 保留现有用户可见 session 管理命令

**Step 4: 再跑测试**

Run: `go test ./pkg/tools -run 'ShellSession|ProcessManager|ExecBackground' -count=1`

**Step 5: 记录进度**

- 更新 `docs/plans/PROGRESS-R4.md`

---

### Task B.4: 为 `ToolRegistry` 增加 nil `ToolResult` 防御，并让旁路通知更可观测

**Files:**
- Modify: `pkg/tools/registry.go`
- Modify: `pkg/tools/registry_test.go`
- Modify: `pkg/agent/pipeline_notify.go`
- Update progress: `docs/plans/PROGRESS-R4.md`

**Step 1: 写失败测试**

- mock 一个 tool 返回 `nil`
- 断言 `ExecuteWithContext` 返回标准错误结果，而不是 panic

**Step 2: 跑测试**

Run: `go test ./pkg/tools -run 'Registry|ExecuteWithContext' -count=1`

**Step 3: 最小修复**

- registry 在读取 `result` 字段前先判空
- `pipeline_notify.go` 中直连 `message` tool 的失败至少要记录日志，不得静默吞掉

**Step 4: 再跑测试**

Run: `go test ./pkg/tools -run 'Registry|ExecuteWithContext' -count=1`

**Step 5: 记录进度**

- 更新 `docs/plans/PROGRESS-R4.md`

---

### Task B.5: 补足 trace / heartbeat / stream follow 的可观测性与健壮性

**Files:**
- Modify: `pkg/agent/loop_trace.go`
- Modify: `pkg/heartbeat/service.go`
- Modify: `pkg/httpapi/console_stream.go`
- Modify: `pkg/httpapi/console_test.go`
- Update progress: `docs/plans/PROGRESS-R4.md`

**Step 1: 写定向测试**

- stream follow 覆盖 truncate / rotate / 首次 tail 失败 / 客户端取消
- 对 heartbeat 发送失败和 trace sync 失败补可观测路径验证（若不适合单测，可至少补单元级 logger/assert）

**Step 2: 跑测试**

Run: `go test ./pkg/httpapi ./pkg/heartbeat ./pkg/agent -run 'Stream|Heartbeat|Trace' -count=1`

**Step 3: 最小修复**

- `loop_trace` 的 `Sync` 失败不能静默吞掉
- `heartbeat` 的 `PublishOutbound` 结果要检查并记录
- `console_stream` 的 follow 逻辑正确处理 rotate/truncate，且首次 tail 失败时给出可观察响应或日志

**Step 4: 再跑测试**

Run: `go test ./pkg/httpapi ./pkg/heartbeat ./pkg/agent -run 'Stream|Heartbeat|Trace' -count=1`

**Step 5: 记录进度**

- 更新 `docs/plans/PROGRESS-R4.md`

---

### Task C.1: 抽取共享 runtime bootstrap，并继续拆 Gateway 组合根

**Files:**
- Modify: `cmd/x-claw/internal/agent/helpers.go`
- Modify: `cmd/x-claw/internal/gateway/helpers.go`
- Modify or Create: `cmd/x-claw/internal/cliutil/*.go`
- Modify: `cmd/x-claw/internal/gateway/command_test.go`
- Update progress: `docs/plans/PROGRESS-R4.md`

**Step 1: 先列出现有重复装配步骤**

- `LoadConfig`
- `CreateProvider`
- model alias 回写
- `NewMessageBus`
- `NewAgentLoop`

**Step 2: 抽取共享 bootstrap helper**

- 只做装配，不带业务逻辑
- 保持 `agent` / `gateway` 命令外部行为不变

**Step 3: 拆 `helpers.go`**

- 至少拆出 bootstrap/runtime/shutdown 其中两类职责

**Step 4: 跑测试**

Run: `go test ./cmd/x-claw/... -count=1`

**Step 5: 记录进度**

- 更新 `docs/plans/PROGRESS-R4.md`

---

### Task C.2: 收敛 provider 选择边界，冻结或退役遗留选择路径

**Files:**
- Modify: `pkg/providers/legacy_provider.go`
- Modify: `pkg/providers/factory_provider.go`
- Modify: `pkg/providers/factory_selection.go`
- Modify: `pkg/agent/loop_fallback.go`
- Modify: `pkg/providers/*_test.go`
- Update progress: `docs/plans/PROGRESS-R4.md`

**Step 1: 明确当前唯一运行时主路径**

- 文档化并在代码中收口

**Step 2: 删除或标注遗留路径**

- 若无法删除，则加明确注释与测试，说明其边界

**Step 3: 统一默认 base / protocol alias / fallback 映射来源**

- 保留单一事实来源

**Step 4: 跑测试**

Run: `go test ./pkg/providers ./pkg/agent -run 'Factory|Fallback|Provider' -count=1`

**Step 5: 记录进度**

- 更新 `docs/plans/PROGRESS-R4.md`

---

### Task C.3: 继续拆 `ContextBuilder` / `AgentInstance` / `filesystem` 并退役 `toolloop.go`

**Files:**
- Modify: `pkg/agent/context.go`
- Modify: `pkg/agent/instance.go`
- Modify: `pkg/tools/filesystem.go`
- Modify or Remove: `pkg/tools/toolloop.go`
- Modify related tests under `pkg/agent` and `pkg/tools`
- Update progress: `docs/plans/PROGRESS-R4.md`

**Step 1: 先只做职责切分，不改行为**

- `context.go`：按 prompt/cache/memory/pruning 切开
- `instance.go`：按 runtime config / tool install / builder install 切开
- `filesystem.go`：按工具行为与底层 fs helper 切开

**Step 2: 处理 `toolloop.go`**

- 若无生产引用，删除并补回归测试
- 若必须保留，则对齐主循环的 loop detection 与执行语义

**Step 3: 跑定向测试**

Run: `go test ./pkg/agent ./pkg/tools -count=1`

**Step 4: 记录进度**

- 更新 `docs/plans/PROGRESS-R4.md`

---

### Task C.4: 收敛 HTTPAPI 鉴权与文件 helper，并补全日志流验证

**Files:**
- Modify: `pkg/httpapi/console.go`
- Modify: `pkg/httpapi/console_notify.go`
- Modify: `pkg/httpapi/console_file.go`
- Modify: `pkg/httpapi/console_stream.go`
- Modify: `pkg/httpapi/console_sessions.go`
- Create if needed: `pkg/httpapi/console_fs.go`
- Modify: `pkg/httpapi/console_test.go`
- Update progress: `docs/plans/PROGRESS-R4.md`

**Step 1: 抽出鉴权 helper**

- notify / resume / console 使用同一套鉴权逻辑

**Step 2: 抽出只读文件 helper**

- 统一 `resolveConsolePath`、tail、stream、首尾非空行扫描

**Step 3: 跑测试**

Run: `go test ./pkg/httpapi -count=1`

**Step 4: 记录进度**

- 更新 `docs/plans/PROGRESS-R4.md`

---

## 阶段 Gate 建议

### Gate A（Task A.1 ~ A.4 后）

Run:

```bash
go test ./pkg/channels ./cmd/x-claw/internal/gateway ./pkg/providers -count=1
go build -p 1 ./...
go vet ./...
```

### Gate B（Task B.1 ~ B.5 后）

Run:

```bash
go test -race ./pkg/agent -run 'Context|BuildMessages|SystemPrompt' -count=1
go test ./pkg/tools ./pkg/httpapi ./pkg/heartbeat -count=1
go build -p 1 ./...
```

### Gate C（Task C.1 ~ C.4 后）

Run:

```bash
go test ./cmd/x-claw/... ./pkg/providers/... ./pkg/agent/... ./pkg/tools/... ./pkg/httpapi/... -count=1
go build -p 1 ./...
go vet ./...
```

### 最终验证

Run:

```bash
go build -p 1 ./...
go vet ./...
go test ./... -run '^$' -count=1
```

若环境对大包 `-race` / 全量测试不稳定，则：

- 保留 compile-only 全仓验证
- 对关键改动包做定向 `-race`
- 在 `docs/plans/PROGRESS-R4.md` 中明确记录实际执行命令和环境限制
