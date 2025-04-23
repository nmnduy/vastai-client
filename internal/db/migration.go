package db

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"sort"
	"strings"
)

// Migration defines the structure for a database migration.
type Migration struct {
	Version int    // Unique version identifier for the migration.
	Script  string // SQL script to execute for this migration.
}

// migrationTableName is the name of the table used to track schema versions.
const migrationTableName = "schema_migrations"

// migrations is the list of all defined migrations, ordered by version.
// IMPORTANT: Add new migrations to this list IN ORDER of their version number.
var migrations = []Migration{
	{
		Version: 1,
		Script: `
-- Create schema_migrations table (handled implicitly by ensureSchemaTableExists)

-- Create instance_status table
CREATE TABLE instance_status (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    vast_ai_id INTEGER NOT NULL,
    status TEXT NOT NULL CHECK(status IN ('rented', 'authenticated', 'running', 'stopped')),
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL
);
-- Index for efficient lookup of latest status per instance
CREATE INDEX idx_instance_status_vast_ai_id_created_at ON instance_status (vast_ai_id, created_at DESC);
-- Index for efficient cleanup based on age
CREATE INDEX idx_instance_status_created_at ON instance_status (created_at);

-- Create job_status table
CREATE TABLE job_status (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    job_id TEXT NOT NULL,
    status TEXT NOT NULL CHECK(status IN ('created', 'queued', 'running', 'completed', 'failed')),
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
    error TEXT,
    result TEXT,
    instance_id INTEGER, -- Can be NULL initially
    input TEXT
);
-- Index for efficient lookup of latest status per job
CREATE INDEX idx_job_status_job_id_created_at ON job_status (job_id, created_at DESC);
-- Index for efficient cleanup based on age
CREATE INDEX idx_job_status_created_at ON job_status (created_at);
-- Index potentially useful for workers finding queued jobs
CREATE INDEX idx_job_status_status_created_at ON job_status (status, created_at);

-- Create worker_auth_token_status table
CREATE TABLE worker_auth_token_status (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    token_id TEXT NOT NULL, -- To group events for the same conceptual token
    token TEXT NOT NULL UNIQUE, -- The actual unique token value
    instance_id INTEGER, -- Which instance it's associated with, can be NULL initially
    status TEXT NOT NULL CHECK(status IN ('created', 'validated', 'invalidated')),
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL
);
-- Index for grouping events by token id
CREATE INDEX idx_worker_auth_token_status_token_id_created_at ON worker_auth_token_status (token_id, created_at DESC);
-- Index for efficient lookup by the unique token value
-- This index is implicitly created by the UNIQUE constraint in most SQLite versions, but explicit for clarity.
-- CREATE UNIQUE INDEX idx_worker_auth_token_status_token ON worker_auth_token_status (token);
-- Index for efficient cleanup based on age
CREATE INDEX idx_worker_auth_token_status_created_at ON worker_auth_token_status (created_at);
`,
	},
	// Add future migrations here, incrementing the Version number.
	// {
	// 	Version: 2,
	// 	Script: `
	// ALTER TABLE some_table ADD COLUMN new_column TEXT;
	// CREATE INDEX idx_new_column ON some_table(new_column);
	// `,
	// },
}

// ensureSchemaTableExists creates the schema migrations table if it doesn't exist.
func ensureSchemaTableExists(ctx context.Context, tx *sql.Tx) error {
	// Use INTEGER for version as it's simpler for comparisons.
	// Use a PRIMARY KEY constraint to ensure only one row exists.
	// The CHECK (version >= 0) isn't strictly necessary but adds clarity.
	// We use a single row with a fixed ID (e.g., 1) or just `version` as the primary key
	// if we only ever store one row. Let's use `version` as the primary key column directly.
	// We will insert or replace the single version row.
	query := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s (
			version INTEGER PRIMARY KEY NOT NULL CHECK(version >= 0)
		);
	`, migrationTableName)
	_, err := tx.ExecContext(ctx, query)
	if err != nil {
		return fmt.Errorf("failed to ensure %s table exists: %w", migrationTableName, err)
	}
	return nil
}

// getCurrentVersion retrieves the current schema version from the database.
// If the table is empty or doesn't exist yet (before creation), it assumes version 0.
func getCurrentVersion(ctx context.Context, tx *sql.Tx) (int, error) {
	var currentVersion int
	// Query the single row. If no row exists, Scan returns sql.ErrNoRows.
	// We use MAX(version) in case multiple rows somehow exist (shouldn't happen with PK).
	query := fmt.Sprintf("SELECT MAX(version) FROM %s", migrationTableName)
	err := tx.QueryRowContext(ctx, query).Scan(&currentVersion)
	if err != nil {
		if err == sql.ErrNoRows {
			// No version record found, implies schema is at version 0.
			return 0, nil
		}
		// Handle case where the value might be NULL if the table is empty
		var nullVersion sql.NullInt64
		err = tx.QueryRowContext(ctx, query).Scan(&nullVersion)
		if err == nil && !nullVersion.Valid {
			return 0, nil // Table exists but is empty
		}
		if err != nil {
			return -1, fmt.Errorf("failed to query current schema version: %w", err)
		}
	}
	return currentVersion, nil
}

// setCurrentVersion updates the schema version in the database.
// It uses INSERT OR REPLACE (or equivalent logic) to handle the single version row.
func setCurrentVersion(ctx context.Context, tx *sql.Tx, version int) error {
	// Ensure only one row exists by deleting existing rows (if any) and inserting the new version.
	// Alternatively, use INSERT OR REPLACE if the table structure supports it (e.g., primary key on version).
	// Since version is the PK, we can use INSERT OR REPLACE.
	// Or simpler: DELETE all first, then INSERT.
	delQuery := fmt.Sprintf("DELETE FROM %s", migrationTableName)
	_, err := tx.ExecContext(ctx, delQuery)
	if err != nil {
		return fmt.Errorf("failed to clear existing schema version: %w", err)
	}

	insQuery := fmt.Sprintf("INSERT INTO %s (version) VALUES (?)", migrationTableName)
	_, err = tx.ExecContext(ctx, insQuery, version)
	if err != nil {
		return fmt.Errorf("failed to set current schema version to %d: %w", version, err)
	}
	return nil
}

// splitStatements splits a multi-statement SQL script into individual statements.
// Handles simple cases, might need refinement for complex SQL with comments or string literals containing ';'.
func splitStatements(script string) []string {
	// Naive split, assumes ';' only terminates statements and isn't within literals etc.
	// Trim whitespace and filter out empty statements.
	statements := strings.Split(script, ";")
	var cleaned []string
	for _, stmt := range statements {
		trimmed := strings.TrimSpace(stmt)
		if trimmed != "" {
			cleaned = append(cleaned, trimmed)
		}
	}
	return cleaned
}

// ApplyMigrations applies all pending migrations to the database.
func ApplyMigrations(ctx context.Context, db *sql.DB) error {
	// Sort migrations by version just in case they were defined out of order.
	sort.SliceStable(migrations, func(i, j int) bool {
		return migrations[i].Version < migrations[j].Version
	})

	// Check for duplicate version numbers
	versions := make(map[int]bool)
	for _, m := range migrations {
		if versions[m.Version] {
			return fmt.Errorf("duplicate migration version found: %d", m.Version)
		}
		if m.Version <= 0 {
			return fmt.Errorf("migration version must be positive, found: %d", m.Version)
		}
		versions[m.Version] = true
	}

	// Start a transaction for the entire migration process or per-migration?
	// Let's do it per migration for simplicity, though a single transaction for all
	// pending migrations might be slightly more robust if intermediate states are undesirable.
	// However, SQLite might lock the DB for longer with one large transaction.
	// Per-migration transaction seems like a reasonable balance.

	// We need a transaction first to check/create the schema table and get the version atomically.
	initialTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin initial transaction for migration check: %w", err)
	}
	defer initialTx.Rollback() // Rollback if not committed

	// 1. Ensure the schema migrations table exists.
	if err := ensureSchemaTableExists(ctx, initialTx); err != nil {
		return err // Error includes context
	}

	// 2. Get the current version.
	currentVersion, err := getCurrentVersion(ctx, initialTx)
	if err != nil {
		return err // Error includes context
	}

	// Commit the initial transaction (table creation/version check)
	if err := initialTx.Commit(); err != nil {
		return fmt.Errorf("failed to commit initial transaction for migration check: %w", err)
	}

	log.Printf("Current database schema version: %d", currentVersion)

	// 3. Apply pending migrations.
	appliedCount := 0
	for _, m := range migrations {
		if m.Version > currentVersion {
			log.Printf("Applying migration version %d...", m.Version)
			// Start a transaction for this specific migration
			tx, err := db.BeginTx(ctx, nil)
			if err != nil {
				return fmt.Errorf("failed to begin transaction for migration %d: %w", m.Version, err)
			}

			// Execute the script statements
			statements := splitStatements(m.Script)
			for _, stmt := range statements {
				if _, err := tx.ExecContext(ctx, stmt); err != nil {
					tx.Rollback() // Rollback on error
					return fmt.Errorf("failed to execute statement in migration %d: %w\nStatement: %s", m.Version, err, stmt)
				}
			}

			// Update the schema version within the same transaction
			if err := setCurrentVersion(ctx, tx, m.Version); err != nil {
				tx.Rollback() // Rollback on error
				return fmt.Errorf("failed to update schema version after migration %d: %w", m.Version, err)
			}

			// Commit the transaction for this migration
			if err := tx.Commit(); err != nil {
				// Attempt rollback, though commit failed likely means connection issue
				tx.Rollback()
				return fmt.Errorf("failed to commit transaction for migration %d: %w", m.Version, err)
			}
			log.Printf("Successfully applied migration version %d.", m.Version)
			appliedCount++
			currentVersion = m.Version // Update our tracked current version
		}
	}

	if appliedCount == 0 {
		log.Println("Database schema is up to date.")
	} else {
		log.Printf("Applied %d migration(s). Database schema is now at version %d.", appliedCount, currentVersion)
	}

	return nil
}
