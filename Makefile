# Simple Makefile for a Go project

ENV_FILE ?= .env
IMAGE_S3_REGION ?= us-east-1
IMAGE_S3_BUCKET ?= menu

-include $(ENV_FILE)

# Build the application
all: build test

build:
	@echo "Building..."
	
	
	@go build -mod=mod -o vanilla-api cmd/api/main.go

# Run the application
run:
	@if [ -f "$(ENV_FILE)" ]; then \
		set -a; . "./$(ENV_FILE)"; set +a; \
	fi; \
	go run -mod=mod cmd/api/main.go
# Create DB container
docker-run:
	@if docker compose up --build 2>/dev/null; then \
		: ; \
	else \
		echo "Falling back to Docker Compose V1"; \
		docker-compose up --build; \
	fi

# Shutdown DB container
docker-down:
	@if docker compose down 2>/dev/null; then \
		: ; \
	else \
		echo "Falling back to Docker Compose V1"; \
		docker-compose down; \
	fi

# Test the application
test:
	@echo "Testing..."
	@go test -mod=mod ./... -v
# Integrations Tests for the application
itest:
	@echo "Running integration tests..."
	@go test -mod=mod ./internal/database -v

# Check access to the existing bucket without creating or deleting anything.
seaweed-check:
	@ENV_FILE="$(ENV_FILE)" ./scripts/check-seaweed.sh

# Clean the binary
clean:
	@echo "Cleaning..."
	@rm -f vanilla-api

# Live Reload
watch:
	@if command -v air > /dev/null; then \
            air; \
            echo "Watching...";\
        else \
            read -p "Go's 'air' is not installed on your machine. Do you want to install it? [Y/n] " choice; \
            if [ "$$choice" != "n" ] && [ "$$choice" != "N" ]; then \
                go install github.com/air-verse/air@latest; \
                air; \
                echo "Watching...";\
            else \
                echo "You chose not to install air. Exiting..."; \
                exit 1; \
            fi; \
        fi

.PHONY: all build run test clean watch docker-run docker-down itest \
	seaweed-check
