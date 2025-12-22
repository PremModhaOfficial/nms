.PHONY: build run dev stop clean help

# Default target
all: help

## build: Build the server and plugins
build:
	@echo "Building NMS Server..."
	@mkdir -p bin
	go build -o bin/nms-server cmd/server/main.go
	@echo "Building winrm plugin..."
	@mkdir -p plugins
	go build -o plugins/winrm plugin-code/winrm/main.go
	@echo "Build complete."

## run: Run the server using start.sh (includes secure env setup)
run:
	@./start.sh

## seed: Populate the database using seed.py
seed:
	@python3 seed.py

## dev: Fast development run (compiles and runs with default credentials)
dev: stop build
	@echo "Starting in development mode..."
	@./start.sh

## stop: Stop any running nms-server instances
stop:
	@echo "Stopping NMS Server..."
	@pkill nms-server || true
	@lsof -t -i :8080 | xargs kill -9 2>/dev/null || true
	@echo "Stopped."

## clean: Remove build artifacts
clean:
	@rm -rf bin/
	@echo "Cleaned."

## help: Show this help message
help:
	@echo "Usage: make [target]"
	@echo ""
	@echo "Targets:"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-10s\033[0m %s\n", $$1, $$2}'
