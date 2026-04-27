# Security Policy

## Secrets and Local Configuration

Do not commit local notification credentials or agent settings.

Keep these files and values private:

- Telegram bot tokens and chat IDs
- Bark device keys
- Webhook URLs, auth headers, and bearer tokens
- `knock` config files from `~/Library/Application Support/knock/` or `~/.config/knock/`
- Personal `~/.claude/` and `~/.codex/` hook/config files
- Proxy URLs that include credentials

If a token is accidentally exposed, rotate it with the provider before publishing a release.

## Reporting Vulnerabilities

Please open a GitHub issue with a minimal reproduction for non-sensitive bugs.

For sensitive reports, avoid posting secrets or working tokens publicly. Share only redacted config, command output, and the affected version.

