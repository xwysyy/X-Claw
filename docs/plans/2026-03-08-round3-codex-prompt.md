# X-Claw Round 3 Codex 执行提示词

你是一个 Go 代码重构专家。你需要对 X-Claw 项目执行 Round 3 重构，精简代码、修复 bug、拆分大文件。

## 参考文档 (必读)

在开始之前，必须阅读以下文档:

1. `docs/plans/2026-03-08-round3-audit.md` — Round 3 审查报告 (52 个发现)
2. `docs/plans/2026-03-08-round3-requirements.md` — 需求文档 (20 个需求)
3. `docs/plans/2026-03-08-round3-implementation.md` — 技术实现文档 (每个任务的具体代码变更)
4. `docs/plans/PROGRESS-R2.md` — Round 2 进度记录 (了解已完成的工作)

## 核心约束

- **保持现有功能不变**: 不修改任何 HTTP 端点、请求/响应格式、消息处理流程
- **保持包结构不变**: 不移动包 (不做 pkg→internal 迁移)
- **拆分时仅移动代码**: 不改变逻辑，保持包内可见性
- **每个阶段完成后验证**: `go build ./...` && `go vet ./...` && `go test ./pkg/... ./internal/... ./cmd/... -count=1` 必须通过

## 任务列表 (5 个阶段, 20 个任务)

请按以下顺序执行。同一 Phase 内的任务如果涉及不同文件可以并行处理，但需要注意 Task A.1 和 A.2 都修改同一个文件 (`model_override.go`)，需要顺序执行。

### Phase A: P0 Bug 修复 (3 个任务)

- **Task A.1**: 修复 `pkg/session/model_override.go` 两处 `_ = writeMetaFile` → 改为检查错误并记录 warn 日志
- **Task A.2**: 修复 `pkg/session/model_override.go` `EffectiveModelOverride` TOCTOU 竞态 → 改用写锁，在持有锁时直接清理过期 override
- **Task A.3**: 修复 `pkg/httpapi/console_stream.go:79` `buf, _ := io.ReadAll(f)` → 检查错误，失败时跳过本次日志尾部读取

Task A.1 和 A.2 修改同一文件需顺序执行；Task A.3 独立可并行。

验证 Gate: `go build ./... && go vet ./...`

### Phase B: P1 Bug + 测试 (4 个任务, 涉及不同文件可并行)

- **Task B.1**: 修复 `pkg/session/events.go:57` `_ = f.Sync()` → 将 Sync 错误传播到返回值
- **Task B.2**: 修复 `pkg/providers/antigravity_provider.go:612` `_ = auth.SetCredential(...)` → 记录 warn 日志
- **Task B.3**: 修复 `pkg/tools/task_ledger.go:77` `_ = l.load()` → 记录 warn 日志
- **Task B.4**: 新建 `pkg/session/model_override_test.go` — 覆盖并发读写、过期清理、幂等清除三个场景，使用 `-race` 验证

验证 Gate: `go build ./... && go vet ./... && go test -race ./pkg/session/... ./pkg/providers/... ./pkg/tools/... -count=1`

### Phase C: P1 文件拆分 (3 个任务, 涉及不同目录可并行)

- **Task C.1**: 拆分 `pkg/agent/loop.go` (3006 行)
  - 新建 `loop_bucket.go`: 移动 `bucket` 结构体及其方法
  - 新建 `loop_errors.go`: 移动 `isLLMTimeoutError`、`isContextWindowError` 等错误分类函数
  - 目标: `loop.go` <2500 行

- **Task C.2**: 拆分 `pkg/tools/web_search.go` (1902 行)
  - 新建 `web_search_brave.go`: Brave API 搜索函数
  - 新建 `web_search_ddg.go`: DuckDuckGo 搜索函数
  - 如果还有 Bing/Serper 后端，也可拆分
  - `web_search.go` 保留入口、公共类型、正则定义、URL fetch/HTML 清洗
  - 目标: 每个文件 <700 行

- **Task C.3**: 拆分 `pkg/tools/shell.go` (1824 行)
  - 新建 `shell_session.go`: `processTable`、`sessionTable`、`shellSession` 及相关方法
  - 新建 `shell_safety.go`: `denyPatterns`、`isDangerousCommand` 等安全检查函数
  - `shell.go` 保留 `ShellTool` 和执行核心逻辑
  - 目标: 每个文件 <700 行

**注意**: 拆分时只移动代码，不修改逻辑。包内小写函数/类型保持小写。注意处理好 import 依赖。

验证 Gate: `go build ./... && go vet ./... && go test ./pkg/agent/... ./pkg/tools/... -count=1`

### Phase D: P2 文件拆分 (2 个任务, 可并行)

- **Task D.1**: 拆分 `pkg/channels/feishu/feishu_64.go` (1537 行)
  - 新建 `feishu_format.go`: 消息格式化和 markdown 转换函数
  - 新建 `feishu_media.go`: 媒体上传/下载函数
  - 目标: `feishu_64.go` <900 行

- **Task D.2**: 拆分 `pkg/agent/run_pipeline_impl.go` (1424 行)
  - 新建 `pipeline_notify.go`: 通知和进度回调函数
  - 新建 `pipeline_helpers.go`: 消息复制、会话快照等辅助函数
  - 目标: `run_pipeline_impl.go` <900 行

验证 Gate: `go build ./... && go vet ./... && go test ./pkg/channels/... ./pkg/agent/... -count=1`

### Phase E: P2 清理 (7 个任务)

- **Task E.1**: 删除 `pkg/tools/toolcall_hooks.go:226` 的 `_ = call` 死参数行
- **Task E.2**: 抽取 JSON 响应 helper
  - 新建 `internal/gateway/response.go`: `func writeJSON(w http.ResponseWriter, status int, v any)`
  - 修改 `handlers_notify.go` 和 `handlers_resume.go` 使用新 helper
- **Task E.3**: `pkg/cron/service.go` CronService ticker 优化为精确计时 (或降低到 5-10 秒)
- **Task E.4**: `pkg/httpapi/console_file.go` HTML 拼接改用 `strings.Builder`
- **Task E.5**: `pkg/health/server.go:80` shutdown 加 10 秒超时
- **Task E.6**: 全仓库搜索 `fmt.Errorf("...: %v", err)` 并将应该 wrap 的改为 `%w`
- **Task E.7**: `pkg/providers/antigravity_provider.go:765` `randomString` 函数添加注释说明不需要密码学强度 (仅用于 requestId 追踪)

验证 Gate: `go build ./... && go vet ./... && go test ./pkg/... ./internal/... ./cmd/... -count=1`

## 最终验证

所有任务完成后执行:

```bash
go build ./...
go vet ./...
go test ./pkg/... ./internal/... ./cmd/... -count=1
```

并确认:
1. 所有测试通过
2. 大文件行数统计符合目标:
```bash
wc -l pkg/agent/loop.go pkg/tools/web_search.go pkg/tools/shell.go pkg/channels/feishu/feishu_64.go pkg/agent/run_pipeline_impl.go
```

## 进度记录

将每个任务的完成状态记录到 `docs/plans/PROGRESS-R3.md`，格式参考 `PROGRESS-R2.md`:

```markdown
# Round 3 重构执行进度

## Phase A: P0 Bug 修复
- [ ] Task A.1: 修复 model_override writeMetaFile 错误吞没
- [ ] Task A.2: 修复 EffectiveModelOverride TOCTOU 竞态
- [ ] Task A.3: 修复 console_stream.go io.ReadAll 错误忽略

## Phase B: P1 Bug + 测试
- [ ] Task B.1: events.go f.Sync 错误处理
- [ ] Task B.2: antigravity 凭证保存错误记录
- [ ] Task B.3: task_ledger load 错误记录
- [ ] Task B.4: model_override 并发测试

## Phase C: P1 文件拆分
- [ ] Task C.1: 拆分 loop.go
- [ ] Task C.2: 拆分 web_search.go
- [ ] Task C.3: 拆分 shell.go

## Phase D: P2 文件拆分
- [ ] Task D.1: 拆分 feishu_64.go
- [ ] Task D.2: 拆分 run_pipeline_impl.go

## Phase E: P2 清理
- [ ] Task E.1: 清理 toolcall_hooks.go 死参数
- [ ] Task E.2: 抽取 JSON 响应 helper
- [ ] Task E.3: CronService 精确计时
- [ ] Task E.4: console_file.go strings.Builder
- [ ] Task E.5: health server shutdown 超时
- [ ] Task E.6: 统一错误 wrap 格式
- [ ] Task E.7: randomString 添加注释

## 最终验证
- [ ] `go build ./...` 通过
- [ ] `go vet ./...` 通过
- [ ] `go test ./pkg/... ./internal/... ./cmd/... -count=1` 通过
- [ ] 大文件行数统计符合目标
```

每完成一个任务，将 `[ ]` 改为 `[x]` 并附上时间戳和简要说明。
