package models

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/azhai/goent"
	"github.com/azhai/goent/drivers"
)

// isNoRows 判断是否为「查不到记录」：goent 在单行查询无结果时返回 sql.ErrNoRows，
// 不是 (nil, nil)，所以调用方要显式映射成 ErrRecordNotFound。
func isNoRows(err error) bool { return errors.Is(err, sql.ErrNoRows) }

// ErrRecordNotFound 查询不到记录时返回，便于上层区分「空」与「真错误」。
var ErrRecordNotFound = errors.New("record not found")

// Tables 全部表。goent 只绑定嵌套结构体内的字段为表，
// 表名默认 snake_case：User→user、Setting→setting、MediaMeta→media_meta。
//
// 明确**不**建的表（按产品约定，数据只留在 APP 本地）：
//   - 播放历史：APP 端维护；
//   - 稍后再看：APP 端维护；
//   - 播放进度（看到第几秒）：APP 端维护。
//
// 服务端只落「收藏」这一项跨设备需要同步的数据。
type Tables struct {
	User        *goent.Table[User]
	Storage     *goent.Table[Storage]
	Setting     *goent.Table[Setting]
	Favorite    *goent.Table[Favorite]
	MediaMeta   *goent.Table[MediaMeta]
	MediaAuthor *goent.Table[MediaAuthor]
	Comment     *goent.Table[Comment]
}

// Database 组合表与连接，对应 flock 的 models.Database。
type Database struct {
	Tables `goe:"public"`
	*goent.DB
}

var dbSchema *Database

// GetSchema 返回已打开的连接。
func GetSchema() *Database { return dbSchema }

// Open 连接 sqlite（或内存库）并自动迁移到最新结构。
func Open(dbFile string) (*Database, error) {
	drv, err := drivers.Connect(drivers.DatabaseConfig{Type: "sqlite", DSN: dbFile})
	if err != nil {
		return nil, fmt.Errorf("connect database: %w", err)
	}
	s, err := goent.Open[Database](drv)
	if err != nil {
		return nil, fmt.Errorf("open schema: %w", err)
	}
	if err = goent.AutoMigrate(s); err != nil {
		return nil, fmt.Errorf("auto migrate: %w", err)
	}
	if err = ensureIndexes(s); err != nil {
		return nil, err
	}
	dbSchema = s
	// 换了一个库就等于换了一套挂载点：旧库的聚合树必须丢掉，
	// 否则新库第一次列目录会看到上一个库的挂载点。
	InvalidateMountTree()
	return s, nil
}

// hotIndexes 查询热点索引。goent 只为 unique 列建索引，
// 下面这些是实际查询条件，数据量上来不加就会全表扫。
var hotIndexes = []string{
	"CREATE INDEX IF NOT EXISTS idx_comment_path_type ON comment(path, type)",
	"CREATE INDEX IF NOT EXISTS idx_comment_user ON comment(user_id)",
	"CREATE INDEX IF NOT EXISTS idx_favorite_user ON favorite(user_id)",
	"CREATE INDEX IF NOT EXISTS idx_media_author_name ON media_author(name)",
	"CREATE INDEX IF NOT EXISTS idx_media_author_media ON media_author(media_id)",
}

func ensureIndexes(s *Database) error {
	ctx := context.Background()
	for _, stmt := range hotIndexes {
		if err := s.RawExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("create index: %w", err)
		}
	}
	return nil
}

// Close 关闭连接。
func Close() error {
	if dbSchema == nil {
		return nil
	}
	return goent.Close(dbSchema)
}
