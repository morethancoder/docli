# docli: one-word targets, `help` first; anything longer than a few lines
# lives in scripts/<verb>.sh. See the makefile-standards skill.

.DEFAULT_GOAL := help
.PHONY: help doctor build install test lint fmt clean release

help: ## show this help
	@awk 'BEGIN {FS = ":.*?## "; printf "\n\033[1mUsage:\033[0m make \033[36m<target>\033[0m\n\n\033[1mTargets:\033[0m\n"} \
	     /^[a-z]+:.*?## / {printf "  \033[36m%-10s\033[0m %s\n", $$1, $$2} \
	     END {print ""}' $(MAKEFILE_LIST)

doctor: ## check Go, gofmt and the optional headless browser
	@bash scripts/doctor.sh

build: ## build bin/docli
	@go build -o bin/docli ./cmd/docli && echo "built bin/docli"

install: ## install docli into your Go bin folder
	@go install ./cmd/docli && echo "installed $$(go env GOPATH)/bin/docli"

test: ## run all tests (SHORT=1 skips the headless-browser test)
	@go test -count=1 $(if $(SHORT),-short) ./...

lint: ## check formatting and run go vet
	@bash scripts/lint.sh

fmt: ## format all Go files
	@gofmt -w . && echo "formatted"

clean: ## remove build output and test caches
	@bash scripts/clean.sh

release: ## tag VERSION=vX.Y.Z and push it; GitHub builds the binaries
	@bash scripts/release.sh
