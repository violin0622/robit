// Package pgsql provides a PostgreSQL-backed robit.Driver for distributed locking.
//
// # Table Structure
//
// Create a table with at least three columns. The Object column (lock ID) must
// have a UNIQUE or PRIMARY KEY constraint for ON CONFLICT to work.
//
// Example:
//
//	CREATE TABLE distributed_locks (
//	    lock_id    BIGINT       NOT NULL,
//	    holder_id  BIGINT       NOT NULL,
//	    expires_at BIGINT       NOT NULL,
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
//	s := pgsql.Schema{
//	    Table:     "distributed_locks",
//	    Subject:   "holder_id",
//	    Object:    "lock_id",
//	    ExpiresAt: "expires_at",
//	}
//
// Identifier rules: Unicode letters, digits, underscore; length 1–64.
// Examples: "lock_tab", "holder_id", "锁表", "持有者".
//
// # Usage
//
//	db, _ := sql.Open("postgres", dsn)
//	dr, err := pgsql.New[int64](instanceID, db, schema)
//	mutex := robit.New(lockID, 30*time.Second, dr)
package pgsql
