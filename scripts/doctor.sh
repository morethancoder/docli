#!/usr/bin/env bash
# Checks the tools docli's build and tests use. Missing Go fails the run;
# a missing browser only warns (JS-built pages and one test need it).
set -euo pipefail
cd "$(dirname "$0")/.."
source scripts/_lib.sh
load_env

fail=0

step "tools"
check_cli go    "https://go.dev/dl/" || fail=1
check_cli gofmt "comes with Go" || fail=1

step "headless browser (for JS-built docs)"
found=""
for b in "${DOCLI_CHROME:-}" \
  "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" \
  "/Applications/Chromium.app/Contents/MacOS/Chromium" \
  google-chrome google-chrome-stable chromium chromium-browser; do
  [ -z "$b" ] && continue
  if [ -x "$b" ] || has_cli "$b"; then found="$b"; break; fi
done
if [ -n "$found" ]; then ok "$found"; else warn "no Chrome/Chromium: render=browser sources fail and TestBrowserRender is skipped (set DOCLI_CHROME)"; fi

step "result"
if [ "$fail" -eq 0 ]; then ok "all required tools present"; else die "missing required tooling above"; fi
