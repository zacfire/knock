#!/bin/sh
set -eu

input=$(cat)
provider=${KNOCK_PROVIDER:-local,telegram}
threshold=${KNOCK_CODEX_MIN_SECONDS:-0}
start_file=${KNOCK_CODEX_START_FILE:-/tmp/knock-codex-prompt-start}

now=$(date +%s)
elapsed=0
if [ -f "$start_file" ]; then
	start=$(cat "$start_file" 2>/dev/null || echo "$now")
	elapsed=$((now - start))
	rm -f "$start_file"
fi

if [ "$elapsed" -ge "$threshold" ]; then
	project=$(
		printf '%s' "$input" | python3 -c 'import json, os, sys
try:
    data = json.load(sys.stdin)
except Exception:
    data = {}
cwd = data.get("cwd") or os.getcwd()
print(os.path.basename(cwd) or cwd)
' 2>/dev/null || basename "$PWD"
	)
	summary=$(
		printf '%s' "$input" | python3 -c 'import json, sys
try:
    msg = (json.load(sys.stdin).get("last_assistant_message") or "").replace("\n", " ")
except Exception:
    msg = ""
print(msg[:140])
' 2>/dev/null || true
	)

	message="[$project] Codex done (${elapsed}s)"
	if [ -n "$summary" ]; then
		message="$message: $summary"
	fi

	knock send --provider "$provider" "$message" >/dev/null 2>&1 || true
fi

printf '{"continue":true}\n'
