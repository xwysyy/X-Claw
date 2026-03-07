# X-Claw Round 4 Codex 执行提示词

你是一个 Go 代码重构专家。你现在要执行 X-Claw 的 **Round 4 残余风险清扫与结构瘦身**。目标是：**保持现有功能不变、继续精简代码、修复已确认 bug、提高长跑稳定性与维护性**。

## 开始前必须阅读的文档

请先完整阅读以下文档，再开始任何修改：

1. `docs/plans/2026-03-08-round4-audit.md`
2. `docs/plans/2026-03-08-round4-requirements.md`
3. `docs/plans/2026-03-08-round4-implementation.md`
4. `docs/plans/PROGRESS-R4.md`
5. `docs/plans/PROGRESS-R3.md`
6. `docs/plans/2026-03-08-round3-implementation.md`
7. `docs/plans/2026-03-07-round2-implementation.md`

## 必须遵守的执行规则

### 1. 先建计划，再做代码

- 先使用计划工具，把 `2026-03-08-round4-implementation.md` 进一步细分为**可执行任务列表**
- 每个任务都要明确：
  - 修改哪些文件
  - 先写/补哪些测试
  - 运行哪些验证命令
  - 何时更新进度文档

### 2. 必须记录开发进度

- 每完成一个任务，立即更新 `docs/plans/PROGRESS-R4.md`
- 记录格式至少包含：
  - 任务编号
  - 完成时间（带时区）
  - 做了什么
  - 跑了哪些命令
  - 命令结果如何

### 3. 可以并行的地方尽量并行

如果写入集合不重叠，请尽量使用 agent 并发处理。推荐并行分组：

- **并行组 A：Gateway / Channels / HTTPAPI**
  - `pkg/channels/manager.go`
  - `pkg/channels/manager_dispatch.go`
  - `cmd/x-claw/internal/gateway/reload.go`
  - `cmd/x-claw/internal/gateway/helpers.go`
  - `pkg/httpapi/console_stream.go`

- **并行组 B：Providers / Config / CLI bootstrap**
  - `pkg/providers/factory_provider.go`
  - `pkg/providers/legacy_provider.go`
  - `pkg/providers/factory_selection.go`
  - `cmd/x-claw/internal/agent/helpers.go`
  - 共享 bootstrap 相关文件

- **并行组 C：Agent / Tools 稳定性**
  - `pkg/agent/context.go`
  - `pkg/agent/instance.go`
  - `pkg/tools/registry.go`
  - `pkg/tools/shell_session.go`
  - `pkg/agent/loop_trace.go`
  - `pkg/heartbeat/service.go`

注意：

- 如果两个任务改同一文件，必须顺序执行
- 不要让多个 agent 同时改 `docs/plans/PROGRESS-R4.md`
- agent 完成后必须由主执行者复核并统一更新进度文档

### 4. 不要中途停下来

- 你需要持续执行到 Round 4 计划完成，而不是做一半就停
- 如果遇到局部失败：
  - 先缩小范围
  - 调整任务顺序
  - 改用更小批次测试
  - 在进度文档中记录阻塞与处理方式
- 只有在出现**无法继续推进的真实阻塞**时，才允许停下，并且必须给出：
  - 已完成内容
  - 卡在哪里
  - 下一步最小解法

### 5. 保持行为不变

- 不改 CLI 主命令面：仍保持 `gateway` / `agent` / `version`
- 不改 HTTP API 路由和请求/响应语义
- 不改 session / state / audit 的对外可见格式，除非是修复明确 bug
- 结构重构优先采用“提取 helper / 拆职责 / 收边界”，不要大迁移

### 6. 注意当前仓库不是干净工作树

- 开始前先看 `git status --short`
- 不要覆盖或回滚现有未提交改动
- 如果需要，优先在不影响现有改动的前提下继续增量修改

## 建议执行顺序

### Phase A：先处理 P0 bug

1. `channels.StartAll` 真值与 ready 修复
2. `StopAll` 后发送安全修复
3. reload 原子切换 / 回滚修复
4. provider factory 默认 base 与错误提示修复

### Phase B：再处理稳定性问题

5. `ContextBuilder` 并发安全
6. `memoryScopes` 回收策略
7. shell session 回收策略
8. `ToolRegistry` nil result 防御
9. trace / heartbeat / stream follow 可观测性与健壮性

### Phase C：最后做结构瘦身

10. 共享 bootstrap 抽取
11. provider 选择路径收口
12. `ContextBuilder` / `AgentInstance` / `filesystem` 继续拆职责
13. `toolloop.go` 退役或对齐主循环
14. HTTPAPI 鉴权与文件 helper 收敛

## 每个阶段后的验证要求

每个 Phase 结束后至少执行：

```bash
go build -p 1 ./...
go vet ./...
go test ./... -run '^$' -count=1
```

对改动包再补充更强的定向验证，例如：

- `go test ./pkg/channels ./cmd/x-claw/internal/gateway -count=1`
- `go test ./pkg/providers -run 'Factory|DefaultAPIBase|Credential' -count=1`
- `go test -race ./pkg/agent -run 'Context|BuildMessages|SystemPrompt' -count=1`
- `go test ./pkg/tools -run 'Registry|ShellSession|ProcessManager' -count=1`
- `go test ./pkg/httpapi -run 'Stream|Console|Notify' -count=1`

如果环境导致全量测试或 race 被 SIGKILL：

- 不要停下
- 改用更小批次的包级/测试族验证
- 将实际命令、结果和限制写入 `docs/plans/PROGRESS-R4.md`

## 交付标准

完成时你必须做到：

1. 代码修改完成
2. `docs/plans/PROGRESS-R4.md` 被完整更新
3. 所有阶段的关键验证结果被记录
4. 最终总结说明：
   - 完成了哪些任务
   - 哪些文档已更新
   - 运行了哪些测试
   - 是否还有环境限制导致的未完全验证项

现在开始执行。不要停在“计划阶段”或“分析阶段”；请把实施计划拆成任务列表，然后持续推进到完成。

