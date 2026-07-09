// Package nltest 提供整合測試 helper：連上真實 PostgreSQL、重置 schema、
// 完成 migration 與權限掛載。框架使用者也可用它測試自己的 schema 與權限規則。
package nltest

import (
	"context"
	"database/sql"
	"os"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/hcchien/nl/ent"
	"github.com/hcchien/nl/server"

	// 註冊 lists 宣告（access/meta 於執行期從 nl registry 導出）
	_ "github.com/hcchien/nl/lists"
)

// DefaultDatabaseURL 是本機測試 DB；CI 以 NL_TEST_DATABASE_URL 覆寫。
const DefaultDatabaseURL = "postgres://localhost:5432/nl_test?sslmode=disable"

// Open 連上測試 DB，重置 public schema 後完成 migration 與權限掛載。
// PostgreSQL 不可用時 t.Skip。
func Open(t *testing.T) *ent.Client {
	t.Helper()
	url := os.Getenv("NL_TEST_DATABASE_URL")
	if url == "" {
		url = DefaultDatabaseURL
	}
	db, err := sql.Open("pgx", url)
	if err != nil {
		t.Skipf("skipping: cannot open test database: %v", err)
	}
	if err := db.Ping(); err != nil {
		// 嘗試建立測試 DB（本機開發常見情境）
		if !tryCreateTestDB(url) {
			t.Skipf("skipping: test database unavailable: %v (set NL_TEST_DATABASE_URL or createdb nl_test)", err)
		}
		db, err = sql.Open("pgx", url)
		if err != nil || db.Ping() != nil {
			t.Skipf("skipping: test database unavailable after create attempt")
		}
	}
	if _, err := db.Exec("DROP SCHEMA public CASCADE; CREATE SCHEMA public;"); err != nil {
		t.Fatalf("resetting schema: %v", err)
	}
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { client.Close() })
	if err := server.Setup(context.Background(), client); err != nil {
		t.Fatalf("migrating: %v", err)
	}
	return client
}

// tryCreateTestDB 對同主機的 postgres 管理 DB 執行 CREATE DATABASE nl_test。
func tryCreateTestDB(url string) bool {
	if os.Getenv("NL_TEST_DATABASE_URL") != "" {
		return false // 使用者指定的 DB 不代管
	}
	admin, err := sql.Open("pgx", "postgres://localhost:5432/postgres?sslmode=disable")
	if err != nil {
		return false
	}
	defer admin.Close()
	_, err = admin.Exec("CREATE DATABASE nl_test")
	return err == nil
}
