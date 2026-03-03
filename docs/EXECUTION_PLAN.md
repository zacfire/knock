# Knock Execution Plan

## Phase 1: Core MVP

- Build CLI skeleton and config management.
- Implement Telegram and Bark providers.
- Ship `init`, `provider add`, `send`, `test`, and `doctor`.

## Phase 2: Watch Mode

- Wrap arbitrary command with `knock watch -- <cmd>`.
- Parse stdout/stderr with profile regex rules.
- Trigger idle timeout notification for approval prompts.

## Phase 3: Multi-Agent Profiles

- Maintain built-in profiles for Claude/Codex/Gemini.
- Add user rule operations (`rule add/list/remove`).
- Support profile export/import.

## Phase 4: Distribution

- Build darwin/linux amd64+arm64 artifacts.
- Release via GitHub tags and optional Homebrew tap.
