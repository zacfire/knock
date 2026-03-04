# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project overview

**knock** is a dependency-free Go CLI that sends push notifications when coding agents (Claude, Codex, Gemini) need user attention. It monitors agent stdout/stderr against regex rules and alerts via Telegram, Bark, or generic Webhook.

All code lives in a single `main.go` (~1200 lines). No external Go dependencies — pure stdlib.

## Build & run

```bash
go build -o knock .          # build binary
./knock doctor               # validate config & provider connectivity
./knock test                  # send a test notification
./knock watch -- claude       # monitor an agent subprocess
```

Cross-platform builds (darwin/linux × amd64/arm64) output to `dist/`:

```bash
./scripts/build.sh
```

## Testing

No test suite exists yet. There are no `*_test.go` files. No linter config is set up.

If adding tests, use standard `go test ./...`.

## Architecture

### Command dispatch

`main()` routes to `cmd*` functions based on `os.Args`:

| Command path | Handler | Purpose |
|---|---|---|
| `init` | `cmdInit` | Create config, optionally configure provider |
| `provider add/use/list` | `cmdProvider` | Manage notification providers |
| `send` | `cmdSend` | Send a one-off notification |
| `test` | `cmdTest` | Send a test notification |
| `profile use/list` | `cmdProfile` | Switch between agent profiles |
| `rule add/update/remove/list` | `cmdRule` | Manage regex rules within profiles |
| `watch` | `cmdWatch` | Core feature — monitor subprocess |
| `doctor` | `cmdDoctor` | Validate config + provider health |
| `update check` | `cmdUpdate` | Check for newer releases |

### Config model

JSON config at `~/Library/Application Support/knock/config.json` (macOS) or `~/.config/knock/config.json` (Linux):

```
Config
├── DefaultProvider, ActiveProfile
├── Providers: { Telegram, Bark, Webhook }
├── Profiles: map[string]Profile → Rules[]
│   └── Rule: Pattern (regex), Event, IdleSeconds, CooldownSeconds, Severity
└── Metadata: UpdateMetadata
```

Key config functions: `loadConfig()`, `loadOrDefaultConfig()`, `saveConfig()`, `mergeMissingDefaults()`.

### Watch mode (`cmdWatch`)

This is the core feature. Flow:

1. Spawns child process with piped stdout/stderr/stdin
2. `streamLines()` goroutines read output line-by-line into a channel
3. `proxyInput()` goroutine forwards user stdin to child, signals activity
4. Main loop matches lines against compiled regex rules
5. Rules with `IdleSeconds > 0` become "pending" — only fire after user goes idle
6. Rules with `IdleSeconds ≤ 0` fire immediately (respecting cooldown)
7. Signals (SIGINT/SIGTERM) are forwarded to the child process

### Notification providers

Each provider has a dedicated `send*()` function making HTTP requests:
- **Telegram**: POST form-encoded to Bot API
- **Bark**: GET with URL-escaped path segments
- **Webhook**: Configurable method, auth headers, JSON body

### Default profiles

Built-in profiles in `defaultProfiles()` contain pre-configured regex rules for Claude, Codex, and Gemini output patterns (approval prompts, errors, task completion).
