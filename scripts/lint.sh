#!/usr/bin/env bash
# Fails when a Go file is not gofmt-formatted, then runs go vet.
set -euo pipefail
cd "$(dirname "$0")/.."
source scripts/_lib.sh

step "gofmt"
bad=$(gofmt -l .)
if [ -n "$bad" ]; then err "not formatted (run make fmt):"; echo "$bad"; exit 1; fi
ok "formatted"

step "go vet"
go vet ./...
ok "vet clean"
