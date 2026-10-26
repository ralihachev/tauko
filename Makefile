.PHONY: build install test run dev claude

build:
	go build -o tauko ./cmd/tauko

install:
	go install ./cmd/tauko

test:
	go test ./...

run: build
	./tauko serve

# Serve courses from disk so content edits apply on restart without rebuilding.
dev: build
	./tauko serve -courses examples/courses -db /tmp/tauko-dev.db

# Claude Code with the local pane, in fullscreen so it docks beside the chat.
# Extra flags go in ARGS, e.g. make claude ARGS=--continue
claude:
	CLAUDE_CODE_NO_FLICKER=1 claude --plugin-dir $(CURDIR)/claude-mod $(ARGS)
