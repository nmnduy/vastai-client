#!/bin/bash

set -e

# Generate gRPC code from the .proto file
echo "Generating gRPC code..."
protoc --go_out=. --go_opt=paths=source_relative \
       --go-grpc_out=. --go-grpc_opt=paths=source_relative \
       internal/grpc/service.proto

echo "Building server application..."
go build -o bin/server ./cmd/server

# 4. Build the worker application
echo "Building worker application..."
go build -o bin/worker ./cmd/worker

# 5. Build the cleanup application
echo "Building cleanup application..."
go build -o bin/cleanup ./cmd/cleanup

echo "Build finished. Binaries are in ./bin/"
