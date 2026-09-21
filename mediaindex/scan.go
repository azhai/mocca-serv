package mediaindex

import (
	"bufio"
	"io"
	"path/filepath"
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

// collectDir 列出一层目录里的子目录与媒体文件（过滤点开头隐藏项与系统保留名）。
// scan 递归与 watcher 单目录更新共用同一份「什么该索引」的判断。
func collectDir(drv drivers.Driver, dir string) (subdirs []string, medias []drivers.Entry, err error) {
	entries, err := drv.List(dir)
	if err != nil {
		return nil, nil, errors.Wrapf(err, "列目录 %q 失败", dir)
	}
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
	return subdirs, medias, nil
}

// ScanDir 单层目录增量更新 `.index.jsonl`（整份按当前媒体文件重写）。
// 目录空（没有媒体）则移除已有索引，避免残留指向已删文件的脏行。
// 供文件监控（WatchStorages）在媒体变化时调用；全量扫描的递归入口仍是 ScanStorage。
func ScanDir(drv drivers.Driver, dir string) error {
	_, medias, err := collectDir(drv, dir)
	if err != nil {
		return err
	}
	if len(medias) > 0 {
		return writeIndex(drv, dir, medias)
	}
	// 索引文件不存在时 Remove 会报错，忽略即可
	_ = drv.Remove(joinRel(dir, IndexFileName))
	return nil
}

// scanDir 递归扫描一层：收集媒体文件写索引，再递归进子目录。
func scanDir(drv drivers.Driver, dir string, total *int) error {
	subdirs, medias, err := collectDir(drv, dir)
	if err != nil {
		return err
	}

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
// is_new 由存储根 `.mocca` 里该 sha1 的海报(.png)+简介(.meta)是否齐备推导：
// 两者都在 → 0（附加信息已就绪），否则 → 1。
//
// 顺带按类型提取元数据（错误一律非致命，失败不阻断索引）：
//   - 图片：EXIF 拍摄时间 → capture_time
//   - 音频：内嵌标签的作者/专辑 → artist/album，缺封面时把内嵌图写成海报
//   - 视频：缺封面时调 ffmpeg 抽首帧写成海报，best-effort 读容器标签 → artist/album
//
// 内容不变的增量扫描复用旧 sha1 与旧提取字段，不重复读文件重算。
func buildRecord(drv drivers.Driver, rel string, m drivers.Entry, prev map[string]Record) (Record, error) {
	modified := m.Modified.UTC().Format(time.RFC3339Nano)
	sizeKB := (m.Size + 1023) / 1024 // ceil，最小 1

	sha := ""
	changed := true
	if old, ok := prev[m.Name]; ok && old.Modified == modified && old.SizeKB == sizeKB {
		sha = old.SHA1 // 内容没变，复用旧哈希，免整读
		changed = false
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

	rec := Record{Name: m.Name, SizeKB: sizeKB, Modified: modified, SHA1: sha, IsNew: 1}

	posterRel, _ := PosterRel(sha)
	summaryRel, _ := SummaryRel(sha)
	posterOK := statMeta(drv, posterRel)

	kind := localKind(m.Name)

	// 内容没变：沿用旧提取字段；都填全了就不再重读文件
	needExtract := changed
	if old, ok := prev[m.Name]; ok && !changed && old.SHA1 == sha {
		rec.CaptureTime, rec.Album, rec.Artist = old.CaptureTime, old.Album, old.Artist
		if rec.CaptureTime != "" && rec.Album != "" && rec.Artist != "" {
			needExtract = false
		}
	}

	switch kind {
	case models.MediaImage:
		// 图片元数据：EXIF 拍摄时间
		if needExtract || rec.CaptureTime == "" {
			if data, err := readAllBytes(drv, rel); err == nil {
				rec.CaptureTime = ImageCaptureTime(data)
			}
		}
	case models.MediaAudio:
		// 音频：作者/专辑 + 缺封面时用内嵌图生成海报
		if needExtract || rec.Album == "" || rec.Artist == "" || !posterOK {
			if data, err := readAllBytes(drv, rel); err == nil {
				album, artist, pic, _ := readAudioMeta(data)
				if album != "" {
					rec.Album = album
				}
				if artist != "" {
					rec.Artist = artist
				}
				if !posterOK && len(pic) > 0 {
					if png, perr := ResizeCoverPNG(pic); perr == nil && WritePoster(drv, sha, png) == nil {
						posterOK = true
					}
				}
			}
		}
	case models.MediaVideo:
		// 启动/增量扫描**不**自动调 ffmpeg 抽封面：避免启动即重负载。
		// 缺封面由「补充截图」手动触发（见 handlers.FsPatch）。
		// 容器标签一般带标题/作者，best-effort 读一次（mp4 容器）
		if needExtract {
			if data, err := readAllBytes(drv, rel); err == nil {
				album, artist, _, _ := readAudioMeta(data)
				rec.Album, rec.Artist = album, artist
			}
		}
	}

	if posterOK && statMeta(drv, summaryRel) {
		rec.IsNew = 0
	}
	return rec, nil
}

// localKind 按扩展名判定媒体类型（供索引时提取元数据用）。
func localKind(name string) int {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".bmp", ".heic", ".avif":
		return models.MediaImage
	case ".mp3", ".flac", ".wav", ".aac", ".ogg", ".m4a", ".wma":
		return models.MediaAudio
	case ".mp4", ".mkv", ".mov", ".avi", ".webm", ".flv", ".m3u8", ".ts":
		return models.MediaVideo
	}
	return models.MediaUnknown
}

// readAllBytes 整读文件内容，读失败返回 nil。
func readAllBytes(drv drivers.Driver, rel string) ([]byte, error) {
	f, _, err := drv.Open(rel)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.(interface{ Close() error }).Close() }()
	return io.ReadAll(f)
}

// statMeta 元数据根（.mocca）下某相对路径是否存在；用作 is_new 的海报/简介判据。
func statMeta(drv drivers.Driver, rel string) bool {
	_, err := drv.MetaStat(rel)
	return err == nil
}
