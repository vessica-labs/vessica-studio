#!/usr/bin/env sh
# Install the Vessica Studio prompts for the OpenAI Codex CLI.
# Copies vstd-* prompt launchers into ~/.codex/prompts so they appear as
# /vstd-deck-new, /vstd-slide-edit, etc. Each launcher loads its canonical
# instructions from `vstd skill <name>` at run time — updating the vstd
# binary updates every workflow, no reinstall needed.
set -e
dir="${CODEX_HOME:-$HOME/.codex}/prompts"
mkdir -p "$dir"
cp "$(dirname "$0")/prompts/"vstd-*.md "$dir/"
echo "Installed $(ls "$(dirname "$0")/prompts/" | wc -l | tr -d ' ') prompts to $dir:"
ls "$dir" | grep '^vstd-' | sed 's/\.md$//;s/^/  \//'
command -v vstd >/dev/null 2>&1 || echo "NOTE: vstd not found on PATH — install it: go install github.com/vessica-labs/vessica-studio/cmd/vstd@latest"
