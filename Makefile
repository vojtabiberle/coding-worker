PREFIX ?= $(HOME)/.local
SKILLS_DIR ?= $(if $(CODEX_HOME),$(CODEX_HOME),$(HOME)/.codex)/skills
.PHONY: build test install install-bin install-skill
build:
	mkdir -p bin
	go build -buildvcs=false -o bin/coding-worker ./cmd/coding-worker
	go build -buildvcs=false -o bin/workerctl ./cmd/workerctl
test:
	go test -race ./...
install: install-bin install-skill
install-bin: build
	install -d "$(PREFIX)/bin"
	install -m 755 bin/coding-worker bin/workerctl "$(PREFIX)/bin/"
install-skill:
	install -d "$(SKILLS_DIR)/coding-worker"
	install -m 644 skills/coding-worker/SKILL.md "$(SKILLS_DIR)/coding-worker/SKILL.md"
