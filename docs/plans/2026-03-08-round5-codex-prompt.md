# Round 5 Codex 执行提示词

> 将此文件内容直接提供给 Codex。Codex 需要先读文档、先把 implementation 细分成任务列表、按进度文档执行，并尽量使用 agent 并行推进。

---

你正在 X-Claw 仓库中执行 Round 5 的重构与精简工作。你的目标是：

- **保持现有功能不变**
- **继续做重构优化、代码精简、边界收敛**
- **顺手修复文档中明确列出的残余 bug / 风险点**
- **执行到底，不要中途停下，除非遇到真实不可推进阻塞**

## 必读文档（按顺序）

1. `docs/plans/2026-03-08-round5-audit.md`
2. `docs/plans/2026-03-08-round5-requirements.md`
3. `docs/plans/2026-03-08-round5-implementation.md`
4. `docs/plans/PROGRESS-R5.md`
5. `docs/plans/PROGRESS-R4.md`
6. 如需要背景，再参考：
   - `docs/plans/2026-03-08-round4-audit.md`
   - `docs/plans/2026-03-08-round4-requirements.md`
   - `docs/plans/2026-03-08-round4-implementation.md`

## 执行要求

### 1. 先细分任务，再开始实现

在动代码前，先把 `docs/plans/2026-03-08-round5-implementation.md` 进一步细分成可执行任务列表，并写回 `docs/plans/PROGRESS-R5.md`。

要求：

- 把每个大 Task 细分成可落实的小任务
- 明确并行边界和串行依赖
- 不要覆盖已有文档内容，只追加/细化

### 2. 每完成一个任务都更新进度文档

`docs/plans/PROGRESS-R5.md` 是唯一进度事实来源。

每完成一个任务：

- 把 `[ ]` 改为 `[x]`
- 追加完成时间
- 追加改动摘要
- 追加验证命令与结果
- 如遇 `SIGKILL(137)` / `exit -1` / 环境限制，必须记录实际替代验证

### 3. 优先使用 agent 并行推进

如果可以并行，请尽量使用 agent 按以下三组并发推进：

#### 组 1：Gateway / Channels

- `pkg/channels`
- `pkg/cron`
- `cmd/x-claw/internal/gateway`
- `internal/gateway`

#### 组 2：Providers / Config

- `pkg/providers`
- `pkg/config`
- `pkg/auth`
- `pkg/routing`
- `pkg/identity`

#### 组 3：Agent / Tools / HTTPAPI / Session

- `pkg/agent`
- `pkg/tools`
- `pkg/httpapi`
- `pkg/session`
- `pkg/memory`

并行原则：

- 不重叠写集合的任务优先并行
- 如果存在共享文件或隐藏依赖，先做前置 bug fix，再做后续拆分
- 文档写集合由主控串行维护

### 4. 执行顺序

建议顺序：

1. 先完成 Phase A（Bug 修复与风险收口）
2. 再做 Phase B（Agent / Tools / HTTPAPI / Session 精简）
3. 并行推进 Phase C（Gateway / Channels）与 Phase D（Providers / Config）
4. 最后补 Gate 和最终验证

### 5. 关键约束

- 不改变任何公开 API、CLI 命令面、HTTP API 语义、会话格式
- 物理拆分默认只搬逻辑，不顺手引入行为改动
- 遇到 bug 时先写测试 / 证明缺口，再最小修复
- 不要回滚已有改动
- 不要中途停下来汇报“做到一半”
- 只有在**真实不可推进阻塞**时才停下

### 6. 必做验证

全仓基线：

```bash
go build -p 1 ./...
go vet ./...
go test ./... -run '^$' -count=1
```

定向验证：

```bash
go test ./pkg/session -count=1
go test ./pkg/httpapi -count=1
go test ./pkg/channels ./cmd/x-claw/internal/gateway ./pkg/providers -count=1
go test ./pkg/agent -run 'TestSanitizeHistoryForProvider|TestMemoryForSession|TestConcurrentBuildSystemPromptWithCache|TestNewAgentInstance_' -count=1
go test ./pkg/tools -run 'TestExecuteToolCalls_ParallelToolCallsPreserveOrder|TestRegistryExecuteWithContextNilResult|TestShellSessionTable_PrunesTerminalSessionsAtCapacity|TestBackgroundSession_' -count=1
```

如果环境允许，再补：

```bash
go test -race ./pkg/agent -run 'Context|BuildMessages|SystemPrompt' -count=1
go test -race ./pkg/session ./pkg/httpapi -count=1
```

### 7. 代理 / 下载环境说明

如果需要从公网下载依赖，先：

```bash
source ~/.zshrc && proxy_on
```

### 8. 收尾要求

全部任务完成后：

1. 确认 `docs/plans/PROGRESS-R5.md` 中所有 Task / Gate / 最终验证都已更新
2. 确认最终验证命令已实际执行并记录
3. 如本地 Gateway 可用，按仓库约定发送一条完成通知

现在开始执行：

- 先阅读上述文档
- 先把 implementation 文档细分成任务列表并写回 `PROGRESS-R5.md`
- 再按三组并发推进
- 务必执行到底，不要中途停下
