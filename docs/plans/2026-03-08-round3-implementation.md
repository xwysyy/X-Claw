# X-Claw Round 3 技术实现文档

> 版本: v1.0
> 依据: [Round 3 需求文档](2026-03-08-round3-requirements.md)
> 前提: Round 1 (22 任务) + Round 2 (18 任务) 已完成

---

## Phase A: P0 Bug 修复

### Task A.1: 修复 model_override.go writeMetaFile 错误吞没 (REQ-R3-BUG-001)

**文件**: `pkg/session/model_override.go`

**变更 1** — 行 83-85 (SetModelOverride):
```go
// Before:
if strings.TrimSpace(metaPath) != "" {
    _ = writeMetaFile(metaPath, meta)
}

// After:
if strings.TrimSpace(metaPath) != "" {
    if err := writeMetaFile(metaPath, meta); err != nil {
        logger.WarnCF("session", "Failed to persist model override meta", map[string]any{
            "key":   key,
            "error": err.Error(),
        })
    }
}
```

**变更 2** — 行 116-118 (ClearModelOverride):
```go
// Before:
if strings.TrimSpace(metaPath) != "" {
    _ = writeMetaFile(metaPath, meta)
}

// After:
if strings.TrimSpace(metaPath) != "" {
    if err := writeMetaFile(metaPath, meta); err != nil {
        logger.WarnCF("session", "Failed to persist model override clear", map[string]any{
            "key":   key,
            "error": err.Error(),
        })
    }
}
```

确认 import 中有 `logger` 包。如果没有，添加:
```go
"github.com/xwysyy/X-Claw/pkg/logger"
```

**验证**:
```bash
grep '_ = writeMetaFile' pkg/session/model_override.go
# 期望: 无输出
go build ./pkg/session/...
```

---

### Task A.2: 修复 EffectiveModelOverride TOCTOU 竞态 (REQ-R3-BUG-002)

**文件**: `pkg/session/model_override.go`

**变更** — 替换整个 `EffectiveModelOverride` 方法:
```go
func (sm *SessionManager) EffectiveModelOverride(key string) (string, bool) {
    key = utils.CanonicalSessionKey(key)
    if key == "" {
        return "", false
    }

    sm.mu.Lock()
    defer sm.mu.Unlock()

    sess, ok := sm.sessions[key]
    if !ok || sess == nil {
        return "", false
    }
    model := strings.TrimSpace(sess.ModelOverride)
    if model == "" {
        return "", false
    }

    expiresAtMS := sess.ModelOverrideExpiresAtMS
    if expiresAtMS != nil && *expiresAtMS > 0 && time.Now().UnixMilli() > *expiresAtMS {
        // 过期：在持有锁的情况下直接清理
        sess.ModelOverride = ""
        sess.ModelOverrideExpiresAtMS = nil
        sess.Updated = time.Now()

        if sm.storage != "" {
            metaPath := sm.metaPath(key)
            meta := buildSessionMeta(sess)
            // 释放锁后异步写 meta (写入不需要锁保护 session map)
            go func() {
                if err := writeMetaFile(metaPath, meta); err != nil {
                    logger.WarnCF("session", "Failed to persist expired model override clear", map[string]any{
                        "key":   key,
                        "error": err.Error(),
                    })
                }
            }()
        }
        return "", false
    }

    return model, true
}
```

**说明**:
- 改用写锁 (`Lock` 而非 `RLock`) 以原子地读取+判断+清理
- 避免释放锁后再调用 `ClearModelOverride` 的 TOCTOU 问题
- meta 文件写入放在 goroutine 中异步完成，避免在锁内做 I/O

**验证**:
```bash
go build ./pkg/session/...
go test -race ./pkg/session/... -count=3
```

---

### Task A.3: 修复 console_stream.go io.ReadAll 错误忽略 (REQ-R3-BUG-003)

**文件**: `pkg/httpapi/console_stream.go`

**变更** — 行 79:
```go
// Before:
buf, _ := io.ReadAll(f)

// After:
buf, err := io.ReadAll(f)
if err != nil {
    return
}
```

**验证**:
```bash
grep 'buf, _ := io.ReadAll' pkg/httpapi/console_stream.go
# 期望: 无输出
go build ./pkg/httpapi/...
```

---

## Phase B: P1 Bug 修复 + 测试

### Task B.1: 修复 events.go f.Sync 错误处理 (REQ-R3-BUG-005)

**文件**: `pkg/session/events.go`

**变更** — 行 57:
```go
// Before:
_ = f.Sync()
return nil

// After:
if err := f.Sync(); err != nil {
    return fmt.Errorf("sync: %w", err)
}
return nil
```

**验证**:
```bash
grep '_ = f.Sync' pkg/session/events.go
# 期望: 无输出
go build ./pkg/session/...
```

---

### Task B.2: antigravity 凭证保存错误记录 (REQ-R3-BUG-007)

**文件**: `pkg/providers/antigravity_provider.go`

**变更** — 行 612:
```go
// Before:
_ = auth.SetCredential("google-antigravity", cred)

// After:
if err := auth.SetCredential("google-antigravity", cred); err != nil {
    logger.WarnCF("antigravity", "Failed to save credential", map[string]any{
        "error": err.Error(),
    })
}
```

确认 `logger` 已导入。

**验证**:
```bash
grep '_ = auth.SetCredential' pkg/providers/antigravity_provider.go
# 期望: 无输出
go build ./pkg/providers/...
```

---

### Task B.3: task_ledger load 错误记录 (REQ-R3-BUG-008)

**文件**: `pkg/tools/task_ledger.go`

**变更** — 行 77:
```go
// Before:
_ = l.load()

// After:
if err := l.load(); err != nil {
    logger.WarnCF("tools/task", "Failed to load task ledger", map[string]any{
        "path":  l.path,
        "error": err.Error(),
    })
}
```

确认 `logger` 已导入，`l.path` 字段存在。如果不存在 path 字段，改用其他标识信息。

**验证**:
```bash
grep '_ = l.load' pkg/tools/task_ledger.go
# 期望: 无输出
go build ./pkg/tools/...
```

---

### Task B.4: model_override 并发测试 (REQ-R3-TEST-001)

**文件**: 新建 `pkg/session/model_override_test.go`

**测试用例**:
```go
package session

import (
    "sync"
    "testing"
    "time"
)

func TestModelOverrideConcurrent(t *testing.T) {
    sm := NewSessionManager("", 100, 1*time.Hour)

    var wg sync.WaitGroup
    for i := range 20 {
        wg.Add(1)
        go func(n int) {
            defer wg.Done()
            key := "test-session"
            model := "model-v" + fmt.Sprint(n)
            sm.SetModelOverride(key, model, 5*time.Minute)
            sm.EffectiveModelOverride(key)
        }(i)
    }
    wg.Wait()
}

func TestModelOverrideExpiry(t *testing.T) {
    sm := NewSessionManager("", 100, 1*time.Hour)
    key := "test-expiry"

    // 设置 10ms TTL 的 override
    sm.SetModelOverride(key, "fast-model", 10*time.Millisecond)

    // 立即检查应该存在
    model, ok := sm.EffectiveModelOverride(key)
    if !ok || model != "fast-model" {
        t.Fatalf("expected fast-model, got %q (ok=%v)", model, ok)
    }

    // 等待过期
    time.Sleep(20 * time.Millisecond)

    // 过期后应该为空
    model, ok = sm.EffectiveModelOverride(key)
    if ok {
        t.Fatalf("expected expired override, got %q", model)
    }
}

func TestModelOverrideClearIdempotent(t *testing.T) {
    sm := NewSessionManager("", 100, 1*time.Hour)
    key := "test-clear"

    // 不存在的 session 清除应该安全
    _, err := sm.ClearModelOverride(key)
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }

    // 设置后清除
    sm.SetModelOverride(key, "some-model", 0)
    _, err = sm.ClearModelOverride(key)
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }

    model, ok := sm.EffectiveModelOverride(key)
    if ok {
        t.Fatalf("expected cleared, got %q", model)
    }
}
```

**验证**:
```bash
go test -race ./pkg/session/... -count=3 -run TestModelOverride
```

---

## Phase C: P1 文件拆分

### Task C.1: 拆分 loop.go — bucket 和错误分类 (REQ-R3-SLIM-001)

**文件**: `pkg/agent/loop.go` → 新建 `loop_bucket.go`, `loop_errors.go`

**步骤**:

1. **创建 `loop_errors.go`**: 移动以下函数:
   - `isLLMTimeoutError` (~行 109)
   - `isContextWindowError` (~行 121)
   - 及其相关 import

2. **创建 `loop_bucket.go`**: 移动以下内容:
   - `bucket` 结构体定义
   - `newBucket`、`bucket` 的所有方法
   - `bucketManager` 及其方法 (如果有)
   - 及其相关 import

3. **确认拆分后 loop.go 的行数**: 目标 <2500 行

**注意**: 所有移动的类型和函数保持小写 (包内可见)，不改变导出状态。

**验证**:
```bash
go build ./pkg/agent/...
go test ./pkg/agent/... -count=1
wc -l pkg/agent/loop.go  # < 2500
```

---

### Task C.2: 拆分 web_search.go — 搜索引擎后端 (REQ-R3-SLIM-002)

**文件**: `pkg/tools/web_search.go` → 新建至少 2 个后端文件

**步骤**:

1. 阅读 `web_search.go` 识别各搜索引擎的函数边界
2. 创建以下文件:
   - `web_search_brave.go` — Brave API 相关函数
   - `web_search_ddg.go` — DuckDuckGo 相关函数
   - 如果 Bing/Serper 代码量足够，也拆分
3. `web_search.go` 保留:
   - 包级别常量和正则定义
   - 入口函数 (路由到各后端)
   - URL fetch 和 HTML 清洗 (如果量大可再拆 `web_fetch.go`)
   - 公共类型和接口

**注意**: 包级别 var/const (如正则) 如果被多个后端共用，保留在 `web_search.go`。

**验证**:
```bash
go build ./pkg/tools/...
go test ./pkg/tools/... -count=1 -run TestWeb
wc -l pkg/tools/web_search*.go  # 每个 <700 行
```

---

### Task C.3: 拆分 shell.go — 进程管理和安全 (REQ-R3-SLIM-003)

**文件**: `pkg/tools/shell.go` → 新建至少 2 个子文件

**步骤**:

1. 阅读 `shell.go` 识别模块边界
2. 创建以下文件:
   - `shell_session.go` — `processTable`, `sessionTable`, `shellSession` 及相关方法
   - `shell_safety.go` — `denyPatterns`, `isDangerousCommand`, `sanitizeCommand` 等安全函数
3. 如果输出格式化代码 (`marshalSilentJSON` 等) 量大，创建 `shell_output.go`
4. `shell.go` 保留:
   - `ShellTool` 结构体和 `Execute` 方法
   - 同步/异步命令执行核心逻辑

**验证**:
```bash
go build ./pkg/tools/...
go test ./pkg/tools/... -count=1 -run TestShell
wc -l pkg/tools/shell*.go  # 每个 <700 行
```

---

## Phase D: P2 文件拆分

### Task D.1: 拆分 feishu_64.go (REQ-R3-SLIM-004)

**文件**: `pkg/channels/feishu/feishu_64.go` → 新建子文件

**步骤**:

1. 阅读文件识别模块边界
2. 创建:
   - `feishu_format.go` — markdown/消息格式化函数
   - `feishu_media.go` — 媒体上传/下载函数
3. `feishu_64.go` 保留:
   - `FeishuChannel` 结构体和核心生命周期方法 (Start/Stop/Send)
   - 消息接收和路由

**验证**:
```bash
go build ./pkg/channels/feishu/...
go test ./pkg/channels/... -count=1
wc -l pkg/channels/feishu/feishu_64.go  # < 900
```

---

### Task D.2: 拆分 run_pipeline_impl.go (REQ-R3-SLIM-005)

**文件**: `pkg/agent/run_pipeline_impl.go` → 新建子文件

**步骤**:

1. 阅读文件识别可拆分的辅助函数
2. 创建:
   - `pipeline_notify.go` — 通知回调和进度报告函数
   - `pipeline_helpers.go` — 消息复制、会话快照等通用辅助函数
3. `run_pipeline_impl.go` 保留:
   - 核心 pipeline 执行逻辑

**验证**:
```bash
go build ./pkg/agent/...
go test ./pkg/agent/... -count=1
wc -l pkg/agent/run_pipeline_impl.go  # < 900
```

---

## Phase E: P2 清理和优化

### Task E.1: 清理 toolcall_hooks.go 死参数 (REQ-R3-BUG-006)

**文件**: `pkg/tools/toolcall_hooks.go`

**变更** — 行 226:
```go
// 删除这一行:
_ = call
```

函数签名 `AfterToolCall(_ context.Context, call providers.ToolCall, ...)` 中 `call` 已经在函数体第一行 `_ = call` 后被忽略。但由于签名是接口实现，参数名不能省略。只需删除函数体内的 `_ = call` 行。

**验证**:
```bash
go build ./pkg/tools/...
```

---

### Task E.2: 抽取 JSON 响应 helper (REQ-R3-SLIM-006)

**文件**: `internal/gateway/handlers_notify.go`, `internal/gateway/handlers_resume.go`, 新建 `internal/gateway/response.go`

**新建 `response.go`**:
```go
package gateway

import (
    "encoding/json"
    "net/http"
)

func writeJSON(w http.ResponseWriter, status int, v any) {
    w.WriteHeader(status)
    _ = json.NewEncoder(w).Encode(v)
}
```

**然后替换 `handlers_notify.go` 和 `handlers_resume.go` 中的重复模式**:
```go
// Before:
w.WriteHeader(http.StatusBadRequest)
_ = json.NewEncoder(w).Encode(notifyResponse{OK: false, Error: "..."})

// After:
writeJSON(w, http.StatusBadRequest, notifyResponse{OK: false, Error: "..."})
```

对于 200 状态码的简写:
```go
// Before:
_ = json.NewEncoder(w).Encode(notifyResponse{OK: true, Channel: channel, To: to})

// After:
writeJSON(w, http.StatusOK, notifyResponse{OK: true, Channel: channel, To: to})
```

**验证**:
```bash
go build ./internal/gateway/...
go test ./internal/gateway/... -count=1
```

---

### Task E.3: CronService 精确计时 (REQ-R3-PERF-001)

**文件**: `pkg/cron/service.go`

**变更** — 行 151-163:

需要先阅读 `checkJobs()` 的实现和 CronService 结构来确定最佳方案。

基本思路:
```go
func (cs *CronService) runLoop(stopChan chan struct{}) {
    for {
        next := cs.nextJobTime()
        var waitCh <-chan time.Time
        if next.IsZero() {
            // 无 job 时每 30 秒检查一次 (以防动态添加了新 job)
            waitCh = time.After(30 * time.Second)
        } else {
            delay := time.Until(next)
            if delay < time.Second {
                delay = time.Second
            }
            waitCh = time.After(delay)
        }

        select {
        case <-stopChan:
            return
        case <-waitCh:
            cs.checkJobs()
        }
    }
}
```

需要实现 `nextJobTime()` 方法，遍历所有 job 找最近的执行时间。

**注意**: 需要确保动态添加 job 时能中断等待。如果 CronService 有 `addJob` 方法，需要额外的通知机制 (如 channel)。如果复杂度过高，改为降低 ticker 频率到 5-10 秒即可。

**验证**:
```bash
go build ./pkg/cron/...
go test ./pkg/cron/... -count=1
```

---

### Task E.4: console_file.go strings.Builder (REQ-R3-PERF-002)

**文件**: `pkg/httpapi/console_file.go`

**变更** — 行 411-607 区域:
```go
// Before:
html += "<tr>" + ...

// After:
var b strings.Builder
b.WriteString("<tr>")
// ...
html = b.String()
```

**验证**:
```bash
go build ./pkg/httpapi/...
go test ./pkg/httpapi/... -count=1
```

---

### Task E.5: health server shutdown 超时 (REQ-R3-SEC-001)

**文件**: `pkg/health/server.go`

**变更** — 行 80:
```go
// Before:
return s.server.Shutdown(context.Background())

// After:
ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
defer cancel()
return s.server.Shutdown(ctx)
```

**验证**:
```bash
go build ./pkg/health/...
```

---

### Task E.6: 统一错误 wrap 格式 (REQ-R3-SLIM-007)

**文件**: 全仓库

**步骤**:
1. 搜索 `fmt.Errorf("...: %v", err)` 模式
2. 判断是否应该使用 `%w` (需要 unwrap 的场景)
3. 修改明确应该 wrap 的场景

**筛选规则**:
- 内部函数间传递 → 用 `%w`
- 跨 API 边界、有意隐藏内部细节 → 保持 `%v` 或 `%s`
- `errors.New` + string → 不变

**验证**:
```bash
go vet ./...
go build ./...
```

---

### Task E.7: randomString 添加注释 (REQ-R3-BUG-004)

**文件**: `pkg/providers/antigravity_provider.go`

**变更** — 行 765:
```go
// Before:
func randomString(n int) string {

// After:
// randomString generates a non-cryptographic random string for request IDs.
// crypto/rand is not needed here as these are used solely for request tracing.
func randomString(n int) string {
```

**验证**:
```bash
go build ./pkg/providers/...
```

---

## 并行执行策略

```
Round 1:  [Task A.1] [Task A.2]                             — model_override.go, 顺序
          [Task A.3]                                         — console_stream.go, 独立
          [Task B.1] [Task B.2] [Task B.3]                   — 3 个不同文件, 全并行
          [Task B.4]                                         — 新测试文件, 独立

Round 2:  [Task C.1] [Task C.2] [Task C.3]                   — 3 个不同目录, 全并行

Round 3:  [Task D.1] [Task D.2]                              — feishu/agent 不同目录, 并行
          [Task E.1] [Task E.2] [Task E.5]                   — 分散文件, 并行

Round 4:  [Task E.3] [Task E.4] [Task E.6] [Task E.7]       — 清理任务, 可并行

Gate: go build ./... && go vet ./... && go test ./pkg/... ./internal/... ./cmd/... -count=1
```

---

## 文件变更总结

| 文件 | Task | 变更类型 |
|------|------|----------|
| `pkg/session/model_override.go` | A.1, A.2 | 修改 |
| `pkg/httpapi/console_stream.go` | A.3 | 修改 |
| `pkg/session/events.go` | B.1 | 修改 |
| `pkg/providers/antigravity_provider.go` | B.2, E.7 | 修改 |
| `pkg/tools/task_ledger.go` | B.3 | 修改 |
| `pkg/session/model_override_test.go` | B.4 | 新建 |
| `pkg/agent/loop.go` | C.1 | 修改(缩减) |
| `pkg/agent/loop_bucket.go` | C.1 | 新建 |
| `pkg/agent/loop_errors.go` | C.1 | 新建 |
| `pkg/tools/web_search.go` | C.2 | 修改(缩减) |
| `pkg/tools/web_search_brave.go` | C.2 | 新建 |
| `pkg/tools/web_search_ddg.go` | C.2 | 新建 |
| `pkg/tools/shell.go` | C.3 | 修改(缩减) |
| `pkg/tools/shell_session.go` | C.3 | 新建 |
| `pkg/tools/shell_safety.go` | C.3 | 新建 |
| `pkg/channels/feishu/feishu_64.go` | D.1 | 修改(缩减) |
| `pkg/channels/feishu/feishu_format.go` | D.1 | 新建 |
| `pkg/channels/feishu/feishu_media.go` | D.1 | 新建 |
| `pkg/agent/run_pipeline_impl.go` | D.2 | 修改(缩减) |
| `pkg/agent/pipeline_notify.go` | D.2 | 新建 |
| `pkg/agent/pipeline_helpers.go` | D.2 | 新建 |
| `pkg/tools/toolcall_hooks.go` | E.1 | 修改 |
| `internal/gateway/response.go` | E.2 | 新建 |
| `internal/gateway/handlers_notify.go` | E.2 | 修改 |
| `internal/gateway/handlers_resume.go` | E.2 | 修改 |
| `pkg/cron/service.go` | E.3 | 修改 |
| `pkg/httpapi/console_file.go` | E.4 | 修改 |
| `pkg/health/server.go` | E.5 | 修改 |
| 多个文件 | E.6 | 修改 |

**总计**: ~15 个新文件, ~16 个修改文件 (20 个任务)
