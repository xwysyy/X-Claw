# PicoClaw

<p align="center">
  <img src="assets/logo.svg" alt="PicoClaw logo" width="120" />
</p>

[English](README.en.md) | [Roadmap](ROADMAP.md)

PicoClaw 是一个使用 Go 编写的轻量级个人 AI 助手。

本仓库包含核心 CLI、Gateway 服务、工具系统和多种渠道集成。

## 项目范围

当前支持：
- CLI 对话（`agent` 模式）
- 常驻网关服务（`gateway` 模式）
- 基于 `model_list` 的多模型配置
- 工具调用（文件、命令、Web、定时、记忆、技能）
- 会话与工作区持久化

## 项目状态

项目仍在持续迭代中。

- 版本升级时可能存在行为变化
- 建议升级后检查配置兼容性
- 未加防护前，不建议直接暴露到公网

## 环境要求

- 推荐 Linux 主机（x86_64 / ARM64 / RISC-V）
- 源码构建需要 Go 工具链
- 至少一个可用模型 API Key（或兼容的本地/代理端点）

## 快速开始（本地构建）

```bash
git clone https://github.com/sipeed/picoclaw.git
cd picoclaw
make deps
make build
```

初始化工作区与配置：

```bash
./build/picoclaw onboard
```

编辑配置：

```bash
vim ~/.picoclaw/config.json
```

最小配置示例：

```json
{
  "agents": {
    "defaults": {
      "workspace": "~/.picoclaw/workspace",
      "model": "gpt-5.2",
      "max_tokens": 8192,
      "max_tool_iterations": 20
    }
  },
  "model_list": [
    {
      "model_name": "gpt-5.2",
      "model": "openai/gpt-5.2",
      "api_key": "YOUR_API_KEY",
      "api_base": "https://api.openai.com/v1"
    }
  ]
}
```

单轮问答：

```bash
./build/picoclaw agent -m "hello"
```

交互模式：

```bash
./build/picoclaw agent
```

## Gateway 模式

启动 gateway：

```bash
./build/picoclaw gateway
```

健康检查：

```bash
curl -sS http://127.0.0.1:18790/health
```

查看运行状态（包含 `last_active` / cron / trace 等）：

```bash
./build/picoclaw status
./build/picoclaw status --json
```

### 通知接口（/api/notify）

Gateway 会额外暴露一个通知接口，用于让外部系统（CI / 脚本 / 守护进程）通过已配置的渠道给你发提醒（例如飞书）。

指定渠道与收件人：

```bash
curl -sS -X POST http://127.0.0.1:18790/api/notify \
  -H 'Content-Type: application/json' \
  -d '{"channel":"feishu","to":"oc_xxx","content":"任务完成了"}'
```

如果省略 `channel/to`，会默认发送到最近一次对话的 `last_active`：

```bash
curl -sS -X POST http://127.0.0.1:18790/api/notify \
  -H 'Content-Type: application/json' \
  -d '{"content":"任务完成了（last_active）"}'
```

注意：如果 gateway 刚启动、还没有任何外部对话记录（`last_active` 为空），省略 `channel/to` 会返回 `channel is required`。此时请先在飞书/Telegram 等渠道给机器人发一句话建立 `last_active`，或显式指定 `channel/to`。

安全说明：
- 当 `gateway.api_key` 为空时，仅允许来自本机 loopback 的请求（例如 `127.0.0.1`）
- 当 `gateway.api_key` 设置为非空时，请携带 `Authorization: Bearer <api_key>`（否则返回 401）

公网暴露建议（如需远程/跨机器通知）：
- 强烈不建议在 `gateway.api_key` 为空时暴露公网
- 建议优先使用反向代理（HTTPS）或私网方案（如 Tailscale）再对外提供 `/api/notify`
- 如必须直连：将 `gateway.host` 设为 `0.0.0.0` 并配置强随机 `gateway.api_key`

外部 Agent（例如 Claude Code / Codex）对接 PicoClaw 通知的扩展文档见：`extensions/picoclaw-notify/SKILL.md`（通过调用 `/api/notify` 推送提醒）。

### Tool Trace（工具调用可追溯 / 可复盘）

当你把配置里的 `tools.trace.enabled` 设为 `true` 时，每一次 tool call 都会追加写入一个 JSONL 事件流，并可选写 per-call 文件，便于排查 “模型为什么调用了某个工具 / 工具到底返回了什么 / 耗时多少”。

默认落盘位置（当 `tools.trace.dir` 为空时）：

- ` <workspace>/.picoclaw/audit/tools/<session>/events.jsonl `
- ` <workspace>/.picoclaw/audit/tools/<session>/calls/*.json|*.md `（当 `tools.trace.write_per_call_files=true`）

配置示例：

```json
{
  "tools": {
    "trace": {
      "enabled": true,
      "write_per_call_files": true
    }
  }
}
```

### Run/Session 导出（picoclaw export）

当你需要提交 bug / 复盘某次对话 / 把 “可回放执行” 资料打包给别人时，可以导出一个 zip bundle（默认会包含：session 快照 + tool traces + cron/state/config 脱敏快照 + manifest）。

常用用法：

```bash
# 直接导出当前 workspace 的 last_active 会话（推荐）
./build/picoclaw export --last-active

# 或导出指定 sessionKey
./build/picoclaw export --session 'agent:main:feishu:group:oc_xxx'
```

默认输出位置：

- ` <workspace>/exports/*.zip `

### 统一工具错误模板（tools.error_template）

当工具执行失败时（`is_error=true`），PicoClaw 会把错误包装成结构化 JSON（`kind=tool_error`），并附带最小的自愈 hints（required 参数、可用工具列表/相似工具名等），让模型更容易自救（换参数 / 换工具 / 先读后写）。

说明：
- 这是 executor 层的统一能力，不需要每个 tool 单独实现
- 只影响 LLM 侧的 `ForLLM`；不会强制把 JSON 错误刷给用户（若 tool 提供了 `ForUser`，会优先保留人类友好输出）

配置示例（默认已启用）：

```json
{
  "tools": {
    "error_template": {
      "enabled": true,
      "include_schema": true
    }
  }
}
```

### 结构化记忆输出（Memory JSON hits）

`memory_search` / `memory_get` 的工具输出对 LLM 侧返回结构化 JSON（`kind` 字段可用于回归测试与稳定引用），同时对人类侧保留简要摘要：

- `memory_search` → `{"kind":"memory_search_result","hits":[...]}`
- `memory_get` → `{"kind":"memory_get_result","found":...,"hit":...}`

这能显著降低 “模型看不懂纯文本结果 / 引用不稳定” 的概率。

### 语义记忆 Embeddings（可选远程）

PicoClaw 的语义记忆（`agents.defaults.memory_vector`）默认使用本地 `hashed` embedder：快、确定性强、无需额外 API / 网络。

如果你希望更高质量的语义检索，可以让 PicoClaw 调用一个 OpenAI-compatible 的 embeddings 端点（`POST <api_base>/embeddings`），例如 SiliconFlow / OpenAI / 其他兼容服务。

兼容方式（直接使用你现成的 `EMBEDDING_*` 环境变量，无需改 `config.json`）：

```bash
export EMBEDDING_API_KEY='sk-...'
export EMBEDDING_BASE_URL='https://api.siliconflow.cn/v1'
export EMBEDDING_MODEL='Qwen/Qwen3-Embedding-8B'
export EMBEDDING_DIM='4096'
```

推荐方式（使用带命名空间的 `PICOCLAW_EMBEDDING_*`；与上面等价但更不容易撞名）：

```bash
export PICOCLAW_EMBEDDING_API_KEY='sk-...'
export PICOCLAW_EMBEDDING_API_BASE='https://api.siliconflow.cn/v1'
export PICOCLAW_EMBEDDING_MODEL='Qwen/Qwen3-Embedding-8B'
```

说明：
- 首次触发语义检索/索引重建时会产生网络请求；索引会落盘缓存，并在源文件或 `api_base/model` 变化时自动重建
- 常用调参：`(PICOCLAW_|)EMBEDDING_PROXY`、`(PICOCLAW_|)EMBEDDING_BATCH_SIZE`、`(PICOCLAW_|)EMBEDDING_REQUEST_TIMEOUT_SECONDS`
- 如果你显式把 `embedding.kind` 设为 `openai_compat`，则 `api_base` 与 `model` 为必填（否则会报错）

### Cron 可运营任务状态（runHistory / lastStatus）

Cron 的任务状态会持久化到工作区：

- ` <workspace>/cron/jobs.json `

其中 `state` 会记录：

- `lastStatus` / `lastRunAtMs` / `lastDurationMs`
- `lastOutputPreview`（截断预览）
- `runHistory`（最近 N 次运行记录）

CLI 侧常用命令：

```bash
./build/picoclaw cron list
./build/picoclaw cron show <job_id>
./build/picoclaw cron show <job_id> --json
```

## Docker Compose

本仓库的 `docker/docker-compose.yml` 提供两个 profile：
- `gateway`：常驻服务
- `agent`：单次/手动执行

容器会挂载本地 `config/config.json`（只读）作为运行配置。
如果容器日志提示 `permission denied` 无法读取 `/home/picoclaw/.picoclaw/config.json`，通常是因为宿主机上的 `config/config.json` 权限过严（例如 `600`），请确保容器用户可读（例如 `chmod 644 config/config.json`）。

构建并启动 gateway：

```bash
docker compose -p picoclaw -f docker/docker-compose.yml --profile gateway up -d --build
docker compose -p picoclaw -f docker/docker-compose.yml ps
curl -sS http://127.0.0.1:18790/health
```

执行单次 agent：

```bash
docker compose -p picoclaw -f docker/docker-compose.yml run --rm picoclaw-agent -m "hello"
```

容器镜像内已包含 `git`，可用于 agent 在工作区内提交/推送代码。请在 `config/config.json` 里配置 `tools.git`（PAT + 身份）：

```json
{
  "tools": {
    "git": {
      "enabled": true,
      "username": "你的 GitHub 用户名",
      "pat": "github_pat_xxx",
      "user_name": "你的名字",
      "user_email": "you@example.com",
      "host": "github.com",
      "protocol": "https"
    }
  }
}
```

容器启动时会自动写入 `~/.git-credentials` 和 `~/.gitconfig`。

停止服务：

```bash
docker compose -p picoclaw -f docker/docker-compose.yml down
```

## 常用命令

- `picoclaw onboard` 初始化工作区与配置
- `picoclaw agent` 交互式对话
- `picoclaw agent -m "..."` 单轮对话
- `picoclaw gateway` 启动网关服务
- `picoclaw status` 查看运行状态
- `picoclaw cron list` 查看定时任务
- `picoclaw cron add ...` 新增定时任务

## 配置说明

- 主配置文件：`~/.picoclaw/config.json`
- 默认工作区：`~/.picoclaw/workspace`
- 配置模板：`config/config.example.json`

进阶配置可直接查看代码中的配置结构（`pkg/config`）。

## 单元测试

单元测试与 TDD 工作流说明见：[UNIT_TESTING.md](UNIT_TESTING.md)。

## 排错

参考：
- `docker compose ... logs -f picoclaw-gateway`

## 许可证

MIT
