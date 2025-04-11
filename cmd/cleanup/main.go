package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/nmnduy/vastai-client/internal/db"
	"github.com/nmnduy/vastai-client/internal/s3"
	// TODO: Import a Parquet writer library (e.g., github.com/parquet-go/parquet-go)
	// in the `db` package where streaming happens.
)

const (
	// Retention periods
	workerAuthTokenRetention = 30 * 24 * time.Hour  // 1 month
	instanceStatusRetention  = 365 * 24 * time.Hour // 1 year
	jobStatusRetention       = 90 * 24 * time.Hour  // 3 months
)

// S3Uploader defines the interface for uploading data streams to S3.
// This allows using the real S3 client or mocks for testing.
type S3Uploader interface {
	UploadStream(ctx context.Context, bucket, key string, body io.Reader) error
}

var _ S3Uploader = (*s3.Client)(nil)

func main() {
	log.Println("Starting cleanup job...")

	// --- Configuration ---
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		log.Fatal("DATABASE_URL environment variable is required")
	}

	s3Bucket := os.Getenv("S3_BUCKET")
	if s3Bucket == "" {
		log.Fatal("S3_BUCKET environment variable is required")
	}
	// S3 client will automatically use AWS env vars like AWS_REGION, AWS_ACCESS_KEY_ID, etc.

	// --- Initialization ---
	dbClient, err := db.NewDB(ctx)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer func() {
		if err := dbClient.Close(); err != nil {
			log.Printf("Error closing database connection: %v", err)
		}
	}()
	log.Println("Database connection established.")

	// Initialize the real S3 client
	s3Client, err := s3.NewClient(ctx)
	if err != nil {
		log.Fatalf("Failed to initialize S3 client: %v", err)
	}
	log.Println("S3 client initialized.")
	// Removed placeholder S3 client instantiation

	// --- Run Cleanup ---
	log.Println("Running cleanup process...")
	runCleanupCycle(ctx, dbClient, s3Client, s3Bucket)

	// Check if the context was cancelled (e.g., by SIGINT/SIGTERM) during the cleanup
	if ctx.Err() != nil {
		log.Printf("Cleanup interrupted by signal: %v", ctx.Err())
		os.Exit(1) // Indicate abnormal termination
	}

	log.Println("Cleanup job finished successfully.")
}

// runCleanupCycle performs one full cleanup cycle for all tables.
// No changes needed in this function or below as it uses the S3Uploader interface.
func runCleanupCycle(ctx context.Context, dbClient *db.DB, s3Client S3Uploader, s3Bucket string) {
	// ... rest of the function remains the same ...
	log.Println("Starting cleanup cycle...")

	// Define cleanup tasks for each table
	tasks := []struct {
		tableName       string
		retentionPeriod time.Duration
		// streamFunc should now stream data in Parquet format.
		// The database layer (`db` package) is responsible for generating the Parquet stream.
		streamFunc func(context.Context, time.Duration) (io.Reader, int64, error) // Returns reader, count, error
		deleteFunc func(context.Context, time.Duration) (int64, error)            // Accepts retention, returns deleted count, error
	}{
		{
			tableName:       "worker_auth_token",
			retentionPeriod: workerAuthTokenRetention,
			streamFunc: func(ctx context.Context, retention time.Duration) (io.Reader, int64, error) {
				// TODO: Implement db.StreamOldWorkerAuthTokensAsParquet(ctx, retention)
				log.Printf("Placeholder: Streaming %s older than %v as Parquet", "worker_auth_token", retention)
				// Simulate no data for now to avoid blocking on unimplemented feature
				// In real implementation, return the stream from the db package.
				return nil, 0, fmt.Errorf("parquet streaming not implemented for worker_auth_token")

				// Example of how it might look when implemented:
				// return dbClient.StreamOldWorkerAuthTokensAsParquet(ctx, retention)
			},
			deleteFunc: dbClient.DeleteOldWorkerAuthTokens,
		},
		{
			tableName:       "instance_status",
			retentionPeriod: instanceStatusRetention,
			streamFunc: func(ctx context.Context, retention time.Duration) (io.Reader, int64, error) {
				// TODO: Implement db.StreamOldInstanceStatusesAsParquet(ctx, retention)
				log.Printf("Placeholder: Streaming %s older than %v as Parquet", "instance_status", retention)
				return nil, 0, fmt.Errorf("parquet streaming not implemented for instance_status")
				// Example:
				// return dbClient.StreamOldInstanceStatusesAsParquet(ctx, retention)
			},
			deleteFunc: dbClient.DeleteOldInstanceStatuses,
		},
		{
			tableName:       "job_status",
			retentionPeriod: jobStatusRetention,
			streamFunc: func(ctx context.Context, retention time.Duration) (io.Reader, int64, error) {
				// TODO: Implement db.StreamOldJobStatusesAsParquet(ctx, retention)
				log.Printf("Placeholder: Streaming %s older than %v as Parquet", "job_status", retention)
				return nil, 0, fmt.Errorf("parquet streaming not implemented for job_status")
				// Example:
				// return dbClient.StreamOldJobStatusesAsParquet(ctx, retention)
			},
			deleteFunc: dbClient.DeleteOldJobStatuses,
		},
	}

	// Execute cleanup tasks
	for _, task := range tasks {
		// Check if context was cancelled before starting next task
		if ctx.Err() != nil {
			log.Printf("Cleanup cycle interrupted: %v", ctx.Err())
			return // Stop processing further tables
		}
		if err := cleanupTable(ctx, s3Client, s3Bucket, task.tableName, task.retentionPeriod, task.streamFunc, task.deleteFunc); err != nil {
			// Log error but continue, unless it's a context cancellation error which should have been caught earlier
			log.Printf("ERROR: Failed to cleanup table %s: %v", task.tableName, err)
			// Check again if the error was due to cancellation during the task execution
			if ctx.Err() != nil {
				log.Printf("Cleanup cycle interrupted during task for %s: %v", task.tableName, ctx.Err())
				return // Stop processing further tables if context cancelled
			}
			// Continue to the next table even if one fails for other reasons
		}
	}

	log.Println("Cleanup cycle finished.")
}

// cleanupTable handles the streaming, uploading, and deleting for a single table.
// No changes needed in this function or below as it uses the S3Uploader interface.
func cleanupTable(
	ctx context.Context,
	s3Client S3Uploader,
	s3Bucket string,
	tableName string,
	retentionPeriod time.Duration,
	streamFunc func(context.Context, time.Duration) (io.Reader, int64, error),
	deleteFunc func(context.Context, time.Duration) (int64, error),
) error {
	log.Printf("Processing cleanup for table: %s (retention: %v)", tableName, retentionPeriod)

	// Check for context cancellation before starting expensive operations
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("context cancelled before processing table %s: %w", tableName, err)
	}

	// 1. Stream old records to a Parquet reader
	parquetStream, recordCount, err := streamFunc(ctx, retentionPeriod)
	if err != nil {
		// Specific handling for "not implemented" errors vs. real errors
		if err.Error() == fmt.Sprintf("parquet streaming not implemented for %s", tableName) {
			log.Printf("Skipping %s: Parquet streaming function not implemented.", tableName)
			return nil // Don't treat as a fatal error for the cycle
		}
		// Check if the error was due to context cancellation during streaming
		if ctx.Err() != nil {
			return fmt.Errorf("context cancelled during streaming for %s: %w", tableName, ctx.Err())
		}
		return fmt.Errorf("failed to stream data for %s: %w", tableName, err)
	}
	// Ensure stream resources are released, especially if it holds DB connections etc.
	// Use a flag to track if the stream was successfully created and needs closing.
	streamNeedsClosing := parquetStream != nil
	if streamNeedsClosing {
		if closer, ok := parquetStream.(io.Closer); ok {
			defer closer.Close() // Ensure closure even on errors/panics later
		}
	}

	if recordCount == 0 {
		log.Printf("No old records found for %s matching retention period.", tableName)
		return nil // Nothing to archive or delete
	}
	log.Printf("Found %d records to archive for %s.", recordCount, tableName)

	// Check for context cancellation before uploading
	if err := ctx.Err(); err != nil {
		// Attempt to close the stream reader if possible, even on cancellation before upload
		if streamNeedsClosing {
			if closer, ok := parquetStream.(io.Closer); ok {
				closer.Close() // Best effort close
			}
		}
		return fmt.Errorf("context cancelled before S3 upload for %s: %w", tableName, err)
	}

	// 2. Upload the Parquet stream to S3
	archiveFileName := generateArchiveFileName(tableName) // Generates a .parquet filename
	log.Printf("Uploading %s archive to s3://%s/%s", tableName, s3Bucket, archiveFileName)

	err = s3Client.UploadStream(ctx, s3Bucket, archiveFileName, parquetStream)
	// Close the stream explicitly *after* the upload attempt (or cancellation check)
	// This ensures resources are released even if upload fails or context is cancelled during upload.
	if streamNeedsClosing {
		if closer, ok := parquetStream.(io.Closer); ok {
			// Calling Close() multiple times is often safe for io.Closers.
			// The defer above handles the general case, this ensures it's closed *before* delete.
			closeErr := closer.Close()
			if closeErr != nil {
				log.Printf("Warning: error closing parquet stream for %s after upload attempt: %v", tableName, closeErr)
			}
		}
	}

	if err != nil {
		// Check if the error was due to context cancellation during upload
		if ctx.Err() != nil {
			return fmt.Errorf("context cancelled during S3 upload for %s: %w", tableName, ctx.Err())
		}
		return fmt.Errorf("failed to upload archive %s to S3: %w", archiveFileName, err)
	}
	log.Printf("Successfully uploaded archive %s to S3.", archiveFileName)

	// Check for context cancellation before deleting
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("context cancelled before deleting records for %s: %w", tableName, err)
	}

	// 3. Delete the old records from the database
	log.Printf("Deleting archived records from %s older than %v...", tableName, retentionPeriod)
	deletedCount, err := deleteFunc(ctx, retentionPeriod) // Pass retention period to delete func
	if err != nil {
		// Check if the error was due to context cancellation during delete
		if ctx.Err() != nil {
			log.Printf("CRITICAL ERROR: Context cancelled during deletion for %s after successful S3 upload: %v", tableName, ctx.Err())
			log.Printf("Manual cleanup for %s might be required.", tableName)
			return fmt.Errorf("context cancelled during delete for %s: %w", tableName, ctx.Err()) // Return error as delete didn't complete
		}

		// Critical: Log failure but don't return error immediately if upload succeeded
		// We might want manual intervention or retry logic here in a real scenario.
		// For now, just log it prominently.
		log.Printf("CRITICAL ERROR: Failed to delete archived records from %s after successful S3 upload: %v", tableName, err)
		log.Printf("Manual cleanup for %s might be required.", tableName)
		// Depending on requirements, you might return the error or not.
		// Not returning error allows the overall job to report success if upload worked,
		// but risks leaving old data. Returning error makes the failure explicit.
		// Let's return the error to make the cron job fail clearly.
		return fmt.Errorf("failed to delete records from %s: %w", tableName, err)
	}

	// Optional: Verify deleted count matches archived count
	if deletedCount != recordCount {
		log.Printf("WARNING: Archived %d records for %s but deleted %d records.", recordCount, tableName, deletedCount)
		// Decide if this is an error condition based on application logic.
		// It might indicate a race condition or issue with the delete logic.
	} else {
		log.Printf("Successfully deleted %d archived records from %s.", deletedCount, tableName)
	}

	return nil
}

// generateArchiveFileName creates a unique filename for the archive based on table and timestamp.
// Now generates a .parquet extension.
func generateArchiveFileName(tableName string) string {
	timestamp := time.Now().UTC().Format("20060102-150405") // YYYYMMDD-HHMMSS
	return fmt.Sprintf("archive-%s-%s.parquet", tableName, timestamp)
}
