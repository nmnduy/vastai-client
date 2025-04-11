package db

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// DB represents the database connection pool.
type DB struct {
	Conn *sql.DB
}

// NewDB creates a new DB connection.
func NewDB(ctx context.Context) (*DB, error) {
	connStr := os.Getenv("DATABASE_URL")
	if connStr == "" {
		return nil, fmt.Errorf("DATABASE_URL environment variable not set")
	}

	db, err := sql.Open("pgx", connStr)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to the database: %w", err)
	}

	// Configure connection pool settings (optional but recommended)
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)

	if err := db.PingContext(ctx); err != nil {
		db.Close() // Close the connection if ping fails
		return nil, fmt.Errorf("failed to ping the database: %w", err)
	}

	log.Println("Successfully connected to the database!")
	return &DB{Conn: db}, nil
}

// Close closes the database connection.
func (db *DB) Close() error {
	if db.Conn != nil {
		log.Println("Closing database connection...")
		return db.Conn.Close()
	}
	return nil
}

// InstanceStatus represents a row in the instance_status table.
type InstanceStatus struct {
	ID        int       `parquet:"id"` // Add parquet tags for schema generation
	VastAIID  int       `parquet:"vast_ai_id"`
	Status    string    `parquet:"status"`
	CreatedAt time.Time `parquet:"created_at"`
}

// JobStatus represents a row in the job_status table.
type JobStatus struct {
	ID         int
	JobID      string
	Status     string
	CreatedAt  time.Time
	Error      sql.NullString // Use sql.NullString for nullable columns
	Result     sql.NullString // Use sql.NullString for nullable columns
	InstanceID sql.NullInt64  // Use sql.NullInt64 for nullable columns
	Input      sql.NullString
}

// JobStatusParquet is a helper struct for Parquet serialization,
// handling sql.Null* types correctly.
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

// WorkerAuthToken represents a row in the worker_auth_token table.
type WorkerAuthToken struct {
	Token     string    `parquet:"token"`
	CreatedAt time.Time `parquet:"created_at"`
}

// InsertInstanceStatus inserts a new instance status record into the database.
func (db *DB) InsertInstanceStatus(ctx context.Context, vastAIID int, status string) error {
	query := `INSERT INTO instance_status (vast_ai_id, status, created_at) VALUES ($1, $2, NOW())`
	_, err := db.Conn.ExecContext(ctx, query, vastAIID, status)
	if err != nil {
		return fmt.Errorf("failed to insert instance status: %w", err)
	}
	return nil
}

// GetInstanceStatus retrieves the latest instance status for a given VastAIID.
func (db *DB) GetInstanceStatus(ctx context.Context, vastAIID int) (*InstanceStatus, error) {
	query := `SELECT id, vast_ai_id, status, created_at FROM instance_status WHERE vast_ai_id = $1 ORDER BY created_at DESC LIMIT 1`
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

// InsertJobStatus inserts a new job status record into the database.
func (db *DB) InsertJobStatus(ctx context.Context, jobID, status string, instanceID *int64, input *string) error {
	query := `INSERT INTO job_status (job_id, status, created_at, instance_id, input) VALUES ($1, $2, NOW(), $3, $4)`

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

// UpdateJobStatus updates the status of a job in the database.
func (db *DB) UpdateJobStatus(ctx context.Context, jobID, status string, errorMsg, result *string) error {
	query := `UPDATE job_status SET status = $1, error = $2, result = $3 WHERE job_id = $4` // Note: This doesn't update created_at

	var errorValue sql.NullString
	if errorMsg != nil {
		errorValue = sql.NullString{String: *errorMsg, Valid: true}
	}

	var resultValue sql.NullString
	if result != nil {
		resultValue = sql.NullString{String: *result, Valid: true}
	}

	res, err := db.Conn.ExecContext(ctx, query, status, errorValue, resultValue, jobID)
	if err != nil {
		return fmt.Errorf("failed to update job status for job_id %s: %w", jobID, err)
	}

	rowsAffected, err := res.RowsAffected()
	if err != nil {
		// Log this error but don't necessarily fail the operation
		log.Printf("Warning: could not get rows affected after updating job status for %s: %v", jobID, err)
	} else if rowsAffected == 0 {
		log.Printf("Warning: updating job status for %s affected 0 rows (job might not exist or status was the same)", jobID)
		// Depending on requirements, you might want to return an error here
		// return fmt.Errorf("job with job_id %s not found for update", jobID)
	}

	return nil
}

// GetJobStatus retrieves the *latest* status record for a job from the database.
// Assuming multiple records can exist per job_id, ordered by created_at.
func (db *DB) GetJobStatus(ctx context.Context, jobID string) (*JobStatus, error) {
	query := `
		SELECT id, job_id, status, created_at, error, result, instance_id, input
		FROM job_status
		WHERE job_id = $1
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

// InsertWorkerAuthToken inserts a new worker auth token into the database.
func (db *DB) InsertWorkerAuthToken(ctx context.Context, token string) error {
	query := `INSERT INTO worker_auth_token (token, created_at) VALUES ($1, NOW())`
	_, err := db.Conn.ExecContext(ctx, query, token)
	if err != nil {
		// Consider logging the token itself might be a security risk, log hash if needed.
		return fmt.Errorf("failed to insert worker auth token: %w", err)
	}
	return nil
}

// GetWorkerAuthToken retrieves a worker auth token from the database.
func (db *DB) GetWorkerAuthToken(ctx context.Context, token string) (*WorkerAuthToken, error) {
	query := `SELECT token, created_at FROM worker_auth_token WHERE token = $1`
	row := db.Conn.QueryRowContext(ctx, query, token)

	var workerAuthToken WorkerAuthToken
	err := row.Scan(&workerAuthToken.Token, &workerAuthToken.CreatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil // No token found
		}
		// Don't log the token here for security.
		return nil, fmt.Errorf("failed to get worker auth token: %w", err)
	}

	return &workerAuthToken, nil
}

// DeleteOldWorkerAuthTokens deletes worker auth tokens older than the specified retention period.
func (db *DB) DeleteOldWorkerAuthTokens(ctx context.Context, retentionPeriod time.Duration) (int64, error) {
	cutoffTime := time.Now().Add(-retentionPeriod)
	query := `DELETE FROM worker_auth_token WHERE created_at < $1`
	result, err := db.Conn.ExecContext(ctx, query, cutoffTime)
	if err != nil {
		return 0, fmt.Errorf("failed to delete old worker auth tokens: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		// This error might occur on drivers that don't support RowsAffected.
		return 0, fmt.Errorf("failed to get rows affected after deleting worker auth tokens: %w", err)
	}
	return rowsAffected, nil
}

// DeleteOldInstanceStatuses deletes instance statuses older than the specified retention period.
func (db *DB) DeleteOldInstanceStatuses(ctx context.Context, retentionPeriod time.Duration) (int64, error) {
	cutoffTime := time.Now().Add(-retentionPeriod)
	query := `DELETE FROM instance_status WHERE created_at < $1`
	result, err := db.Conn.ExecContext(ctx, query, cutoffTime)
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
	query := `DELETE FROM job_status WHERE created_at < $1`
	result, err := db.Conn.ExecContext(ctx, query, cutoffTime)
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

// GetOldWorkerAuthTokensPaged fetches a page of worker auth tokens older than the retention period.
func (db *DB) GetOldWorkerAuthTokensPaged(ctx context.Context, retentionPeriod time.Duration, limit, offset int) ([]WorkerAuthToken, error) {
	cutoffTime := time.Now().Add(-retentionPeriod)
	query := `
		SELECT token, created_at
		FROM worker_auth_token
		WHERE created_at < $1
		ORDER BY created_at -- Consistent ordering is important for pagination
		LIMIT $2 OFFSET $3`

	rows, err := db.Conn.QueryContext(ctx, query, cutoffTime, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to query old worker auth tokens (paged): %w", err)
	}
	defer rows.Close()

	var tokens []WorkerAuthToken
	for rows.Next() {
		var token WorkerAuthToken
		if err := rows.Scan(&token.Token, &token.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan worker auth token row (paged): %w", err)
		}
		tokens = append(tokens, token)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating worker auth token rows (paged): %w", err)
	}

	return tokens, nil
}

// GetOldInstanceStatusesPaged fetches a page of instance statuses older than the retention period.
func (db *DB) GetOldInstanceStatusesPaged(ctx context.Context, retentionPeriod time.Duration, limit, offset int) ([]InstanceStatus, error) {
	cutoffTime := time.Now().Add(-retentionPeriod)
	query := `
		SELECT id, vast_ai_id, status, created_at
		FROM instance_status
		WHERE created_at < $1
		ORDER BY created_at -- Consistent ordering is important for pagination
		LIMIT $2 OFFSET $3`

	rows, err := db.Conn.QueryContext(ctx, query, cutoffTime, limit, offset)
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
	query := `
		SELECT id, job_id, status, created_at, error, result, instance_id, input
		FROM job_status
		WHERE created_at < $1
		ORDER BY created_at -- Consistent ordering is important for pagination
		LIMIT $2 OFFSET $3`

	rows, err := db.Conn.QueryContext(ctx, query, cutoffTime, limit, offset)
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
