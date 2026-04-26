# Agent Integrations

## Strategy

Use hook mode when available; fallback to watch mode for portability.

## Claude

```bash
knock profile use claude
knock watch -- claude
```

Suggested trigger patterns:

- `Allow? [y/N]`
- `task complete` / `plan complete`
- `error` / `failed`

## Codex

Prefer hooks for interactive Codex sessions:

```bash
chmod +x scripts/codex-record-start-hook.sh scripts/codex-stop-hook.sh
```

Add the hook commands to `~/.codex/hooks.json` and enable `codex_hooks` in `~/.codex/config.toml`. See `README.md` for the full example.

For non-interactive runs, watch mode can also work:

```bash
knock profile use codex
knock watch --provider local,telegram -- codex exec "your prompt"
```

Suggested trigger patterns:

- confirmation prompts
- completion summaries
- runtime failures

The `codex` profile also sends a completion notification when the watched process exits, so it does not depend on Codex printing a specific `done` line.

## Gemini

```bash
knock profile use gemini
knock watch -- gemini
```

Suggested trigger patterns:

- permission/confirmation prompts
- completion markers
- failure markers
