package pgsql_test

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/lib/pq"
	"github.com/violin0622/robit"
	"github.com/violin0622/robit/pgsql"
)

const nowMsExpr = `(FLOOR(EXTRACT(EPOCH FROM clock_timestamp()) * 1000))::bigint`

var testschema = pgsql.Schema{
	Table:     "mutex_tab",
	Subject:   "holder_id_col",
	Object:    "lock_id_col",
	ExpiresAt: "expires_at_col",
}

func TestElectStmt(t *testing.T) {
	stmt, err := pgsql.ElectStmt(testschema)
	if err != nil {
		t.Fatal(err)
	}

	expect := `WITH now_ms AS (` +
		`  SELECT (FLOOR(EXTRACT(EPOCH FROM clock_timestamp()) * 1000))::bigint AS ms` +
		`) ` +
		`INSERT INTO "mutex_tab" ("holder_id_col", "lock_id_col", "expires_at_col") ` +
		`SELECT $1, $2, (SELECT ms FROM now_ms) + $3 ` +
		`FROM now_ms ` +
		`ON CONFLICT ("lock_id_col") DO UPDATE SET ` +
		`"holder_id_col" = CASE WHEN "mutex_tab"."expires_at_col" < (SELECT ms FROM now_ms) THEN EXCLUDED."holder_id_col" ELSE "mutex_tab"."holder_id_col" END, ` +
		`"expires_at_col" = CASE WHEN "mutex_tab"."expires_at_col" < (SELECT ms FROM now_ms) THEN EXCLUDED."expires_at_col" ELSE "mutex_tab"."expires_at_col" END ` +
		`RETURNING "mutex_tab"."holder_id_col", "mutex_tab"."expires_at_col", ("mutex_tab"."holder_id_col" = EXCLUDED."holder_id_col" AND "mutex_tab"."expires_at_col" = EXCLUDED."expires_at_col");`
	if stmt != expect {
		t.Fatalf("elect statement not match expect\nactual: %s", stmt)
	}
}

func TestRefreshStmt(t *testing.T) {
	stmt, err := pgsql.RefreshStmt(testschema)
	if err != nil {
		t.Fatal(err)
	}

	expect := `UPDATE "mutex_tab" ` +
		`SET "expires_at_col" = ` + nowMsExpr + ` + $1 ` +
		`WHERE "holder_id_col" = $2 ` +
		`AND "lock_id_col" = $3;`
	if stmt != expect {
		t.Fatalf("refresh statement not match expect\nactual: %s", stmt)
	}
}

func TestCheckStmt(t *testing.T) {
	stmt, err := pgsql.CheckStmt(testschema)
	if err != nil {
		t.Fatal(err)
	}

	expect := `SELECT COUNT(*) FROM "mutex_tab" ` +
		`WHERE "holder_id_col" = $1 ` +
		`AND "lock_id_col" = $2 ` +
		`AND "expires_at_col" >= ` + nowMsExpr + `;`
	if stmt != expect {
		t.Fatalf("check statement not match expect\nactual: %s", stmt)
	}
}

func TestCheckHolderStmt(t *testing.T) {
	stmt, err := pgsql.CheckHolderStmt(testschema)
	if err != nil {
		t.Fatal(err)
	}

	expect := `SELECT "holder_id_col" FROM "mutex_tab" ` +
		`WHERE "lock_id_col" = $1 ` +
		`AND "expires_at_col" >= ` + nowMsExpr + ` ` +
		`LIMIT 1;`
	if stmt != expect {
		t.Fatalf("check holder statement not match expect\nactual: %s", stmt)
	}
}

func TestReleaseStmt(t *testing.T) {
	stmt, err := pgsql.ReleaseStmt(testschema)
	if err != nil {
		t.Fatal(err)
	}

	expect := `DELETE FROM "mutex_tab" ` +
		`WHERE "holder_id_col" = $1 ` +
		`AND "lock_id_col" = $2;`
	if stmt != expect {
		t.Fatalf("release statement not match expect\nactual: %s", stmt)
	}
}

func TestNew_ValidateSchema(t *testing.T) {
	dsn := os.Getenv("PGSQL_DSN")
	if dsn == "" {
		dsn = "postgres://localhost/postgres?sslmode=disable"
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	tests := []struct {
		name    string
		schema  pgsql.Schema
		wantErr string
	}{
		{"empty Table", pgsql.Schema{Table: "", Subject: "s", Object: "o", ExpiresAt: "e"}, "schema Table"},
		{"empty Subject", pgsql.Schema{Table: "t", Subject: "", Object: "o", ExpiresAt: "e"}, "schema Subject"},
		{"empty Object", pgsql.Schema{Table: "t", Subject: "s", Object: "", ExpiresAt: "e"}, "schema Object"},
		{"empty ExpiresAt", pgsql.Schema{Table: "t", Subject: "s", Object: "o", ExpiresAt: ""}, "schema ExpiresAt"},
		{"invalid char semicolon", pgsql.Schema{Table: "t;DROP", Subject: "s", Object: "o", ExpiresAt: "e"}, "invalid identifier"},
		{"invalid char quote", pgsql.Schema{Table: "t", Subject: "s\"x", Object: "o", ExpiresAt: "e"}, "invalid identifier"},
		{"valid Unicode", pgsql.Schema{Table: "锁表", Subject: "持有者", Object: "锁名", ExpiresAt: "过期时间"}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := pgsql.New[int64](1, db, tt.schema)
			if tt.wantErr == "" {
				if err != nil {
					t.Errorf("New() unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("New() expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("New() error %q does not contain %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestDriver_New(t *testing.T) {
	db := getDB(t)
	defer db.Close()

	dr, err := pgsql.New[int64](12, db, testschema)
	if err != nil {
		t.Fatal(`new: `, err)
	}
	if dr == nil {
		t.Fatal(`new driver got nil pointer `)
	}
}

func TestDriver_Acquire(t *testing.T) {
	db := getDB(t)
	defer db.Close()

	dr := newDriver(t, db)
	ctx := context.Background()

	lease, err := dr.Acquire(ctx, object, ttl)
	if err != nil {
		t.Fatal(`acquire: `, err)
	}
	if lease == nil {
		t.Fatal(`acquire got nil lease`)
	}
	defer dr.Release(lease)

	lease, err = dr.Acquire(ctx, object, ttl)
	if err != robit.ErrAlreadyHeld {
		t.Fatal(`acquire should return ErrAlreadyHeld, got: `, err)
	}
	if lease != nil {
		t.Fatal(`acquire got non-nil lease`)
	}

	lease2, err := dr.Acquire(ctx, object+1, ttl)
	if err != nil {
		t.Fatal(`acquire another: `, err)
	}
	if lease2 == nil {
		t.Fatal(`acquire another got nil lease`)
	}
	defer dr.Release(lease2)
}

const (
	subject = 12
	object  = 25
	ttl     = time.Second
)

func newDriver(t *testing.T, db *sql.DB) *pgsql.Driver[int64] {
	t.Helper()
	dr, err := pgsql.New[int64](subject, db, testschema)
	if err != nil {
		t.Fatal(`new: `, err)
	}
	if dr == nil {
		t.Fatal(`new driver got nil pointer `)
	}
	return dr
}

func getDB(t *testing.T) *sql.DB {
	t.Helper()

	dsn := os.Getenv("PGSQL_DSN")
	if dsn == "" {
		dsn = "postgres://localhost/postgres?sslmode=disable"
	}

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Skipf("skip: cannot open postgres: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		t.Skipf("skip: cannot connect to postgres: %v", err)
	}

	// Create test table if not exists
	_, _ = db.Exec(`
		CREATE TABLE IF NOT EXISTS mutex_tab (
			lock_id_col    BIGINT NOT NULL,
			holder_id_col  BIGINT NOT NULL,
			expires_at_col BIGINT NOT NULL,
			PRIMARY KEY (lock_id_col)
		)
	`)

	return db
}
