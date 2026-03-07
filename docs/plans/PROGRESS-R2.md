# Round 2 重构执行进度

## Phase A: P0 Bug + 安全修复
- [x] Task A.1: 消除 pkg/ 层 fmt.Printf (REQ-R2-BUG-001) — 2026-03-07 23:57:08 +0800，替换 `pkg/agent/instance.go`、`pkg/tools/web_search.go`、`cmd/x-claw/internal/gateway/httpapi.go` 的 `fmt.Printf` 为结构化日志；验收 grep 仅剩既有 CLI 交互输出
- [x] Task A.2: 修复 antigravity io.ReadAll (REQ-R2-BUG-002) — 2026-03-07 23:57:08 +0800，`FetchAntigravityProjectID/Models` 两处改为检查 `io.ReadAll` 错误并包装返回
- [x] Task A.3: 修复 Feishu reaction undo context (REQ-R2-BUG-003) — 2026-03-07 23:57:08 +0800，undo 删除 reaction 改为 `context.WithTimeout(..., 5s)` 并在失败时记录 debug 日志
- [x] Task A.4: HTTP 错误响应不泄露内部信息 (REQ-R2-SEC-001) — 2026-03-07 23:57:08 +0800，notify/resume 对非 loopback 返回通用错误，对内保留详细日志与 loopback 详情

## Phase B: P1 Bug 修复
- [x] Task B.1: 统一 os.MkdirAll 错误处理 (REQ-R2-BUG-004) — 2026-03-07 23:57:08 +0800，agent workspace 与 session storage 目录创建均改为显式 warn 日志
- [x] Task B.2: 删除死函数 ensureActiveAgentForSession (REQ-R2-BUG-005) — 2026-03-07 23:57:08 +0800，删除空实现及两个调用点，相关 grep 已清零
- [x] Task B.3: compaction save 错误记录日志 (REQ-R2-BUG-006) — 2026-03-08 00:04:54 +0800，补齐 compaction/summarize 路径中的 session save 与 summarizeBatch 吞错日志
- [x] Task B.4: session meta 持久化错误记录 (REQ-R2-BUG-007) — 2026-03-08 00:04:54 +0800，`writeMetaFile` / `applyEvents` / `migrateLegacyToJSONL` 全部改为 warn 记录

## Phase C: P1 性能 + 安全 + 测试
- [x] Task C.1: 复用 antigravity HTTP Client (REQ-R2-PERF-001) — 2026-03-08 00:04:54 +0800，`FetchAntigravityProjectID/Models` 改为共享 `antigravityFetchClient`
- [x] Task C.2: 缓存 Telegram bot username 正则 (REQ-R2-PERF-002) — 2026-03-08 00:04:54 +0800，在 channel 初始化/启动时缓存 mention 正则，`stripBotMention` 不再重复编译
- [x] Task C.3: HTTP 安全头部补全 (REQ-R2-SEC-002) — 2026-03-08 00:04:54 +0800，确认 `withSecurityHeaders` 已包含 `X-Frame-Options` 与 `X-Content-Type-Options`，基线测试通过
- [x] Task C.4: auditlog 包测试 (REQ-R2-TEST-001) — 2026-03-08 00:13:32 +0800，新增 `Record` / `VerifyHMACSignature` / rotation / 并发写入回归，`go test -race ./pkg/auditlog -count=1 -v -parallel 1` 通过

## Phase D: P2 清理
- [x] Task D.1: auditlog 写入错误处理 (REQ-R2-BUG-008) — 2026-03-08 00:13:32 +0800，`auditlog` 的 rotate/write/close 失败统一写入 stderr，避免递归 logger
- [x] Task D.2: 清理死参数 (REQ-R2-BUG-009) — 2026-03-08 00:13:32 +0800，移除 `memory`/`embedder`/`loop_commands`/`filesystem` 中的无效 `_ = xxx`，并顺手补一处 model override 清理 warn
- [x] Task D.3: API Key 常量时间比较 (REQ-R2-SEC-003) — 2026-03-08 00:13:32 +0800，notify handler 改为 `subtle.ConstantTimeCompare`
- [x] Task D.4: media.go HTTP Client 复用 (REQ-R2-PERF-003) — 2026-03-08 00:13:32 +0800，默认下载路径复用共享 HTTP client，仅在自定义 timeout/proxy 时新建 client
- [x] Task D.5: 抽取重复 isLoopback (REQ-R2-QUAL-001) — 2026-03-08 00:13:32 +0800，新增 `pkg/utils/net.go` 并复用到 gateway/config
- [x] Task D.6: archcheck 扩展 (REQ-R2-TEST-002) — 2026-03-08 00:13:32 +0800，新增 `pkg/tools`→`channels` 与 `pkg/config`→`agent/session` 依赖守护测试

## 最终验证
- [x] `go build ./...` 通过 — 2026-03-08 00:16:58 +0800，使用仓库本地 `.cache` fresh 重跑通过
- [x] `go vet ./...` 通过 — 2026-03-08 00:16:58 +0800，使用仓库本地 `.cache` fresh 重跑通过
- [x] 关键包定向测试通过 — 2026-03-08 00:16:58 +0800，`pkg/auditlog` / `internal/archcheck` / `pkg/agent` / `pkg/session` / `pkg/channels` / `pkg/providers` / `pkg/tools` / `pkg/config` / `pkg/httpapi` / `internal/gateway` / `internal/...` / `cmd/...` 已按包或定向命令验证
