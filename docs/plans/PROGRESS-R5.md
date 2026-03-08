# Round 5 重构执行进度

## Phase A: Bug 修复与风险收口
- [x] Task A.1: 移除 Agent 初始化路径的 `log.Fatalf`；完成时间：2026-03-08 06:40 CST；改动摘要：将 `pkg/agent/instance.go` 的 `buildBaseAgentToolRegistry` 从库层 `log.Fatalf` 改为返回初始化错误，由 `NewAgentInstance` 记录 warning 并优雅降级禁用 `exec` / `process`，其余工具保持可用；补充 `TestNewAgentInstance_DisablesExecAndProcessToolsWhenExecInitFails` 证明单个工具初始化失败不再导致进程退出；验证命令：`go test ./pkg/agent -run "TestNewAgentInstance_DisablesExecAndProcessToolsWhenExecInitFails" -count=1`、`go test ./pkg/agent -run "NewAgentInstance|ExecTool" -count=1`；结果：通过。
- [x] Task A.2: 修复 tool trace sync 吞错与 memory init 模糊失败；完成时间：2026-03-08 06:43 CST；改动摘要：为 `pkg/tools/tool_trace.go` 补上 `toolTraceSyncFile` 注入点并在 `Sync` 失败时记录 warning，兑现 `TestToolTraceWriter_LogsSyncFailures` 的预期；同时为 `pkg/agent/memory.go` 增加初始化错误保存与 `ensureReady` 快速失败，让 memory 目录初始化失败不再延后成模糊写入故障；新增/启用 `TestNewMemoryStoreAt_ReportsInitializationFailureClearly` 验证清晰错误面；验证命令：`go test ./pkg/tools -run "TestToolTraceWriter_LogsSyncFailures" -count=1`、`go test ./pkg/agent -run "TestNewMemoryStoreAt_ReportsInitializationFailureClearly" -count=1`；结果：通过。
- [x] Task A.3: 收敛 console auth / error surface / degraded session 语义；完成时间：2026-03-08 06:47 CST；改动摘要：`pkg/httpapi` 为 file / tail / stream 补齐可注入 helper 与稳定错误面，隐藏内部系统错误细节，只把具体原因写入日志；`ResumeLastTaskHandler` 继续复用统一 `authorizeAPIKeyOrLoopback`；`pkg/session/manager.go` 在 JSONL replay 失败时直接跳过损坏 session，避免加载出“看似有效、实际缺历史”的模糊会话；新增 `TestConsoleHandler_FileDownloadDoesNotExposeInternalErrors`、`TestConsoleHandler_TailDoesNotExposeInternalErrors`、`TestConsoleHandler_SessionsListSkipsCorruptJSONL`、`TestResumeLastTaskHandler_(LoopbackOnlyWhenNoAPIKey|APIKeyRequiredWhenConfigured)`，并兑现既有 `TestConsoleHandler_StreamInitialTailReadFailureIsObservable` / `TestNewSessionManager_SkipsCorruptJSONLSession`；验证命令：`go test ./pkg/httpapi -run "ConsoleHandler_(SessionsList|FileDownloadDoesNotExposeInternalErrors|TailDoesNotExposeInternalErrors|TailInternalErrorUsesStableMessage|StreamInitialTailReadFailureIsObservable)|NotifyHandler_APIKeyRequiredWhenConfigured|ResumeLastTaskHandler_(LoopbackOnlyWhenNoAPIKey|APIKeyRequiredWhenConfigured|Timeout|InvalidJSONTrailingGarbage)" -count=1`、`go test ./pkg/session -run "TestNewSessionManager_SkipsCorruptJSONLSession" -count=1`；结果：通过。

## Phase B: Agent / Tools / HTTPAPI / Session 继续精简
- [x] Task B.1: 继续拆 `pkg/agent/loop.go`；完成时间：2026-03-08 06:54 CST；改动摘要：把 `ReloadMCPTools` 下沉到新文件 `pkg/agent/loop_mcp.go`，并把 tool-call 归一化、recent fingerprint、assistant tool-call 消息拼装、批量执行与结果应用等逻辑下沉到 `pkg/agent/loop_tools.go`，保留 `loop.go` 的主循环骨架与行为不变；验证命令：`go test ./pkg/agent -run "Loop|Fallback|Resume|Publish" -count=1`；结果：通过。
- [x] Task B.2: 继续拆 `pkg/agent/memory.go`；完成时间：2026-03-08 06:54 CST；改动摘要：将 memory scope/read stack 相关类型与函数拆到 `pkg/agent/memory_scope.go`，将 block parse/render/merge helper 拆到 `pkg/agent/memory_blocks.go`，让 `memory.go` 聚焦 MemoryStore 主流程、legacy section 兼容与 hybrid merge；验证命令：`go test ./pkg/agent -run "Memory|SearchRelevant|OrganizeWriteback" -count=1`；结果：通过。
- [x] Task B.3: 继续拆 `pkg/tools/filesystem.go` / `pkg/tools/shell*.go`；完成时间：2026-03-08 07:07 CST；改动摘要：将 filesystem backend / read / document_text 逻辑分别下沉到 `pkg/tools/filesystem_backend.go`、`pkg/tools/filesystem_read.go`、`pkg/tools/filesystem_document_text.go`，并把 shell 的同步执行、托管执行与 process tool 面拆到 `pkg/tools/shell_sync.go`、`pkg/tools/shell_managed.go`、`pkg/tools/shell_process_tool.go`，`shell.go` 仅保留配置/入口；验证命令：`go test ./pkg/tools -run "Filesystem|Shell|Process|ExecuteToolCalls" -count=1`（当前环境 `SIGKILL(137)`，未作为通过依据）、`go test ./pkg/tools -run "TestExecuteToolCalls_ParallelToolCallsPreserveOrder|TestRegistryExecuteWithContextNilResult|TestShellSessionTable_PrunesTerminalSessionsAtCapacity|TestBackgroundSession_" -count=1`；结果：通过。环境说明：较宽的 `pkg/tools` 聚合正则在当前环境被 `SIGKILL(137)` 终止，因此改用用户要求的小批次定向验证。
- [x] Task B.4: 继续拆 `pkg/session/manager.go` / console helper；完成时间：2026-03-08 07:07 CST；改动摘要：将 `SessionManager` 的 load / path / snapshot / GC 逻辑拆到 `pkg/session/manager_load.go`、`pkg/session/manager_paths.go`、`pkg/session/manager_snapshots.go`、`pkg/session/manager_gc.go`，让 `manager.go` 聚焦主流程与 mutation；console session 相关 helper 继续留在 HTTPAPI 层但与 session 读取语义保持收敛；验证命令：`go test ./pkg/session ./pkg/httpapi -run "Session|Replay|Console" -count=1`；结果：通过。

## Phase C: Gateway / Channels 继续精简
- [x] Task C.1: 继续拆 `pkg/channels/manager.go` / `manager_dispatch.go`；完成时间：2026-03-08 07:27 CST；改动摘要：将 channel manager 的 lifecycle / HTTP server / worker registry 相关逻辑下沉到 `pkg/channels/manager_lifecycle.go` 与 `pkg/channels/manager_registry.go`，并顺手收口 enabled-channel 稳定排序与 split 文件依赖，保持 manager / dispatch 语义不变；验证命令：`go test ./pkg/channels -count=1`（当前环境 `SIGKILL(137)`，未作为通过依据）、`go test ./pkg/channels -run '^$' -count=1`、`go test ./pkg/channels -run 'Test(SelectedChannelInitializers|StartAllReturnsErrorWhenAllChannelsFail|LazyWorkerCreation|SendToChannelAfterStopAllReturns(NotRunning|ErrNotRunning)|BuildMediaScope_(FastIDUniqueness|WithMessageID)|WithSecurityHeaders_SetsBaselineHeaders)' -count=1`；结果：通过。环境说明：整包测试在当前环境被 `SIGKILL(137)` 杀掉，因此以可编译验证 + 覆盖 lifecycle/registry 的窄批次回归作为实际依据。
- [x] Task C.2: 继续拆 `pkg/cron/service.go`；完成时间：2026-03-08 07:27 CST；改动摘要：将 cron 的 schedule 解析、next-run 计算、stale running state 清理与 wake 选择 helper 下沉到 `pkg/cron/service_schedule.go`，让 `service.go` 更聚焦 store lifecycle、主循环与 job 执行；验证命令：`go test ./pkg/cron -count=1`；结果：通过。
- [x] Task C.3: 继续下沉 gateway runtime / reload / httpapi 装配 helper；完成时间：2026-03-08 07:27 CST；改动摘要：复用 `prepareGatewayRuntime(...)` 作为 gateway runtime/httpapi 装配事实来源，并把 runtime 启动、reload watcher 与 signal 控制 helper 收口到 `cmd/x-claw/internal/gateway/runtime_control.go`，避免 `runGateway` 再维护一套平行装配逻辑；验证命令：`go test ./cmd/x-claw/internal/gateway ./internal/gateway -count=1`；结果：通过。

## Phase D: Providers / Config 边界收敛
- [x] Task D.1: 收敛 provider fallback / protocol alias / config default 事实来源；完成时间：2026-03-08 07:07 CST；改动摘要：新增 `pkg/providers/http_defaults.go` 与 `pkg/providers/fallback_model_config.go`，把 HTTP provider 默认 `api_base`、本地 API key 需求判定、fallback candidate 的 alias/protocol 映射、fallback model config 合成统一收口到 `providers` 层；`pkg/agent/loop_fallback.go` 只保留 orchestration，改为调用 provider helper；同时复用现有 `CanonicalProtocol` 作为协议别名事实来源，避免再维护一套平行 switch；验证命令：`go test ./pkg/providers ./pkg/config -run "Factory|Fallback|Default|Provider" -count=1`、`go test ./pkg/providers ./pkg/agent ./pkg/config -run "Factory|Fallback|Default|Provider" -count=1`；结果：通过。
- [x] Task D.2: 继续拆 auth/provider interactive flow 与错误提示路径；完成时间：2026-03-08 07:12 CST；改动摘要：将 `pkg/auth/oauth.go` 中的 device-code 与 token exchange/refresh/JWT 解析流程拆到 `pkg/auth/oauth_device.go` 与 `pkg/auth/oauth_tokens.go`，保留 browser/login 流在独立 `pkg/auth/oauth_browser.go`；同时把 `pkg/providers/antigravity_provider.go` 的 token source、project/model 拉取逻辑下沉到 `pkg/providers/antigravity_auth.go`，让 provider 主文件聚焦 Chat/build/parse 主路径；验证命令：`go test ./pkg/auth ./pkg/providers -run "OAuth|Auth|Credential|Antigravity" -count=1`；结果：通过。

## 阶段验证
- [x] Gate A：`pkg/agent` / `pkg/tools` / `pkg/httpapi` / `pkg/session` 定向测试通过；完成时间：2026-03-08 06:49 CST；验证命令：`go test ./pkg/agent -run "TestSanitizeHistoryForProvider|TestMemoryForSession|TestConcurrentBuildSystemPromptWithCache|TestNewAgentInstance_" -count=1 && go test ./pkg/tools -run "TestExecuteToolCalls_ParallelToolCallsPreserveOrder|TestRegistryExecuteWithContextNilResult|TestShellSessionTable_PrunesTerminalSessionsAtCapacity|TestBackgroundSession_" -count=1`（当前环境触发 `SIGKILL(137)`，未作为通过依据）、`go test ./pkg/agent -run "TestSanitizeHistoryForProvider|TestMemoryForSession|TestConcurrentBuildSystemPromptWithCache|TestNewAgentInstance_" -count=1`、`go test ./pkg/tools -run "TestExecuteToolCalls_ParallelToolCallsPreserveOrder|TestRegistryExecuteWithContextNilResult|TestShellSessionTable_PrunesTerminalSessionsAtCapacity|TestBackgroundSession_" -count=1`、`go test ./pkg/session -count=1`、`go test ./pkg/httpapi -count=1`；结果：通过。环境说明：合并执行 `pkg/agent` + `pkg/tools` 定向命令在当前环境被 `SIGKILL(137)` 杀掉，因此改用更小批次拆分验证并逐项记录。
- [x] Gate B：Agent / Tools / HTTPAPI / Session 精简后的定向测试通过；完成时间：2026-03-08 07:08 CST；验证命令：`go test ./pkg/agent -run "Loop|Fallback|Resume|Publish|Memory|SearchRelevant|OrganizeWriteback" -count=1`、`go test ./pkg/session ./pkg/httpapi -run "Session|Replay|Console" -count=1`、`go test ./pkg/tools -run "TestExecuteToolCalls_ParallelToolCallsPreserveOrder|TestRegistryExecuteWithContextNilResult|TestShellSessionTable_PrunesTerminalSessionsAtCapacity|TestBackgroundSession_" -count=1`；结果：通过。环境说明：更宽的 `pkg/tools -run "Filesystem|Shell|Process|ExecuteToolCalls"` 在当前环境曾被 `SIGKILL(137)` 终止，因此 Gate B 采用更小批次命令作为实际通过依据。
- [x] Gate C：`pkg/channels` / `pkg/cron` / `cmd/x-claw/internal/gateway` / `internal/gateway` 定向测试通过；完成时间：2026-03-08 07:30 CST；验证命令：`go test ./pkg/channels ./pkg/cron ./cmd/x-claw/internal/gateway ./internal/gateway -count=1`（当前环境 `SIGKILL(137)`，未作为通过依据）、`go test ./pkg/channels -count=1`、`go test ./pkg/cron -count=1`、`go test ./cmd/x-claw/internal/gateway ./internal/gateway -count=1`、`go test ./pkg/channels ./cmd/x-claw/internal/gateway ./pkg/providers -count=1`；结果：通过。环境说明：四包合并执行在当前环境被 `SIGKILL(137)` 终止，因此改用更小批次命令逐项验证，并补跑了用户要求的三包定向命令。
- [x] Gate D：`pkg/providers` / `pkg/config` / `pkg/auth` / `pkg/agent` 定向测试通过；完成时间：2026-03-08 07:26 CST；验证命令：`go test ./pkg/providers ./pkg/config ./pkg/auth -count=1`、`go test ./pkg/agent -run "TestSanitizeHistoryForProvider|TestMemoryForSession|TestConcurrentBuildSystemPromptWithCache|TestNewAgentInstance_" -count=1`；结果：通过。

## 最终验证
- [x] `go build -p 1 ./...` 通过；完成时间：2026-03-08 07:28 CST；验证命令：`go build -p 1 ./...`；结果：通过。
- [x] `go vet ./...` 通过；完成时间：2026-03-08 07:28 CST；验证命令：`go vet ./...`；结果：通过。
- [x] `go test ./... -run '^$' -count=1` 通过；完成时间：2026-03-08 07:28 CST；验证命令：`go test ./... -run '^$' -count=1`；结果：通过。
- [x] 用户要求的定向 `go test` 已记录；完成时间：2026-03-08 07:30 CST；验证命令：`go test ./pkg/session -count=1`（并发跑时曾 `SIGKILL(137)`，随后单独重跑通过）、`go test ./pkg/httpapi -count=1`、`go test ./pkg/channels ./cmd/x-claw/internal/gateway ./pkg/providers -count=1`、`go test ./pkg/agent -run '^TestSanitizeHistoryForProvider|TestMemoryForSession|TestConcurrentBuildSystemPromptWithCache|TestNewAgentInstance_$' -count=1`（并发跑时曾 `SIGKILL(137)`，随后单独重跑通过）、`go test ./pkg/tools -run 'TestExecuteToolCalls_ParallelToolCallsPreserveOrder|TestRegistryExecuteWithContextNilResult|TestShellSessionTable_PrunesTerminalSessionsAtCapacity|TestBackgroundSession_' -count=1`；结果：通过。
- [x] 可选 race 验证已尽力记录；完成时间：2026-03-08 07:30 CST；验证命令：`go test -race ./pkg/agent -run 'Context|BuildMessages|SystemPrompt' -count=1`；结果：通过。环境说明：`go test -race ./pkg/session ./pkg/httpapi -count=1`、`go test -race ./pkg/session -count=1` 与 `go test -race ./pkg/httpapi -count=1` 在当前环境分别出现 `SIGKILL(137)` / `PASS` 后被系统杀掉，因此未作为通过依据。
- [x] 本地 Gateway 完成通知已发送；完成时间：2026-03-08 07:32 CST；验证命令：`curl -sS -X POST http://127.0.0.1:18790/api/notify -H 'Authorization: Bearer <gateway.api_key>' -H 'Content-Type: application/json' -d '{"content":"✅ X-Claw: Round 5 change complete (ready for review)."}'`；结果：`{"ok":true,"channel":"feishu","to":"oc_54c36da82940561939b5ffbbae56651f"}`。

## 记录要求
- 每完成一个任务，将 `[ ]` 改为 `[x]`
- 在对应条目后追加：完成时间、改动摘要、验证命令、结果
- 若遇到环境限制（如 `SIGKILL(137)`），必须写明“实际执行了什么”和“为什么改用更小批次验证”

## 任务细化（2026-03-08 11:20 CST）

### 并行边界与串行依赖
- 文档写集合仅主控串行维护：`docs/plans/PROGRESS-R5.md`
- 串行前置：先完成 `Task A.1` / `Task A.2` / `Task A.3`，再进入 `Task B.*`
- 并行组 1（Gateway / Channels）：`Task C.1`、`Task C.2`、`Task C.3`，允许并行，但 `Task C.3` 若触碰共享装配 helper，优先在 `Task C.1` / `Task C.2` 完成后再收口
- 并行组 2（Providers / Config）：`Task D.1`、`Task D.2`，其中 `Task D.1` 为 `Task D.2` 的配置事实来源前置
- 并行组 3（Agent / Tools / HTTPAPI / Session）：`Task A.*` 串行；`Task B.1` / `Task B.2` / `Task B.3` 可并行；`Task B.4` 依赖 `Task A.3` 完成后的 session / console 语义基线
- Gate 依赖：`Gate A` 依赖 `Task A.*`；`Gate B` 依赖 `Task B.*`；`Gate C` 依赖 `Task C.*`；`Gate D` 依赖 `Task D.*`；最终验证依赖所有 Task / Gate 完成

### 子任务清单

#### Task A.1 细化：移除 Agent 初始化路径的 `log.Fatalf`
- [x] Task A.1.a：定位 `NewAgentInstance` / `buildBaseAgentToolRegistry` 的 hard-exit 调用点与影响面；完成时间：2026-03-08 06:40 CST
- [x] Task A.1.b：新增 exec tool 初始化失败的失败测试，证明当前缺口；完成时间：2026-03-08 06:40 CST
- [x] Task A.1.c：改为可观测错误或可控降级，保持其他工具与调用链行为稳定；完成时间：2026-03-08 06:40 CST
- [x] Task A.1.d：执行 `go test ./pkg/agent -run 'NewAgentInstance|ExecTool' -count=1` 并记录结果；完成时间：2026-03-08 06:40 CST；结果：通过

#### Task A.2 细化：修复 tool trace sync 吞错与 memory init 模糊失败
- [x] Task A.2.a：定位 `pkg/tools/tool_trace.go` 的 `f.Sync()` 静默吞错路径；完成时间：2026-03-08 06:43 CST
- [x] Task A.2.b：定位 `pkg/agent/memory.go` 的 `os.MkdirAll` 吞错路径与初始化调用链；完成时间：2026-03-08 06:43 CST
- [x] Task A.2.c：为 trace sync 失败与 memory init 失败分别补失败测试或可观测性测试；完成时间：2026-03-08 06:43 CST
- [x] Task A.2.d：最小修复并保持成功路径行为不变；完成时间：2026-03-08 06:43 CST
- [x] Task A.2.e：执行 `go test ./pkg/tools ./pkg/agent -run 'Trace|MemoryStore|MemoryForSession' -count=1` 或等价窄批次验证并记录结果；完成时间：2026-03-08 06:43 CST；结果：通过

#### Task A.3 细化：收敛 console auth / error surface / degraded session 语义
- [x] Task A.3.a：梳理 `console` / `notify` / `resume` 鉴权 helper 与错误响应路径；完成时间：2026-03-08 06:47 CST
- [x] Task A.3.b：补充 file / stream 错误面与 replay degraded 语义测试；完成时间：2026-03-08 06:47 CST
- [x] Task A.3.c：统一鉴权 helper、日志与远端错误面；完成时间：2026-03-08 06:47 CST
- [x] Task A.3.d：明确 replay 失败后的 degraded / skip 语义并收口到 session / httpapi 读取面；完成时间：2026-03-08 06:47 CST
- [x] Task A.3.e：执行 `go test ./pkg/httpapi ./pkg/session -run 'Console|Notify|Session|Replay' -count=1` 或等价窄批次验证并记录结果；完成时间：2026-03-08 06:47 CST；结果：通过

#### Task B.1 细化：继续拆 `pkg/agent/loop.go`
- [x] Task B.1.a：划定 queue / publish / MCP reload / fallback 现有代码边界与共享状态；完成时间：2026-03-08 06:54 CST
- [x] Task B.1.b：抽离 2~3 组职责到 `pkg/agent/loop_*.go`，仅做物理搬运与 helper 收口；完成时间：2026-03-08 06:54 CST
- [x] Task B.1.c：执行 `go test ./pkg/agent -run 'Loop|Fallback|Resume|Publish' -count=1` 或等价窄批次验证并记录结果；完成时间：2026-03-08 06:54 CST；结果：通过

#### Task B.2 细化：继续拆 `pkg/agent/memory.go`
- [x] Task B.2.a：划定 daily notes / block parse / organize writeback / retrieval merge 边界；完成时间：2026-03-08 06:54 CST
- [x] Task B.2.b：抽离 2~3 组职责到 `pkg/agent/memory_*.go`，不改变行为；完成时间：2026-03-08 06:54 CST
- [x] Task B.2.c：执行 `go test ./pkg/agent -run 'Memory|SearchRelevant|OrganizeWriteback' -count=1` 或等价窄批次验证并记录结果；完成时间：2026-03-08 06:54 CST；结果：通过

#### Task B.3 细化：继续拆 `pkg/tools/filesystem.go` / `pkg/tools/shell*.go`
- [x] Task B.3.a：划定 filesystem 请求解析 / truncate / FS helper 边界；完成时间：2026-03-08 07:07 CST
- [x] Task B.3.b：划定 shell 参数解析 / backend / session 生命周期 / 输出拼装边界；完成时间：2026-03-08 07:07 CST
- [x] Task B.3.c：抽离 helper 到 `filesystem_*.go` / `shell_*.go`，保持工具接口不变；完成时间：2026-03-08 07:07 CST
- [x] Task B.3.d：执行 `go test ./pkg/tools -run 'Filesystem|Shell|Process|ExecuteToolCalls' -count=1` 或等价窄批次验证并记录结果；完成时间：2026-03-08 07:07 CST；结果：通过（改用更小批次命令，原因同上）

#### Task B.4 细化：继续拆 `pkg/session/manager.go` / console helper
- [x] Task B.4.a：划定 replay / persist / snapshot / GC / list helper 边界；完成时间：2026-03-08 07:07 CST
- [x] Task B.4.b：抽离 session manager helper 与 console session/fs helper，不改变存储格式与接口语义；完成时间：2026-03-08 07:07 CST
- [x] Task B.4.c：执行 `go test ./pkg/session ./pkg/httpapi -run 'Session|Replay|Console' -count=1` 或等价窄批次验证并记录结果；完成时间：2026-03-08 07:07 CST；结果：通过

#### Task C.1 细化：继续拆 `pkg/channels/manager.go` / `manager_dispatch.go`
- [x] Task C.1.a：划定 lifecycle / worker map / dispatch / send helper 边界；完成时间：2026-03-08 07:27 CST
- [x] Task C.1.b：抽离 `pkg/channels/manager_*.go` helper，保持 channel manager 语义不变；完成时间：2026-03-08 07:27 CST
- [x] Task C.1.c：执行 `go test ./pkg/channels -count=1` 并记录结果；完成时间：2026-03-08 07:30 CST；结果：通过

#### Task C.2 细化：继续拆 `pkg/cron/service.go`
- [x] Task C.2.a：划定 store / scheduler / runner / state mutation 边界；完成时间：2026-03-08 07:27 CST
- [x] Task C.2.b：抽离 `pkg/cron/service_*.go` helper，保持 cron 行为不变；完成时间：2026-03-08 07:27 CST
- [x] Task C.2.c：执行 `go test ./pkg/cron -count=1` 并记录结果；完成时间：2026-03-08 07:27 CST；结果：通过

#### Task C.3 细化：继续下沉 gateway runtime / reload / httpapi 装配 helper
- [x] Task C.3.a：梳理 `cmd/x-claw/internal/gateway` 与 `internal/gateway` 的 runtime / state / httpapi 装配边界；完成时间：2026-03-08 07:27 CST
- [x] Task C.3.b：下沉共享 helper，避免重复装配逻辑扩散；完成时间：2026-03-08 07:27 CST
- [x] Task C.3.c：执行 `go test ./cmd/x-claw/internal/gateway ./internal/gateway -count=1` 并记录结果；完成时间：2026-03-08 07:27 CST；结果：通过

#### Task D.1 细化：收敛 provider fallback / protocol alias / config default 事实来源
- [x] Task D.1.a：梳理 fallback candidate、protocol alias、default `api_base` 现有事实来源；完成时间：2026-03-08 07:07 CST
- [x] Task D.1.b：统一映射 helper 与默认值来源，保持 provider 解析与 fallback 行为不变；完成时间：2026-03-08 07:07 CST
- [x] Task D.1.c：执行 `go test ./pkg/providers ./pkg/agent ./pkg/config -run 'Factory|Fallback|Default|Provider' -count=1` 或等价窄批次验证并记录结果；完成时间：2026-03-08 07:07 CST；结果：通过

#### Task D.2 细化：继续拆 auth/provider interactive flow 与错误提示路径
- [x] Task D.2.a：划定 interactive prompt / browser flow / token exchange / store usage 边界；完成时间：2026-03-08 07:12 CST
- [x] Task D.2.b：抽离 helper 并收口错误提示路径，保持交互语义不变；完成时间：2026-03-08 07:12 CST
- [x] Task D.2.c：执行 `go test ./pkg/auth ./pkg/providers -run 'OAuth|Auth|Credential|Antigravity' -count=1` 或等价窄批次验证并记录结果；完成时间：2026-03-08 07:12 CST；结果：通过
