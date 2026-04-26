#!/bin/sh
set -eu

start_file=${KNOCK_CODEX_START_FILE:-/tmp/knock-codex-prompt-start}
date +%s > "$start_file"

printf '{"continue":true}\n'
