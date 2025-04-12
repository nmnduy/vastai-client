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

// WorkerAuthTokenStatus represents a row in the worker_auth_token_status table.
// Each row represents a state transition for a token identified by TokenID.
type WorkerAuthTokenStatus struct {
	ID         int           // Primary key for the status record
	TokenID    string        // Groups status events for the same logical token
	Token      string        // The actual unique token value
	InstanceID sql.NullInt64 // Associated Vast AI instance ID
	Status     string        // Status: created, validated, expired, invalidated
	CreatedAt  time.Time     // Timestamp of this status event
}

// WorkerAuthTokenStatusParquet is a helper struct for Parquet serialization.
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
// Note: This function assumes each insert represents a new state, matching the table description.
// It does not update existing rows based on job_id.
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

// UpdateJobStatus adds a new record representing the updated status for a job.
// This aligns with the table description where each record represents a state change.
func (db *DB) UpdateJobStatus(ctx context.Context, jobID, status string, instanceID *int64, errorMsg, result *string) error {
	query := `INSERT INTO job_status (job_id, status, created_at, instance_id, error, result)
	          VALUES ($1, $2, NOW(), $3, $4, $5)`

	var instanceIDValue sql.NullInt64
	if instanceID != nil {
		instanceIDValue = sql.NullInt64{Int64: *instanceID, Valid: true}
	}

	var errorValue sql.NullString
	if errorMsg != nil {
		errorValue = sql.NullString{String: *errorMsg, Valid: true}
	}

	var resultValue sql.NullString
	if result != nil {
		resultValue = sql.NullString{String: *result, Valid: true}
	}

	_, err := db.Conn.ExecContext(ctx, query, jobID, status, instanceIDValue, errorValue, resultValue)
	if err != nil {
		return fmt.Errorf("failed to insert updated job status for job_id %s: %w", jobID, err)
	}
	return nil
}

// GetJobStatus retrieves the *latest* status record for a job from the database.
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

// InsertWorkerAuthTokenStatus inserts a new worker auth token status record.
func (db *DB) InsertWorkerAuthTokenStatus(ctx context.Context, tokenID, token string, instanceID *int64, status string) error {
	query := `INSERT INTO worker_auth_token_status (token_id, token, instance_id, status, created_at) VALUES ($1, $2, $3, $4, NOW())`

	var instanceIDValue sql.NullInt64
	if instanceID != nil {
		instanceIDValue = sql.NullInt64{Int64: *instanceID, Valid: true}
	}

	_, err := db.Conn.ExecContext(ctx, query, tokenID, token, instanceIDValue, status)
	if err != nil {
		// Avoid logging the actual token value for security. Log tokenID if needed.
		return fmt.Errorf("failed to insert worker auth token status (token_id: %s): %w", tokenID, err)
	}
	return nil
}

// GetLatestWorkerAuthTokenStatusByToken retrieves the *latest* status record for a specific token value.
func (db *DB) GetLatestWorkerAuthTokenStatusByToken(ctx context.Context, token string) (*WorkerAuthTokenStatus, error) {
	query := `
		SELECT id, token_id, token, instance_id, status, created_at
		FROM worker_auth_token_status
		WHERE token = $1
		ORDER BY created_at DESC
		LIMIT 1`
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
	query := `DELETE FROM worker_auth_token_status WHERE created_at < $1`
	result, err := db.Conn.ExecContext(ctx, query, cutoffTime)
	if err != nil {
		return 0, fmt.Errorf("failed to delete old worker auth token statuses: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		// This error might occur on drivers that don't support RowsAffected.
		return 0, fmt.Errorf("failed to get rows affected after deleting worker auth token statuses: %w", err)
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

// GetOldWorkerAuthTokenStatusesPaged fetches a page of worker auth token statuses older than the retention period.
func (db *DB) GetOldWorkerAuthTokenStatusesPaged(ctx context.Context, retentionPeriod time.Duration, limit, offset int) ([]WorkerAuthTokenStatus, error) {
	cutoffTime := time.Now().Add(-retentionPeriod)
	query := `
		SELECT id, token_id, token, instance_id, status, created_at
		FROM worker_auth_token_status
		WHERE created_at < $1
		ORDER BY created_at 
		LIMIT $2 OFFSET $3`

	rows, err := db.Conn.QueryContext(ctx, query, cutoffTime, limit, offset)
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
