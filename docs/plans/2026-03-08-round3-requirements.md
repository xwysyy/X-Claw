# X-Claw Round 3 重构需求文档

> 版本: v1.0
> 依据: [Round 3 审查报告](2026-03-08-round3-audit.md)
> 约束: 保持全部现有功能和 API 不变
> 参考: hermes-agent 项目特色

---

## 1. Bug 修复需求

### REQ-R3-BUG-001: 修复 model_override.go writeMetaFile 错误吞没
- **来源**: BUG-R3-001
- **优先级**: P0
- **范围**: `pkg/session/model_override.go:84,117`
- **要求**: 将 `_ = writeMetaFile(metaPath, meta)` 改为检查错误并记录 warn 日志
- **验证**: `grep '_ = writeMetaFile' pkg/session/model_override.go` 无输出

### REQ-R3-BUG-002: 修复 EffectiveModelOverride TOCTOU 竞态
- **来源**: BUG-R3-002
- **优先级**: P0
- **范围**: `pkg/session/model_override.go:12-37`
- **要求**: 将 `EffectiveModelOverride` 改为使用写锁，在过期检查和清除之间不释放锁
- **验证**: `go test -race ./pkg/session/... -count=3` 通过

### REQ-R3-BUG-003: 修复 console_stream.go io.ReadAll 错误忽略
- **来源**: BUG-R3-003
- **优先级**: P0
- **范围**: `pkg/httpapi/console_stream.go:79`
- **要求**: `buf, _ := io.ReadAll(f)` 改为检查错误，失败时跳过本次日志尾部读取
- **验证**: `grep 'buf, _ := io.ReadAll' pkg/httpapi/console_stream.go` 无输出

### REQ-R3-BUG-004: randomString 改用 crypto/rand 或添加注释
- **来源**: BUG-R3-004
- **优先级**: P1
- **范围**: `pkg/providers/antigravity_provider.go:765-772`
- **要求**: 评估 `randomString` 用途，改用 `crypto/rand` 或添加注释说明不需要密码学强度
- **验证**: `go build ./pkg/providers/...` 通过

### REQ-R3-BUG-005: 修复 events.go f.Sync 错误处理
- **来源**: BUG-R3-005
- **优先级**: P1
- **范围**: `pkg/session/events.go:57`
- **要求**: `_ = f.Sync()` 改为检查错误并 wrap 到返回值
- **验证**: `grep '_ = f.Sync' pkg/session/events.go` 无输出

### REQ-R3-BUG-006: 清理 toolcall_hooks.go 死参数
- **来源**: BUG-R3-006
- **优先级**: P2
- **范围**: `pkg/tools/toolcall_hooks.go:226`
- **要求**: 删除 `_ = call` 行
- **验证**: `go build ./pkg/tools/...` 通过

### REQ-R3-BUG-007: antigravity 凭证保存错误记录
- **来源**: BUG-R3-007
- **优先级**: P1
- **范围**: `pkg/providers/antigravity_provider.go:612`
- **要求**: `_ = auth.SetCredential(...)` 改为 `if err := ...; err != nil { logger.WarnCF(...) }`
- **验证**: `grep '_ = auth.SetCredential' pkg/providers/` 无输出

### REQ-R3-BUG-008: task_ledger load 错误记录
- **来源**: BUG-R3-008
- **优先级**: P1
- **范围**: `pkg/tools/task_ledger.go:77`
- **要求**: `_ = l.load()` 改为检查错误并记录 warn 日志
- **验证**: `grep '_ = l.load' pkg/tools/task_ledger.go` 无输出

---

## 2. 代码精简需求

### REQ-R3-SLIM-001: 拆分 loop.go 核心循环
- **来源**: SLIM-R3-001
- **优先级**: P1
- **范围**: `pkg/agent/loop.go` (3006 行)
- **要求**:
  - 将 `bucket` 结构体和相关方法拆分到 `loop_bucket.go`
  - 将错误分类函数拆分到 `loop_errors.go`
  - 保持原有包内可见性 (不改变导出)
- **目标**: loop.go 行数降至 2500 行以下
- **验证**: `go build ./pkg/agent/... && go test ./pkg/agent/... -count=1` 通过

### REQ-R3-SLIM-002: 拆分 web_search.go 搜索引擎后端
- **来源**: SLIM-R3-002
- **优先级**: P1
- **范围**: `pkg/tools/web_search.go` (1902 行)
- **要求**:
  - 将每个搜索引擎后端拆分到独立文件
  - 至少拆分 Brave 和 DuckDuckGo 两个后端
  - web_search.go 保留入口和公共类型
- **目标**: 每个文件 <600 行
- **验证**: `go build ./pkg/tools/... && go test ./pkg/tools/... -count=1 -run TestWeb` 通过

### REQ-R3-SLIM-003: 拆分 shell.go
- **来源**: SLIM-R3-003
- **优先级**: P1
- **范围**: `pkg/tools/shell.go` (1824 行)
- **要求**:
  - 将进程/会话管理 (`processTable`, `sessionTable`) 拆分到 `shell_session.go`
  - 将 deny patterns 和安全检查拆分到 `shell_safety.go`
  - 将输出格式化拆分到 `shell_output.go`
- **目标**: 每个文件 <700 行
- **验证**: `go build ./pkg/tools/... && go test ./pkg/tools/... -count=1 -run TestShell` 通过

### REQ-R3-SLIM-004: 拆分 feishu_64.go
- **来源**: SLIM-R3-004
- **优先级**: P2
- **范围**: `pkg/channels/feishu/feishu_64.go` (1537 行)
- **要求**:
  - 将消息格式化/markdown 处理拆分到 `feishu_format.go`
  - 将媒体处理拆分到 `feishu_media.go`
- **目标**: feishu_64.go <900 行
- **验证**: `go build ./pkg/channels/feishu/... && go test ./pkg/channels/... -count=1` 通过

### REQ-R3-SLIM-005: 拆分 run_pipeline_impl.go
- **来源**: SLIM-R3-005
- **优先级**: P2
- **范围**: `pkg/agent/run_pipeline_impl.go` (1424 行)
- **要求**:
  - 将通知/进度回调拆分到 `pipeline_notify.go`
  - 将复制辅助函数拆分到 `pipeline_helpers.go`
- **目标**: run_pipeline_impl.go <900 行
- **验证**: `go build ./pkg/agent/... && go test ./pkg/agent/... -count=1` 通过

### REQ-R3-SLIM-006: 抽取 JSON 响应 helper
- **来源**: SLIM-R3-009
- **优先级**: P2
- **范围**: `internal/gateway/handlers_notify.go`, `handlers_resume.go`
- **要求**: 抽取 `writeJSON(w http.ResponseWriter, v any)` helper 函数
- **验证**: `go build ./internal/gateway/...` 通过

### REQ-R3-SLIM-007: 统一错误 wrap 格式
- **来源**: SLIM-R3-011
- **优先级**: P2
- **范围**: 全仓库
- **要求**: 将不必要的 `fmt.Errorf("%v", err)` 和 `fmt.Errorf("%s", err)` 改为 `fmt.Errorf("%w", err)` (在需要 unwrap 的场景)
- **不变**: 有意不 wrap 的场景 (如跨 API 边界) 保持不变
- **验证**: `go vet ./...` 通过

---

## 3. 性能优化需求

### REQ-R3-PERF-001: CronService 精确计时
- **来源**: PERF-R3-001
- **优先级**: P2
- **范围**: `pkg/cron/service.go:151-163`
- **要求**: 将 1 秒 ticker 改为计算下次执行时间并精确等待
- **限制**: 不改变 job 执行语义
- **验证**: `go test ./pkg/cron/... -count=1` 通过

### REQ-R3-PERF-002: console_file.go 使用 strings.Builder
- **来源**: PERF-R3-004 / SLIM-R3-012
- **优先级**: P2
- **范围**: `pkg/httpapi/console_file.go:411-607`
- **要求**: 将 `html += "..."` 改为 `strings.Builder`
- **验证**: `go build ./pkg/httpapi/...` 通过

---

## 4. 安全加固需求

### REQ-R3-SEC-001: health server shutdown 超时
- **来源**: SEC-R3-003
- **优先级**: P2
- **范围**: `pkg/health/server.go:80`
- **要求**: `context.Background()` 改为 `context.WithTimeout(context.Background(), 10*time.Second)`
- **验证**: `go build ./pkg/health/...` 通过

---

## 5. 测试需求

### REQ-R3-TEST-001: model_override 并发测试
- **来源**: TEST-R3-001
- **优先级**: P1
- **范围**: 新建或追加到 `pkg/session/model_override_test.go`
- **要求**: 覆盖以下场景:
  - SetModelOverride 并发写入
  - EffectiveModelOverride 过期自动清理
  - 并发读写竞态安全 (-race)
- **验证**: `go test -race ./pkg/session/... -count=3` 通过

---

## 6. 优先级与阶段划分

| 阶段 | 需求 | 文件影响 | 复杂度 |
|------|------|----------|--------|
| **Phase A** (P0 Bug) | REQ-R3-BUG-001/002/003 | 2 文件 | 低 |
| **Phase B** (P1 Bug+Test) | REQ-R3-BUG-005/007/008, REQ-R3-TEST-001 | 4 文件 | 低 |
| **Phase C** (P1 拆分) | REQ-R3-SLIM-001/002/003 | 3→9 文件 | 中 |
| **Phase D** (P2 拆分) | REQ-R3-SLIM-004/005 | 2→6 文件 | 中 |
| **Phase E** (P2 清理) | REQ-R3-BUG-004/006, REQ-R3-SLIM-006/007, REQ-R3-PERF-001/002, REQ-R3-SEC-001 | 8+ 文件 | 低 |

**并行安全矩阵**:
- Phase A 和 Phase B: 无文件冲突，可并行
- Phase C 内部: loop.go / web_search.go / shell.go 无冲突，3 个拆分可并行
- Phase D: 依赖 Phase C 完成
- Phase E: 与 Phase D 无冲突，可并行

---

## 7. 约束条件

1. **API 不变**: 所有 HTTP 端点路径、请求/响应格式不变
2. **行为不变**: 消息处理、session 持久化、tool 执行流程不变
3. **配置兼容**: 不新增必填配置项
4. **包结构不变**: 不移动包 (不做 pkg→internal 迁移，影响面过大)
5. **构建通过**: 每个阶段完成后 `go build ./...` 和 `go vet ./...` 必须通过
6. **测试通过**: 每个阶段完成后 `go test ./pkg/... ./internal/... ./cmd/... -count=1` 中已有测试必须通过
7. **文件拆分规则**: 拆分时仅移动代码，不改变逻辑；保持包内可见性
