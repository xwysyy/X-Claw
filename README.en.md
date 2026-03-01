# PicoClaw

<p align="center">
  <img src="assets/logo.svg" alt="PicoClaw logo" width="120" />
</p>

[中文](README.md) | [Roadmap](ROADMAP.md)

PicoClaw is a lightweight personal AI assistant written in Go.

This repository contains the core CLI, gateway service, tool system, and channel integrations.

## Scope

PicoClaw supports:
- CLI chat (`agent` mode)
- Long-running gateway service (`gateway` mode)
- Multi-model configuration via `model_list`
- Tool calling (filesystem, exec, web, cron, memory, skills)
- Session and workspace persistence

## Project Status

This project is under active development.

- Expect behavior changes between versions.
- Review config changes when upgrading.
- Avoid exposing it directly to the public internet without your own security controls.

## Requirements

- Linux host recommended (x86_64 / ARM64 / RISC-V)
- Go toolchain for source builds
- At least one model provider API key (or a local compatible endpoint)

## Quick Start (Local Build)

```bash
git clone https://github.com/sipeed/picoclaw.git
cd picoclaw
make deps
make build
```

Initialize workspace/config:

```bash
./build/picoclaw onboard
```

Edit config:

```bash
vim ~/.picoclaw/config.json
```

Minimal example:

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

Run one-shot chat:

```bash
./build/picoclaw agent -m "hello"
```

Run interactive chat:

```bash
./build/picoclaw agent
```

## Gateway Mode

Start gateway:

```bash
./build/picoclaw gateway
```

Health endpoint:

```bash
curl -sS http://127.0.0.1:18790/health
```

### Notification API (`/api/notify`)

Gateway also exposes a lightweight notification endpoint so external systems (CI / scripts / daemons) can push a reminder to you via configured channels (e.g. Feishu).

Send to a specific channel + recipient:

```bash
curl -sS -X POST http://127.0.0.1:18790/api/notify \
  -H 'Content-Type: application/json' \
  -d '{"channel":"feishu","to":"oc_xxx","content":"Task completed"}'
```

If you omit `channel/to`, it defaults to the most recent conversation (`last_active`):

```bash
curl -sS -X POST http://127.0.0.1:18790/api/notify \
  -H 'Content-Type: application/json' \
  -d '{"content":"Task completed (last_active)"}'
```

Note: on a fresh gateway start, if there has been no external conversation yet (`last_active` is empty), omitting `channel/to` will return `channel is required`. Send one message to the bot via Feishu/Telegram to establish `last_active`, or specify `channel/to` explicitly.

Security notes:
- If `gateway.api_key` is empty, only loopback requests are allowed (e.g. `127.0.0.1`)
- If `gateway.api_key` is set, include `Authorization: Bearer <api_key>` (otherwise you'll get 401)

Public exposure (remote / cross-machine notifications):
- Never expose `/api/notify` to the public internet with an empty `gateway.api_key`
- Prefer HTTPS reverse proxy or private networking (e.g. Tailscale) for remote access
- If you must bind it publicly: set `gateway.host` to `0.0.0.0` and configure a strong random `gateway.api_key`

For external agents (Claude Code / Codex) to notify PicoClaw, see: `extensions/picoclaw-notify/SKILL.md` (calls `/api/notify`).

### Tool Trace (replayable tool-call logs)

When `tools.trace.enabled=true`, every tool call appends to an on-disk JSONL event stream, and can optionally write per-call files. This makes it easy to debug:

- why the model decided to call a tool
- what arguments were used
- what the tool returned
- duration / error summary

Default trace locations (when `tools.trace.dir` is empty):

- ` <workspace>/.picoclaw/audit/tools/<session>/events.jsonl `
- ` <workspace>/.picoclaw/audit/tools/<session>/calls/*.json|*.md ` (when `tools.trace.write_per_call_files=true`)

Config example:

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

### Structured Memory Output (JSON hits)

`memory_search` / `memory_get` return structured JSON to the LLM side (with a stable `kind` field for regression tests and reliable quoting), while still keeping a short human-readable summary:

- `memory_search` → `{"kind":"memory_search_result","hits":[...]}`
- `memory_get` → `{"kind":"memory_get_result","found":...,"hit":...}`

This significantly improves second-pass consumption and reduces “model misreads plain text” issues.

### Operable Cron State (runHistory / lastStatus)

Cron job state is persisted under your workspace:

- ` <workspace>/cron/jobs.json `

The `state` section records:

- `lastStatus` / `lastRunAtMs` / `lastDurationMs`
- `lastOutputPreview` (truncated preview)
- `runHistory` (latest N runs)

## Docker Compose

This repo ships `docker/docker-compose.yml` with profiles:
- `gateway` for long-running service
- `agent` for one-shot/manual CLI runs

Use your local config at `config/config.json` (mounted read-only into the container).
If the container logs show `permission denied` for `/home/picoclaw/.picoclaw/config.json`, your host `config/config.json` is likely too strict (e.g. `600`). Ensure it is readable by the container user (e.g. `chmod 644 config/config.json`).

Build and run gateway:

```bash
docker compose -p picoclaw -f docker/docker-compose.yml --profile gateway up -d --build
docker compose -p picoclaw -f docker/docker-compose.yml ps
curl -sS http://127.0.0.1:18790/health
```

Run one-shot agent:

```bash
docker compose -p picoclaw -f docker/docker-compose.yml run --rm picoclaw-agent -m "hello"
```

Git is available in the container image for agent-side commits/pushes. Configure PAT and identity in `config/config.json` under `tools.git`:

```json
{
  "tools": {
    "git": {
      "enabled": true,
      "username": "your-github-username",
      "pat": "github_pat_xxx",
      "user_name": "Your Name",
      "user_email": "you@example.com",
      "host": "github.com",
      "protocol": "https"
    }
  }
}
```

At container startup, this is written to `~/.git-credentials` and `~/.gitconfig` automatically.

Stop gateway:

```bash
docker compose -p picoclaw -f docker/docker-compose.yml down
```

## Common Commands

- `picoclaw onboard` initialize workspace and default config
- `picoclaw agent` interactive chat
- `picoclaw agent -m "..."` one-shot chat
- `picoclaw gateway` run channel gateway
- `picoclaw status` show runtime status
- `picoclaw cron list` list scheduled jobs
- `picoclaw cron add ...` add scheduled job

## Configuration Notes

- Main config file: `~/.picoclaw/config.json`
- Default workspace: `~/.picoclaw/workspace`
- Example config template: `config/config.example.json`

For advanced options, inspect in-code config structs under `pkg/config`.

## Unit Testing

See: [UNIT_TESTING.md](UNIT_TESTING.md) (Chinese doc, includes the recommended TDD workflow and project-specific test commands).

## Troubleshooting

Use:
- `docker compose ... logs -f picoclaw-gateway`

## License

MIT
