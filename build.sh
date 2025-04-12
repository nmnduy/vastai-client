#!/bin/bash

set -e

# Generate gRPC code from the .proto file
echo "Generating gRPC code..."
protoc --go_out=. --go_opt=paths=source_relative \
       --go-grpc_out=. --go-grpc_opt=paths=source_relative \
       internal/grpc/service.proto

# The errors like "package internal/db is not in std" or "package cmd/server is not in std"
# usually indicate that the Go command is not recognizing the project as a module correctly,
# often resolved by running the build from the project root (where go.mod is)
# and ensuring dependencies are tidy (handled by 'go mod tidy' above).
# The error "found packages server (grpc.go) and main (main.go)" indicates a code issue:
# all .go files in cmd/server must declare 'package main'. This script cannot fix that,
# the code in cmd/server/grpc.go needs to be changed from 'package server' to 'package main'.
echo "Building server application..."
go build -o bin/server ./cmd/server

# 4. Build the worker application
echo "Building worker application..."
go build -o bin/worker ./cmd/worker

# 5. Build the cleanup application
echo "Building cleanup application..."
go build -o bin/cleanup ./cmd/cleanup

echo "Build finished. Binaries are in ./bin/"
