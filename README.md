# knock

Agent notification infrastructure — the last mile between your coding agents and your attention.

Zero dependencies. Single binary. Pure Go stdlib.

## What it does

knock sits between your coding agents (Claude, Codex, Gemini) and your notification channels, solving the "I walked away and missed the prompt" problem.

**Supported providers:**

| Provider | Platform | What it does |
|----------|----------|-------------|
| **local** | macOS | Native system notification (no setup needed) |
| **telegram** | Cross-platform | Push to your phone via Telegram Bot, with bidirectional interaction |
| **bark** | iOS | Push via [Bark](https://github.com/Finb/Bark) app |
| **webhook** | Any | HTTP POST to any endpoint (Slack, Discord, Feishu, etc.) |

**Multi-provider support:** Use comma-separated providers to notify multiple channels at once: `--provider local,telegram`

**Three layers of capability:**

| Layer | Command | What it does |
|-------|---------|-------------|
| **Watch** | `knock watch` | Monitor agent stdout/stderr, match regex rules, alert when you're idle |
| **Send** | `knock send` | One-shot notification from scripts, hooks, or CI |
| **Listen** | `knock listen` | HTTP server that receives webhooks and forwards to your provider |

**Telegram bidirectional interaction:** When a high-severity rule fires (e.g., agent asking for approval), knock sends a Telegram message with [Yes] / [No] inline buttons. Tap a button on your phone → the reply is piped directly into the agent's stdin. Authorize your agent from anywhere.

## Install

```bash
# From source
go install github.com/zacfire/knock@latest

# Or build locally
go build -o knock .
```

Or grab a prebuilt binary from [Releases](https://github.com/zacfire/knock/releases).

Make sure `~/go/bin` is in your PATH:

```bash
echo 'export PATH="$HOME/go/bin:$PATH"' >> ~/.zshrc
source ~/.zshrc
```

## Quick Start

### macOS local notification (simplest)

```bash
# 1. Initialize with local provider
knock init --provider local

# 2. Verify
knock doctor
knock test    # you should see a macOS notification

# 3. Watch an agent
knock watch -- claude
```

### Telegram (remote push to phone)

```bash
# 1. Create a bot: talk to @BotFather on Telegram, send /newbot
# 2. Send a message to your bot, then get your chat ID:
#    https://api.telegram.org/bot<TOKEN>/getUpdates

# 3. Configure
knock init --provider telegram --token <BOT_TOKEN> --chat-id <CHAT_ID>

# 4. Verify
knock doctor
knock test

# 5. Watch an agent
knock watch -- claude
```

> **Note:** If Telegram API is blocked in your region, set a proxy: `export https_proxy=http://127.0.0.1:7890`

### Both local + Telegram

```bash
# Set up both providers
knock init --provider local
knock provider add telegram --token <BOT_TOKEN> --chat-id <CHAT_ID>

# Watch with both — local notification on screen + Telegram on phone
knock watch --provider local,telegram -- claude
```

### Make it the default for non-interactive CLIs

```bash
# Alias for tools that output plain text (not TUI)
echo 'alias codex="knock watch --profile codex -- codex"' >> ~/.zshrc
source ~/.zshrc
```

> **Note:** `knock watch` pipes stdin/stdout, which breaks interactive TUI tools like Claude Code. For Claude Code, use the hooks integration below instead.

## Integration Patterns

### Claude Code hooks (recommended)

The best way to use knock with Claude Code — no wrapper needed, works with your existing `claude` command.

**Auto-notify when Claude finishes long tasks (>60s):**

1. Create two hook scripts:

```bash
mkdir -p ~/.claude/hooks

# Record when user submits a prompt
cat > ~/.claude/hooks/record-start.sh << 'EOF'
#!/bin/bash
date +%s > /tmp/knock-prompt-start
EOF

# Notify only if Claude took >60 seconds (with project name + summary)
cat > ~/.claude/hooks/notify-if-long.sh << 'EOF'
#!/bin/bash
THRESHOLD=60
START_FILE=/tmp/knock-prompt-start

if [ ! -f "$START_FILE" ]; then
  exit 0
fi

start=$(cat "$START_FILE")
now=$(date +%s)
elapsed=$((now - start))

if [ "$elapsed" -ge "$THRESHOLD" ]; then
  # Read hook context from stdin (JSON with cwd, last_assistant_message)
  input=$(cat)
  project=$(echo "$input" | jq -r '.cwd // empty' 2>/dev/null | xargs basename 2>/dev/null)
  summary=$(echo "$input" | jq -r '.last_assistant_message // empty' 2>/dev/null | head -c 100)

  if [ -z "$project" ]; then
    project="unknown"
  fi

  msg="[$project] done (${elapsed}s)"
  if [ -n "$summary" ]; then
    msg="$msg: $summary"
  fi

  knock send --provider local "$msg"
fi

rm -f "$START_FILE"
EOF

chmod +x ~/.claude/hooks/record-start.sh ~/.claude/hooks/notify-if-long.sh
```

2. Add to `~/.claude/settings.json`:

```json
{
  "hooks": {
    "UserPromptSubmit": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "~/.claude/hooks/record-start.sh"
          }
        ]
      }
    ],
    "Stop": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "~/.claude/hooks/notify-if-long.sh"
          }
        ]
      }
    ]
  }
}
```

Now every time Claude takes more than 60 seconds to respond, you get a macOS notification automatically. Adjust `THRESHOLD` in the script to change the delay.

> **Tip:** For Telegram notifications in hooks, replace `--provider local` with `--provider local,telegram`. Make sure `https_proxy` is set if Telegram is blocked in your region.

### Claude Code hooks (simple)

For simpler use cases like monitoring specific tool usage:

```json
{
  "hooks": {
    "PostToolUse": [
      {
        "matcher": "Bash",
        "hooks": [
          {
            "type": "command",
            "command": "knock send --severity high 'Bash tool used'"
          }
        ]
      }
    ]
  }
}
```

### Remote server notification

```bash
# On your remote server, start the listener
knock listen --port 9090 --token my-secret --provider telegram

# From anywhere, send a notification
curl -X POST http://your-server:9090/send \
  -H "Authorization: Bearer my-secret" \
  -H "Content-Type: application/json" \
  -d '{"title":"deploy","body":"Build #42 completed","severity":"info"}'
```

### Telegram remote authorization

```bash
# Watch an agent with Telegram as provider
knock watch --provider telegram -- claude

# When agent asks "Allow? [y/N]":
# → Telegram receives message with [Yes] [No] buttons
# → Tap [Yes] on your phone
# → "y" is written to agent's stdin
# → Agent proceeds
```

This only activates for **severity=high** rules with Telegram provider. Other providers and severity levels work as normal one-way notifications.

## Commands

### `knock init`

Create config file with optional provider setup.

```bash
knock init
knock init --provider local
knock init --provider telegram --token <token> --chat-id <id>
knock init --provider bark --key <device-key>
knock init --provider webhook --url <url>
```

### `knock provider`

Manage notification providers.

```bash
knock provider add local [--sound default]
knock provider add telegram --token <token> --chat-id <id>
knock provider add bark --key <device-key> [--server https://api.day.app]
knock provider add webhook --url <url> [--method POST] [--auth-header Authorization] [--auth-value 'Bearer ...']
knock provider use <local|telegram|bark|webhook>
knock provider list
```

### `knock send`

Send a one-off notification.

```bash
knock send "deployment complete"
knock send --title "CI" --severity high "build failed"
knock send --provider local,telegram "notify both channels"
```

### `knock test`

Send a test notification to verify provider connectivity.

```bash
knock test
knock test --provider telegram
knock test --provider local,telegram
```

### `knock listen`

Start an HTTP server that receives webhook POSTs and forwards them as notifications.

```bash
knock listen                              # default port 9090
knock listen --port 8080 --token secret   # custom port + bearer auth
knock listen --provider bark              # override provider
```

**POST /send** payload:

```json
{
  "title": "optional title",
  "body": "notification body (required)",
  "severity": "info|high"
}
```

### `knock watch`

Core feature. Monitor a subprocess and notify based on regex rules.

```bash
knock watch -- claude
knock watch --profile codex -- codex
knock watch --provider local,telegram --debug -- claude
```

### `knock profile`

Switch between agent profiles (claude, codex, gemini).

```bash
knock profile list
knock profile use codex
```

### `knock rule`

Manage regex rules within profiles.

```bash
knock rule list
knock rule add --name my-rule --pattern "DEPLOY" --event "Deploy detected" --idle 0 --cooldown 30 --severity high
knock rule update --name my-rule --cooldown 60
knock rule remove --name my-rule
```

### `knock doctor`

Validate config and provider connectivity.

### `knock version`

Print current version.

### `knock update check`

Check for newer releases on GitHub.

```bash
knock update check
knock update check --quiet
```

## Config

Config is stored at:

- **macOS:** `~/Library/Application Support/knock/config.json`
- **Linux:** `~/.config/knock/config.json`
- **Override:** `KNOCK_CONFIG_PATH` environment variable

## Build

```bash
go build -o knock .           # local build
./scripts/build.sh             # cross-platform (darwin/linux × amd64/arm64)
```

## License

[MIT](LICENSE)
