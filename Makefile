# Directory for binaries
BIN_DIR = bin

# Go source files for each application
SERVER_SRC = $(shell find cmd/server -name '*.go')
WORKER_SRC = $(shell find cmd/worker -name '*.go')
CLEANUP_SRC = $(shell find cmd/cleanup -name '*.go')

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
	@go build -o $@ ./cmd/server

# Build the worker application
$(BIN_DIR)/worker: $(WORKER_SRC) $(PROTO_GO_FILES) | $(BIN_DIR)
	@echo "Building worker application..."
	@go build -o $@ ./cmd/worker

# Build the cleanup application
$(BIN_DIR)/cleanup: $(CLEANUP_SRC) | $(BIN_DIR)
	@echo "Building cleanup application..."
	@go build -o $@ ./cmd/cleanup

# Clean up generated files and binaries
.PHONY: clean
clean:
	@echo "Cleaning up..."
	@rm -rf $(BIN_DIR)
	@rm -f $(PROTO_GO_FILES)

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
	@echo "  all        Build all applications (default)"
	@echo "  build      Alias for 'all'"
	@echo "  proto      Generate gRPC code"
	@echo "  server     Build the server application"
	@echo "  worker     Build the worker application"
	@echo "  cleanup    Build the cleanup application"
	@echo "  run-server Run the server application"
	@echo "  run-worker Run the worker application"
	@echo "  run-cleanup Run the cleanup application"
	@echo "  clean      Remove generated files and binaries"
	@echo "  help       Show this help message"

