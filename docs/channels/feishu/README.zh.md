# 飞书（Feishu）通道

> 目标：把“能跑起来 + 少踩坑”的关键点写清楚。

## 配置示例

```json
{
  "channels": {
    "feishu": {
      "enabled": true,
      "app_id": "YOUR_APP_ID",
      "app_secret": "YOUR_APP_SECRET",
      "verification_token": "YOUR_VERIFICATION_TOKEN",
      "encrypt_key": "YOUR_ENCRYPT_KEY",
      "allow_from": [],
      "group_trigger": {
        "mention_only": true,
        "command_bypass": true,
        "prefixes": []
      },
      "placeholder": {
        "enabled": true,
        "text": "Thinking... 💭",
        "delay_ms": 2500
      }
    }
  }
}
```

说明：
- `allow_from` 为空数组表示允许所有用户（建议生产环境按需收紧）。
- `group_trigger` 默认更保守：群聊优先要求 `@机器人`（或命令 bypass / 前缀触发）。
- `placeholder.delay_ms` 用来避免“很快就回复 → 先发占位符又立刻被编辑”的闪烁。

## 常见问题：群聊发消息不回复（group_trigger / allow_from）

**现象**：飞书群里发消息，机器人不回复；但你用 `/api/notify` 或“任务完成提醒”能收到机器人主动推送。

优先按下面两点排查：

1) `group_trigger` 是 safe-by-default  
X-Claw 默认不会在群里“看到就回”，只有命中触发条件才会回复。常用字段：
- `mention_only=true`：群聊必须 `@机器人` 才回复（更安全）
- `command_bypass=true`：允许以 `/` 开头的命令绕过 `@` 要求（便于运维指令）
- `prefixes=["/ask", "!"]`：只响应指定前缀（触发后会剥离前缀）
- `mentionless=true`：群聊无需 `@` 也会回复（最放开，也最吵）

如果你的群里“只有你自己”，想要更省事，可以直接打开 `mentionless`：

```json
{
  "channels": {
    "feishu": {
      "group_trigger": {
        "mention_only": false,
        "mentionless": true,
        "command_bypass": true,
        "command_prefixes": ["/"]
      }
    }
  }
}
```

2) `allow_from` 会直接拦截入站消息  
`allow_from=[]` 表示允许所有人；如果你填了非空列表，只有命中的 sender 才会被处理（其余会被静默忽略）。

## 仍然收不到消息：检查飞书事件加密参数

如果你“连私聊都收不到”，或群聊完全没有任何入站反应，且飞书后台启用了事件加密：
- 请补齐 `encrypt_key` 与 `verification_token`
- 然后重启 gateway（或开启 gateway reload 热更新）

## 常见坑：图片/文件下载失败（“资源共享给机器人”/权限不足）

### 症状

- 用户发了图片/文件，但 Agent 看不到附件内容（无法做图像理解/文件解析）。
- 日志里可能出现类似：
  - `Resource download api error`（带 code/msg）
  - 或 “Failed to download resource”

### 原因（高频）

飞书对消息资源（图片/文件等）的下载存在权限/可见性限制：
- 机器人需要具备相应的 API 权限；
- 某些场景下资源需要对机器人“可见”（可理解为需要被共享给机器人/具备访问权限），否则下载接口会失败。

### X-Claw 的行为

当飞书消息类型是图片/文件/音频/视频，并且下载不到资源时：
- X-Claw 会在入站文本里追加一个轻量提示：
  - `[media: unavailable - 请确认图片/文件已共享给机器人且机器人具备下载权限]`
- 这样 Agent 至少知道“用户确实发了附件，只是拿不到”，可以主动引导用户排障。

### 排障建议（Checklist）

1) 确认机器人已被加入对应群聊/会话（至少能收到消息）。  
2) 检查飞书应用权限：是否开通了消息/资源下载相关权限。  
3) 若仍失败：尝试让用户把资源“共享给机器人/可访问”，或改用可公开访问的链接（例如网盘链接、可直接下载的 URL）。

> 提示：具体权限名称/控制台入口可能随飞书版本变化，优先以你当前飞书开发者后台为准。
