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

# Apply idempotent PostgreSQL migrations in filename order.
migrate:
	@set -a; \
	if [ -f "$(ENV_FILE)" ]; then . "./$(ENV_FILE)"; fi; \
	set +a; \
	db_url="$${DATABASE_URL:-postgresql://$${BLUEPRINT_DB_USERNAME:-postgres}:$${BLUEPRINT_DB_PASSWORD:-postgres}@$${BLUEPRINT_DB_HOST:-localhost}:$${BLUEPRINT_DB_PORT:-5432}/$${BLUEPRINT_DB_DATABASE:-vanilla_api}}"; \
	for migration in internal/sdk/sqldb/migrations/*.sql; do \
		echo "Applying $$migration"; \
		psql "$$db_url" -X -v ON_ERROR_STOP=1 -f "$$migration"; \
	done
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

.PHONY: all build run test migrate clean watch docker-run docker-down itest \
	seaweed-check
