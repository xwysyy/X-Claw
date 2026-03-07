# X-Claw Round 2 全面审查报告

> 审查日期: 2026-03-07
> 审查范围: 240 Go 文件, ~76K LOC (排除 ref/workspace/cache)
> 前提: Round 1 重构已完成 (见 PROGRESS.md, 22 个任务全部完成)

---

## 一、审查维度总览

| 维度 | 发现数 | 严重 | 中等 | 低 |
|------|--------|------|------|-----|
| Bug & 错误处理 | 14 | 3 | 7 | 4 |
| 代码质量 | 8 | 0 | 5 | 3 |
| 性能 | 5 | 0 | 3 | 2 |
| 安全 | 4 | 1 | 2 | 1 |
| 架构 & 结构 | 6 | 0 | 4 | 2 |
| 测试覆盖 | 5 | 0 | 3 | 2 |
| **合计** | **42** | **4** | **24** | **14** |

---

## 二、Bug & 错误处理

### BUG-R2-001 [严重] fmt.Printf 残留在 pkg/ 层

Round 1 修复了 `pkg/tools/shell.go` 的 `fmt.Printf`, 但以下位置仍然存在:

| 文件 | 行号 | 内容 |
|------|------|------|
| `pkg/agent/instance.go` | 304 | `fmt.Printf("Warning: invalid path pattern %q: %v\n", p, err)` |
| `pkg/tools/web_search.go` | 207 | `fmt.Printf("Brave API Error Body: %s\n", string(body))` |
| `cmd/x-claw/internal/gateway/httpapi.go` | 114 | `fmt.Printf("Warning: failed to register %s: %v\n", reg.pattern, err)` |

**风险**: Gateway 模式下 stdout 输出可能干扰结构化日志、被容器运行时错误捕获。
**修复**: 改为 `logger.WarnCF(...)`.

### BUG-R2-002 [严重] io.ReadAll 错误被忽略

`pkg/providers/antigravity_provider.go` 两处:

```
643:  body, _ := io.ReadAll(resp.Body)   // FetchAntigravityProjectID
684:  body, _ := io.ReadAll(resp.Body)   // FetchAntigravityModels
```

如果 ReadAll 失败, `body` 为空, 后续 `json.Unmarshal(body, &result)` 会返回 unhelpful 的 "unexpected end of JSON", 丢失真正的 I/O 错误。

### BUG-R2-003 [严重] context.Background() 应使用父 context

`pkg/channels/feishu/feishu_64.go:282`:
```go
_, _ = c.client.Im.V1.MessageReaction.Delete(context.Background(), delReq)
```
在 reaction undo 回调中使用了 `context.Background()`, 而非从 `ctx` 派生。如果 channel 正在 shutdown, 这个调用无法被取消, 可能导致 goroutine 挂起。

### BUG-R2-004 [中等] os.MkdirAll 错误处理不一致

三种模式并存, 风格不统一:

| 文件 | 行号 | 模式 |
|------|------|------|
| `pkg/agent/instance.go` | 75 | `os.MkdirAll(workspace, 0o755)` — 错误完全丢弃 |
| `pkg/agent/memory.go` | 75 | `_ = os.MkdirAll(memoryDir, 0o755)` — 显式忽略 |
| `pkg/session/manager.go` | 61 | `os.MkdirAll(storage, 0o755)` — 错误完全丢弃 |
| `pkg/agent/loop.go` | 1143 | `if err := os.MkdirAll(...); err != nil { return }` — 正确 |

在 instance.go:75 和 session/manager.go:61, 如果目录创建失败, 后续文件操作都会静默失败。
**修复**: 统一为检查并返回/记录错误。

### BUG-R2-005 [中等] ensureActiveAgentForSession 是死函数

`pkg/agent/run_pipeline_impl.go:257-260`:
```go
func (al *AgentLoop) ensureActiveAgentForSession(sessionKey string, agent *AgentInstance) {
    _ = sessionKey
    _ = agent
}
```
该函数接收两个参数但完全不使用, 是 no-op 占位。应删除或实现其预期逻辑。

### BUG-R2-006 [中等] compaction save 错误被静默吞没

`pkg/agent/loop_compaction.go` 多处:
```
197: finalSummary, _ = al.summarizeBatch(...)  // 摘要失败被吞
331: _ = agent.Sessions.Save(sessionKey)       // 持久化失败被吞
413: _ = agent.Sessions.Save(sessionKey)       // 持久化失败被吞
473: _ = agent.Sessions.Save(sessionKey)       // 持久化失败被吞
```
compaction 后 Session.Save 失败意味着会话数据可能丢失, 至少应记录 warn 日志。

### BUG-R2-007 [中等] session meta 持久化错误被吞

`pkg/session/manager_mutations.go:179`:
```go
_ = writeMetaFile(path, buildSessionMeta(session))
```
同样出现在 `tree.go:169`, `manager.go:658,683,754`。

meta 文件写入失败会导致重启后会话状态丢失, 应至少记录日志。

### BUG-R2-008 [中等] credential save 错误被吞

`pkg/providers/antigravity_provider.go:609`:
```go
_ = auth.SetCredential("google-antigravity", cred)
```
保存凭证失败意味着每次重启都需要重新获取 project ID。

### BUG-R2-009 [中等] PublishOutbound 错误未处理

`pkg/agent/loop.go:2962`:
```go
r.loop.bus.PublishOutbound(r.ctx, bus.OutboundMessage{...})
```
发送工具结果给用户的调用没有检查返回值。

### BUG-R2-010 [中等] auditlog 写入错误被吞

`pkg/auditlog/auditlog.go:157-158`:
```go
_, _ = f.Write(line)
_ = f.Close()
```
审计日志写入失败是安全相关事件, 应至少在 stderr 或专用通道告警。

### BUG-R2-011 [低] TaskLedger.load 错误被吞

`pkg/tools/task_ledger.go:77`:
```go
_ = l.load()
```
任务账本加载失败后使用空 map, 可能丢失已有任务。

### BUG-R2-012 [低] Feishu reaction undo 错误被吞

`pkg/channels/feishu/feishu_64.go:282`:
```go
_, _ = c.client.Im.V1.MessageReaction.Delete(context.Background(), delReq)
```
结合 BUG-R2-003, 既使用了 Background ctx 又忽略了错误。

### BUG-R2-013 [低] memory_vector nil ctx fallback

`pkg/agent/memory_vector.go:194,207,284,349`:
```go
if ctx == nil {
    ctx = context.Background()
}
```
4 处 nil ctx fallback, 但 Go 惯例是永远不传 nil context, 这些检查暗示调用方可能有 bug。

### BUG-R2-014 [低] http_retry 可能返回 nil resp

`pkg/utils/http_retry.go:47`:
```go
return resp, err
```
如果所有重试都因 ctx cancel 退出, 返回的 `(nil, ctx.Err())` 组合正确。但如果最后一次 client.Do 返回 `(resp, err)` 且 err != nil (如 DNS 错误), resp 可能为 nil, 而调用方若未检查 err 就访问 resp 会 panic。

---

## 三、代码质量

### QUAL-R2-001 [中等] 大文件仍然存在

Round 1 拆分后, 以下文件仍超过 1000 行:

| 文件 | 行数 | 建议 |
|------|------|------|
| `pkg/agent/loop.go` | 3006 | 可继续拆分 LLM 迭代运行器 (2200+ 行) |
| `pkg/tools/web_search.go` | 1899 | 可拆分搜索引擎后端 (Brave/DuckDuckGo/Bing/Serper) |
| `pkg/tools/shell.go` | 1824 | 可拆分 Docker 执行/进程管理/会话管理 |
| `pkg/channels/feishu/feishu_64.go` | 1528 | 可拆分消息处理/媒体/markdown |
| `pkg/tools/toolcall_executor_test.go` | 1479 | 测试文件, 可拆分 |
| `pkg/agent/run_pipeline_impl.go` | 1432 | 可拆分 resume/notify/copy 辅助 |
| `pkg/agent/memory.go` | 1243 | Round 1 已拆分部分, 剩余合理 |
| `pkg/channels/manager.go` | 1186 | Round 1 已拆分部分, 剩余合理 |
| `pkg/tools/filesystem.go` | 1136 | 可拆分 zip/tar 操作 |
| `pkg/skills/registry.go` | 1122 | 可拆分 HTTP 下载/搜索/匹配 |
| `pkg/agent/context.go` | 1105 | 可拆分 prompt 构建和 memory 检索 |

### QUAL-R2-002 [中等] 死代码/死参数

| 文件 | 行号 | 描述 |
|------|------|------|
| `run_pipeline_impl.go` | 257-260 | `ensureActiveAgentForSession` 完全 no-op |
| `run_pipeline_impl.go` | 258-259 | `_ = sessionKey; _ = agent` |
| `memory.go` | 287 | `_ = ctx` (dead param in a method) |
| `memory_embedder.go` | 260,286-287 | `_ = ctx; _ = inputs` (dead params) |
| `loop_commands.go` | 81 | `_ = agent` |
| `filesystem.go` | 1047 | `_ = ctx` |

### QUAL-R2-003 [中等] TODO/未实现代码

`pkg/providers/github_copilot_provider.go:29`:
```go
// TODO: Implement stdio mode for GitHub Copilot provider
```

### QUAL-R2-004 [中等] cmd/ 层 fmt.Printf 应使用 logger

`cmd/x-claw/internal/gateway/helpers.go` 有 20+ 处 `fmt.Printf`/`fmt.Println` 用于启动消息和状态输出。虽然 CLI 层 stdout 输出是合理的, 但当作为 daemon 运行时应统一用 logger 以便日志聚合。

### QUAL-R2-005 [中等] regexp.Compile 在运行时热路径

`pkg/channels/telegram/telegram.go:782`:
```go
re := regexp.MustCompile(`(?i)@` + regexp.QuoteMeta(botUsername))
```
每次处理消息时都重新编译正则, 应缓存。

### QUAL-R2-006 [低] 同名函数重复

`isLoopbackRemote` / `isLoopbackHost` 在两个包中分别实现:
- `internal/gateway/handlers_notify.go:171` — `isLoopbackRemote`
- `pkg/config/validation.go:12` — `isLoopbackHost`

逻辑高度相似, 可抽取到 `pkg/utils/net.go`。

### QUAL-R2-007 [低] 常量散落

- `rateLimitDelay = 1 * time.Second` 在 `pkg/channels/manager.go:49`
- `maxRetries = 3` 在 `pkg/utils/http_retry.go:10`
- `64 << 10` (64KB maxBody) 在 `internal/gateway/handlers_notify.go:36`

这些分散的常量没有集中管理。

### QUAL-R2-008 [低] 测试中的 t.Skip 分散

17 处 `t.Skip()`, 部分是平台限制 (Windows), 部分是缺少外部依赖 (codex CLI)。应记录并跟踪。

---

## 四、性能

### PERF-R2-001 [中等] HTTP Client 每次调用新建

以下位置在函数级别创建 `&http.Client{}`, 未复用连接池:

| 文件 | 行号 | 函数 |
|------|------|------|
| `pkg/providers/antigravity_provider.go` | 636 | `FetchAntigravityProjectID` |
| `pkg/providers/antigravity_provider.go` | 677 | `FetchAntigravityModels` |
| `pkg/utils/media.go` | 96 | `DownloadMediaFile` |

**影响**: 每次调用都建立新 TCP 连接 + TLS 握手, 增加 P99 延迟。
**修复**: 复用包级别 client 或传入 shared client。

### PERF-R2-002 [中等] Telegram 正则每消息编译

`pkg/channels/telegram/telegram.go:782`:
```go
re := regexp.MustCompile(`(?i)@` + regexp.QuoteMeta(botUsername))
```
每条 Telegram 消息都重新编译正则。`botUsername` 在 channel 生命周期内不变, 应在初始化时编译并缓存。

### PERF-R2-003 [中等] CronService 1 秒 ticker

`pkg/cron/service.go:152`:
```go
ticker := time.NewTicker(1 * time.Second)
```
每秒 tick 一次检查定时任务, 即使没有任何 job。可以改为计算下次执行时间并 sleep 到那个时间点。

### PERF-R2-004 [低] web_search 大量正则

`pkg/tools/web_search.go:34-51` 定义了 15+ 个包级别正则, 这些已正确放在包级别, 但在 HTML 清洗时全量应用。对于大页面 (1MB+) 可能较慢。

### PERF-R2-005 [低] compaction 中 estimateTokens 重复遍历

`pkg/agent/loop_compaction.go:424-448` 先全量 estimateTokens, 然后再从末尾逆序累加。可以在一次遍历中完成。

---

## 五、安全

### SEC-R2-001 [严重] 错误消息向客户端泄露内部信息

`internal/gateway/handlers_notify.go:147`:
```go
_ = json.NewEncoder(w).Encode(notifyResponse{OK: false, Channel: channel, To: to, Error: err.Error()})
```
`err.Error()` 可能包含内部路径、数据库错误、provider API 密钥等敏感信息。应返回通用错误消息。

同样在 `handlers_resume.go:96`:
```go
_ = json.NewEncoder(w).Encode(resumeLastTaskResponse{OK: false, Error: res.err.Error()})
```

### SEC-R2-002 [中等] HTTP 安全头部不完整

`pkg/channels/manager.go:919-935` 的 `withSecurityHeaders` 中间件设置了一些头部, 但缺少:
- `X-Frame-Options: DENY`
- `Strict-Transport-Security` (如果支持 HTTPS)
- `Content-Security-Policy`

console 端点暴露在 HTTP API 上, 应防止 clickjacking。

### SEC-R2-003 [中等] shell.go deny patterns 可被绕过

`pkg/tools/shell.go:56-100` 定义了 46 个 deny patterns, 但都基于正则匹配简单字符串, 可能被 shell 展开、变量替换、别名等绕过。例如:
- `r"m" -rf /` (拆分命令名)
- `$(echo rm) -rf /` (命令替换, 虽然 `$()` 被拦截但有其他变体)

这是已知限制, 但应在文档中记录风险。

### SEC-R2-004 [低] API Key 比较未使用常量时间

`internal/gateway/handlers_notify.go:160,166`:
```go
if strings.TrimSpace(r.Header.Get("X-API-Key")) == apiKey {
```
使用 `==` 比较 API key 而非 `subtle.ConstantTimeCompare`, 存在时序攻击风险 (实际影响很小, 因为 API key 通常只在本地回环使用)。

---

## 六、架构 & 结构

### ARCH-R2-001 [中等] pkg/ 包过度暴露

以下 pkg/ 包仅在仓库内部使用, 应迁移到 `internal/`:

| 包 | 理由 |
|------|------|
| `pkg/auditlog` | 仅 agent/channels 内部使用 |
| `pkg/bus` | 仅 agent/channels/gateway 内部使用 |
| `pkg/constants` | 仅内部使用 |
| `pkg/fileutil` | 通用工具, 仅内部使用 |
| `pkg/identity` | 仅内部使用 |
| `pkg/logger` | 仅内部使用 |
| `pkg/state` | 仅 agent 内部使用 |
| `pkg/routing` | 仅 agent 内部使用 |

**风险**: 语义上 `pkg/` 意味着公开 API, 外部导入后如果变更会破坏兼容性。
**注意**: 此项变更影响面大, 需要修改所有 import 路径, 建议低优先级。

### ARCH-R2-002 [中等] internal/gateway 与 pkg/httpapi 职责重叠

`internal/gateway/handlers_*.go` 和 `pkg/httpapi/console*.go` 都处理 HTTP API:
- `internal/gateway`: notify, resume, health handlers
- `pkg/httpapi`: console, status, sessions, file, stream handlers

两者都有独立的 auth 检查 (`authorizeAPIKeyOrLoopback` vs console 的 API key 检查)。应统一 HTTP 层。

### ARCH-R2-003 [中等] ChannelDirectory 接口和 MediaResolver 接口在 agent 包内重定义

`pkg/agent/loop.go` 中 `AgentLoop` 使用了 `ChannelDirectory` 和 `MediaResolver`, 但这些接口在:
- `internal/core/ports/channel_directory.go`
- `internal/core/ports/media.go`

agent 包是否直接使用 ports 包中的定义? 还是有类型别名? 需要确认一致性。

### ARCH-R2-004 [中等] 架构守护测试覆盖不足

`internal/archcheck/archcheck_test.go` 只有 2 个测试:
1. `TestAgentDoesNotImportInfraChannels` — 禁止 agent 导入 channels/httpapi/media
2. `TestInternalCoreDoesNotImportAppOrPkg` — 禁止 core 导入非 core 包

缺少:
- internal/gateway 不应导入 pkg/agent (反向依赖)
- pkg/config 不应导入 pkg/agent/session 等上层包
- pkg/tools 不应导入 pkg/channels

### ARCH-R2-005 [低] 双重 health 端点

- `internal/gateway/routes.go:7` — `/health` 注册在 internal Server
- `pkg/health/server.go:42-46` — `/health`, `/healthz`, `/ready`, `/readyz` 注册在 health Server

两个独立的 health 端点系统, 需要确认是否有冲突。

### ARCH-R2-006 [低] provider 包 protocoltypes 重复路径

- `internal/core/provider/protocoltypes/types.go`
- `pkg/providers/protocoltypes/` (如果存在)

需要确认是否有路径名混淆。

---

## 七、测试覆盖

### TEST-R2-001 [中等] 7 个包缺少测试文件

| 包 | 文件数 |
|------|--------|
| `internal/core/events` | 1 |
| `internal/core/ports` | 4 |
| `internal/core/provider` | 1 |
| `internal/core/provider/protocoltypes` | 1 |
| `internal/core/session` | 1 |
| `pkg/auditlog` | 1 |
| `cmd/x-claw/internal/cliutil` | 1+ |

ports 包主要是接口定义, 可以不需要测试。但 `pkg/auditlog` 包含实际逻辑 (HMAC 签名、日志轮转), 应有测试。

### TEST-R2-002 [中等] auditlog 包无测试

`pkg/auditlog/auditlog.go` (287 行) 包含:
- HMAC 签名验证 (`VerifyHMACSignature`)
- 日志轮转 (`rotateLocked`)
- 文件追加写入

这些都是关键安全功能, 缺少测试。

### TEST-R2-003 [中等] 集成测试依赖外部 CLI

17 处 `t.Skip()`, 其中 11 处因缺少 codex/claude CLI 而跳过。这些应标记为集成测试并在 CI 中有条件执行。

### TEST-R2-004 [低] 部分测试文件过大

- `pkg/channels/manager_test.go` — 1910 行
- `pkg/agent/loop_test.go` — 1900 行
- `pkg/tools/toolcall_executor_test.go` — 1479 行

### TEST-R2-005 [低] 核心 domain types 无测试

`internal/core/session/types.go` 定义了 Session 等核心类型, 但没有对其方法 (如果有) 的测试。

---

## 八、Round 1 遗留确认

检查 Round 1 PROGRESS.md 中声称已修复的项目:

| 项目 | 状态 | 验证 |
|------|------|------|
| SchedulePlaceholder ctx 修复 | OK | `manager_placeholder.go:74` 使用了父 ctx |
| io.ReadAll 错误处理 (oauth) | OK | `oauth.go:215` 正确检查 err |
| Session GC | OK | SessionManager 有 maxSessions/ttl |
| DB close on error | OK | `memory_fts.go:205,209` 正确 close |
| shell.go fmt.Printf 修复 | OK | shell.go 已使用 logger |
| config.go 拆分 | OK | 6 个域文件 |
| memory.go 拆分 | OK | vector/embedder/fts/tools |
| loop.go 拆分 | OK | commands/compaction/token_usage/model_downgrade |

**结论**: Round 1 已完成项目验证通过。Round 2 发现的是新的或 Round 1 遗漏的问题。

---

## 九、优先级评分卡

### 当前质量评分: 7.1/10 (Round 1 前 6.3)

| 维度 | 评分 | 说明 |
|------|------|------|
| 正确性 | 7 | 错误处理仍有 ~20 处静默吞错 |
| 安全性 | 7 | 错误泄露 + header 不完整 |
| 性能 | 8 | HTTP client 复用 + 正则缓存可优化 |
| 可维护性 | 7 | 大文件仍存在, 死代码需清理 |
| 测试 | 6.5 | auditlog 等关键包缺测试 |
| 架构 | 7.5 | pkg/internal 边界可改善 |
