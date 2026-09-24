package drivers

import (
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"strings"

	"github.com/azhai/mocca/models"
	"github.com/cloudsoda/go-smb2"
	"github.com/pkg/errors"
)

// smbAddition Samba 存储的私有配置，字段沿用原有 SMB 驱动，便于直接复用旧数据。
type smbAddition struct {
	Address        string `json:"address"` // host 或 host:445
	Username       string `json:"username"`
	Password       string `json:"password"`
	ShareName      string `json:"share_name"`       // 共享名
	RootFolderPath string `json:"root_folder_path"` // 共享内的根目录
	MetaDir        string `json:"meta_dir"`         // 元数据目录（共享内，缺省 <根>/.mocca）
}

// SMB Samba 共享。连接来自复用池，Close 只归还引用不会断开。
type SMB struct {
	key      string
	conn     *smbConn
	Root     string
	metaRoot string // 元数据根：共享内的 .mocca 目录
	closed   bool
}

// NewSMB 从连接池取一条到该共享的连接（没有才真正拨号 + 挂载）。
func NewSMB(s *models.Storage) (Driver, error) {
	var a smbAddition
	if err := json.Unmarshal([]byte(s.Addition), &a); err != nil {
		return nil, errors.Wrap(err, "解析 Samba 配置失败")
	}
	if a.Address == "" || a.ShareName == "" {
		return nil, errors.New("Samba 配置缺少 address 或 share_name")
	}
	addr := a.Address
	if !strings.Contains(addr, ":") {
		addr += ":445"
	}
	if a.RootFolderPath == "" {
		a.RootFolderPath = "."
	}

	dialer := &smb2.Dialer{
		Initiator: &smb2.NTLMInitiator{
			User:     a.Username,
			Password: a.Password,
		},
	}
	conn, err := smbAcquire(smbKey(s, a), func() (*smb2.Share, *smb2.Session, error) {
		session, err := dialer.Dial(context.Background(), addr)
		if err != nil {
			return nil, nil, errors.Wrapf(err, "连接 Samba %s 失败", addr)
		}
		share, err := session.Mount(a.ShareName)
		if err != nil {
			_ = session.Logoff()
			return nil, nil, errors.Wrapf(err, "挂载共享 %s 失败", a.ShareName)
		}
		return share, session, nil
	})
	if err != nil {
		return nil, err
	}
	// 元数据根默认在共享内的 .mocca；meta_dir 始终视为相对共享根（去掉前导斜杠后拼）。
	meta := a.MetaDir
	if meta == "" {
		meta = ".mocca"
	}
	metaRoot := filepath.Join(a.RootFolderPath, strings.TrimPrefix(meta, "/"))
	return &SMB{key: smbKey(s, a), conn: conn, Root: a.RootFolderPath, metaRoot: metaRoot}, nil
}

func (d *SMB) full(rel string) string {
	return filepath.Join(d.Root, relPath(rel))
}

// metaFull 元数据根下的绝对路径（共享内），meta_dir 即 .mocca 目录本身。
func (d *SMB) metaFull(rel string) string {
	return filepath.Join(d.metaRoot, relPath(rel))
}

func (d *SMB) List(rel string) ([]Entry, error) {
	infos, err := d.conn.share.ReadDir(d.full(rel))
	if err != nil {
		return nil, errors.WithStack(err)
	}
	out := make([]Entry, 0, len(infos))
	for _, i := range infos {
		out = append(out, Entry{
			Name:     i.Name(),
			Size:     i.Size(),
			IsDir:    i.IsDir(),
			Modified: i.ModTime(),
		})
	}
	return out, nil
}

func (d *SMB) Stat(rel string) (Entry, error) { return d.statAt(d.full(rel)) }
func (d *SMB) statAt(p string) (Entry, error) {
	info, err := d.conn.share.Stat(p)
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

func (d *SMB) Open(rel string) (io.ReadSeeker, int64, error) { return d.openAt(d.full(rel)) }
func (d *SMB) openAt(p string) (io.ReadSeeker, int64, error) {
	f, err := d.conn.share.Open(p)
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

func (d *SMB) MkdirAll(rel string) error {
	return errors.WithStack(d.conn.share.MkdirAll(d.full(rel), 0o755))
}

// Create 先把父目录逐级建好再创建文件（Samba 不会自动建父目录）。
func (d *SMB) Create(rel string) (io.WriteCloser, error) { return d.createAt(d.full(rel)) }
func (d *SMB) createAt(p string) (io.WriteCloser, error) {
	if dir := filepath.Dir(p); dir != "" && dir != "." {
		if err := d.conn.share.MkdirAll(dir, 0o755); err != nil {
			return nil, errors.WithStack(err)
		}
	}
	f, err := d.conn.share.Create(p)
	if err != nil {
		return nil, errors.WithStack(err)
	}
	return f, nil
}

// Remove 删除文件或目录：先 Stat 判断类型，目录需递归删。
func (d *SMB) Remove(rel string) error { return d.removeAt(d.full(rel)) }
func (d *SMB) removeAt(p string) error {
	info, err := d.conn.share.Stat(p)
	if err != nil {
		return errors.WithStack(err)
	}
	if info.IsDir() {
		return errors.WithStack(d.conn.share.RemoveAll(p))
	}
	return errors.WithStack(d.conn.share.Remove(p))
}

// Rename 同目录改名。
func (d *SMB) Rename(rel, newName string) error {
	src := d.full(rel)
	if newName = filepath.Base(newName); newName == "" || newName == "." || newName == "/" {
		return errors.New("新名称不合法")
	}
	return errors.WithStack(d.conn.share.Rename(src, filepath.Join(filepath.Dir(src), newName)))
}

// Move 移动到另一个目录，保留原文件名。
func (d *SMB) Move(rel, dstDirRel string) error {
	src := d.full(rel)
	dst := filepath.Join(d.full(dstDirRel), filepath.Base(src))
	return errors.WithStack(d.conn.share.Rename(src, dst))
}

// Close 归还连接到池里，不真正断开：
// 每次请求都重连的代价太高，空闲超时由池统一回收（见 smbpool.go）。
func (d *SMB) Close() error {
	if d.closed {
		return nil
	}
	d.closed = true
	smbRelease(d.key)
	return nil
}

// Meta* 系列：元数据根（meta_dir / .mocca）与媒体根（Root）可能不同，
// 故走独立的 metaFull 路径，而不是复用内容方法。
func (d *SMB) MetaStat(rel string) (Entry, error) { return d.statAt(d.metaFull(rel)) }
func (d *SMB) MetaOpen(rel string) (io.ReadSeeker, int64, error) {
	return d.openAt(d.metaFull(rel))
}
func (d *SMB) MetaMkdirAll(rel string) error {
	return errors.WithStack(d.conn.share.MkdirAll(d.metaFull(rel), 0o755))
}
func (d *SMB) MetaCreate(rel string) (io.WriteCloser, error) {
	return d.createAt(d.metaFull(rel))
}
func (d *SMB) MetaRemove(rel string) error { return d.removeAt(d.metaFull(rel)) }
