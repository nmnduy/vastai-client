package main

import (
	"context"
	"errors" // Import errors package
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/parquet-go/parquet-go"

	"github.com/nmnduy/vastai-client/internal/db"
	"github.com/nmnduy/vastai-client/internal/s3"
)

const (
	// Retention periods
	workerAuthTokenRetention = 30 * 24 * time.Hour  // 1 month
	instanceStatusRetention  = 365 * 24 * time.Hour // 1 year
	jobStatusRetention       = 90 * 24 * time.Hour  // 3 months

	// Database query page size for streaming
	dbPageSize = 20000
)

// S3Uploader defines the interface for uploading data streams to S3.
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
		// Attempt to close DB connection gracefully.
		if closeErr := dbClient.Close(); closeErr != nil {
			log.Printf("Error closing database connection: %v", closeErr)
		} else {
			log.Println("Database connection closed.")
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
		// streamFunc now returns a reader, channels for count and error, and an initial setup error.
		streamFunc func(context.Context, time.Duration) (io.Reader, <-chan int64, <-chan error, error)
		deleteFunc func(context.Context, time.Duration) (int64, error)
	}{
		{
			tableName:       "worker_auth_token",
			retentionPeriod: workerAuthTokenRetention,
			streamFunc: func(ctx context.Context, retention time.Duration) (io.Reader, <-chan int64, <-chan error, error) {
				pr, pw := io.Pipe()
				countCh := make(chan int64, 1)
				errCh := make(chan error, 1)

				go func() {
					var writeErr error
					var totalCount int64
					defer func() {
						// Ensure channels are written to and pipe is closed on exit.
						// Only send count if successful.
						if writeErr == nil {
							countCh <- totalCount
						}
						errCh <- writeErr           // Send nil on success, error otherwise
						pw.CloseWithError(writeErr) // Close pipe with error or nil
					}()

					// Create Parquet writer targeting the pipe. Use struct tags for schema.
					writer := parquet.NewWriter(pw, parquet.SchemaOf(new(db.WorkerAuthToken)))

					offset := 0
					for {
						// Check for cancellation before each DB query
						if err := ctx.Err(); err != nil {
							writeErr = fmt.Errorf("context cancelled during %s paging: %w", "worker_auth_token", err)
							return
						}

						// Fetch a page of records
						records, fetchErr := dbClient.GetOldWorkerAuthTokensPaged(ctx, retention, dbPageSize, offset)
						if fetchErr != nil {
							writeErr = fmt.Errorf("failed to fetch %s page (offset %d): %w", "worker_auth_token", offset, fetchErr)
							return
						}

						// Stop if no more records
						if len(records) == 0 {
							break
						}

						// Write the fetched batch to the Parquet writer
						if wErr := writer.Write(records); wErr != nil {
							writeErr = fmt.Errorf("failed to write %s parquet batch (offset %d): %w", "worker_auth_token", offset, wErr)
							return
						}

						totalCount += int64(len(records))
						offset += len(records) // Correctly increment offset by records processed

						// Optional: Log progress periodically
						// if offset % (dbPageSize * 10) == 0 { // Log every 10 pages
						//     log.Printf("Archiving %s: processed %d records...", "worker_auth_token", totalCount)
						// }
					}

					// Close the Parquet writer to finalize the file structure
					writeErr = writer.Close()
					if writeErr != nil {
						log.Printf("ERROR closing parquet writer for %s: %v", "worker_auth_token", writeErr)
					}
				}()

				return pr, countCh, errCh, nil // Initial setup error is nil
			},
			deleteFunc: dbClient.DeleteOldWorkerAuthTokens,
		},
		{
			tableName:       "instance_status",
			retentionPeriod: instanceStatusRetention,
			streamFunc: func(ctx context.Context, retention time.Duration) (io.Reader, <-chan int64, <-chan error, error) {
				pr, pw := io.Pipe()
				countCh := make(chan int64, 1)
				errCh := make(chan error, 1)

				go func() {
					var writeErr error
					var totalCount int64
					defer func() {
						if writeErr == nil {
							countCh <- totalCount
						}
						errCh <- writeErr
						pw.CloseWithError(writeErr)
					}()

					writer := parquet.NewWriter(pw, parquet.SchemaOf(new(db.InstanceStatus))) // Uses struct tags

					offset := 0
					for {
						if err := ctx.Err(); err != nil {
							writeErr = fmt.Errorf("context cancelled during %s paging: %w", "instance_status", err)
							return
						}
						records, fetchErr := dbClient.GetOldInstanceStatusesPaged(ctx, retention, dbPageSize, offset)
						if fetchErr != nil {
							writeErr = fmt.Errorf("failed to fetch %s page (offset %d): %w", "instance_status", offset, fetchErr)
							return
						}
						if len(records) == 0 {
							break
						}
						if wErr := writer.Write(records); wErr != nil {
							writeErr = fmt.Errorf("failed to write %s parquet batch (offset %d): %w", "instance_status", offset, wErr)
							return
						}
						totalCount += int64(len(records))
						offset += len(records) // Correctly increment offset
					}

					writeErr = writer.Close()
					if writeErr != nil {
						log.Printf("ERROR closing parquet writer for %s: %v", "instance_status", writeErr)
					}
				}()
				return pr, countCh, errCh, nil
			},
			deleteFunc: dbClient.DeleteOldInstanceStatuses,
		},
		{
			tableName:       "job_status",
			retentionPeriod: jobStatusRetention,
			streamFunc: func(ctx context.Context, retention time.Duration) (io.Reader, <-chan int64, <-chan error, error) {
				pr, pw := io.Pipe()
				countCh := make(chan int64, 1)
				errCh := make(chan error, 1)

				go func() {
					var writeErr error
					var totalCount int64
					defer func() {
						if writeErr == nil {
							countCh <- totalCount
						}
						errCh <- writeErr
						pw.CloseWithError(writeErr)
					}()

					// Use the dedicated Parquet struct
					writer := parquet.NewWriter(pw, parquet.SchemaOf(new(db.JobStatusParquet)))

					offset := 0
					for {
						if err := ctx.Err(); err != nil {
							writeErr = fmt.Errorf("context cancelled during %s paging: %w", "job_status", err)
							return
						}
						records, fetchErr := dbClient.GetOldJobStatusesPaged(ctx, retention, dbPageSize, offset)
						if fetchErr != nil {
							writeErr = fmt.Errorf("failed to fetch %s page (offset %d): %w", "job_status", offset, fetchErr)
							return
						}
						if len(records) == 0 {
							break
						}

						// Map db.JobStatus to db.JobStatusParquet before writing
						parquetRecords := make([]db.JobStatusParquet, len(records))
						for i, r := range records {
							parquetRecords[i] = db.JobStatusParquet{
								ID:        r.ID,
								JobID:     r.JobID,
								Status:    r.Status,
								CreatedAt: r.CreatedAt,
							}
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

						if wErr := writer.Write(parquetRecords); wErr != nil {
							writeErr = fmt.Errorf("failed to write %s parquet batch (offset %d): %w", "job_status", offset, wErr)
							return
						}
						totalCount += int64(len(records))
						offset += len(records) // Correctly increment offset
					}

					writeErr = writer.Close()
					if writeErr != nil {
						log.Printf("ERROR closing parquet writer for %s: %v", "job_status", writeErr)
					}
				}()
				return pr, countCh, errCh, nil
			},
			deleteFunc: dbClient.DeleteOldJobStatuses,
		},
	}

	// Execute cleanup tasks sequentially
	for _, task := range tasks {
		// Check if context was cancelled before starting next task
		if ctx.Err() != nil {
			log.Printf("Cleanup cycle interrupted before processing %s: %v", task.tableName, ctx.Err())
			return // Stop processing further tables
		}

		log.Printf("Processing cleanup for table: %s (retention: %v)", task.tableName, task.retentionPeriod)
		err := cleanupTable(ctx, s3Client, s3Bucket, task.tableName, task.retentionPeriod, task.streamFunc, task.deleteFunc)

		if err != nil {
			// Log error but continue to the next table unless the context was cancelled.
			// Context cancellation errors should be returned immediately to stop the cycle.
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				log.Printf("Cleanup cycle interrupted during %s: %v", task.tableName, err)
				return // Stop processing further tables if context cancelled
			}
			// Log other errors and continue
			log.Printf("ERROR: Failed to cleanup table %s: %v", task.tableName, err)
		}
	}

	log.Println("Cleanup cycle finished.")
}

// cleanupTable handles the streaming, uploading, and deleting for a single table.
func cleanupTable(
	ctx context.Context,
	s3Client S3Uploader,
	s3Bucket string,
	tableName string,
	retentionPeriod time.Duration,
	streamFunc func(context.Context, time.Duration) (io.Reader, <-chan int64, <-chan error, error),
	deleteFunc func(context.Context, time.Duration) (int64, error),
) error {
	// Check for context cancellation before starting expensive operations
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("context cancelled before processing table %s: %w", tableName, err)
	}

	// 1. Initiate the streaming process
	parquetStreamReader, countCh, streamErrCh, setupErr := streamFunc(ctx, retentionPeriod)
	if setupErr != nil {
		// Handle potential errors during pipe/goroutine setup (less likely)
		return fmt.Errorf("failed to initiate streaming setup for %s: %w", tableName, setupErr)
	}
	// Ensure the reader (pipe reader) is eventually closed, especially if upload fails early.
	if closer, ok := parquetStreamReader.(io.Closer); ok {
		defer closer.Close() // This helps unblock the writer goroutine if cleanupTable returns early.
	}

	// Check for context cancellation before uploading
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("context cancelled before S3 upload for %s: %w", tableName, err)
	}

	// 2. Upload the Parquet stream to S3
	archiveFileName := generateArchiveFileName(tableName)
	log.Printf("Uploading %s archive to s3://%s/%s", tableName, s3Bucket, archiveFileName)

	// UploadStream will read from parquetStreamReader. Errors during upload are captured in uploadErr.
	// The actual streaming (DB reads, Parquet writes) happens concurrently in the goroutine.
	uploadErr := s3Client.UploadStream(ctx, s3Bucket, archiveFileName, parquetStreamReader)

	// 3. Wait for the streaming goroutine to finish and check its results
	var streamErr error
	var recordCount int64 = -1 // Initialize to -1 to indicate count not received yet

	// Wait for the streaming goroutine's error channel OR context cancellation
	select {
	case streamErr = <-streamErrCh:
		// Goroutine finished (successfully or with error)
		if streamErr == nil {
			// If goroutine succeeded, get the count
			select {
			case recordCount = <-countCh:
				// Successfully received count
			case <-ctx.Done():
				// Context cancelled while waiting for count (after stream finished ok)
				streamErr = fmt.Errorf("context cancelled while waiting for record count for %s: %w", tableName, ctx.Err())
			}
		}
	case <-ctx.Done():
		// Context cancelled before goroutine sent its status
		streamErr = fmt.Errorf("context cancelled while waiting for streaming goroutine for %s: %w", tableName, ctx.Err())
	}

	// 4. Determine overall success/failure and handle cleanup

	// Combine upload and streaming errors. Prioritize upload error if both exist.
	finalErr := uploadErr
	if finalErr == nil {
		finalErr = streamErr // If upload was ok, use the stream error status
	}

	// If any error occurred (upload or streaming), return the error now.
	if finalErr != nil {
		// Log specific context cancellation messages
		if errors.Is(finalErr, context.Canceled) || errors.Is(finalErr, context.DeadlineExceeded) {
			return fmt.Errorf("operation cancelled for %s: %w", tableName, finalErr) // Return wrapped context error
		}
		if uploadErr != nil && streamErr != nil {
			log.Printf("S3 upload for %s failed (%v) and streaming also failed (%v)", tableName, uploadErr, streamErr)
		} else if uploadErr != nil {
			log.Printf("S3 upload for %s failed: %v", tableName, uploadErr)
		} else { // streamErr must be non-nil here
			log.Printf("Parquet streaming/generation for %s failed: %v", tableName, streamErr)
		}
		// Even if upload seemed ok but streaming failed, the archive might be incomplete/corrupt. Don't delete.
		return fmt.Errorf("archiving failed for %s, original records not deleted: %w", tableName, finalErr)
	}

	// --- Success Path: Upload and Streaming both OK ---

	// Check the record count received from the stream
	if recordCount == -1 {
		// This shouldn't happen if streamErr was nil, but handle defensively.
		log.Printf("WARNING: Streaming goroutine for %s succeeded but did not provide a record count.", tableName)
		// We could potentially proceed with deletion but log a strong warning.
		// Or return an error to be safer. Let's return an error.
		return fmt.Errorf("internal error: missing record count after successful stream for %s", tableName)
	}

	if recordCount == 0 {
		log.Printf("No old records found and archived for %s.", tableName)
		return nil // Nothing to delete
	}

	log.Printf("Successfully streamed and uploaded %d records for %s to %s.", recordCount, tableName, archiveFileName)

	// 5. Delete the old records from the database (only if upload and stream succeeded)
	// Check for context cancellation one last time before destructive delete
	if err := ctx.Err(); err != nil {
		log.Printf("CRITICAL ERROR: Context cancelled before deleting records for %s after successful S3 upload: %v", tableName, err)
		log.Printf("Manual cleanup for %s might be required.", tableName)
		return fmt.Errorf("context cancelled before delete for %s: %w", tableName, err) // Return error as delete didn't complete
	}

	log.Printf("Deleting %d archived records from %s older than %v...", recordCount, tableName, retentionPeriod)
	deletedCount, deleteErr := deleteFunc(ctx, retentionPeriod)
	if deleteErr != nil {
		// Log critical failure: Upload succeeded, but delete failed. Manual intervention needed.
		log.Printf("CRITICAL ERROR: Failed to delete %d archived records from %s after successful S3 upload: %v", recordCount, tableName, deleteErr)
		log.Printf("Manual cleanup for %s IS REQUIRED.", tableName)
		// Return the delete error to make the overall job fail clearly.
		return fmt.Errorf("failed to delete records from %s after successful archive: %w", tableName, deleteErr)
	}

	// Verify deleted count matches archived count
	if deletedCount != recordCount {
		log.Printf("WARNING: Archived %d records for %s but deleted %d records. There might be a race condition or issue with delete logic.", recordCount, tableName, deletedCount)
		// Depending on policy, this might be treated as an error. For now, just a warning.
	} else {
		log.Printf("Successfully deleted %d archived records from %s.", deletedCount, tableName)
	}

	return nil // Success!
}

// generateArchiveFileName creates a unique filename for the archive based on table and timestamp.
func generateArchiveFileName(tableName string) string {
	timestamp := time.Now().UTC().Format("20060102-150405") // YYYYMMDD-HHMMSS
	return fmt.Sprintf("archive-%s-%s.parquet", tableName, timestamp)
}
