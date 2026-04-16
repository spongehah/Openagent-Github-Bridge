.PHONY: build run test clean docker docker-run lint fmt help

# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOTEST=$(GOCMD) test
GOGET=$(GOCMD) get
GOMOD=$(GOCMD) mod
GOFMT=gofmt
BINARY_NAME=server
BINARY_PATH=./cmd/server

# Docker parameters
DOCKER_IMAGE=openagent-github-bridge
DOCKER_TAG=latest

## help: Show this help message
help:
	@echo "Usage: make [target]"
	@echo ""
	@echo "Targets:"
	@sed -n 's/^##//p' $(MAKEFILE_LIST) | column -t -s ':' | sed -e 's/^/ /'

## build: Build the application
build:
	$(GOBUILD) -o $(BINARY_NAME) $(BINARY_PATH)

## run: Run the application
run: build
	./$(BINARY_NAME)

## dev: Run with hot reload (requires air)
dev:
	air

## test: Run tests
test:
	$(GOTEST) -v -race ./...

## test-cover: Run tests with coverage
test-cover:
	$(GOTEST) -v -race -coverprofile=coverage.out ./...
	$(GOCMD) tool cover -html=coverage.out -o coverage.html

## lint: Run linter (requires golangci-lint)
lint:
	golangci-lint run ./...

## fmt: Format code
fmt:
	$(GOFMT) -s -w .

## tidy: Tidy go modules
tidy:
	$(GOMOD) tidy

## deps: Download dependencies
deps:
	$(GOMOD) download

## clean: Clean build artifacts
clean:
	rm -f $(BINARY_NAME)
	rm -f coverage.out coverage.html

## docker: Build Docker image
docker:
	docker build -t $(DOCKER_IMAGE):$(DOCKER_TAG) .

## docker-run: Run Docker container
docker-run:
	docker run -p 7777:7777 \
		-e GITHUB_WEBHOOK_SECRET=$(GITHUB_WEBHOOK_SECRET) \
		-e GITHUB_TOKEN=$(GITHUB_TOKEN) \
		-e OPENCODE_HOST=$(OPENCODE_HOST) \
		-e OPENCODE_API_KEY=$(OPENCODE_API_KEY) \
		$(DOCKER_IMAGE):$(DOCKER_TAG)

## compose-up: Start with docker-compose
compose-up:
	docker-compose up -d

## compose-down: Stop docker-compose
compose-down:
	docker-compose down

## compose-logs: View docker-compose logs
compose-logs:
	docker-compose logs -f

## all: Run fmt, lint, test, and build
all: fmt lint test build
