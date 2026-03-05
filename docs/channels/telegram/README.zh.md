# Telegram 通道

> 目标：把“能跑起来 + 少踩坑”的关键点写清楚。

## 配置示例

```json
{
  "channels": {
    "telegram": {
      "enabled": true,
      "token": "YOUR_TELEGRAM_BOT_TOKEN",
      "base_url": "",
      "proxy": "",
      "allow_from": [
        "telegram:123456789"
      ],
      "group_trigger": {
        "command_bypass": true,
        "command_prefixes": ["/"],
        "mention_only": false,
        "mentionless": false,
        "prefixes": []
      },
      "placeholder": {
        "enabled": true,
        "text": "Thinking...",
        "delay_ms": 2500
      },
      "typing": {
        "enabled": true
      },
      "reasoning_channel_id": ""
    }
  }
}
```

说明：
- `token`：Telegram Bot Token（必填）。
- `proxy`：可选，HTTP/HTTPS 代理 URL（例如 `http://127.0.0.1:7890`）。如果不填但环境变量里设置了 `HTTP_PROXY/HTTPS_PROXY`，X-Claw 也会自动使用环境代理。
- `allow_from`：允许列表（建议生产环境使用）。
  - `allow_from=[]` 表示允许所有人（调试时方便，但不建议长期暴露）。
  - Telegram 的 `allow_from` 匹配的是 **发送者 user id**（不是群 chat id）。
  - 支持多种格式：`"telegram:<id>"`、纯数字 `"123456789"`、`"@username"`、`"id|username"`。

## 群聊触发规则（group_trigger）

X-Claw 对群聊采用 **safe-by-default** 策略：群里如果不满足触发条件，会直接忽略消息，避免机器人在群里过吵。

触发字段含义：
- `mention_only=true`：必须 `@机器人` 才回复（更安全）
- `command_bypass=true`：允许“看起来像命令”的消息绕过群聊限制（默认 `/` 开头）
- `prefixes=["/ask","!"]`：只响应指定前缀（触发后会剥离前缀）
- `mentionless=true`：群聊不需要 `@` 也会回复（最放开，也最吵）

注意：`group_trigger` 只影响 **群聊**。私聊（`private` chat）默认会正常回复。

## 常见问题：群里发消息不回复

最常见原因有两个：
1) 群聊触发未打开：没有 `@机器人`、没有命中前缀、也不是命令。
2) `allow_from` 不匹配：你填的是 chat id 或填错了 user id，导致消息在入站阶段被拦截。

如果你确定群里“只有你自己”，想要群里随便发一句都能触发，可以这样配：

```json
{
  "channels": {
    "telegram": {
      "group_trigger": {
        "mentionless": true,
        "command_bypass": true,
        "command_prefixes": ["/"]
      }
    }
  }
}
```

