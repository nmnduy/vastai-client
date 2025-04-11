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

type DB struct {
	Conn *sql.DB
}

func NewDB(ctx context.Context) (*DB, error) {
	// Database connection string.
	// Example: "postgresql://user:password@host:port/database_name"
	connStr := os.Getenv("DATABASE_URL")
	if connStr == "" {
		return nil, fmt.Errorf("DATABASE_URL environment variable not set")
	}

	// Connect to the database.
	db, err := sql.Open("pgx", connStr)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to the database: %w", err)
	}

	// Test the connection.
	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("failed to ping the database: %w", err)
	}

	log.Println("Successfully connected to the database!")

	return &DB{Conn: db}, nil
}

// Close closes the database connection.
func (db *DB) Close() error {
	if db.Conn != nil {
		return db.Conn.Close()
	}
	return nil
}

// InstanceStatus represents a row in the instance_status table.
type InstanceStatus struct {
	ID        int
	VastAIID  int
	Status    string
	CreatedAt time.Time
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

// WorkerAuthToken represents a row in the worker_auth_token table.
type WorkerAuthToken struct {
	Token     string
	CreatedAt time.Time
}

// InsertInstanceStatus inserts a new instance status record into the database.
func (db *DB) InsertInstanceStatus(ctx context.Context, vastAIID int, status string) error {
	query := `INSERT INTO instance_status (vast_ai_id, status, created_at) VALUES ($1, $2, NOW())`
	_, err := db.Conn.ExecContext(ctx, query, vastAIID, status)
	return err
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
		return nil, err
	}

	return &instanceStatus, nil
}

// InsertJobStatus inserts a new job status record into the database.
func (db *DB) InsertJobStatus(ctx context.Context, jobID, status string, instanceID *int64, input *string) error {
	query := `INSERT INTO job_status (job_id, status, created_at, instance_id, input) VALUES ($1, $2, NOW(), $3, $4)`

	var instanceIDValue sql.NullInt64
	if instanceID != nil {
		instanceIDValue = sql.NullInt64{Int64: *instanceID, Valid: true}
	} else {
		instanceIDValue = sql.NullInt64{Valid: false}
	}

	var inputValue sql.NullString
	if input != nil {
		inputValue = sql.NullString{String: *input, Valid: true}
	} else {
		inputValue = sql.NullString{Valid: false}
	}

	_, err := db.Conn.ExecContext(ctx, query, jobID, status, instanceIDValue, inputValue)
	return err
}

// UpdateJobStatus updates the status of a job in the database.
func (db *DB) UpdateJobStatus(ctx context.Context, jobID, status string, errorMsg, result *string) error {
	query := `UPDATE job_status SET status = $1, error = $2, result = $3 WHERE job_id = $4`

	var errorValue sql.NullString
	if errorMsg != nil {
		errorValue = sql.NullString{String: *errorMsg, Valid: true}
	} else {
		errorValue = sql.NullString{Valid: false}
	}

	var resultValue sql.NullString
	if result != nil {
		resultValue = sql.NullString{String: *result, Valid: true}
	} else {
		resultValue = sql.NullString{Valid: false}
	}

	_, err := db.Conn.ExecContext(ctx, query, status, errorValue, resultValue, jobID)
	return err
}

// GetJobStatus retrieves a job status from the database.
func (db *DB) GetJobStatus(ctx context.Context, jobID string) (*JobStatus, error) {
	query := `SELECT id, job_id, status, created_at, error, result, instance_id, input FROM job_status WHERE job_id = $1`
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
		return nil, err
	}

	return &jobStatus, nil
}

// InsertWorkerAuthToken inserts a new worker auth token into the database.
func (db *DB) InsertWorkerAuthToken(ctx context.Context, token string) error {
	query := `INSERT INTO worker_auth_token (token, created_at) VALUES ($1, NOW())`
	_, err := db.Conn.ExecContext(ctx, query, token)
	return err
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
		return nil, err
	}

	return &workerAuthToken, nil
}

// DeleteOldWorkerAuthTokens deletes worker auth tokens older than 1 month.
func (db *DB) DeleteOldWorkerAuthTokens(ctx context.Context) error {
	query := `DELETE FROM worker_auth_token WHERE created_at < NOW() - INTERVAL '1 month'`
	_, err := db.Conn.ExecContext(ctx, query)
	return err
}

// DeleteOldInstanceStatuses deletes instance statuses older than 1 year.
func (db *DB) DeleteOldInstanceStatuses(ctx context.Context) error {
	query := `DELETE FROM instance_status WHERE created_at < NOW() - INTERVAL '1 year'`
	_, err := db.Conn.ExecContext(ctx, query)
	return err
}

// DeleteOldJobStatuses deletes job statuses older than 3 months.
func (db *DB) DeleteOldJobStatuses(ctx context.Context) error {
	query := `DELETE FROM job_status WHERE created_at < NOW() - INTERVAL '3 months'`
	_, err := db.Conn.ExecContext(ctx, query)
	return err
}
