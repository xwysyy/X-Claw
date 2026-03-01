---
name: task-notify
description: Proactively notify the user when a task completes (preferred: `message` tool; external: Gateway `/api/notify` with API key).
metadata: {"requires":{"bins":["curl"]}}
---

# Task Notify

Use this skill when:
- You finished a **long-running** or **background** task and want to proactively ping the user.
- You ran work in a **different session** / **headless environment** (CI, cron, daemon) and need out-of-band notification.
- The user explicitly says “done后提醒我 / 完成后通知我 / ping我一下”.

## 0) Prefer inside PicoClaw: `message` tool (best UX)

If you are currently running as PicoClaw (you can call tools), **prefer the built-in `message` tool**:

- It sends a message through the currently active chat channel by default.
- You can optionally target a specific `channel` + `chat_id`.
- It avoids needing `curl`, HTTP routing, or exposing ports.

Recommended payload:
- `content`: 1–3 lines, start with status emoji-like marker (`✅` / `⚠` / `❌`) + outcome + next action.
- Keep it short; link/point to artifacts (file path, session id) instead of pasting huge output.

## 1) External integration: Gateway notification API (`POST /api/notify`)

Use this when **you are not inside** the running PicoClaw process (scripts/CI/cron on another machine), but you still want PicoClaw to send the message via configured channels (e.g. Feishu).

### 1.1 Security model (important)

- If `gateway.api_key` is **empty**: the endpoint is **loopback-only** (only `127.0.0.1/::1`).
- If `gateway.api_key` is **set**: requests must include the API key (otherwise 401).

If you expose it publicly:
- Always set `gateway.api_key` (required).
- Prefer HTTPS (reverse proxy) or private networking (e.g. Tailscale) instead of raw public HTTP.

### 1.2 Request fields

JSON body (accepted aliases):
- Destination:
  - `channel` (string, optional)
  - `to` or `chat_id` (string, optional)
- Content:
  - `content` (string, required)
  - or `text` / `message` (aliases)
  - optional `title` (prepended to content)

Default routing:
- If **both** `channel` and `to/chat_id` are omitted, it sends to `last_active` conversation.
  - Tip: talk to the bot once on your target channel to set `last_active`, then you can omit destination.

### 1.3 Examples

Local (send to `last_active`):

```bash
curl -sS -X POST "http://127.0.0.1:18790/api/notify" \
  -H "Content-Type: application/json" \
  -d '{"content":"✅ Task completed (last_active)."}'
```

Public / remote (API key required):

```bash
curl -sS -X POST "https://your-domain.example.com/api/notify" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer YOUR_GATEWAY_API_KEY" \
  -d '{"content":"✅ Task completed."}'
```

Send to a specific channel + recipient (example Feishu):

```bash
curl -sS -X POST "https://your-domain.example.com/api/notify" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer YOUR_GATEWAY_API_KEY" \
  -d '{"channel":"feishu","to":"oc_xxx","content":"✅ Job finished."}'
```

### 1.4 When to send notifications (avoid spam)

- Always: one final notification when the task finishes (success/failure).
- Optional: progress notifications only if the user explicitly asked, or the job is very long.
- If you already replied in the same chat with the final result, do NOT send duplicate notifications unless requested.

## 2) Troubleshooting

- `401 unauthorized`:
  - `gateway.api_key` is set but you didn’t send it (or it’s wrong).
  - Fix: add `Authorization: Bearer <api_key>` (or `X-API-Key` header).

- `400 channel is required` / `to/chat_id is required`:
  - You omitted destination but `last_active` is not available (or is not a deliverable channel).
  - Fix: provide `channel` + `to/chat_id`, or ask user to chat once so `last_active` is set.

- `500`:
  - Channel send failed (token/permission/config issue).
  - Fix: check Gateway logs and channel configuration.
