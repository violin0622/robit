// Package mysql provides a MySQL-backed robit.Driver for distributed locking.
//
// # Table Structure
//
// Create a table with at least three columns. The Object column (lock ID) must
// have a UNIQUE or PRIMARY KEY constraint for ON DUPLICATE KEY UPDATE to work.
//
// Example:
//
//	CREATE TABLE distributed_locks (
//	    lock_id    BIGINT       NOT NULL COMMENT 'lock target ID',
//	    holder_id  BIGINT       NOT NULL COMMENT 'current holder ID',
//	    expires_at BIGINT       NOT NULL COMMENT 'expiry time, Unix ms',
//	    PRIMARY KEY (lock_id)
//	);
//
// Column semantics:
//   - Subject (holder): identifies the lock holder; use instance ID, process ID, etc.
//   - Object (lock_id): identifies the lock target; one row per lock.
//   - ExpiresAt: absolute expiry time as Unix milliseconds (BIGINT).
//
// # Schema Configuration
//
// Schema maps your column names to the driver's expected roles:
//
//	s := mysql.Schema{
//	    Table:     "distributed_locks",
//	    Subject:   "holder_id",
//	    Object:    "lock_id",
//	    ExpiresAt: "expires_at",
//	}
//
// Identifier rules: Unicode letters, digits, underscore; length 1–64.
// Examples: "lock_tab", "holder_id", "锁表", "持有者".
//
// # DSN Constraints
//
// Do NOT enable clientFoundRows in the MySQL DSN. It changes RowsAffected
// semantics and breaks Acquire's ra=0/1/2 logic.
//
// Bad:  "user:pass@tcp(host:3306)/db?clientFoundRows=true"
// Good: "user:pass@tcp(host:3306)/db"
//
// # Usage
//
//	db, _ := sql.Open("mysql", dsn)
//	dr, err := mysql.New[int64](instanceID, db, schema)
//	mutex := robit.New(lockID, 30*time.Second, dr)
package mysql
