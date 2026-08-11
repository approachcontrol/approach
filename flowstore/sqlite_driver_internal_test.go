package flowstore

import (
	"database/sql"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestModerncSQLiteDSNConfiguresBusyTimeoutAndImmediateTransactions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "driver.db")
	dsnURL := url.URL{Scheme: "file", Path: path}
	query := dsnURL.Query()
	query.Add("_pragma", "busy_timeout(25)")
	query.Set("_txlock", "immediate")
	dsnURL.RawQuery = query.Encode()

	db, err := sql.Open("sqlite", dsnURL.String())
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(2)

	for i := 0; i < 2; i++ {
		conn, err := db.Conn(t.Context())
		if err != nil {
			t.Fatalf("Conn(%d) error = %v", i, err)
		}
		var timeout int
		if err := conn.QueryRowContext(t.Context(), "PRAGMA busy_timeout").Scan(&timeout); err != nil {
			_ = conn.Close()
			t.Fatalf("PRAGMA busy_timeout on conn %d error = %v", i, err)
		}
		if timeout != 25 {
			_ = conn.Close()
			t.Fatalf("busy_timeout on conn %d = %d, want 25", i, timeout)
		}
		if err := conn.Close(); err != nil {
			t.Fatalf("close conn %d: %v", i, err)
		}
	}

	first, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatalf("first BeginTx() error = %v", err)
	}
	t.Cleanup(func() { _ = first.Rollback() })
	started := time.Now()
	_, err = db.BeginTx(t.Context(), nil)
	if err == nil {
		t.Fatal("second BeginTx() error = nil, want an immediate busy failure")
	}
	if elapsed := time.Since(started); elapsed < 15*time.Millisecond || elapsed > time.Second {
		t.Fatalf("second BeginTx() waited %v, want approximately the configured 25ms", elapsed)
	}
}
