package models

import (
	"time"

	"github.com/pkg/errors"
)

// Storage 挂载点：一个驱动实例对应一个对外路径。
type Storage struct {
	ID                  uint      `json:"id" goe:"pk"`
	MountPath           string    `json:"mount_path" goe:"unique" binding:"required"`
	Order               int       `json:"order" goe:"column:sort_order"`
	Driver              string    `json:"driver"`
	Status              string    `json:"status"`
	Addition            string    `json:"addition" goe:"type:text"` // 驱动私有配置 JSON
	CacheExpiration     int       `json:"cache_expiration"`
	CustomCachePolicies string    `json:"custom_cache_policies" goe:"type:text"`
	Disabled            bool      `json:"disabled"`
	Modified            time.Time `json:"modified"`
}

// SettingFsWatch settings 表里控制「媒体文件监控（FS Watch）」的键。
//
// 放在 models 而不是某个 handler 里：启动流程（main）与后台开关（handlers）
// 都要读它，谁都不该为了一个字符串常量去依赖对方。
const SettingFsWatch = "fs_watch"

// Setting 运行期配置项（是否开放注册、签名有效期等），避免为新开关改表结构。
type Setting struct {
	ID    uint   `json:"id" goe:"pk"`
	Key   string `json:"key" goe:"unique"`
	Value string `json:"value" goe:"type:text"`
	Type  string `json:"type"`
}

func CreateStorage(s *Storage) error {
	// 挂载点在这里统一规范化：开头补斜线、末尾去斜线（`media/` → `/media`）。
	// 放在 models 而不是 handler，是为了让任何调用方（含测试、未来的导入功能）
	// 都不可能往库里写进未规范化的路径。
	s.MountPath = NormalizeMountPath(s.MountPath)
	if s.Modified.IsZero() {
		s.Modified = time.Now()
	}
	if err := GetSchema().Storage.Insert().One(s); err != nil {
		return errors.WithStack(err)
	}
	InvalidateMountTree()
	return nil
}

func UpdateStorage(s *Storage) error {
	s.MountPath = NormalizeMountPath(s.MountPath)
	s.Modified = time.Now()
	if err := GetSchema().Storage.Save().One(s); err != nil {
		return errors.WithStack(err)
	}
	InvalidateMountTree()
	return nil
}

func GetStorageByMountPath(path string) (*Storage, error) {
	s, err := GetSchema().Storage.Where("mount_path = ?", path).Select().One()
	if err != nil {
		if isNoRows(err) {
			return nil, ErrRecordNotFound
		}
		return nil, errors.Wrapf(err, "failed find storage %q", path)
	}
	if s == nil {
		return nil, ErrRecordNotFound
	}
	return s, nil
}

// GetStorageByID 按主键取挂载点。
func GetStorageByID(id uint) (*Storage, error) {
	s, err := GetSchema().Storage.Where("id = ?", id).Select().One()
	if err != nil {
		if isNoRows(err) {
			return nil, ErrRecordNotFound
		}
		return nil, errors.Wrapf(err, "failed find storage #%d", id)
	}
	if s == nil {
		return nil, ErrRecordNotFound
	}
	return s, nil
}

func ListStorages() ([]*Storage, error) {
	all, err := GetSchema().Storage.Select().All()
	return all, errors.Wrap(err, "failed list storages")
}

func DeleteStorage(id uint) error {
	if err := GetSchema().Storage.Delete().Where("id = ?", id).Exec(); err != nil {
		return errors.WithStack(err)
	}
	InvalidateMountTree()
	return nil
}

func GetSetting(key string) (*Setting, error) {
	s, err := GetSchema().Setting.Where("key = ?", key).Select().One()
	if err != nil {
		if isNoRows(err) {
			return nil, ErrRecordNotFound
		}
		return nil, errors.Wrapf(err, "failed find setting %q", key)
	}
	if s == nil {
		return nil, ErrRecordNotFound
	}
	return s, nil
}

// SetSetting 存在则更新、不存在则插入。
//
// 注意：goent 的 Save 不按唯一键 upsert —— 对一个 key 已存在的新对象直接 Save
// 会撞 UNIQUE 约束，所以必须先查出旧记录的 ID 再更新。
func SetSetting(s *Setting) error {
	if s.Key == "" {
		return errors.New("setting key is empty")
	}
	if old, err := GetSetting(s.Key); err == nil && old != nil {
		s.ID = old.ID
	}
	if s.ID == 0 {
		return errors.WithStack(GetSchema().Setting.Insert().One(s))
	}
	return errors.WithStack(GetSchema().Setting.Save().One(s))
}

// DeleteSetting 删除设置项。
func DeleteSetting(key string) error {
	return errors.WithStack(GetSchema().Setting.Delete().Where("key = ?", key).Exec())
}

// SettingBool 读布尔开关；未配置时返回 def。
// 这样「是否开放注册」这类开关可以后台改，不用重新编译。
func SettingBool(key string, def bool) bool {
	s, err := GetSetting(key)
	if err != nil || s == nil {
		return def
	}
	return s.Value == "true" || s.Value == "1"
}

// SetSettingBool 写布尔开关。
func SetSettingBool(key string, v bool) error {
	val := "false"
	if v {
		val = "true"
	}
	return SetSetting(&Setting{Key: key, Value: val, Type: "bool"})
}
