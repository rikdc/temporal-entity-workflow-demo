.PHONY: build clean test lint worker enroll status setup-temporal

# Build the rewards binary
build:
	go build -o rewards ./cmd/rewards

# Clean build artifacts
clean:
	rm -f rewards

# Run tests
test:
	go test ./...

# Run linter
lint:
	golangci-lint run

# Setup Temporal search attributes (run once after starting Temporal server)
setup-temporal:
	@echo "Configuring Temporal search attributes..."
	@temporal operator search-attribute list | grep -q CustomStringField || \
		temporal operator search-attribute create --name CustomStringField --type Keyword
	@temporal operator search-attribute list | grep -q CustomIntField || \
		temporal operator search-attribute create --name CustomIntField --type Int
	@echo "✓ Search attributes configured"

# Start the worker (requires Temporal server running)
worker: build
	./rewards worker

# Quick commands for common operations (examples)
enroll:
	@if [ -z "$(ID)" ]; then \
		echo "Usage: make enroll ID=customer-42"; \
		exit 1; \
	fi
	./rewards enroll $(ID)

status:
	@if [ -z "$(ID)" ]; then \
		echo "Usage: make status ID=customer-42"; \
		exit 1; \
	fi
	./rewards status $(ID)

# Install dependencies
deps:
	go mod download
	go mod tidy
