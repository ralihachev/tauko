.PHONY: build install test run dev

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
