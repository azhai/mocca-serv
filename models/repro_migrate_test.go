package models

import (
	"path/filepath"
	"testing"

	"database/sql"

	_ "modernc.org/sqlite"
)

// TestOpenMigratesExistingDB 回归：老库（无 director/year/region/studio/language 列）
// 在 Open 自动迁移时曾报 "near ',': syntax error" —— 重建表的 INSERT..SELECT 对新列
// 注入空表达式产生裸逗号。此处校验迁移不报错、迁移前数据保留，且二次打开幂等。
func TestOpenMigratesExistingDB(t *testing.T) {
	dir := t.TempDir()
	dbFile := filepath.Join(dir, "old.db")

	db, err := sql.Open("sqlite", dbFile)
	if err != nil {
		t.Fatal(err)
	}
	old := `
CREATE TABLE media_meta (
	id integer PRIMARY KEY AUTOINCREMENT NOT NULL,
	path varchar(255) NOT NULL,
	kind integer NOT NULL DEFAULT 0,
	size integer NOT NULL DEFAULT 0,
	title varchar(255) NOT NULL DEFAULT '',
	duration integer NOT NULL DEFAULT 0,
	cover varchar(255) NOT NULL DEFAULT '',
	description text NOT NULL DEFAULT '',
	created_at datetime NOT NULL DEFAULT '0000-01-01',
	updated_at datetime NOT NULL DEFAULT '0000-01-01'
);`
	if _, err = db.Exec(old); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO media_meta(path, kind, size, title) VALUES('/a.mp4', 2, 12345, '老片')`); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := Open(dbFile)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	row, err := GetSchema().MediaMeta.Where("path = ?", "/a.mp4").Select().One()
	if err != nil {
		t.Fatalf("read row: %v", err)
	}
	if row == nil || row.Size != 12345 || row.Director != "" {
		t.Fatalf("data not preserved: %+v", row)
	}
	if err = Close(); err != nil {
		t.Fatal(err)
	}
	_ = s

	// 二次打开应幂等，不再报迁移错误
	if _, err = Open(dbFile); err != nil {
		t.Fatalf("re-open failed: %v", err)
	}
}
