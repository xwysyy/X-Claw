# Codex 执行提示词 — X-Claw Round 2 重构

你是一个 Go 后端工程师, 负责执行 X-Claw 项目的 Round 2 重构。所有变更必须保持现有功能和 API 不变。

---

## 1. 必读文档 (按优先级)

1. **`docs/plans/2026-03-07-round2-requirements.md`** — 需求定义, 每个 REQ-R2-* 的验收标准
2. **`docs/plans/2026-03-07-round2-implementation.md`** — 技术实现细节, 每个 Task 的 before/after 代码和验证命令
3. **`docs/plans/2026-03-07-round2-audit.md`** — 完整审查报告, 了解问题背景
4. **`docs/plans/PROGRESS.md`** — Round 1 进度 (已完成, 仅供参考)

---

## 2. 进度跟踪

**在 `docs/plans/PROGRESS-R2.md` 中追踪进度。** 每完成一个 Task, 立即更新。格式:

```markdown
# Round 2 重构执行进度

## Phase A: P0 Bug + 安全修复
- [ ] Task A.1: 消除 pkg/ 层 fmt.Printf (REQ-R2-BUG-001)
- [ ] Task A.2: 修复 antigravity io.ReadAll (REQ-R2-BUG-002)
- [ ] Task A.3: 修复 Feishu reaction undo context (REQ-R2-BUG-003)
- [ ] Task A.4: HTTP 错误响应不泄露内部信息 (REQ-R2-SEC-001)

## Phase B: P1 Bug 修复
- [ ] Task B.1: 统一 os.MkdirAll 错误处理 (REQ-R2-BUG-004)
- [ ] Task B.2: 删除死函数 ensureActiveAgentForSession (REQ-R2-BUG-005)
- [ ] Task B.3: compaction save 错误记录日志 (REQ-R2-BUG-006)
- [ ] Task B.4: session meta 持久化错误记录 (REQ-R2-BUG-007)

## Phase C: P1 性能 + 安全 + 测试
- [ ] Task C.1: 复用 antigravity HTTP Client (REQ-R2-PERF-001)
- [ ] Task C.2: 缓存 Telegram bot username 正则 (REQ-R2-PERF-002)
- [ ] Task C.3: HTTP 安全头部补全 (REQ-R2-SEC-002)
- [ ] Task C.4: auditlog 包测试 (REQ-R2-TEST-001)

## Phase D: P2 清理
- [ ] Task D.1: auditlog 写入错误处理 (REQ-R2-BUG-008)
- [ ] Task D.2: 清理死参数 (REQ-R2-BUG-009)
- [ ] Task D.3: API Key 常量时间比较 (REQ-R2-SEC-003)
- [ ] Task D.4: media.go HTTP Client 复用 (REQ-R2-PERF-003)
- [ ] Task D.5: 抽取重复 isLoopback (REQ-R2-QUAL-001)
- [ ] Task D.6: archcheck 扩展 (REQ-R2-TEST-002)

## 最终验证
- [ ] `go build ./...` 通过
- [ ] `go vet ./...` 通过
- [ ] 关键包定向测试通过
```

完成一个 Task 后将 `[ ]` 改为 `[x]` 并附上时间戳和简要说明。

---

## 3. 执行流程

### 每个 Task 的执行步骤:

1. **读取需求**: 打开 `round2-requirements.md` 找到对应 REQ-R2-* 编号, 确认验收标准
2. **读取实现**: 打开 `round2-implementation.md` 找到对应 Task, 确认 before/after 代码和目标文件
3. **读源码**: 读取要修改的文件, 确认当前代码和行号 (行号可能因前序修改而偏移)
4. **实施修改**: 按实现文档执行修改
5. **构建验证**: `go build ./...` 确保编译通过
6. **定向测试**: 运行实现文档中指定的验证命令
7. **更新进度**: 在 `PROGRESS-R2.md` 中标记完成

### 对于新建测试文件 (Task C.4):

1. 读取被测文件 (`pkg/auditlog/auditlog.go`)
2. 理解核心逻辑: Record, VerifyHMACSignature, rotateLocked
3. 编写测试, 覆盖正常路径 + 边界条件
4. 运行 `go test -race ./pkg/auditlog/... -count=1 -v`

---

## 4. 并行执行策略

利用 Agent 工具并行处理无文件冲突的 Task:

### Round 1 (6 个 Task 并行):
```
Agent 1: Task A.1 (instance.go, web_search.go, httpapi.go)
Agent 2: Task A.2 (antigravity_provider.go)
Agent 3: Task A.3 (feishu_64.go)
Agent 4: Task A.4 (handlers_notify.go, handlers_resume.go)
Agent 5: Task B.1 (instance.go 与 A.1 有冲突 — 需串行或合并到 Agent 1)
Agent 6: Task B.2 (run_pipeline_impl.go)
```

**修正**: instance.go 在 A.1 和 B.1 中都需要修改, 合并为一个 Agent:
```
Agent 1: Task A.1 + Task B.1 (instance.go, web_search.go, httpapi.go, manager.go)
Agent 2: Task A.2 (antigravity_provider.go)
Agent 3: Task A.3 (feishu_64.go)
Agent 4: Task A.4 (handlers_notify.go, handlers_resume.go)
Agent 5: Task B.2 (run_pipeline_impl.go)
```
> 5 个 Agent 并行, 无文件冲突

### Round 1 Gate:
```bash
go build ./... && go vet ./...
```

### Round 2 (5 个 Task 并行):
```
Agent 1: Task B.3 (loop_compaction.go)
Agent 2: Task B.4 (manager_mutations.go, tree.go, manager.go)
Agent 3: Task C.1 (antigravity_provider.go — Round 1 已完成 A.2, 无冲突)
Agent 4: Task C.2 (telegram.go)
Agent 5: Task C.3 (channels/manager.go)
```
> 5 个 Agent 并行, 无文件冲突

### Round 2 Gate:
```bash
go build ./... && go vet ./...
go test ./pkg/agent/... ./pkg/session/... ./pkg/providers/... ./pkg/channels/... -count=1
```

### Round 3 (6 个 Task 并行):
```
Agent 1: Task C.4 (新建 auditlog_test.go)
Agent 2: Task D.1 (auditlog.go)
Agent 3: Task D.2 (memory.go, memory_embedder.go, loop_commands.go, filesystem.go)
Agent 4: Task D.3 + D.5 (handlers_notify.go, validation.go, 新建 net.go — 合并因 handlers_notify.go 冲突)
Agent 5: Task D.4 (media.go)
Agent 6: Task D.6 (archcheck_test.go)
```
> 注意: D.1 修改 auditlog.go, C.4 新建 auditlog_test.go, 无文件冲突但逻辑相关
> D.3 和 D.5 都修改 handlers_notify.go, 必须合并

### Round 3 Gate (最终验证):
```bash
go build ./...
go vet ./...
# 分批测试 (受限环境下):
go test ./pkg/auditlog/... -count=1 -v
go test ./pkg/agent/... -count=1
go test ./pkg/session/... -count=1
go test ./pkg/channels/... -count=1
go test ./pkg/providers/... -count=1
go test ./pkg/tools/... -count=1
go test ./pkg/config/... -count=1
go test ./internal/... -count=1
go test ./cmd/... -count=1
```

---

## 5. 关键约束

1. **不修改 API**: 所有 HTTP 端点路径、请求/响应 JSON 格式不变
2. **不修改行为**: 消息处理、session 语义、tool 执行流程不变
3. **不跨阶段混用**: Round 1 的 Task 不能与 Round 2 混在同一个修改中
4. **import 顺序**: 保持现有 import 分组风格 (stdlib / external / internal)
5. **Logger 而非 Printf**: pkg/ 层所有日志使用 `logger.{Info,Warn,Debug}CF`; cmd/ 层 CLI 交互输出可用 fmt
6. **错误不吞没**: 所有 `_ = someFunc()` 改为至少 logger.WarnCF; 但 cleanup/defer 中的 `_ = f.Close()` 或 `_ = os.Remove()` 保留 (best-effort cleanup)
7. **测试必须通过**: 每个 Phase 完成后, 已有测试不能 break

---

## 6. 完成标准

当以下条件全部满足时, 视为完成:

- [ ] `PROGRESS-R2.md` 中所有 16 个 Task 标记为 `[x]`
- [ ] `go build ./...` 通过
- [ ] `go vet ./...` 通过
- [ ] `go test ./pkg/auditlog/... -count=1` 通过 (新增测试)
- [ ] `go test ./internal/archcheck/... -count=1` 通过 (扩展守护测试)
- [ ] 关键包定向测试通过: pkg/agent, pkg/session, pkg/channels, pkg/providers, pkg/tools, pkg/config, pkg/httpapi, internal/gateway
- [ ] `grep -rn 'fmt\.Print' pkg/ | grep -v _test.go` 仅输出已知合法项 (CLI helpers)
- [ ] `grep 'body, _ := io.ReadAll' pkg/providers/antigravity_provider.go` 无输出
- [ ] `grep '_ = agent.Sessions.Save' pkg/agent/loop_compaction.go` 无输出

---

## 7. 务必执行完毕

这是一个完整的执行计划, 包含 16 个 Task, 分 3 轮并行执行。请按照 Round 1 → Gate → Round 2 → Gate → Round 3 → 最终验证 的顺序执行。

**不要中途停下来。** 如果某个 Task 遇到问题 (如行号偏移、代码已变更), 根据实现文档的意图自行调整, 确保验收标准满足。

**每完成一个 Round, 立即运行 Gate 验证命令, 确认构建和测试通过后再进入下一个 Round。**
