# knock

`knock` is a lightweight CLI that notifies you when coding agents need attention.

It is designed for `claude`, `codex`, and `gemini` CLI workflows.

## Features

- Send push notifications from terminal events.
- Watch agent output and detect approval prompts.
- Alert when prompt is waiting and user is idle.
- Built-in profiles for Claude/Codex/Gemini patterns.
- Works with Telegram and Bark providers.
- Works with Telegram, Bark, and generic Webhook providers.

## Install

```bash
go build -o knock .
```

## Quick Start

1. Initialize config:

```bash
./knock init
```

2. Add provider:

```bash
./knock provider add telegram --token <BOT_TOKEN> --chat-id <CHAT_ID>
# or
./knock provider add bark --key <DEVICE_KEY> --server https://api.day.app
# or
./knock provider add webhook --url <WEBHOOK_URL> --method POST --auth-header Authorization --auth-value "Bearer <TOKEN>"
```

3. Send test notification:

```bash
./knock test
```

4. Watch an agent command:

```bash
./knock profile use claude
./knock watch -- claude
```

## Commands

```bash
knock init
knock provider add telegram --token <token> --chat-id <id>
knock provider add bark --key <device-key> [--server <url>]
knock provider add webhook --url <url> [--method POST] [--auth-header <header>] [--auth-value <value>]
knock send [--provider <name>] [--title <title>] [--severity info|high] <message>
knock test [--provider <name>]
knock profile use <claude|codex|gemini>
knock profile list
knock rule list [--profile <name>]
knock rule add --name <name> --pattern <regex> [--event <text>] [--idle <sec>] [--cooldown <sec>] [--severity info|high] [--profile <name>]
knock rule remove --name <name> [--profile <name>]
knock watch [--profile <name>] [--provider <name>] [--debug] -- <command>
knock doctor
```

## Config

Config is stored at:

- macOS: `$HOME/Library/Application Support/knock/config.json`
- Linux: `$XDG_CONFIG_HOME/knock/config.json` or `$HOME/.config/knock/config.json`

## Build Multi-Platform Binaries

```bash
./scripts/build.sh
```

## Roadmap

- Extra providers (Pushover)
- Predefined wrappers for Claude/Codex/Gemini
- GitHub Actions release pipeline
