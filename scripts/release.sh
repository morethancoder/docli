#!/usr/bin/env bash
# Tag a release and push the tag; GitHub Actions builds the binaries.
# Usage: make release VERSION=v0.1.0   (YES=1 skips the confirm)
set -euo pipefail
cd "$(dirname "$0")/.."
source scripts/_lib.sh
load_env

require_cli git "https://git-scm.com"
v="${VERSION:-}"
[[ "$v" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.]+)?$ ]] || die "set VERSION=vX.Y.Z (got '${v}')"
[ -z "$(git status --porcelain)" ] || die "the working tree has uncommitted changes"
git rev-parse -q --verify "refs/tags/$v" >/dev/null && die "tag $v already exists"
[ "$(git rev-parse HEAD)" = "$(git rev-parse '@{u}' 2>/dev/null || true)" ] || die "push main first: HEAD is not on the remote"

step "tests"
go test -count=1 -short ./... >/dev/null && ok "tests pass"

confirm "tag $v at $(git rev-parse --short HEAD) and push it (starts the release build)?" || die "cancelled"
git tag -a "$v" -m "docli $v"
git push origin "$v"
ok "pushed $v; the release workflow builds it: gh run watch \$(gh run list -w release -L1 --json databaseId -q '.[0].databaseId')"
