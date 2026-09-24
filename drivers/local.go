package drivers

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/azhai/mocca/models"
	"github.com/pkg/errors"
)

// Local 本地目录。Root 是内容根（媒体），MetaRoot 是元数据根（meta_dir）：
// 即 .mocca 目录本身，默认落在内容根下的 .mocca，让封面/简介随物理设备走。
type Local struct {
	Root     string
	MetaRoot string
}

// NewLocal 从存储的私有配置里取根目录。
func NewLocal(s *models.Storage) (Driver, error) {
	var a struct {
		RootFolderPath string `json:"root_folder_path"`
		MetaDir        string `json:"meta_dir"`
	}
	if err := json.Unmarshal([]byte(s.Addition), &a); err != nil {
		return nil, errors.Wrap(err, "解析本地存储配置失败")
	}
	root := a.RootFolderPath
	if root == "" {
		root = "."
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, errors.WithStack(err)
	}
	// meta_dir = 元数据目录（.mocca 目录本身）。缺省用内容根下的 .mocca；
	// 相对路径视作相对内容根，绝对路径原样使用（可在设备根甚至别处）。
	storeDir := a.MetaDir
	switch {
	case storeDir == "":
		storeDir = filepath.Join(abs, ".mocca")
	case !filepath.IsAbs(storeDir):
		storeDir = filepath.Join(abs, storeDir)
	}
	if storeDir, err = filepath.Abs(storeDir); err != nil {
		return nil, errors.WithStack(err)
	}
	return &Local{Root: abs, MetaRoot: storeDir}, nil
}

func (d *Local) List(rel string) ([]Entry, error) {
	infos, err := os.ReadDir(filepath.Join(d.Root, relPath(rel)))
	if err != nil {
		return nil, errors.WithStack(err)
	}
	out := make([]Entry, 0, len(infos))
	for _, i := range infos {
		info, err := i.Info()
		if err != nil {
			continue
		}
		out = append(out, Entry{
			Name:     i.Name(),
			Size:     info.Size(),
			IsDir:    i.IsDir(),
			Modified: info.ModTime(),
		})
	}
	return out, nil
}

func (d *Local) Stat(rel string) (Entry, error) {
	info, err := os.Stat(filepath.Join(d.Root, relPath(rel)))
	if err != nil {
		return Entry{}, errors.WithStack(err)
	}
	return Entry{
		Name:     info.Name(),
		Size:     info.Size(),
		IsDir:    info.IsDir(),
		Modified: info.ModTime(),
	}, nil
}

func (d *Local) Open(rel string) (io.ReadSeeker, int64, error) {
	f, err := os.Open(filepath.Join(d.Root, relPath(rel)))
	if err != nil {
		return nil, 0, errors.WithStack(err)
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, 0, errors.WithStack(err)
	}
	return f, info.Size(), nil
}

func (d *Local) MkdirAll(rel string) error {
	return errors.WithStack(os.MkdirAll(filepath.Join(d.Root, relPath(rel)), 0o755))
}

// Create 建父目录再建文件，让调用方不用关心目录是否存在。
func (d *Local) Create(rel string) (io.WriteCloser, error) {
	p := filepath.Join(d.Root, relPath(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return nil, errors.WithStack(err)
	}
	f, err := os.Create(p)
	if err != nil {
		return nil, errors.WithStack(err)
	}
	return f, nil
}

// Remove 文件或目录都走 RemoveAll：目录需要递归删，文件也无差别。
func (d *Local) Remove(rel string) error {
	return errors.WithStack(os.RemoveAll(filepath.Join(d.Root, relPath(rel))))
}

// Rename 同目录改名。
func (d *Local) Rename(rel, newName string) error {
	src := filepath.Join(d.Root, relPath(rel))
	if newName = filepath.Base(newName); newName == "." || newName == "/" || newName == "" {
		return errors.New("新名称不合法")
	}
	return errors.WithStack(os.Rename(src, filepath.Join(filepath.Dir(src), newName)))
}

// Move 移动到另一个目录，保留原文件名。
func (d *Local) Move(rel, dstDirRel string) error {
	src := filepath.Join(d.Root, relPath(rel))
	dst := filepath.Join(d.Root, relPath(dstDirRel), filepath.Base(src))
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return errors.WithStack(err)
	}
	return errors.WithStack(os.Rename(src, dst))
}

func (d *Local) Close() error { return nil }

// metaJoin 拼元数据根下的绝对路径。
func (d *Local) metaJoin(rel string) string {
	return filepath.Join(d.MetaRoot, relPath(rel))
}

// Meta* 系列与内容方法同实现，只是根换成 MetaRoot（设备根，.mocca 所在处）。
func (d *Local) MetaStat(rel string) (Entry, error) {
	info, err := os.Stat(d.metaJoin(rel))
	if err != nil {
		return Entry{}, errors.WithStack(err)
	}
	return Entry{
		Name: info.Name(), Size: info.Size(), IsDir: info.IsDir(), Modified: info.ModTime(),
	}, nil
}

func (d *Local) MetaOpen(rel string) (io.ReadSeeker, int64, error) {
	f, err := os.Open(d.metaJoin(rel))
	if err != nil {
		return nil, 0, errors.WithStack(err)
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, 0, errors.WithStack(err)
	}
	return f, info.Size(), nil
}

func (d *Local) MetaMkdirAll(rel string) error {
	return errors.WithStack(os.MkdirAll(d.metaJoin(rel), 0o755))
}

func (d *Local) MetaCreate(rel string) (io.WriteCloser, error) {
	p := d.metaJoin(rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return nil, errors.WithStack(err)
	}
	return os.Create(p)
}

func (d *Local) MetaRemove(rel string) error {
	return errors.WithStack(os.RemoveAll(d.metaJoin(rel)))
}

// filepathCleanSlash 清理路径并确保带前导斜杠。
func filepathCleanSlash(rel string) string {
	if !strings.HasPrefix(rel, "/") {
		rel = "/" + rel
	}
	return filepath.Clean(rel)
}
