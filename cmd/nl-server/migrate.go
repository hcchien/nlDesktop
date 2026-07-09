package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net/url"
	"os"

	"ariga.io/atlas/sql/sqltool"
	"entgo.io/ent/dialect"
	entschema "entgo.io/ent/dialect/sql/schema"
	gomigrate "github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"

	"github.com/hcchien/nl/ent/migrate"
)

const migrationsDir = "migrations"

// migrateDiff 產生版本化 migration 檔（golang-migrate 格式，含 up/down）。
// 以 dev scratch DB 重放既有 migrations 後與宣告 schema 做 diff，
// 產出的 SQL 檔進 git、與 schema 變更同一個 PR 接受 review。
func migrateDiff(ctx context.Context, name string) error {
	devURL := envOr("NL_DEV_DATABASE_URL", "postgres://localhost:5432/nl_migrate_dev?sslmode=disable")
	ensureDatabase(devURL)
	if err := os.MkdirAll(migrationsDir, 0o755); err != nil {
		return err
	}
	dir, err := sqltool.NewGolangMigrateDir(migrationsDir)
	if err != nil {
		return err
	}
	return migrate.NamedDiff(ctx, devURL, name,
		entschema.WithDir(dir),
		entschema.WithMigrationMode(entschema.ModeReplay),
		entschema.WithDialect(dialect.Postgres),
		entschema.WithFormatter(sqltool.GolangMigrateFormatter),
		// 版本化檔案允許破壞性 DDL —— 由 code review 與 CI 把關，而非執行期猜測
		entschema.WithDropColumn(true),
		entschema.WithDropIndex(true),
	)
}

// migrateUp 依序套用 migrations/ 中尚未執行的版本（歷史記錄於 schema_migrations 表）。
func migrateUp(dbURL string) error {
	m, err := gomigrate.New("file://"+migrationsDir, dbURL)
	if err != nil {
		return err
	}
	defer m.Close()
	if err := m.Up(); err != nil {
		if errors.Is(err, gomigrate.ErrNoChange) {
			log.Println("migrate up: already up to date")
			return nil
		}
		return err
	}
	v, _, _ := m.Version()
	log.Printf("migrate up: applied through version %d", v)
	return nil
}

// ensureDatabase 若目標 DB 不存在則嘗試建立（連到同主機的 postgres 管理 DB）。
func ensureDatabase(dbURL string) {
	u, err := url.Parse(dbURL)
	if err != nil || len(u.Path) < 2 {
		return
	}
	dbName := u.Path[1:]
	probe, err := sql.Open("pgx", dbURL)
	if err == nil && probe.Ping() == nil {
		probe.Close()
		return
	}
	if probe != nil {
		probe.Close()
	}
	admin := *u
	admin.Path = "/postgres"
	adminDB, err := sql.Open("pgx", admin.String())
	if err != nil {
		return
	}
	defer adminDB.Close()
	if _, err := adminDB.Exec(fmt.Sprintf("CREATE DATABASE %q", dbName)); err == nil {
		log.Printf("created database %s", dbName)
	}
}
