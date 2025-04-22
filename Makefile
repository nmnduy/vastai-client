# Project name vastai-client
PROJECT_NAME = vastai-client

# Read version from VERSION file
VERSION := $(shell cat VERSION)

# Docker related variables
# Define your docker registry/username here
DOCKER_REGISTRY ?= localhost:30050
SERVER_IMAGE = $(DOCKER_REGISTRY)/$(PROJECT_NAME)-server:$(VERSION)
WORKER_IMAGE = $(DOCKER_REGISTRY)/$(PROJECT_NAME)-worker:$(VERSION)
CLEANUP_IMAGE = $(DOCKER_REGISTRY)/$(PROJECT_NAME)-cleanup:$(VERSION)
# Dockerfiles
SERVER_DOCKERFILE = docker/Dockerfile.server
WORKER_DOCKERFILE = docker/Dockerfile.worker
CLEANUP_DOCKERFILE = docker/Dockerfile.cleanup

# Directory for binaries
BIN_DIR = bin

# Go source files for each application
SERVER_SRC = $(shell find cmd/server internal -name '*.go') # Include internal for dependencies
WORKER_SRC = $(shell find cmd/worker internal -name '*.go') # Include internal for dependencies
CLEANUP_SRC = $(shell find cmd/cleanup internal -name '*.go') # Include internal for dependencies

# Proto files and generated Go files
PROTO_FILE = internal/grpc/service.proto
PROTO_GO_FILES = internal/grpc/service.pb.go internal/grpc/service_grpc.pb.go

# Default target: build all applications
.PHONY: all
all: build

# Build all applications
.PHONY: build
build: $(BIN_DIR)/server $(BIN_DIR)/worker $(BIN_DIR)/cleanup

# Generate gRPC code from proto file
.PHONY: proto
proto: $(PROTO_GO_FILES)

$(PROTO_GO_FILES): $(PROTO_FILE)
	# Ensure the gRPC code generator plugin is installed
	@echo "Installing/updating protoc-gen-go-grpc..."
	@go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
	@echo "Generating gRPC code..."
	@protoc --go_out=. --go_opt=paths=source_relative \
	       --go-grpc_out=. --go-grpc_opt=paths=source_relative \
	       $(PROTO_FILE)

# Ensure bin directory exists
$(BIN_DIR):
	@mkdir -p $(BIN_DIR)

# Build the server application
$(BIN_DIR)/server: $(SERVER_SRC) $(PROTO_GO_FILES) | $(BIN_DIR)
	@echo "Building server application..."
	@CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o $@ ./cmd/server

# Build the worker application
$(BIN_DIR)/worker: $(WORKER_SRC) $(PROTO_GO_FILES) | $(BIN_DIR)
	@echo "Building worker application..."
	@CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o $@ ./cmd/worker

# Build the cleanup application
$(BIN_DIR)/cleanup: $(CLEANUP_SRC) | $(BIN_DIR)
	@echo "Building cleanup application..."
	@CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o $@ ./cmd/cleanup

# --- Docker Targets ---

# Build all docker images
.PHONY: docker-build
docker-build: docker-build-server docker-build-worker docker-build-cleanup

# Build server docker image
.PHONY: docker-build-server
docker-build-server: $(BIN_DIR)/server $(SERVER_DOCKERFILE)
	@echo "Building server Docker image: $(SERVER_IMAGE)"
	@docker build -f $(SERVER_DOCKERFILE) -t $(SERVER_IMAGE) .

# Build worker docker image
.PHONY: docker-build-worker
docker-build-worker: $(BIN_DIR)/worker $(WORKER_DOCKERFILE)
	@echo "Building worker Docker image: $(WORKER_IMAGE)"
	@docker build -f $(WORKER_DOCKERFILE) -t $(WORKER_IMAGE) .

# Build cleanup docker image
.PHONY: docker-build-cleanup
docker-build-cleanup: $(BIN_DIR)/cleanup $(CLEANUP_DOCKERFILE)
	@echo "Building cleanup Docker image: $(CLEANUP_IMAGE)"
	@docker build -f $(CLEANUP_DOCKERFILE) -t $(CLEANUP_IMAGE) .

# Push all docker images
.PHONY: docker-push
docker-push: docker-push-server docker-push-worker docker-push-cleanup

# Push server docker image
.PHONY: docker-push-server
docker-push-server: docker-build-server
	@echo "Pushing server Docker image: $(SERVER_IMAGE)"
	@docker push $(SERVER_IMAGE)

# Push worker docker image
.PHONY: docker-push-worker
docker-push-worker: docker-build-worker
	@echo "Pushing worker Docker image: $(WORKER_IMAGE)"
	@docker push $(WORKER_IMAGE)

# Push cleanup docker image
.PHONY: docker-push-cleanup
docker-push-cleanup: docker-build-cleanup
	@echo "Pushing cleanup Docker image: $(CLEANUP_IMAGE)"
	@docker push $(CLEANUP_IMAGE)

# Clean up docker images
.PHONY: docker-clean
docker-clean:
	@echo "Removing Docker images..."
	-@docker rmi $(SERVER_IMAGE) $(WORKER_IMAGE) $(CLEANUP_IMAGE) 2>/dev/null || true


# Clean up generated files and binaries
.PHONY: clean
clean:
	@echo "Cleaning up Go binaries and generated proto files..."
	@rm -rf $(BIN_DIR)
	@rm -f $(PROTO_GO_FILES)
	@echo "Consider running 'make docker-clean' to remove Docker images."

# Run applications (optional convenience targets)
.PHONY: run-server
run-server: $(BIN_DIR)/server
	@echo "Running server..."
	@$(BIN_DIR)/server

.PHONY: run-worker
run-worker: $(BIN_DIR)/worker
	@echo "Running worker..."
	@# Add necessary environment variables like WORKER_AUTH_TOKEN here
	@$(BIN_DIR)/worker

.PHONY: run-cleanup
run-cleanup: $(BIN_DIR)/cleanup
	@echo "Running cleanup..."
	@$(BIN_DIR)/cleanup

# Help target
.PHONY: help
help:
	@echo "Available targets:"
	@echo "  all                 Build all applications (default)"
	@echo "  build               Alias for 'all'"
	@echo "  proto               Generate gRPC code"
	@echo "  server              Build the server application"
	@echo "  worker              Build the worker application"
	@echo "  cleanup             Build the cleanup application"
	@echo "  docker-build        Build all Docker images"
	@echo "  docker-build-server Build the server Docker image"
	@echo "  docker-build-worker Build the worker Docker image"
	@echo "  docker-build-cleanup Build the cleanup Docker image"
	@echo "  docker-push         Push all Docker images to the registry ($(DOCKER_REGISTRY))"
	@echo "  docker-push-server  Push the server Docker image"
	@echo "  docker-push-worker  Push the worker Docker image"
	@echo "  docker-push-cleanup Push the cleanup Docker image"
	@echo "  run-server          Run the server application locally"
	@echo "  run-worker          Run the worker application locally"
	@echo "  run-cleanup         Run the cleanup application locally"
	@echo "  clean               Remove generated Go files and binaries"
	@echo "  docker-clean        Remove built Docker images locally"
	@echo "  help                Show this help message"
	@echo ""
	@echo "Configuration:"
	@echo "  DOCKER_REGISTRY     Set the Docker registry/username (current: $(DOCKER_REGISTRY))"
	@echo "                      Example: make docker-push DOCKER_REGISTRY=myuser"

