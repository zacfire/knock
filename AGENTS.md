# AGENTS.md

Guidance for coding agents working in this repository.

## Project Overview

`knock` is a dependency-free Go CLI for sending notifications when coding agents need user attention. It can wrap agent subprocesses, match their output against regex rules, and notify through local macOS notifications, Telegram, Bark, or generic webhooks.

The project is intentionally small:

- Single Go module: `module knock`
- Main implementation: `main.go`
- No third-party Go dependencies
- Release/build helper: `scripts/build.sh`
- Supporting docs: `README.md`, `CLAUDE.md`, `docs/`

## Common Commands

```bash
go build -o knock .
./knock version
./knock doctor
./knock test
./knock watch -- codex
```

Run the standard Go checks before handing off code changes:

```bash
gofmt -w main.go
go test ./...
go build -o knock .
```

There are currently no `*_test.go` files, but `go test ./...` should still pass.

## Architecture Notes

Most behavior lives in `main.go`.

- `main()` dispatches CLI subcommands.
- `cmdInit`, `cmdProvider`, `cmdSend`, `cmdTest`, `cmdProfile`, `cmdRule`, `cmdUpdate`, `cmdListen`, `cmdWatch`, and `cmdDoctor` own command behavior.
- Config is JSON stored in the user config directory and represented by `Config`, `ProviderCollection`, `Profile`, and `Rule`.
- `cmdWatch` is the core monitoring path: it starts a child process, streams stdout/stderr, applies compiled regex rules, tracks idle time, and sends notifications.
- Notification providers are implemented as dedicated send functions for local, Telegram, Bark, and webhook delivery.

Keep new behavior close to these existing command/provider/profile boundaries unless a larger refactor is explicitly requested.

## Coding Guidelines

- Use Go standard library APIs; do not add external dependencies unless the change clearly requires it.
- Preserve the single-binary, zero-dependency character of the project.
- Keep command output plain, scriptable, and consistent with existing messages.
- Prefer small helper functions over broad rewrites of `main.go`.
- Always run `gofmt` on edited Go files.
- Be careful with user config compatibility. When adding config fields, update defaulting and merge behavior so existing config files continue to work.
- Avoid logging secrets such as Telegram bot tokens, webhook auth values, and chat IDs.

## Testing Guidance

For narrow changes, at minimum run:

```bash
go test ./...
go build -o knock .
```

When touching provider behavior, also exercise relevant CLI paths where possible:

```bash
./knock doctor
./knock test --provider local
```

When touching watch/rule behavior, prefer adding focused tests if the logic can be isolated. If manual testing is used, describe the exact command and observed output in the handoff.

## Documentation Updates

Update docs when behavior changes:

- `README.md` for user-facing install, setup, and workflow changes.
- `CLAUDE.md` for agent-facing development notes.
- `docs/AGENT_INTEGRATIONS.md` for agent integration patterns.
- `docs/EXECUTION_PLAN.md` only when roadmap/status changes.

## Handoff Expectations

When finishing work, summarize:

- What changed
- Which files changed
- Which checks were run
- Any remaining risks or manual setup needed

