# Intro 🚀

Server to run AI job using vast ai 🤖

## Wiki 📚

For architecture see docs/architecture.md
For vast AI node management see docs/vast_ai.md

## How to Build 🏗️

This project uses a `Makefile` to streamline the build process. Ensure you have `make` and the Go toolchain installed.

*   To build all applications (server, worker, cleanup):
    ```bash
    make all
    # or equivalently
    make build
    ```
*   To build only the server:
    ```bash
    make server
    ```
*   To build only the worker:
    ```bash
    make worker
    ```
*   To build only the cleanup tool:
    ```bash
    make cleanup
    ```
*   To generate the gRPC Go code from the `.proto` definition (this is usually done automatically when building applications that depend on it):
    ```bash
    make proto
    ```
*   To remove all generated binaries and gRPC code:
    ```bash
    make clean
    ```

The compiled binaries will be placed in the `bin/` directory.
