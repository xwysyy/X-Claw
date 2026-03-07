# X-Claw Round 2 重构需求文档

> 版本: v1.0
> 依据: [Round 2 审查报告](2026-03-07-round2-audit.md)
> 约束: 保持全部现有功能和 API 不变

---

## 1. Bug 修复需求

### REQ-R2-BUG-001: 消除 pkg/ 层 fmt.Printf 残留
- **来源**: BUG-R2-001
- **优先级**: P0
- **范围**: `pkg/agent/instance.go:304`, `pkg/tools/web_search.go:207`, `cmd/x-claw/internal/gateway/httpapi.go:114`
- **要求**: 将所有 `fmt.Printf` 替换为 `logger.WarnCF` 或 `logger.DebugCF`
- **验证**: `grep -rn 'fmt\.Print' pkg/ cmd/x-claw/internal/gateway/httpapi.go | grep -v '_test.go' | grep -v '/agent/helpers.go' | grep -v '/version/' | grep -v '/gateway/helpers.go'` 无输出
- **说明**: `cmd/x-claw/internal/agent/helpers.go` 和 `cmd/x-claw/internal/gateway/helpers.go` 中的 `fmt.Print` 属于 CLI 交互输出, 保留不动

### REQ-R2-BUG-002: 修复 antigravity provider io.ReadAll 错误忽略
- **来源**: BUG-R2-002
- **优先级**: P0
- **范围**: `pkg/providers/antigravity_provider.go:643,684`
- **要求**: `body, err := io.ReadAll(resp.Body)` 并在 err != nil 时返回包装后的错误
- **验证**: `grep 'body, _ := io.ReadAll' pkg/providers/antigravity_provider.go` 无输出

### REQ-R2-BUG-003: 修复 Feishu reaction undo context
- **来源**: BUG-R2-003
- **优先级**: P0
- **范围**: `pkg/channels/feishu/feishu_64.go:282`
- **要求**: 将 `context.Background()` 替换为从 undo 闭包捕获的带超时父 context
- **验证**: undo 回调使用 `context.WithTimeout(parentCtx, 5*time.Second)`

### REQ-R2-BUG-004: 统一 os.MkdirAll 错误处理
- **来源**: BUG-R2-004
- **优先级**: P1
- **范围**: `pkg/agent/instance.go:75`, `pkg/session/manager.go:61`
- **要求**:
  - `instance.go:75` — 改为 `if err := os.MkdirAll(...); err != nil { return nil, err }` 或 logger.Warn
  - `session/manager.go:61` — 同上
- **验证**: `grep -n 'os.MkdirAll' pkg/agent/instance.go pkg/session/manager.go` 显示所有调用都有错误处理

### REQ-R2-BUG-005: 删除死函数 ensureActiveAgentForSession
- **来源**: BUG-R2-005
- **优先级**: P1
- **范围**: `pkg/agent/run_pipeline_impl.go:257-260`
- **要求**: 删除 `ensureActiveAgentForSession` 函数及所有调用点
- **验证**: `grep -rn 'ensureActiveAgentForSession' pkg/` 无输出; `go build ./...` 通过

### REQ-R2-BUG-006: compaction save 错误记录日志
- **来源**: BUG-R2-006
- **优先级**: P1
- **范围**: `pkg/agent/loop_compaction.go:331,413,473`
- **要求**: `_ = agent.Sessions.Save(sessionKey)` 改为 `if err := ...; err != nil { logger.WarnCF(...) }`
- **验证**: `grep '_ = agent.Sessions.Save' pkg/agent/loop_compaction.go` 无输出

### REQ-R2-BUG-007: session meta 持久化错误记录日志
- **来源**: BUG-R2-007
- **优先级**: P1
- **范围**: `pkg/session/manager_mutations.go:179`, `pkg/session/tree.go:169`, `pkg/session/manager.go:658,683,754`
- **要求**: `_ = writeMetaFile(...)` 和 `_ = applyEvents(...)` 改为记录 warn 日志
- **验证**: `grep '_ = writeMetaFile\|_ = applyEvents\|_ = sm.migrateLegacyToJSONL' pkg/session/` 无输出

### REQ-R2-BUG-008: auditlog 写入错误处理
- **来源**: BUG-R2-010
- **优先级**: P2
- **范围**: `pkg/auditlog/auditlog.go:157-158`
- **要求**: `f.Write` 和 `f.Close` 错误时记录到 stderr (不能用 logger, 可能造成递归)
- **验证**: 无 `_, _ = f.Write(line)` 和 `_ = f.Close()` (在非 test 文件中)

### REQ-R2-BUG-009: 清理死参数
- **来源**: QUAL-R2-002
- **优先级**: P2
- **范围**:
  - `pkg/agent/memory.go:287` — `_ = ctx`
  - `pkg/agent/memory_embedder.go:260,286-287` — `_ = ctx; _ = inputs`
  - `pkg/agent/loop_commands.go:81` — `_ = agent`
  - `pkg/tools/filesystem.go:1047` — `_ = ctx`
- **要求**: 如果参数确实未使用, 删除 `_ = xxx` 并在函数签名中使用 `_` 占位; 如果函数实现了接口则保留签名但删除 `_ =` 行
- **验证**: `go build ./...` 通过

---

## 2. 安全加固需求

### REQ-R2-SEC-001: HTTP 错误响应不泄露内部信息
- **来源**: SEC-R2-001
- **优先级**: P0
- **范围**: `internal/gateway/handlers_notify.go:147`, `internal/gateway/handlers_resume.go:96`
- **要求**:
  - 对外返回通用错误消息 (如 "internal error"), 详细错误记录到日志
  - 仅 loopback 请求可返回详细错误
- **验证**: 非 loopback 请求的错误响应不包含文件路径、数据库错误、堆栈跟踪等

### REQ-R2-SEC-002: HTTP 安全头部补全
- **来源**: SEC-R2-002
- **优先级**: P1
- **范围**: `pkg/channels/manager.go` `withSecurityHeaders` 中间件
- **要求**: 添加 `X-Frame-Options: DENY`, `X-Content-Type-Options: nosniff` (如缺失)
- **验证**: console 端点响应包含安全头部

### REQ-R2-SEC-003: API Key 常量时间比较
- **来源**: SEC-R2-004
- **优先级**: P2
- **范围**: `internal/gateway/handlers_notify.go:160,166`
- **要求**: 使用 `crypto/subtle.ConstantTimeCompare` 替代 `==` 比较 API key
- **验证**: `grep -rn '== apiKey' internal/gateway/` 无输出

---

## 3. 性能优化需求

### REQ-R2-PERF-001: 复用 antigravity HTTP Client
- **来源**: PERF-R2-001
- **优先级**: P1
- **范围**: `pkg/providers/antigravity_provider.go:636,677`
- **要求**: 使用包级别或结构体级别的 `http.Client` 替代函数内创建
- **验证**: `FetchAntigravityProjectID` 和 `FetchAntigravityModels` 不再创建 `&http.Client{}`

### REQ-R2-PERF-002: 缓存 Telegram bot username 正则
- **来源**: PERF-R2-002
- **优先级**: P1
- **范围**: `pkg/channels/telegram/telegram.go:782`
- **要求**: 在 channel 初始化时编译一次正则, 缓存到结构体字段
- **验证**: `782` 行附近不再有 `regexp.MustCompile`

### REQ-R2-PERF-003: media.go HTTP Client 复用
- **来源**: PERF-R2-001
- **优先级**: P2
- **范围**: `pkg/utils/media.go:96`
- **要求**: 使用共享 `http.Client` 或接受 `*http.Client` 参数
- **验证**: `DownloadMediaFile` 不再创建 `&http.Client{}`

---

## 4. 代码质量提升需求

### REQ-R2-QUAL-001: 抽取重复的 isLoopback 函数
- **来源**: QUAL-R2-006
- **优先级**: P2
- **范围**: `internal/gateway/handlers_notify.go:171`, `pkg/config/validation.go:12`
- **要求**: 统一为 `pkg/utils/net.go` (或类似位置) 中一个公共函数, 两处改为调用它
- **验证**: `isLoopbackRemote` 和 `isLoopbackHost` 只在一处定义

### REQ-R2-QUAL-002: 清理 Telegram 正则编译
- **来源**: 同 REQ-R2-PERF-002, 归属代码质量
- **优先级**: P1
- **范围**: `pkg/channels/telegram/telegram.go:782`
- **要求**: 缓存到结构体字段

---

## 5. 测试补充需求

### REQ-R2-TEST-001: auditlog 包测试
- **来源**: TEST-R2-002
- **优先级**: P1
- **范围**: `pkg/auditlog/auditlog.go`
- **要求**: 覆盖以下场景:
  - Record + 文件追加写入
  - HMAC 签名生成与验证
  - 日志轮转 (超过 maxBytes)
  - 并发写入安全
- **验证**: `go test ./pkg/auditlog/... -count=1` 通过

### REQ-R2-TEST-002: archcheck 扩展
- **来源**: ARCH-R2-004
- **优先级**: P2
- **范围**: `internal/archcheck/archcheck_test.go`
- **要求**: 添加以下守护测试:
  - `TestToolsDoesNotImportChannels` — pkg/tools 不应导入 pkg/channels
  - `TestConfigDoesNotImportAgentOrSession` — pkg/config 不应导入 pkg/agent, pkg/session
- **验证**: `go test ./internal/archcheck/... -count=1` 通过

---

## 6. 优先级与阶段划分

| 阶段 | 需求 | 文件影响 | 估计复杂度 |
|------|------|----------|-----------|
| **Phase A** (P0 Bug+安全) | REQ-R2-BUG-001/002/003, REQ-R2-SEC-001 | 5 文件 | 低 |
| **Phase B** (P1 Bug) | REQ-R2-BUG-004/005/006/007 | 6 文件 | 低 |
| **Phase C** (P1 性能+质量) | REQ-R2-PERF-001/002, REQ-R2-SEC-002, REQ-R2-TEST-001 | 5 文件 | 中 |
| **Phase D** (P2 清理) | REQ-R2-BUG-008/009, REQ-R2-SEC-003, REQ-R2-PERF-003, REQ-R2-QUAL-001, REQ-R2-TEST-002 | 8 文件 | 低 |

**并行安全矩阵**:

Phase A 和 Phase B 无文件冲突, 可并行。
Phase C 和 Phase D 无文件冲突, 可并行。
Phase A/B 完成后再启动 Phase C/D (Phase C 的 auditlog 测试可能依赖 Phase B 的 bug 修复)。

---

## 7. 约束条件

1. **API 不变**: 所有 HTTP 端点路径、请求/响应格式不变
2. **行为不变**: 消息处理流程、session 持久化语义、tool 执行流程不变
3. **配置兼容**: 不新增必填配置项; 可新增可选配置项 (有合理默认值)
4. **构建通过**: 每个阶段完成后 `go build ./...` 和 `go vet ./...` 必须通过
5. **测试通过**: 每个阶段完成后 `go test ./pkg/... ./internal/... ./cmd/... -count=1` 中已有测试必须通过
