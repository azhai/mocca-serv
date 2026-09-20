package mediaindex

import (
	"bufio"
	"sort"
	"strings"
	"time"

	"github.com/azhai/mocca/drivers"
	"github.com/azhai/mocca/models"
	"github.com/pkg/errors"
)

// ScanStorage 全量扫描一个存储（挂载点）：递归它根目录下所有目录，
// 每层有媒体文件的目录都写一份 .index.jsonl。返回索引到的媒体文件总数。
func ScanStorage(s *models.Storage) (int, error) {
	drv, err := drivers.Open(s)
	if err != nil {
		return 0, errors.Wrapf(err, "打开存储 %s 失败", s.MountPath)
	}
	defer func() { _ = drv.Close() }()
	n, err := ScanDriver(drv)
	if err != nil {
		return n, errors.Wrapf(err, "扫描存储 %s 失败", s.MountPath)
	}
	return n, nil
}

// ScanDriver 对已打开的驱动递归扫描。暴露出来便于测试与复用。
func ScanDriver(drv drivers.Driver) (int, error) {
	total := 0
	if err := scanDir(drv, "", &total); err != nil {
		return total, err
	}
	return total, nil
}

// joinRel 拼子路径：根目录为空串，子项就是 name 本身。
func joinRel(dir, name string) string {
	if dir == "" {
		return name
	}
	return dir + "/" + name
}

// skipName 扫描时跳过点开头的隐藏条目：.mocca、.index.jsonl、隐藏目录/
// 文件都不该被索引或递归。系统保留名目录同样跳过，避免递归进回收站之类。
func skipName(name string) bool {
	if strings.HasPrefix(name, ".") {
		return true
	}
	lower := strings.ToLower(name)
	for _, r := range []string{
		"$recycle.bin", "system volume information", "recycler", "thumbs.db",
		"ehthumbs.db", "desktop.ini", "lost+found", "found.000",
	} {
		if lower == r {
			return true
		}
	}
	return false
}

// scanDir 递归扫描一层：收集媒体文件写索引，再递归进子目录。
func scanDir(drv drivers.Driver, dir string, total *int) error {
	entries, err := drv.List(dir)
	if err != nil {
		return errors.Wrapf(err, "列目录 %q 失败", dir)
	}
	var subdirs []string
	var medias []drivers.Entry
	for _, e := range entries {
		if skipName(e.Name) {
			continue
		}
		if e.IsDir {
			subdirs = append(subdirs, e.Name)
		} else if isMediaName(e.Name) {
			medias = append(medias, e)
		}
	}
	sort.Strings(subdirs)
	sort.SliceStable(medias, func(i, j int) bool {
		return models.LessName(medias[i].Name, medias[j].Name)
	})

	if len(medias) > 0 {
		if err := writeIndex(drv, dir, medias); err != nil {
			return err
		}
		*total += len(medias)
	}
	for _, sd := range subdirs {
		if err := scanDir(drv, joinRel(dir, sd), total); err != nil {
			return err
		}
	}
	return nil
}

// writeIndex 把一层目录的媒体文件写成 `.index.jsonl`（整份重写）。
// 增量：文件 size+modified 与旧行一致就复用旧 sha1，否则整读重算。
func writeIndex(drv drivers.Driver, dir string, medias []drivers.Entry) error {
	indexRel := joinRel(dir, IndexFileName)

	prev := map[string]Record{}
	if f, _, err := drv.Open(indexRel); err == nil {
		if p, perr := readIndex(f); perr == nil {
			prev = p
		}
		_ = f.(interface{ Close() error }).Close()
	}

	records := make([]Record, 0, len(medias))
	for _, m := range medias {
		rec, err := buildRecord(drv, joinRel(dir, m.Name), m, prev)
		if err != nil {
			return errors.Wrapf(err, "索引 %s 失败", m.Name)
		}
		records = append(records, rec)
	}

	f, err := drv.Create(indexRel)
	if err != nil {
		return errors.Wrapf(err, "写 %s 失败", indexRel)
	}
	defer func() { _ = f.(interface{ Close() error }).Close() }()
	bw := bufio.NewWriter(f)
	for _, r := range records {
		line, err := jsonMarshal(r)
		if err != nil {
			return err
		}
		if _, err := bw.Write(line); err != nil {
			return errors.Wrapf(err, "写 %s 失败", indexRel)
		}
		if err := bw.WriteByte('\n'); err != nil {
			return errors.Wrapf(err, "写 %s 失败", indexRel)
		}
	}
	return errors.WithStack(bw.Flush())
}

// buildRecord 为一个媒体文件生成索引行。
// is_new 由存储根 `.mocca` 里该 sha1 的海报(.png)+简介(.json)是否齐备推导：
// 两者都在 → 0（外部工具已写好附加信息），否则 → 1。
func buildRecord(drv drivers.Driver, rel string, m drivers.Entry, prev map[string]Record) (Record, error) {
	modified := m.Modified.UTC().Format(time.RFC3339Nano)
	sizeKB := (m.Size + 1023) / 1024 // ceil，最小 1

	sha := ""
	if old, ok := prev[m.Name]; ok && old.Modified == modified && old.SizeKB == sizeKB {
		sha = old.SHA1 // 内容没变，复用旧哈希，免整读
	} else {
		f, _, err := drv.Open(rel)
		if err != nil {
			return Record{}, errors.Wrapf(err, "打开 %s 失败", rel)
		}
		shaTmp, herr := hashContent(f)
		_ = f.(interface{ Close() error }).Close()
		if herr != nil {
			return Record{}, herr
		}
		sha = shaTmp
	}

	isNew := 1
	if poster, perr := PosterRel(sha); perr == nil {
		if summary, serr := SummaryRel(sha); serr == nil {
			if statOK(drv, poster) && statOK(drv, summary) {
				isNew = 0
			}
		}
	}
	return Record{Name: m.Name, SizeKB: sizeKB, Modified: modified, SHA1: sha, IsNew: isNew}, nil
}

// statOK 条目是否存在（针对存储根的相对路径。
func statOK(drv drivers.Driver, rel string) bool {
	_, err := drv.Stat(rel)
	return err == nil
}
