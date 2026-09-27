# JobMatcher: one command to run it, one command to prove it works.
#   make up              start everything (installs/starts Ollama, pulls models, builds, waits for health)
#   make verify-phase1   self-checking acceptance tests against the running stack

SHELL := /usr/bin/env bash
ENV   := source scripts/env.sh &&
TOOLS := $(ENV) docker compose --profile tools run --rm --no-deps tester

.DEFAULT_GOAL := help

.PHONY: help
help: ## Show this help
	@awk 'BEGIN {FS = ":.*##"} /^[a-zA-Z0-9_-]+:.*##/ {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

.PHONY: doctor
doctor: ## Check this machine has what JobMatcher needs
	@scripts/doctor.sh

.PHONY: up
up: ## Start the whole stack (safe to re-run)
	@scripts/up.sh

.PHONY: down
down: ## Stop the stack (keeps data)
	@$(ENV) docker compose down

.PHONY: restart
restart: down up ## Restart the stack

.PHONY: ps
ps: ## Show service status
	@$(ENV) docker compose ps

.PHONY: logs
logs: ## Follow logs (make logs S=core for one service)
	@$(ENV) docker compose logs -f --tail=100 $(S)

.PHONY: test
test: ## Unit tests (no stack needed; runs in a container)
	@$(TOOLS) go test -count=1 ./...

.PHONY: verify-phase1
verify-phase1: ## Phase 1 acceptance tests against the running stack
	@$(TOOLS) go test -tags acceptance -count=1 -timeout 30m -run TestPhase1 ./tests/acceptance/ -v 2>&1 | scripts/report.sh

.PHONY: verify
verify: verify-phase1 ## Run every phase's acceptance tests

.PHONY: eval-phase1
eval-phase1: ## Compare fast vs smart models on extraction quality (writes docs/experiments/)
	@$(TOOLS) go run ./cmd/eval

.PHONY: demo
demo: ## Process your resume: make demo RESUME=private/me.pdf
	@test -n "$(RESUME)" || (echo "usage: make demo RESUME=path/to/resume.pdf" && exit 1)
	@test -f "$(RESUME)" || (echo "file not found: $(RESUME)" && exit 1)
	@$(ENV) docker compose --profile tools run --rm --no-deps -v "$(abspath $(RESUME)):/in/$(notdir $(RESUME)):ro" \
		tester go run ./cmd/jmctl upload "/in/$(notdir $(RESUME))"

.PHONY: fixtures
fixtures: ## Regenerate PDF/DOCX test fixtures from the .txt sources
	@$(TOOLS) go run ./cmd/genfixtures -dir /work/testdata/fixtures

.PHONY: clean
clean: ## Stop the stack and DELETE all its data (asks first)
	@read -p "Delete all JobMatcher data (database, files, traces)? [y/N] " a; [ "$$a" = "y" ] || exit 1
	@$(ENV) docker compose --profile tools --profile ollama down -v
	@rm -rf .run
