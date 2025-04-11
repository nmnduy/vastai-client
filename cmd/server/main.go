package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"cmd/server"
	dbpkg "internal/db"
)

func main() {
	// Create a context that is cancelled when the program receives an interrupt signal
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Listen for OS interrupts
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, syscall.SIGINT, syscall.SIGTERM)

	// Initialize the database connection
	db, err := dbpkg.NewDB(ctx)
	if err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			log.Printf("Error closing database connection: %v", err)
		}
	}()

	// Start the gRPC server in a goroutine
	go func() {
		if err := server.ServeGRPC(db); err != nil {
			log.Fatalf("Failed to start gRPC server: %v", err)
		}
	}()

	// Handle OS interrupts
	<-signalChan
	log.Println("Received interrupt signal. Shutting down...")
}
