package mysql_test

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/violin0622/robit"
	"github.com/violin0622/robit/mysql"
)

var testschema = mysql.Schema{
	Table:     "mutex_tab",
	Subject:   "holder_id_col",
	Object:    "lock_id_col",
	ExpiresAt: "expires_at_col",
}

func TestElectStmt(t *testing.T) {
	stmt, err := mysql.ElectStmt(testschema)
	if err != nil {
		t.Fatal(err)
	}

	expect := "INSERT INTO `mutex_tab` (`holder_id_col`, `lock_id_col`, `expires_at_col`) " +
		"VALUES (?, ?, FLOOR(UNIX_TIMESTAMP(NOW(3)) * 1000) + ?) " +
		"ON DUPLICATE KEY UPDATE " +
		"`holder_id_col` = IF(`expires_at_col` < FLOOR(UNIX_TIMESTAMP(NOW(3)) * 1000), VALUES(`holder_id_col`), `holder_id_col`), " +
		"`expires_at_col` = IF(`expires_at_col` < FLOOR(UNIX_TIMESTAMP(NOW(3)) * 1000), VALUES(`expires_at_col`), `expires_at_col`);"
	if stmt != expect {
		t.Fatal("elect statement not match expect, actual: ", stmt)
	}
}

func TestRefreshStmt(t *testing.T) {
	stmt, err := mysql.RefreshStmt(testschema)
	if err != nil {
		t.Fatal(err)
	}

	expect := "UPDATE `mutex_tab` " +
		"SET `expires_at_col` = FLOOR(UNIX_TIMESTAMP(NOW(3)) * 1000) + ? " +
		"WHERE `holder_id_col` = ? " +
		"AND `lock_id_col` = ?;"
	if stmt != expect {
		t.Fatal("refresh statement not match expect, actual: ", stmt)
	}
}

func TestCheckStmt(t *testing.T) {
	stmt, err := mysql.CheckStmt(testschema)
	if err != nil {
		t.Fatal(err)
	}

	expect := "SELECT COUNT(*) FROM `mutex_tab` " +
		"WHERE `holder_id_col` = ? " +
		"AND `lock_id_col` = ? " +
		"AND `expires_at_col` >= FLOOR(UNIX_TIMESTAMP(NOW(3)) * 1000);"
	if stmt != expect {
		t.Fatal("check statement not match expect, actual: ", stmt)
	}
}

func TestCheckHolderStmt(t *testing.T) {
	stmt, err := mysql.CheckHolderStmt(testschema)
	if err != nil {
		t.Fatal(err)
	}

	expect := "SELECT `holder_id_col` FROM `mutex_tab` " +
		"WHERE `lock_id_col` = ? " +
		"AND `expires_at_col` >= FLOOR(UNIX_TIMESTAMP(NOW(3)) * 1000) " +
		"LIMIT 1;"
	if stmt != expect {
		t.Fatal("check holder statement not match expect, actual: ", stmt)
	}
}

func TestReleaseStmt(t *testing.T) {
	stmt, err := mysql.ReleaseStmt(testschema)
	if err != nil {
		t.Fatal(err)
	}

	expect := "DELETE FROM `mutex_tab` " +
		"WHERE `holder_id_col` = ? " +
		"AND `lock_id_col` = ?;"
	if stmt != expect {
		t.Fatal("release statement not match expect, actual: ", stmt)
	}
}

func TestNew_ValidateSchema(t *testing.T) {
	// Use a valid DSN format; New() does not connect, only validates and builds queries.
	addr := os.Getenv("MYSQL_ADDR")
	if addr == "" {
		addr = "127.0.0.1:3306"
	}
	db, err := sql.Open("mysql", "root@tcp("+addr+")/")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	tests := []struct {
		name    string
		schema  mysql.Schema
		wantErr string
	}{
		{"empty Table", mysql.Schema{Table: "", Subject: "s", Object: "o", ExpiresAt: "e"}, "schema Table is empty"},
		{"empty Subject", mysql.Schema{Table: "t", Subject: "", Object: "o", ExpiresAt: "e"}, "schema Subject is empty"},
		{"empty Object", mysql.Schema{Table: "t", Subject: "s", Object: "", ExpiresAt: "e"}, "schema Object is empty"},
		{"empty ExpiresAt", mysql.Schema{Table: "t", Subject: "s", Object: "o", ExpiresAt: ""}, "schema ExpiresAt is empty"},
		{"invalid char semicolon", mysql.Schema{Table: "t;DROP", Subject: "s", Object: "o", ExpiresAt: "e"}, "invalid identifier"},
		{"invalid char backtick", mysql.Schema{Table: "t", Subject: "s`x", Object: "o", ExpiresAt: "e"}, "invalid identifier"},
		{"valid Unicode", mysql.Schema{Table: "锁表", Subject: "持有者", Object: "锁名", ExpiresAt: "过期时间"}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := mysql.New[int64](1, db, tt.schema)
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

	dr, err := mysql.New[int64](12, db, testschema)
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
		t.Fatal(`acquire should return error`)
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

func newDriver(t *testing.T, db *sql.DB) *mysql.Driver[int64] {
	dr, err := mysql.New[int64](subject, db, testschema)
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

	addr := os.Getenv("MYSQL_ADDR")
	if addr == "" {
		addr = "localhost:3306"
	}

	db, err := sql.Open(`mysql`, addr+`/`)
	if err != nil {
		panic(err)
	}

	return db
}
