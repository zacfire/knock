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

```bash
knock profile use codex
knock watch -- codex
```

Suggested trigger patterns:

- confirmation prompts
- completion summaries
- runtime failures

## Gemini

```bash
knock profile use gemini
knock watch -- gemini
```

Suggested trigger patterns:

- permission/confirmation prompts
- completion markers
- failure markers
