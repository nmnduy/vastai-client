package main

import (
	"context"
	"log"

	"github.com/nmnduy/vastai-client/internal/db"
)

func main() {
	// Create a background context.
	ctx := context.Background()

	// Initialize the database connection.
	database, err := db.NewDB(ctx)
	if err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	defer database.Close()

	// Delete old worker auth tokens.
	if err := database.DeleteOldWorkerAuthTokens(ctx); err != nil {
		log.Printf("Failed to delete old worker auth tokens: %v", err)
	} else {
		log.Println("Successfully deleted old worker auth tokens.")
	}

	// Delete old instance statuses.
	if err := database.DeleteOldInstanceStatuses(ctx); err != nil {
		log.Printf("Failed to delete old instance statuses: %v", err)
	} else {
		log.Println("Successfully deleted old instance statuses.")
	}

	// Delete old job statuses.
	if err := database.DeleteOldJobStatuses(ctx); err != nil {
		log.Printf("Failed to delete old job statuses: %v", err)
	} else {
		log.Println("Successfully deleted old job statuses.")
	}

	log.Println("Cleanup completed.")
}
