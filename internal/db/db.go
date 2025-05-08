package db

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

var (
	// globalDB holds the single database connection pool managed by this package.
	globalDB *sql.DB
	// once ensures the database initialization happens only once.
	once sync.Once
	// initErr holds any error that occurred during initialization.
	initErr error
)

// DB represents the database connection pool.
type DB struct {
	Conn *sql.DB
}

// NewDB returns the initialized DB instance.
// It relies on the init() function to set up the global database connection.
// If initialization failed, this function will return the initialization error.
func NewDB(ctx context.Context) (*DB, error) {
	// Check if initialization failed.
	if initErr != nil {
		return nil, fmt.Errorf("database initialization failed: %w", initErr)
	}
	// Check if the globalDB is somehow nil even if initErr is nil (should not happen with sync.Once).
	if globalDB == nil {
		return nil, fmt.Errorf("database connection is nil after initialization without error")
	}

	// Return the struct wrapper around the globally initialized connection pool.
	return &DB{Conn: globalDB}, nil
}

// init initializes the global database connection exactly once.
// It reads the database path, applies necessary options, opens the connection,
// sets connection limits, verifies PRAGMA settings, and applies migrations.
// If any step fails, it sets initErr, and subsequent calls to NewDB will fail.
func init() {
	once.Do(func() {
		dbPath := os.Getenv("DATABASE_URL") // Expect DATABASE_URL to be the path to the SQLite file
		if dbPath == "" {
			initErr = fmt.Errorf("DATABASE_URL environment variable (SQLite file path) not set")
			log.Printf("Error initializing database: %v", initErr) // Log error during init
			return
		}

		// Add SQLite options to the connection string for better performance and concurrency.
		var dsn string
		dsn, initErr = AddSQLiteOptions(dbPath)
		if initErr != nil {
			initErr = fmt.Errorf("failed to add SQLite options to DSN '%s': %w", dbPath, initErr)
			log.Printf("Error initializing database: %v", initErr)
			return
		}
		log.Printf("Using database DSN: %s", dsn)

		// Open the SQLite database connection.
		globalDB, initErr = sql.Open("sqlite3", dsn) // Use "sqlite3" driver
		if initErr != nil {
			initErr = fmt.Errorf("failed to open sqlite database with DSN '%s': %w", dsn, initErr)
			log.Printf("Error initializing database: %v", initErr)
			return
		}

		// Set max open connections to 1 for SQLite to prevent "database is locked" errors.
		setMaxOpenConns(globalDB)

		// Check database connectivity.
		pingCtx, pingCancel := context.WithTimeout(context.Background(), 5*time.Second)
		initErr = globalDB.PingContext(pingCtx)
		pingCancel() // Cancel context immediately after use
		if initErr != nil {
			initErr = fmt.Errorf("failed to ping database: %w", initErr)
			log.Printf("Error initializing database: %v", initErr)
			// Close the potentially problematic connection pool
			if closeErr := globalDB.Close(); closeErr != nil {
				log.Printf("Error closing database after ping failure: %v", closeErr)
			}
			globalDB = nil // Ensure globalDB is nil if ping failed
			return
		}

		// Apply database migrations
		log.Println("Applying database migrations...")
		migrationCtx, migrationCancel := context.WithTimeout(context.Background(), 30*time.Second) // Allow more time for migrations
		initErr = ApplyMigrations(migrationCtx, globalDB)
		migrationCancel() // Cancel context immediately after use
		if initErr != nil {
			initErr = fmt.Errorf("failed to apply database migrations: %w", initErr)
			log.Printf("Error initializing database: %v", initErr)
			// Close the connection pool as migrations failed
			if closeErr := globalDB.Close(); closeErr != nil {
				log.Printf("Error closing database after migration failure: %v", closeErr)
			}
			globalDB = nil
			return
		}
		log.Println("Database migrations applied successfully.")

		// Check and log the actual PRAGMA settings applied.
		settings := checkPragmaSettings(globalDB)
		log.Printf("SQLite PRAGMA settings confirmed: journal_mode=%s, synchronous=%s, foreign_keys=%s, busy_timeout=%s",
			settings["journal_mode"], settings["synchronous"], settings["foreign_keys"], settings["busy_timeout"])
		log.Println("Database initialization successful.")
	})
}

// Close closes the global database connection pool.
// It's safe to call multiple times, but the underlying pool will only be closed once.
func (db *DB) Close() error {
	if db.Conn != nil {
		log.Println("Closing global database connection...")
		err := db.Conn.Close()
		// Set globalDB to nil after closing to prevent reuse, though sync.Once prevents re-initialization.
		if err == nil {
			globalDB = nil
		}
		return err
	}
	return nil // Already closed or never initialized properly
}

// InstanceStatus represents a row in the instance_status table.
// No changes needed here for SQLite vs PG, assuming schema matches.
type InstanceStatus struct {
	ID        int       `parquet:"id"`
	VastAIID  int       `parquet:"vast_ai_id"`
	Status    string    `parquet:"status"`
	CreatedAt time.Time `parquet:"created_at"`
}

// JobStatus represents a row in the job_status table.
// No changes needed here for SQLite vs PG, assuming schema matches.
type JobStatus struct {
	ID         int
	JobID      string
	Status     string
	CreatedAt  time.Time
	Error      sql.NullString
	Result     sql.NullString
	InstanceID sql.NullInt64
	Input      sql.NullString
}

// JobStatusParquet is a helper struct for Parquet serialization.
// No changes needed here.
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

// WorkerAuthTokenStatus represents a row in the worker_auth_token_status table.
// No changes needed here for SQLite vs PG, assuming schema matches.
type WorkerAuthTokenStatus struct {
	ID         int
	TokenID    string
	Token      string
	InstanceID sql.NullInt64
	Status     string
	CreatedAt  time.Time
}

// WorkerAuthTokenStatusParquet is a helper struct for Parquet serialization.
// No changes needed here.
type WorkerAuthTokenStatusParquet struct {
	ID         int       `parquet:"id"`
	TokenID    string    `parquet:"token_id"`
	Token      string    `parquet:"token"`
	InstanceID *int64    `parquet:"instance_id,optional"`
	Status     string    `parquet:"status"`
	CreatedAt  time.Time `parquet:"created_at"`
}

// InsertInstanceStatus inserts a new instance status record into the database.
func (db *DB) InsertInstanceStatus(ctx context.Context, vastAIID int, status string) error {
	// Use CURRENT_TIMESTAMP for SQLite and ? for placeholders
	query := `INSERT INTO instance_status (vast_ai_id, status) VALUES (?, ?)` // Rely on DEFAULT for created_at
	_, err := db.Conn.ExecContext(ctx, query, vastAIID, status)
	if err != nil {
		return fmt.Errorf("failed to insert instance status: %w", err)
	}
	return nil
}

// GetInstanceStatus retrieves the latest instance status for a given VastAIID.
func (db *DB) GetInstanceStatus(ctx context.Context, vastAIID int) (*InstanceStatus, error) {
	// Use ? for placeholder
	query := `SELECT id, vast_ai_id, status, created_at FROM instance_status WHERE vast_ai_id = ? ORDER BY created_at DESC LIMIT 1`
	row := db.Conn.QueryRowContext(ctx, query, vastAIID)

	var instanceStatus InstanceStatus
	err := row.Scan(&instanceStatus.ID, &instanceStatus.VastAIID, &instanceStatus.Status, &instanceStatus.CreatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil // No status found for this vastAIID
		}
		return nil, fmt.Errorf("failed to get instance status for vast_ai_id %d: %w", vastAIID, err)
	}

	return &instanceStatus, nil
}

// CreateJob inserts the initial 'created' status record for a new job.
func (db *DB) CreateJob(ctx context.Context, jobID string, input *string) error {
	// A new job starts in the 'created' state and is not assigned to an instance yet.
	// instanceID is nil, error is nil, result is nil.
	return db.InsertJobStatus(ctx, jobID, "created", nil, input)
}

// InsertJobStatus inserts a new job status record into the database.
func (db *DB) InsertJobStatus(ctx context.Context, jobID, status string, instanceID *int64, input *string) error {
	// Use CURRENT_TIMESTAMP for SQLite and ? for placeholders
	query := `INSERT INTO job_status (job_id, status, instance_id, input) VALUES (?, ?, ?, ?)` // Rely on DEFAULT for created_at

	var instanceIDValue sql.NullInt64
	if instanceID != nil {
		instanceIDValue = sql.NullInt64{Int64: *instanceID, Valid: true}
	}

	var inputValue sql.NullString
	if input != nil {
		inputValue = sql.NullString{String: *input, Valid: true}
	}

	_, err := db.Conn.ExecContext(ctx, query, jobID, status, instanceIDValue, inputValue)
	if err != nil {
		return fmt.Errorf("failed to insert job status for job_id %s: %w", jobID, err)
	}
	return nil
}

// UpdateJobStatus adds a new record representing the updated status for a job.
// This function assumes that when updating (e.g., to completed/failed), we are creating a *new* status record
// for that job, preserving history. The instance_id and input are typically set on creation.
func (db *DB) UpdateJobStatus(ctx context.Context, jobID, status string, errorMsg, result *string) error {
	// Get the latest instance ID and input associated with this job ID to preserve them if needed,
	// although typically these might not change after the 'created' state.
	// For simplicity here, we insert a new row without carrying over instance_id/input,
	// assuming the GetJobStatus logic always fetches the LATEST row, which will have the final status.
	// If you need to associate the final state with the original instance_id/input, you'd query first or adjust the Insert.

	query := `INSERT INTO job_status (job_id, status, error, result)
	          VALUES (?, ?, ?, ?)` // Rely on DEFAULT for created_at, instance_id/input default to NULL

	var errorValue sql.NullString
	if errorMsg != nil {
		errorValue = sql.NullString{String: *errorMsg, Valid: true}
	}

	var resultValue sql.NullString
	if result != nil {
		resultValue = sql.NullString{String: *result, Valid: true}
	}

	_, err := db.Conn.ExecContext(ctx, query, jobID, status, errorValue, resultValue)
	if err != nil {
		return fmt.Errorf("failed to insert updated job status for job_id %s: %w", jobID, err)
	}
	return nil
}

// GetJobStatus retrieves the *latest* status record for a job from the database.
func (db *DB) GetJobStatus(ctx context.Context, jobID string) (*JobStatus, error) {
	// Use ? for placeholder
	query := `
		SELECT id, job_id, status, created_at, error, result, instance_id, input
		FROM job_status
		WHERE job_id = ?
		ORDER BY created_at DESC
		LIMIT 1`
	row := db.Conn.QueryRowContext(ctx, query, jobID)

	var jobStatus JobStatus
	err := row.Scan(
		&jobStatus.ID,
		&jobStatus.JobID,
		&jobStatus.Status,
		&jobStatus.CreatedAt,
		&jobStatus.Error,
		&jobStatus.Result,
		&jobStatus.InstanceID,
		&jobStatus.Input,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil // No job status found for this jobID
		}
		return nil, fmt.Errorf("failed to get job status for job_id %s: %w", jobID, err)
	}

	return &jobStatus, nil
}

// InsertWorkerAuthTokenStatus inserts a new worker auth token status record.
func (db *DB) InsertWorkerAuthTokenStatus(ctx context.Context, tokenID, token string, instanceID *int64, status string) error {
	// Use CURRENT_TIMESTAMP for SQLite and ? for placeholders
	query := `INSERT INTO worker_auth_token_status (token_id, token, instance_id, status) VALUES (?, ?, ?, ?)` // Rely on DEFAULT for created_at

	var instanceIDValue sql.NullInt64
	if instanceID != nil {
		instanceIDValue = sql.NullInt64{Int64: *instanceID, Valid: true}
	}

	_, err := db.Conn.ExecContext(ctx, query, tokenID, token, instanceIDValue, status)
	if err != nil {
		// Avoid logging the actual token value for security. Log tokenID if needed.
		// Check for UNIQUE constraint violation specifically if helpful
		if strings.Contains(err.Error(), "UNIQUE constraint failed: worker_auth_token_status.token") {
			// Don't log token here either
			return fmt.Errorf("failed to insert worker auth token status (token_id: %s): unique token constraint violated", tokenID)
		}
		return fmt.Errorf("failed to insert worker auth token status (token_id: %s): %w", tokenID, err)
	}
	return nil
}

// GetLatestWorkerAuthTokenStatusByToken retrieves the *latest* status record for a specific token value.
func (db *DB) GetLatestWorkerAuthTokenStatusByToken(ctx context.Context, token string) (*WorkerAuthTokenStatus, error) {
	// Use ? for placeholder
	query := `
		SELECT id, token_id, token, instance_id, status, created_at
		FROM worker_auth_token_status
		WHERE token = ?
		ORDER BY created_at DESC
		LIMIT 1` // Ordering by created_at is technically not needed due to UNIQUE constraint on token, but good practice.
	row := db.Conn.QueryRowContext(ctx, query, token)

	var tokenStatus WorkerAuthTokenStatus
	err := row.Scan(
		&tokenStatus.ID,
		&tokenStatus.TokenID,
		&tokenStatus.Token,
		&tokenStatus.InstanceID,
		&tokenStatus.Status,
		&tokenStatus.CreatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil // No status found for this token value
		}
		// Don't log the token here for security.
		return nil, fmt.Errorf("failed to get latest worker auth token status: %w", err)
	}

	return &tokenStatus, nil
}

// DeleteOldWorkerAuthTokenStatuses deletes worker auth token status records older than the specified retention period.
func (db *DB) DeleteOldWorkerAuthTokenStatuses(ctx context.Context, retentionPeriod time.Duration) (int64, error) {
	cutoffTime := time.Now().Add(-retentionPeriod)
	// Use ? for placeholder
	// Assumes created_at is stored in a format comparable by SQLite (e.g., TEXT ISO8601 or INTEGER Unix timestamp)
	// SQLite's default CURRENT_TIMESTAMP stores TEXT in 'YYYY-MM-DD HH:MM:SS' format, which is comparable.
	query := `DELETE FROM worker_auth_token_status WHERE created_at < ?`
	result, err := db.Conn.ExecContext(ctx, query, cutoffTime.Format("2006-01-02 15:04:05")) // Use standard SQLite time format
	if err != nil {
		return 0, fmt.Errorf("failed to delete old worker auth token statuses: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		// Getting RowsAffected might fail on some drivers or configurations, though it usually works with go-sqlite3.
		return 0, fmt.Errorf("failed to get rows affected after deleting worker auth token statuses: %w", err)
	}
	return rowsAffected, nil
}

// DeleteOldInstanceStatuses deletes instance statuses older than the specified retention period.
func (db *DB) DeleteOldInstanceStatuses(ctx context.Context, retentionPeriod time.Duration) (int64, error) {
	cutoffTime := time.Now().Add(-retentionPeriod)
	// Use ? for placeholder
	query := `DELETE FROM instance_status WHERE created_at < ?`
	result, err := db.Conn.ExecContext(ctx, query, cutoffTime.Format("2006-01-02 15:04:05")) // Use standard SQLite time format
	if err != nil {
		return 0, fmt.Errorf("failed to delete old instance statuses: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("failed to get rows affected after deleting instance statuses: %w", err)
	}
	return rowsAffected, nil
}

// DeleteOldJobStatuses deletes job statuses older than the specified retention period.
func (db *DB) DeleteOldJobStatuses(ctx context.Context, retentionPeriod time.Duration) (int64, error) {
	cutoffTime := time.Now().Add(-retentionPeriod)
	// Use ? for placeholder
	query := `DELETE FROM job_status WHERE created_at < ?`
	result, err := db.Conn.ExecContext(ctx, query, cutoffTime.Format("2006-01-02 15:04:05")) // Use standard SQLite time format
	if err != nil {
		return 0, fmt.Errorf("failed to delete old job statuses: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("failed to get rows affected after deleting job statuses: %w", err)
	}
	return rowsAffected, nil
}

// --- Paged Getters for Archiving ---

// GetOldWorkerAuthTokenStatusesPaged fetches a page of worker auth token statuses older than the retention period.
func (db *DB) GetOldWorkerAuthTokenStatusesPaged(ctx context.Context, retentionPeriod time.Duration, limit, offset int) ([]WorkerAuthTokenStatus, error) {
	cutoffTime := time.Now().Add(-retentionPeriod)
	// Use ? for placeholders
	query := `
		SELECT id, token_id, token, instance_id, status, created_at
		FROM worker_auth_token_status
		WHERE created_at < ?
		ORDER BY created_at
		LIMIT ? OFFSET ?`

	rows, err := db.Conn.QueryContext(ctx, query, cutoffTime.Format("2006-01-02 15:04:05"), limit, offset) // Use standard SQLite time format
	if err != nil {
		return nil, fmt.Errorf("failed to query old worker auth token statuses (paged): %w", err)
	}
	defer rows.Close()

	var statuses []WorkerAuthTokenStatus
	for rows.Next() {
		var status WorkerAuthTokenStatus
		if err := rows.Scan(
			&status.ID,
			&status.TokenID,
			&status.Token,
			&status.InstanceID,
			&status.Status,
			&status.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan worker auth token status row (paged): %w", err)
		}
		statuses = append(statuses, status)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating worker auth token status rows (paged): %w", err)
	}

	return statuses, nil
}

// GetOldInstanceStatusesPaged fetches a page of instance statuses older than the retention period.
func (db *DB) GetOldInstanceStatusesPaged(ctx context.Context, retentionPeriod time.Duration, limit, offset int) ([]InstanceStatus, error) {
	cutoffTime := time.Now().Add(-retentionPeriod)
	// Use ? for placeholders
	query := `
		SELECT id, vast_ai_id, status, created_at
		FROM instance_status
		WHERE created_at < ?
		ORDER BY created_at -- Consistent ordering is important for pagination
		LIMIT ? OFFSET ?`

	rows, err := db.Conn.QueryContext(ctx, query, cutoffTime.Format("2006-01-02 15:04:05"), limit, offset) // Use standard SQLite time format
	if err != nil {
		return nil, fmt.Errorf("failed to query old instance statuses (paged): %w", err)
	}
	defer rows.Close()

	var statuses []InstanceStatus
	for rows.Next() {
		var status InstanceStatus
		if err := rows.Scan(&status.ID, &status.VastAIID, &status.Status, &status.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan instance status row (paged): %w", err)
		}
		statuses = append(statuses, status)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating instance status rows (paged): %w", err)
	}

	return statuses, nil
}

// GetOldJobStatusesPaged fetches a page of job statuses older than the retention period.
func (db *DB) GetOldJobStatusesPaged(ctx context.Context, retentionPeriod time.Duration, limit, offset int) ([]JobStatus, error) {
	cutoffTime := time.Now().Add(-retentionPeriod)
	// Use ? for placeholders
	query := `
		SELECT id, job_id, status, created_at, error, result, instance_id, input
		FROM job_status
		WHERE created_at < ?
		ORDER BY created_at -- Consistent ordering is important for pagination
		LIMIT ? OFFSET ?`

	rows, err := db.Conn.QueryContext(ctx, query, cutoffTime.Format("2006-01-02 15:04:05"), limit, offset) // Use standard SQLite time format
	if err != nil {
		return nil, fmt.Errorf("failed to query old job statuses (paged): %w", err)
	}
	defer rows.Close()

	var statuses []JobStatus
	for rows.Next() {
		var status JobStatus
		if err := rows.Scan(
			&status.ID,
			&status.JobID,
			&status.Status,
			&status.CreatedAt,
			&status.Error,
			&status.Result,
			&status.InstanceID,
			&status.Input,
		); err != nil {
			return nil, fmt.Errorf("failed to scan job status row (paged): %w", err)
		}
		statuses = append(statuses, status)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating job status rows (paged): %w", err)
	}

	return statuses, nil
}

// setMaxOpenConns applies SetMaxOpenConns(1) to a *sql.DB.
// This is often useful for SQLite to prevent "database is locked" errors,
// especially when WAL mode is not enabled or under high write contention.
func setMaxOpenConns(db *sql.DB) {
	// Fix for "database is locked" error
	// https://github.com/mattn/go-sqlite3/issues/274
	if db == nil {
		log.Println("Warning: setMaxOpenConns called with nil *sql.DB")
		return
	}
	log.Println("Setting MaxOpenConns to 1 for SQLite.")
	db.SetMaxOpenConns(1)
}

// AddSQLiteOptions appends common performance/concurrency options to a SQLite file path.
func AddSQLiteOptions(dbPath string) (string, error) {
	options := map[string]string{
		"_journal_mode": "WAL",    // Write-Ahead Logging for better concurrency
		"_synchronous":  "NORMAL", // Less durable but faster than FULL
		"_busy_timeout": "5000",   // Wait 5 seconds if the db is locked
		"_foreign_keys": "ON",     // Enforce foreign key constraints
	}

	// Check if dbPath already contains options
	u, err := url.Parse(dbPath)
	if err != nil {
		// If parsing fails, assume it's just a file path without URI scheme
		// We need to add a scheme for url.ParseQuery to work correctly if it's just a path like /data/my.db
		// Using a placeholder scheme like "file:" helps, even if the driver ignores it later.
		// However, sql.Open("sqlite3", ...) handles paths directly. net/url is mostly for query params.
		// Let's handle the case where it might already be a file URI like "file:path/to/db?option=value"
		// or just a path like "path/to/db".
		if u, err = url.Parse("file:" + dbPath); err == nil && u.Scheme == "file" {
			// Parsed as file URI, proceed
		} else {
			// Still fails or is not a file URI, fallback to simple path handling
			// Treat the original dbPath as just the path part.
			u = &url.URL{Path: dbPath}
		}
	}

	q := u.Query()
	modified := false
	for key, value := range options {
		// Add option only if not already present in the DSN
		if q.Get(key) == "" {
			q.Set(key, value)
			modified = true
		}
	}

	// Only modify the DSN if options were actually added
	if !modified && !strings.Contains(dbPath, "?") {
		return dbPath, nil // Return original path if no options needed adding and none existed
	}

	u.RawQuery = q.Encode()

	// Return the path with encoded query parameters.
	// If the original was just a path, u.String() might add "file:",
	// but go-sqlite3 usually handles "path?query" correctly without the scheme.
	// Let's return path + "?" + query if no scheme was present originally.
	if u.Scheme == "" || u.Scheme == "file" {
		// Return path?query=value string, which sqlite3 driver expects.
		// u.Path might have a leading slash if parsed from "file:/path", remove it if needed,
		// but usually fine. sqlite3 driver handles `file:path?query` and `path?query`.
		res := u.Path
		// If the original path input did not contain '?', append '?'.
		// This handles edge cases where url.Parse might interpret parts of the path.
		// Safest is to check if the original dbPath contained '?'
		if !strings.Contains(dbPath, "?") {
			if u.RawQuery != "" {
				res += "?" + u.RawQuery
			}
		} else {
			// If original path had '?', RawQuery should be appended correctly.
			// We rely on url.URL structure here. Path should be correct.
			if u.RawQuery != "" {
				res += "?" + u.RawQuery
			}

		}

		// Handle potential leading slash if original path didn't have one and "file:" was added
		// Example: input "my.db", parsed as "file:my.db", Path is "my.db" - correct.
		// Example: input "/tmp/my.db", parsed as "file:/tmp/my.db", Path is "/tmp/my.db" - correct.
		// Example: input "file:data.db", Path is "data.db" - correct.
		return res, nil
	}

	// If it had a different scheme originally, preserve it.
	return u.String(), nil
}

// checkPragmaSettings retrieves and returns current PRAGMA settings from the database.
func checkPragmaSettings(db *sql.DB) map[string]string {
	options := map[string]string{}
	var err error

	if db == nil {
		log.Println("Warning: checkPragmaSettings called with nil *sql.DB")
		return options
	}

	// Use QueryRowContext for better cancellation handling, though less critical here.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Declare temporary variables to scan into, as we cannot take the address of map elements.
	var journalMode string
	var synchronous string
	var foreignKeys string
	var busyTimeout string

	err = db.QueryRowContext(ctx, "PRAGMA journal_mode;").Scan(&journalMode)
	if err != nil {
		log.Printf("Error getting PRAGMA journal_mode: %v", err)
		options["journal_mode"] = "error" // Indicate failure
	} else {
		options["journal_mode"] = journalMode
	}

	err = db.QueryRowContext(ctx, "PRAGMA synchronous;").Scan(&synchronous)
	if err != nil {
		log.Printf("Error getting PRAGMA synchronous: %v", err)
		options["synchronous"] = "error" // Indicate failure
	} else {
		// SQLite returns 0, 1, 2, 3 for synchronous levels. Let's map them.
		switch synchronous {
		case "0":
			options["synchronous"] = "OFF (0)"
		case "1":
			options["synchronous"] = "NORMAL (1)"
		case "2":
			options["synchronous"] = "FULL (2)"
		case "3":
			options["synchronous"] = "EXTRA (3)"
		default:
			options["synchronous"] = synchronous // Keep original value if not standard
		}
	}

	err = db.QueryRowContext(ctx, "PRAGMA foreign_keys;").Scan(&foreignKeys)
	if err != nil {
		log.Printf("Error getting PRAGMA foreign_keys: %v", err)
		options["foreign_keys"] = "error" // Indicate failure
	} else {
		switch foreignKeys {
		case "0":
			options["foreign_keys"] = "OFF (0)"
		case "1":
			options["foreign_keys"] = "ON (1)"
		default:
			options["foreign_keys"] = foreignKeys // Keep original value if not standard
		}
	}

	err = db.QueryRowContext(ctx, "PRAGMA busy_timeout;").Scan(&busyTimeout)
	if err != nil {
		log.Printf("Error getting PRAGMA busy_timeout: %v", err)
		options["busy_timeout"] = "error" // Indicate failure
	} else {
		options["busy_timeout"] = busyTimeout
	}

	// Get and log connection pool statistics
	stats := db.Stats()
	log.Printf("DB Stats: Open=%d, Idle=%d, InUse=%d, WaitCount=%d, WaitDuration=%s, MaxIdleClosed=%d, MaxLifetimeClosed=%d",
		stats.OpenConnections,
		stats.Idle,
		stats.InUse,
		stats.WaitCount,
		stats.WaitDuration,
		stats.MaxIdleClosed,
		stats.MaxLifetimeClosed) // MaxIdleTimeClosed deprecated

	return options
}
