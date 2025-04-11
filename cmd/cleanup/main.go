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

	"github.com/parquet-go/parquet-go" // Import the parquet library

	"github.com/nmnduy/vastai-client/internal/db"
	"github.com/nmnduy/vastai-client/internal/s3"
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
func runCleanupCycle(ctx context.Context, dbClient *db.DB, s3Client S3Uploader, s3Bucket string) {
	log.Println("Starting cleanup cycle...")

	// Define cleanup tasks for each table
	tasks := []struct {
		tableName       string
		retentionPeriod time.Duration
		// streamFunc now fetches raw data and generates Parquet stream in-memory via io.Pipe.
		streamFunc func(context.Context, time.Duration) (io.Reader, int64, error) // Returns reader, count, error
		deleteFunc func(context.Context, time.Duration) (int64, error)            // Accepts retention, returns deleted count, error
	}{
		{
			tableName:       "worker_auth_token",
			retentionPeriod: workerAuthTokenRetention,
			streamFunc: func(ctx context.Context, retention time.Duration) (io.Reader, int64, error) {
				records, count, err := dbClient.GetOldWorkerAuthTokens(ctx, retention)
				if err != nil || count == 0 {
					return nil, count, err // Return DB error or nil if no records
				}

				// Create a pipe: writer writes to it, reader reads from it.
				pr, pw := io.Pipe()

				// Start a goroutine to write Parquet data to the pipe writer.
				go func() {
					// Ensure the pipe writer is closed when the goroutine finishes.
					// If an error occurs, close with the error; otherwise, close normally.
					var writeErr error
					defer func() {
						pw.CloseWithError(writeErr)
					}()

					// Create a Parquet writer targeting the pipe writer.
					// parquet.SchemaOf automatically derives the schema from the struct.
					writer := parquet.NewWriter(pw, parquet.SchemaOf(new(db.WorkerAuthToken)))

					// Write all fetched records to the Parquet writer.
					writeErr = writer.Write(records)
					if writeErr != nil {
						log.Printf("ERROR writing parquet for %s: %v", "worker_auth_token", writeErr)
						return // Error will be propagated via pw.CloseWithError(writeErr)
					}

					// Close the Parquet writer to finalize the file structure.
					writeErr = writer.Close()
					if writeErr != nil {
						log.Printf("ERROR closing parquet writer for %s: %v", "worker_auth_token", writeErr)
						// Error will be propagated via pw.CloseWithError(writeErr)
					}
				}()

				// Return the pipe reader, record count, and no error (yet).
				// Errors from the goroutine will be surfaced when the reader is used (e.g., by S3 upload).
				return pr, count, nil
			},
			deleteFunc: dbClient.DeleteOldWorkerAuthTokens,
		},
		{
			tableName:       "instance_status",
			retentionPeriod: instanceStatusRetention,
			streamFunc: func(ctx context.Context, retention time.Duration) (io.Reader, int64, error) {
				records, count, err := dbClient.GetOldInstanceStatuses(ctx, retention)
				if err != nil || count == 0 {
					return nil, count, err
				}

				pr, pw := io.Pipe()
				go func() {
					var writeErr error
					defer func() {
						pw.CloseWithError(writeErr)
					}()
					writer := parquet.NewWriter(pw, parquet.SchemaOf(new(db.InstanceStatus)))
					writeErr = writer.Write(records)
					if writeErr != nil {
						log.Printf("ERROR writing parquet for %s: %v", "instance_status", writeErr)
						return
					}
					writeErr = writer.Close()
					if writeErr != nil {
						log.Printf("ERROR closing parquet writer for %s: %v", "instance_status", writeErr)
					}
				}()
				return pr, count, nil
			},
			deleteFunc: dbClient.DeleteOldInstanceStatuses,
		},
		{
			tableName:       "job_status",
			retentionPeriod: jobStatusRetention,
			streamFunc: func(ctx context.Context, retention time.Duration) (io.Reader, int64, error) {
				records, count, err := dbClient.GetOldJobStatuses(ctx, retention)
				if err != nil || count == 0 {
					return nil, count, err
				}

				pr, pw := io.Pipe()
				go func() {
					var writeErr error
					defer func() {
						pw.CloseWithError(writeErr)
					}()
					// Need a helper struct for Parquet as parquet-go doesn't directly handle sql.Null* types well.
					type JobStatusParquet struct {
						ID         int       `parquet:"id"`
						JobID      string    `parquet:"job_id"`
						Status     string    `parquet:"status"`
						CreatedAt  time.Time `parquet:"created_at"`
						Error      *string   `parquet:"error,optional"`
						Result     *string   `parquet:"result,optional"`
						InstanceID *int64    `parquet:"instance_id,optional"`
						Input      *string   `parquet:"input,optional"`
					}

					// Map db.JobStatus to JobStatusParquet before writing
					parquetRecords := make([]JobStatusParquet, len(records))
					for i, r := range records {
						parquetRecords[i] = JobStatusParquet{
							ID:        r.ID,
							JobID:     r.JobID,
							Status:    r.Status,
							CreatedAt: r.CreatedAt,
						}
						// Handle nullable fields
						if r.Error.Valid {
							parquetRecords[i].Error = &r.Error.String
						}
						if r.Result.Valid {
							parquetRecords[i].Result = &r.Result.String
						}
						if r.InstanceID.Valid {
							parquetRecords[i].InstanceID = &r.InstanceID.Int64
						}
						if r.Input.Valid {
							parquetRecords[i].Input = &r.Input.String
						}
					}

					writer := parquet.NewWriter(pw, parquet.SchemaOf(new(JobStatusParquet)))
					writeErr = writer.Write(parquetRecords)
					if writeErr != nil {
						log.Printf("ERROR writing parquet for %s: %v", "job_status", writeErr)
						return
					}
					writeErr = writer.Close()
					if writeErr != nil {
						log.Printf("ERROR closing parquet writer for %s: %v", "job_status", writeErr)
					}
				}()
				return pr, count, nil
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

	// 1. Get the Parquet reader by executing the streamFunc
	// The streamFunc now fetches data and sets up the Parquet encoding goroutine via io.Pipe
	parquetStreamReader, recordCount, err := streamFunc(ctx, retentionPeriod)
	if err != nil {
		// Check if the error was due to context cancellation during DB fetch
		if ctx.Err() != nil {
			return fmt.Errorf("context cancelled during data fetch for %s: %w", tableName, ctx.Err())
		}
		// Handle DB errors or other setup errors from streamFunc
		return fmt.Errorf("failed to initiate streaming for %s: %w", tableName, err)
	}

	// Ensure pipe reader resources are eventually closed
	streamNeedsClosing := parquetStreamReader != nil
	if streamNeedsClosing {
		// The reader is an *io.PipeReader, ensure it's closed to unblock the writer goroutine in case of errors below.
		if closer, ok := parquetStreamReader.(io.Closer); ok {
			defer closer.Close()
		}
	}

	if recordCount == 0 {
		log.Printf("No old records found for %s matching retention period.", tableName)
		// Close the reader explicitly if it was created, even if count is 0 (shouldn't happen often)
		if streamNeedsClosing {
			if closer, ok := parquetStreamReader.(io.Closer); ok {
				closer.Close()
			}
		}
		return nil // Nothing to archive or delete
	}
	log.Printf("Found %d records to archive for %s.", recordCount, tableName)

	// Check for context cancellation before uploading
	if err := ctx.Err(); err != nil {
		// Attempt to close the stream reader if possible, even on cancellation before upload
		// This helps signal the writer goroutine to stop.
		if streamNeedsClosing {
			if closer, ok := parquetStreamReader.(io.Closer); ok {
				closer.Close() // Best effort close
			}
		}
		return fmt.Errorf("context cancelled before S3 upload for %s: %w", tableName, err)
	}

	// 2. Upload the Parquet stream to S3
	archiveFileName := generateArchiveFileName(tableName) // Generates a .parquet filename
	log.Printf("Uploading %s archive to s3://%s/%s", tableName, s3Bucket, archiveFileName)

	// UploadStream will read from parquetStreamReader, which pulls data from the goroutine writing parquet data.
	err = s3Client.UploadStream(ctx, s3Bucket, archiveFileName, parquetStreamReader)

	// Close the pipe reader *after* the upload attempt. This is crucial.
	// Closing the reader signals EOF to the S3 client (if upload was successful)
	// or signals an error to the writer goroutine if the upload failed mid-stream.
	if streamNeedsClosing {
		if closer, ok := parquetStreamReader.(io.Closer); ok {
			closeErr := closer.Close()
			if closeErr != nil {
				// Logging this might be noisy if the writer goroutine already closed with error.
				// log.Printf("Warning: error closing parquet stream reader for %s after upload attempt: %v", tableName, closeErr)
			}
		}
	}

	if err != nil {
		// Check if the error was due to context cancellation during upload
		if ctx.Err() != nil {
			return fmt.Errorf("context cancelled during S3 upload for %s: %w", tableName, ctx.Err())
		}
		// Check if the error came from the Parquet writing goroutine via the pipe
		if pipeErr, ok := err.(*io.PipeError); ok {
			return fmt.Errorf("failed during parquet generation/streaming for %s: %w", tableName, pipeErr.Err)
		}
		// Otherwise, it's likely an S3 upload error
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
		log.Printf("CRITICAL ERROR: Failed to delete archived records from %s after successful S3 upload: %v", tableName, err)
		log.Printf("Manual cleanup for %s might be required.", tableName)
		return fmt.Errorf("failed to delete records from %s: %w", tableName, err) // Return error to make the job fail clearly.
	}

	// Optional: Verify deleted count matches archived count
	if deletedCount != recordCount {
		log.Printf("WARNING: Archived %d records for %s but deleted %d records.", recordCount, tableName, deletedCount)
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
