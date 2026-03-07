# X-Claw Round 2 技术实现文档

> 版本: v1.0
> 依据: [Round 2 需求文档](2026-03-07-round2-requirements.md)
> 前提: Round 1 全部完成 (见 PROGRESS.md)

---

## Phase A: P0 Bug + 安全修复

### Task A.1: 消除 pkg/ 层 fmt.Printf (REQ-R2-BUG-001)

**文件**: `pkg/agent/instance.go`, `pkg/tools/web_search.go`, `cmd/x-claw/internal/gateway/httpapi.go`

**变更 1** — `pkg/agent/instance.go:304`:
```go
// Before:
fmt.Printf("Warning: invalid path pattern %q: %v\n", p, err)

// After:
logger.WarnCF("agent", "Invalid path pattern", map[string]any{
    "pattern": p,
    "error":   err.Error(),
})
```
确认 import 中已有 `logger` 包。

**变更 2** — `pkg/tools/web_search.go:207`:
```go
// Before:
fmt.Printf("Brave API Error Body: %s\n", string(body))

// After:
logger.DebugCF("tools/web_search", "Brave API error body", map[string]any{
    "body": utils.Truncate(string(body), 500),
})
```
注意: 使用 Debug 级别, 避免在正常 API 错误时污染日志; 截断 body 防止日志膨胀。

**变更 3** — `cmd/x-claw/internal/gateway/httpapi.go:114`:
```go
// Before:
fmt.Printf("Warning: failed to register %s: %v\n", reg.pattern, err)

// After:
logger.WarnCF("gateway", "Failed to register HTTP handler", map[string]any{
    "pattern": reg.pattern,
    "error":   err.Error(),
})
```

**验证**:
```bash
grep -rn 'fmt\.Print' pkg/agent/instance.go pkg/tools/web_search.go cmd/x-claw/internal/gateway/httpapi.go | grep -v _test.go
# 期望: 无输出
go build ./pkg/agent/... ./pkg/tools/... ./cmd/x-claw/...
```

---

### Task A.2: 修复 antigravity io.ReadAll (REQ-R2-BUG-002)

**文件**: `pkg/providers/antigravity_provider.go`

**变更** — 行 643 和 684 (两处相同模式):
```go
// Before (line ~643):
body, _ := io.ReadAll(resp.Body)

// After:
body, err := io.ReadAll(resp.Body)
if err != nil {
    return "", fmt.Errorf("read response body: %w", err)
}
```

```go
// Before (line ~684):
body, _ := io.ReadAll(resp.Body)

// After:
body, err := io.ReadAll(resp.Body)
if err != nil {
    return nil, fmt.Errorf("read response body: %w", err)
}
```

注意两个函数返回值类型不同: `FetchAntigravityProjectID` 返回 `(string, error)`, `FetchAntigravityModels` 返回 `([]AntigravityModelInfo, error)`。

**验证**:
```bash
grep 'body, _ := io.ReadAll' pkg/providers/antigravity_provider.go
# 期望: 无输出
go test ./pkg/providers/... -count=1 -run TestAntigravity
```

---

### Task A.3: 修复 Feishu reaction undo context (REQ-R2-BUG-003)

**文件**: `pkg/channels/feishu/feishu_64.go`

当前代码 (~行 270-283):
```go
func (c *FeishuChannel) ReactToMessage(ctx context.Context, chatID, messageID string) (func(), error) {
    // ... 创建 reaction ...
    var undone atomic.Bool
    undo := func() {
        if !undone.CompareAndSwap(false, true) {
            return
        }
        delReq := larkim.NewDeleteMessageReactionReqBuilder()...
        _, _ = c.client.Im.V1.MessageReaction.Delete(context.Background(), delReq)
    }
    return undo, nil
}
```

**变更**: undo 闭包内使用带超时的独立 context (不用父 ctx, 因为 undo 可能在父 ctx 已取消后调用):
```go
undo := func() {
    if !undone.CompareAndSwap(false, true) {
        return
    }
    delCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    delReq := larkim.NewDeleteMessageReactionReqBuilder().
        MessageId(messageID).
        ReactionId(reactionID).
        Build()
    if _, err := c.client.Im.V1.MessageReaction.Delete(delCtx, delReq); err != nil {
        logger.DebugCF("feishu", "Failed to undo reaction", map[string]any{
            "message_id":  messageID,
            "reaction_id": reactionID,
            "error":       err.Error(),
        })
    }
}
```

**说明**: undo 回调的调用时机是在消息发送完成后, 此时原始 ctx 可能已过期。使用 `context.WithTimeout(context.Background(), 5s)` 是正确做法, 同时记录错误而非静默忽略。

**验证**:
```bash
go build ./pkg/channels/feishu/...
go test ./pkg/channels/... -count=1
```

---

### Task A.4: HTTP 错误响应不泄露内部信息 (REQ-R2-SEC-001)

**文件**: `internal/gateway/handlers_notify.go`, `internal/gateway/handlers_resume.go`

**变更 1** — `handlers_notify.go:145-148`:
```go
// Before:
if err := h.sender.SendToChannel(sendCtx, channel, to, content); err != nil {
    w.WriteHeader(http.StatusInternalServerError)
    _ = json.NewEncoder(w).Encode(notifyResponse{OK: false, Channel: channel, To: to, Error: err.Error()})
    return
}

// After:
if err := h.sender.SendToChannel(sendCtx, channel, to, content); err != nil {
    logger.WarnCF("gateway.notify", "Send failed", map[string]any{
        "channel": channel,
        "to":      to,
        "error":   err.Error(),
    })
    w.WriteHeader(http.StatusInternalServerError)
    errMsg := "send failed"
    if isLoopbackRemote(r.RemoteAddr) {
        errMsg = err.Error()
    }
    _ = json.NewEncoder(w).Encode(notifyResponse{OK: false, Channel: channel, To: to, Error: errMsg})
    return
}
```

**变更 2** — `handlers_resume.go:94-97`:
```go
// Before:
if res.err != nil {
    _ = json.NewEncoder(w).Encode(resumeLastTaskResponse{OK: false, Error: res.err.Error(), Candidate: res.candidate})

// After:
if res.err != nil {
    logger.WarnCF("gateway.resume", "Resume failed", map[string]any{
        "error": res.err.Error(),
    })
    errMsg := "resume failed"
    if isLoopbackRemote(r.RemoteAddr) {
        errMsg = res.err.Error()
    }
    _ = json.NewEncoder(w).Encode(resumeLastTaskResponse{OK: false, Error: errMsg, Candidate: res.candidate})
```

需要确保 `handlers_resume.go` 可以调用 `isLoopbackRemote`(同包内已有定义在 `handlers_notify.go`)。

**验证**:
```bash
go build ./internal/gateway/...
go test ./internal/gateway/... -count=1
```

---

## Phase B: P1 Bug 修复

### Task B.1: 统一 os.MkdirAll 错误处理 (REQ-R2-BUG-004)

**文件**: `pkg/agent/instance.go`, `pkg/session/manager.go`

**变更 1** — `instance.go:75`:
```go
// Before:
os.MkdirAll(workspace, 0o755)

// After:
if err := os.MkdirAll(workspace, 0o755); err != nil {
    logger.WarnCF("agent", "Failed to create workspace directory", map[string]any{
        "path":  workspace,
        "error": err.Error(),
    })
}
```

注: 不改为 return error, 因为 `NewAgentInstance` 的签名为 `*AgentInstance` (无 error), 修改签名影响面太大。使用 warn 日志是合理折中。

**变更 2** — `session/manager.go:61`:
```go
// Before:
os.MkdirAll(storage, 0o755)

// After:
if err := os.MkdirAll(storage, 0o755); err != nil {
    logger.WarnCF("session", "Failed to create storage directory", map[string]any{
        "path":  storage,
        "error": err.Error(),
    })
}
```

**验证**:
```bash
grep -n 'os.MkdirAll' pkg/agent/instance.go pkg/session/manager.go
# 确认所有调用都有错误处理
go build ./pkg/agent/... ./pkg/session/...
```

---

### Task B.2: 删除死函数 (REQ-R2-BUG-005)

**文件**: `pkg/agent/run_pipeline_impl.go`

**变更**: 删除行 257-260:
```go
// DELETE:
func (al *AgentLoop) ensureActiveAgentForSession(sessionKey string, agent *AgentInstance) {
    _ = sessionKey
    _ = agent
}
```

然后搜索并删除所有调用点:
```bash
grep -rn 'ensureActiveAgentForSession' pkg/
```

**验证**:
```bash
grep -rn 'ensureActiveAgentForSession' pkg/
# 期望: 无输出
go build ./pkg/agent/...
```

---

### Task B.3: compaction save 错误记录日志 (REQ-R2-BUG-006)

**文件**: `pkg/agent/loop_compaction.go`

**变更** — 3 处 (行 331, 413, 473):
```go
// Before:
_ = agent.Sessions.Save(sessionKey)

// After:
if err := agent.Sessions.Save(sessionKey); err != nil {
    logger.WarnCF("agent.compaction", "Failed to save session after compaction", map[string]any{
        "session_key": sessionKey,
        "error":       err.Error(),
    })
}
```

同时, 行 197:
```go
// Before:
finalSummary, _ = al.summarizeBatch(ctx, agent, validMessages, summary)

// After:
var batchErr error
finalSummary, batchErr = al.summarizeBatch(ctx, agent, validMessages, summary)
if batchErr != nil {
    logger.WarnCF("agent.compaction", "Summarize batch failed", map[string]any{
        "session_key": sessionKey,
        "error":       batchErr.Error(),
    })
}
```

**验证**:
```bash
grep '_ = agent.Sessions.Save\|_ = al.summarizeBatch' pkg/agent/loop_compaction.go
# 期望: 无输出
go build ./pkg/agent/...
```

---

### Task B.4: session meta 持久化错误记录 (REQ-R2-BUG-007)

**文件**: `pkg/session/manager_mutations.go`, `pkg/session/tree.go`, `pkg/session/manager.go`

**变更模式** — 所有 `_ = writeMetaFile(...)`:
```go
// Before:
_ = writeMetaFile(path, buildSessionMeta(session))

// After:
if err := writeMetaFile(path, buildSessionMeta(session)); err != nil {
    logger.WarnCF("session", "Failed to persist session meta", map[string]any{
        "key":   key,
        "path":  path,
        "error": err.Error(),
    })
}
```

对 `_ = applyEvents(...)` (manager.go:658):
```go
// Before:
_ = applyEvents(sess, sf.jsonl, sess.LastEventID)

// After:
if err := applyEvents(sess, sf.jsonl, sess.LastEventID); err != nil {
    logger.WarnCF("session", "Failed to apply JSONL events", map[string]any{
        "key":   sess.Key,
        "error": err.Error(),
    })
}
```

对 `_ = sm.migrateLegacyToJSONL(sess)` (manager.go:683):
```go
if err := sm.migrateLegacyToJSONL(sess); err != nil {
    logger.WarnCF("session", "Legacy migration failed", map[string]any{
        "key":   sess.Key,
        "error": err.Error(),
    })
}
```

**验证**:
```bash
grep '_ = writeMetaFile\|_ = applyEvents\|_ = sm.migrateLegacyToJSONL' pkg/session/
# 期望: 无输出
go test ./pkg/session/... -count=1
```

---

## Phase C: P1 性能 + 安全 + 测试

### Task C.1: 复用 antigravity HTTP Client (REQ-R2-PERF-001)

**文件**: `pkg/providers/antigravity_provider.go`

**变更**: 在包级别定义 shared client:
```go
var antigravityUtilClient = &http.Client{Timeout: 15 * time.Second}
```

然后在 `FetchAntigravityProjectID` (行 636) 和 `FetchAntigravityModels` (行 677):
```go
// Before:
client := &http.Client{Timeout: 15 * time.Second}

// After:
client := antigravityUtilClient
```

**验证**:
```bash
grep '&http.Client{Timeout: 15' pkg/providers/antigravity_provider.go
# 期望: 无输出
go build ./pkg/providers/...
```

---

### Task C.2: 缓存 Telegram bot username 正则 (REQ-R2-PERF-002)

**文件**: `pkg/channels/telegram/telegram.go`

需要先查看 TelegramChannel struct 和 botUsername 的获取逻辑。

**变更思路**: 在 struct 中添加缓存字段:
```go
type TelegramChannel struct {
    // ...existing fields...
    mentionRe     *regexp.Regexp  // 缓存 @botUsername 正则
    mentionReOnce sync.Once
}
```

在行 782 附近:
```go
// Before:
re := regexp.MustCompile(`(?i)@` + regexp.QuoteMeta(botUsername))

// After:
c.mentionReOnce.Do(func() {
    c.mentionRe = regexp.MustCompile(`(?i)@` + regexp.QuoteMeta(botUsername))
})
re := c.mentionRe
```

或者更简单: 如果 botUsername 在 channel 生命周期内不变, 在初始化时直接编译:
```go
// 在 Start() 中, 获取 botUsername 后:
c.mentionRe = regexp.MustCompile(`(?i)@` + regexp.QuoteMeta(botUsername))
```

需要阅读获取 botUsername 的时机来决定方案。

**验证**:
```bash
go build ./pkg/channels/telegram/...
go test ./pkg/channels/... -count=1
```

---

### Task C.3: HTTP 安全头部补全 (REQ-R2-SEC-002)

**文件**: `pkg/channels/manager.go`

阅读现有 `withSecurityHeaders`:
```go
func withSecurityHeaders(next http.Handler) http.Handler {
    // ...existing code...
}
```

**变更**: 确保包含:
```go
w.Header().Set("X-Frame-Options", "DENY")
w.Header().Set("X-Content-Type-Options", "nosniff")
```

如果已有部分头部, 只补全缺失的。

**验证**:
```bash
go build ./pkg/channels/...
go test ./pkg/channels/... -count=1
```

---

### Task C.4: auditlog 包测试 (REQ-R2-TEST-001)

**文件**: 新建 `pkg/auditlog/auditlog_test.go`

**测试用例**:

```go
func TestRecord_AppendsToFile(t *testing.T) {
    // 创建临时目录, 写入一条审计事件, 验证文件内容
}

func TestVerifyHMACSignature(t *testing.T) {
    // 正常签名验证
    // 篡改后验证失败
    // 空 key 返回错误
}

func TestRotation(t *testing.T) {
    // 设置小的 maxBytes, 写入多条, 验证旧文件被轮转
}

func TestConcurrentWrites(t *testing.T) {
    // 多 goroutine 并发 Record, 验证无 data race
    // 使用 -race flag
}
```

**验证**:
```bash
go test -race ./pkg/auditlog/... -count=1
```

---

## Phase D: P2 清理

### Task D.1: auditlog 写入错误处理 (REQ-R2-BUG-008)

**文件**: `pkg/auditlog/auditlog.go`

**变更** — 行 157-158:
```go
// Before:
_, _ = f.Write(line)
_ = f.Close()

// After:
if _, err := f.Write(line); err != nil {
    fmt.Fprintf(os.Stderr, "auditlog: write error: %v\n", err)
}
if err := f.Close(); err != nil {
    fmt.Fprintf(os.Stderr, "auditlog: close error: %v\n", err)
}
```

注: 这里故意使用 `fmt.Fprintf(os.Stderr, ...)` 而非 logger, 避免审计日志→日志→审计日志的递归。

**验证**:
```bash
go build ./pkg/auditlog/...
go test ./pkg/auditlog/... -count=1
```

---

### Task D.2: 清理死参数 (REQ-R2-BUG-009)

**文件**: 多个文件

**变更清单**:

1. `pkg/agent/memory.go:287` — 检查方法是否实现接口。如果是, 保留签名但删除 `_ = ctx`:
```go
// 方法签名保持: func (ms *MemoryStore) SomeMethod(ctx context.Context, ...) {
// 删除: _ = ctx
```

2. `pkg/agent/memory_embedder.go:260,286-287` — 同上, 检查是否实现 `memoryVectorEmbedder` 接口

3. `pkg/agent/loop_commands.go:81` — 检查函数签名

4. `pkg/tools/filesystem.go:1047` — 检查函数签名

**验证**:
```bash
go build ./...
go vet ./...
```

---

### Task D.3: API Key 常量时间比较 (REQ-R2-SEC-003)

**文件**: `internal/gateway/handlers_notify.go`

**变更** — 行 160 和 166:
```go
// 在 import 中添加:
"crypto/subtle"

// Before (line 160):
if strings.TrimSpace(r.Header.Get("X-API-Key")) == apiKey {

// After:
if subtle.ConstantTimeCompare([]byte(strings.TrimSpace(r.Header.Get("X-API-Key"))), []byte(apiKey)) == 1 {

// Before (line 166):
return token != "" && token == apiKey

// After:
return token != "" && subtle.ConstantTimeCompare([]byte(token), []byte(apiKey)) == 1
```

**验证**:
```bash
grep '== apiKey' internal/gateway/handlers_notify.go
# 期望: 无输出
go test ./internal/gateway/... -count=1
```

---

### Task D.4: media.go HTTP Client 复用 (REQ-R2-PERF-003)

**文件**: `pkg/utils/media.go`

**变更** — 行 96 附近:

方案: 让 `DownloadMediaFile` 接受可选的 `*http.Client`, 未提供时使用默认:
```go
// 包级别:
var defaultMediaClient = &http.Client{Timeout: 30 * time.Second}

// 函数内:
// Before:
client := &http.Client{Timeout: opts.Timeout}

// After:
client := defaultMediaClient
if opts.Timeout > 0 && opts.Timeout != 30*time.Second {
    client = &http.Client{Timeout: opts.Timeout}
}
```

需要检查 `opts.Timeout` 的使用方式和调用方。

**验证**:
```bash
go build ./pkg/utils/...
go test ./pkg/utils/... -count=1
```

---

### Task D.5: 抽取重复 isLoopback (REQ-R2-QUAL-001)

**文件**: `pkg/utils/net.go` (新建), `internal/gateway/handlers_notify.go`, `pkg/config/validation.go`

**变更**:

1. 新建 `pkg/utils/net.go`:
```go
package utils

import "net"

// IsLoopbackAddr checks whether the given address (host or host:port) is a loopback address.
func IsLoopbackAddr(addr string) bool {
    host := strings.TrimSpace(addr)
    if host == "" {
        return false
    }
    if h, _, err := net.SplitHostPort(host); err == nil {
        host = h
    }
    if strings.EqualFold(host, "localhost") {
        return true
    }
    ip := net.ParseIP(host)
    return ip != nil && ip.IsLoopback()
}
```

2. 修改 `internal/gateway/handlers_notify.go` — 将 `isLoopbackRemote` 改为调用 `utils.IsLoopbackAddr`
3. 修改 `pkg/config/validation.go` — 将 `isLoopbackHost` 改为调用 `utils.IsLoopbackAddr`

注意: `isLoopbackHost` 不处理 host:port (只传 host), 而 `isLoopbackRemote` 处理 host:port。统一函数需要兼顾两种输入。上面的实现已兼顾。

**验证**:
```bash
go build ./...
go test ./internal/gateway/... ./pkg/config/... -count=1
```

---

### Task D.6: archcheck 扩展 (REQ-R2-TEST-002)

**文件**: `internal/archcheck/archcheck_test.go`

**新增测试**:

```go
func TestToolsDoesNotImportChannels(t *testing.T) {
    root := findRepoRoot(t)
    toolsDir := filepath.Join(root, "pkg", "tools")
    importsByFile := scanImports(t, toolsDir)
    banned := map[string]bool{
        "github.com/xwysyy/X-Claw/pkg/channels": true,
    }
    var violations []string
    for file, imports := range importsByFile {
        for _, imp := range imports {
            if banned[imp] || strings.HasPrefix(imp, "github.com/xwysyy/X-Claw/pkg/channels/") {
                rel, _ := filepath.Rel(root, file)
                violations = append(violations, rel+": "+imp)
            }
        }
    }
    if len(violations) > 0 {
        t.Fatalf("architecture violation: pkg/tools imports channels:\n%s", strings.Join(violations, "\n"))
    }
}

func TestConfigDoesNotImportAgentOrSession(t *testing.T) {
    root := findRepoRoot(t)
    configDir := filepath.Join(root, "pkg", "config")
    importsByFile := scanImports(t, configDir)
    banned := []string{
        "github.com/xwysyy/X-Claw/pkg/agent",
        "github.com/xwysyy/X-Claw/pkg/session",
    }
    var violations []string
    for file, imports := range importsByFile {
        for _, imp := range imports {
            for _, b := range banned {
                if imp == b || strings.HasPrefix(imp, b+"/") {
                    rel, _ := filepath.Rel(root, file)
                    violations = append(violations, rel+": "+imp)
                }
            }
        }
    }
    if len(violations) > 0 {
        t.Fatalf("architecture violation: pkg/config imports agent/session:\n%s", strings.Join(violations, "\n"))
    }
}
```

**验证**:
```bash
go test ./internal/archcheck/... -count=1 -v
```

---

## 并行执行策略

```
Round 1:  [Task A.1] [Task A.2] [Task A.3] [Task A.4]   — 4 个文件无冲突, 全并行
          [Task B.1] [Task B.2]                           — 与 A 无文件冲突, 可同轮

Round 2:  [Task B.3] [Task B.4]                           — session/compaction 文件
          [Task C.1] [Task C.2] [Task C.3]                — provider/telegram/channels

Round 3:  [Task C.4] [Task D.1] [Task D.6]               — 新文件创建, 无冲突
          [Task D.2] [Task D.3] [Task D.4] [Task D.5]    — 清理任务, 分散文件

Gate: go build ./... && go vet ./... && go test ./pkg/... ./internal/... ./cmd/... -count=1 -p 1
```

---

## 文件变更总结

| 文件 | Task | 变更类型 |
|------|------|----------|
| `pkg/agent/instance.go` | A.1, B.1 | 修改 |
| `pkg/tools/web_search.go` | A.1 | 修改 |
| `cmd/x-claw/internal/gateway/httpapi.go` | A.1 | 修改 |
| `pkg/providers/antigravity_provider.go` | A.2, C.1 | 修改 |
| `pkg/channels/feishu/feishu_64.go` | A.3 | 修改 |
| `internal/gateway/handlers_notify.go` | A.4, D.3, D.5 | 修改 |
| `internal/gateway/handlers_resume.go` | A.4 | 修改 |
| `pkg/session/manager.go` | B.1, B.4 | 修改 |
| `pkg/agent/run_pipeline_impl.go` | B.2 | 修改 |
| `pkg/agent/loop_compaction.go` | B.3 | 修改 |
| `pkg/session/manager_mutations.go` | B.4 | 修改 |
| `pkg/session/tree.go` | B.4 | 修改 |
| `pkg/channels/telegram/telegram.go` | C.2 | 修改 |
| `pkg/channels/manager.go` | C.3 | 修改 |
| `pkg/auditlog/auditlog.go` | D.1 | 修改 |
| `pkg/auditlog/auditlog_test.go` | C.4 | 新建 |
| `pkg/agent/memory.go` | D.2 | 修改 |
| `pkg/agent/memory_embedder.go` | D.2 | 修改 |
| `pkg/agent/loop_commands.go` | D.2 | 修改 |
| `pkg/tools/filesystem.go` | D.2 | 修改 |
| `pkg/utils/media.go` | D.4 | 修改 |
| `pkg/utils/net.go` | D.5 | 新建 |
| `pkg/config/validation.go` | D.5 | 修改 |
| `internal/archcheck/archcheck_test.go` | D.6 | 修改 |
