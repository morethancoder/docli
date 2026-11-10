#!/usr/bin/env bash
# Remove build output and caches that rebuild themselves; print what was freed.
# Never prompts: agents run it unattended.
set -euo pipefail
cd "$(dirname "$0")/.."
source scripts/_lib.sh

# bin: build output. .docli: docs fetched while trying docli out in this folder.
paths=(bin .docli .playwright)

total=0
for p in "${paths[@]}"; do
  [ -e "$p" ] || continue
  kb=$(du -sk "$p" 2>/dev/null | awk '{print $1}')
  rm -rf "$p"
  ok "removed $p (${kb} KB)"
  total=$((total + kb))
done
go clean -testcache && ok "cleared the Go test cache"
say "freed ${total} KB"
