# Round 3 重构执行进度

## Phase A: P0 Bug 修复
- [x] Task A.1: 修复 model_override writeMetaFile 错误吞没 — 2026-03-08 01:03:02 +0800，`SetModelOverride/ClearModelOverride` 改为显式记录 meta 持久化失败 warn 日志
- [x] Task A.2: 修复 EffectiveModelOverride TOCTOU 竞态 — 2026-03-08 01:03:02 +0800，`EffectiveModelOverride` 改为写锁内原子判定并直接清理过期 override，新增并发回归测试覆盖旧竞态
- [x] Task A.3: 修复 console_stream.go io.ReadAll 错误忽略 — 2026-03-08 01:03:02 +0800，日志尾部读取改为检查 `io.ReadAll` 错误并在失败时跳过本次 tail；本阶段 `go build -p 1 ./...` 与 `go vet ./...` 已通过

## Phase B: P1 Bug + 测试
- [x] Task B.1: events.go f.Sync 错误处理 — 2026-03-08 01:03:02 +0800，`appendJSONLEvent` 改为把 `f.Sync()` 失败 wrap 返回，避免落盘失败静默丢失
- [x] Task B.2: antigravity 凭证保存错误记录 — 2026-03-08 01:03:02 +0800，projectID 回写凭证失败时记录 `provider.antigravity` warn 日志；并补充 `randomString` 非密码学用途注释预备 Phase E.7
- [x] Task B.3: task_ledger load 错误记录 — 2026-03-08 01:03:02 +0800，`NewTaskLedger` 改为在账本加载失败时记录路径和错误，避免静默回退空 map
- [x] Task B.4: model_override 并发测试 — 2026-03-08 01:03:02 +0800，新增并发读写、过期清理、幂等清除和 fresh override 抗误清理测试；`go test -race -p 1 ./pkg/session ...` 与 `go test -race -p 1 ./pkg/providers -run '^$'` 通过，`go test -race -p 1 ./pkg/tools -run '^$'` 在当前环境被 SIGKILL(137)

## Phase C: P1 文件拆分
- [x] Task C.1: 拆分 loop.go — 2026-03-08 01:21:37 +0800，新增 `loop_errors.go`、`loop_bucket.go`、`loop_trace.go`、`loop_audit.go`、`loop_fallback.go`，仅搬移顶层类型/函数与桶类型定义，`pkg/agent/loop.go` 降至 1846 行
- [x] Task C.2: 拆分 web_search.go — 2026-03-08 01:21:37 +0800，新增 `web_search_brave.go`、`web_search_tavily.go`、`web_search_ddg.go`、`web_search_llm.go`、`web_fetch.go`，主文件保留入口/公共类型/正则，`pkg/tools/web_search.go` 降至 607 行
- [x] Task C.3: 拆分 shell.go — 2026-03-08 01:21:37 +0800，新增 `shell_output.go`、`shell_safety.go`、`shell_session.go`，主文件保留 `ExecTool` 执行核心，`pkg/tools/shell.go` 降至 659 行；Phase C gate 采用分段串行验证：`go build -p 1 ./...`、`go vet ./...`、`go test ./pkg/agent/... -count=1`、`go test ./pkg/tools -gcflags=all='-N -l' -run '^TestWeb'`、`go test ./pkg/tools -gcflags=all='-N -l' -run 'TaskLedger|ExecBackground|ProcessTool|ShellTool...'` 均通过

## Phase D: P2 文件拆分
- [x] Task D.1: 拆分 feishu_64.go — 2026-03-08 01:31:53 +0800，新增 `feishu_format.go`、`feishu_media.go`，将 markdown/mention/post 解析与媒体上传下载分离，`pkg/channels/feishu/feishu_64.go` 降至 486 行
- [x] Task D.2: 拆分 run_pipeline_impl.go — 2026-03-08 01:31:53 +0800，新增 `pipeline_notify.go`、`pipeline_helpers.go`、`pipeline_state.go`、`pipeline_permissions.go`、`pipeline_resume.go`、`pipeline_media.go`、`pipeline_entrypoints.go`，主文件保留 pipeline 主干，`pkg/agent/run_pipeline_impl.go` 降至 448 行；Phase D gate `go build -p 1 ./...`、`go vet ./...`、`go test ./pkg/channels/... -count=1`、`go test ./pkg/agent/... -count=1` 通过

## Phase E: P2 清理
- [x] Task E.1: 清理 toolcall_hooks.go 死参数 — 2026-03-08 01:47:48 +0800，删除 `ToolResultRedactHook.AfterToolCall` 中无效的 `_ = call`
- [x] Task E.2: 抽取 JSON 响应 helper — 2026-03-08 01:47:48 +0800，新增 `internal/gateway/response.go` 的 `writeJSON`，`handlers_notify.go` / `handlers_resume.go` 统一复用
- [x] Task E.3: CronService 精确计时 — 2026-03-08 01:47:48 +0800，`runLoop` 改为基于 `getNextWakeMS()` 的动态 `timer`，空闲/远期任务时最多 5 秒唤醒一次
- [x] Task E.4: console_file.go strings.Builder — 2026-03-08 01:47:48 +0800，定位到实际热点为前端表格 `html +=` 拼接，改为数组累积后 `join("")`，避免在控制台页面内循环反复拼接字符串
- [x] Task E.5: health server shutdown 超时 — 2026-03-08 01:47:48 +0800，`StartContext` 在外部取消时改为 `context.WithTimeout(..., 10s)` 再执行 `Shutdown`
- [x] Task E.6: 统一错误 wrap 格式 — 2026-03-08 01:47:48 +0800，修正 `pkg/channels/manager.go` 中三处 `%w: %v` 为 `%w: %w`，保留可 `errors.Is/As` 的链路
- [x] Task E.7: randomString 添加注释 — 2026-03-08 01:47:48 +0800，`pkg/providers/antigravity_provider.go` 为 requestId 辅助随机串补充“非密码学强度”说明

## 最终验证
- [x] `go build ./...` 通过 — 2026-03-08 01:47:48 +0800，串行执行 `go build -p 1 ./...` 通过
- [x] `go vet ./...` 通过 — 2026-03-08 01:47:48 +0800，fresh `go vet ./...` 通过
- [x] `go test ./pkg/... ./internal/... ./cmd/... -count=1` 通过 — 2026-03-08 01:47:48 +0800，按分段串行等价执行通过：`go test ./internal/...`、`go test ./cmd/...`、`pkg` 逐包/逐测试族执行；对 `pkg/tools` / `pkg/channels` / `pkg/memory` 使用更小批次与 `-gcflags=all='-N -l'` 避免环境 SIGKILL(137)
- [x] 大文件行数统计符合目标 — 2026-03-08 01:47:48 +0800，`loop.go=1846`、`web_search.go=607`、`shell.go=659`、`feishu_64.go=486`、`run_pipeline_impl.go=448`
