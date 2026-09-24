package mediaindex

import (
	"bufio"
	"io"
	"sort"
	"strings"
	"sync"
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
// 还要吃掉目录末尾的 "/"：根目录传进来是 "/"（见 handlers 的 path.Dir），
// 不处理就会拼出 "//xxx" 这种双斜杠路径，落到 SMB 等后端上容易出错。
func joinRel(dir, name string) string {
	if dir = strings.TrimSuffix(dir, "/"); dir == "" {
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
		_, err := writeIndex(drv, dir, medias)
		return err
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
		if _, err := writeIndex(drv, dir, medias); err != nil {
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

// indexMu 串行化索引写入。
//
// 索引是「读旧行 → 合并 → 整份重写」，两次并发重写会互相丢行（后写的用自己读到的旧行，
// 覆盖掉前一次刚加的内容）。写入者不止一个：启动全量扫描、文件监控的增量重扫、
// 后台「重新索引」按钮（前端按文件并发扇出）。所以统一在这里排队，简单且够用。
var indexMu sync.Mutex

// writeIndex 把一层目录的媒体文件写成 `.index.jsonl`（整份重写）。
// 增量：文件 size+modified 与旧行一致就复用旧行（**不读文件**），否则整读重算。
// 返回真正重算（读过内容）的条数。
func writeIndex(drv drivers.Driver, dir string, medias []drivers.Entry) (int, error) {
	return writeIndexSkip(drv, dir, medias, nil)
}

// writeIndexSkip 同 writeIndex，但 skip 里的文件名**忽略上一行**，强制重读内容重算 sha1。
//
// 保留这个口子是为了"同名同大小同 mtime、内容却被换过"的极少数情况：那时按增量判据
// （size+modified）看不出任何变化，而索引里那条 sha1 已经过期，按它寻址的海报/附加信息
// 就全失联。**日常的「重新索引」不走这里**（走纯增量，见 RebuildFiles），
// 否则勾一个文件就要把它整读一遍。
func writeIndexSkip(drv drivers.Driver, dir string, medias []drivers.Entry, skip map[string]bool) (int, error) {
	indexMu.Lock()
	defer indexMu.Unlock()

	indexRel := joinRel(dir, IndexFileName)

	prev := map[string]Record{}
	if f, _, err := drv.Open(indexRel); err == nil {
		if p, perr := readIndex(f); perr == nil {
			prev = p
		}
		closeStream(f)
	}
	for name := range skip {
		delete(prev, name) // 当作"没有旧行"，buildRecord 就会重读文件重算
	}

	records := make([]Record, 0, len(medias))
	recomputed := 0
	for _, m := range medias {
		rec, reused, err := buildRecord(drv, joinRel(dir, m.Name), m, prev)
		if err != nil {
			return 0, errors.Wrapf(err, "索引 %s 失败", m.Name)
		}
		if !reused {
			recomputed++
		}
		records = append(records, rec)
	}

	f, err := drv.Create(indexRel)
	if err != nil {
		return 0, errors.Wrapf(err, "写 %s 失败", indexRel)
	}
	defer func() { _ = f.Close() }() // Create 返回的是 io.WriteCloser，一定有 Close
	bw := bufio.NewWriter(f)
	for _, r := range records {
		line, err := jsonMarshal(r)
		if err != nil {
			return 0, err
		}
		if _, err := bw.Write(line); err != nil {
			return 0, errors.Wrapf(err, "写 %s 失败", indexRel)
		}
		if err := bw.WriteByte('\n'); err != nil {
			return 0, errors.Wrapf(err, "写 %s 失败", indexRel)
		}
	}
	return recomputed, errors.WithStack(bw.Flush())
}

// RebuildFiles 重新生成一层的 `.index.jsonl`：**按增量重建**。
//
// 判定完全落在"这个文件在索引里记的那条，与它现在的实际情况是否一致"上：
// 逐条比 **name + size + modified** ——
//
//   - 一致  → 直接沿用旧行（sha1 与已提取的元数据都照抄，**不读文件内容**）；
//   - 不一致 / 没有旧行 → 整读一遍算 sha1，并重新解析元数据。
//
// 返回 (写进索引的记录数, 其中真正重算的条数)。
//
// force 是给"同名同大小、mtime 也被保留、内容却被换过"的极少数情况留的口子：列进去的文件
// 无视增量判据强制重算。**正常调用传 nil**（这就是"做增量的重建"）。
//
// 为什么不再整层强制重算：索引确实按目录一份（.index.jsonl 与同层媒体放在一起），
// 所以写入必然整份重写；但**代价**取决于"要重读几个文件"。整层强制会让勾 1 个文件也把
// 同目录几十个视频全部重读（20 个 4GB 的片子 = 80GB I/O），点一次「重新索引」要等很久。
func RebuildFiles(drv drivers.Driver, dir string, force []string) (int, int, error) {
	_, medias, err := collectDir(drv, dir)
	if err != nil {
		return 0, 0, err
	}
	if len(medias) == 0 {
		// 目录空了：和 ScanDir 一致，把残留的索引删掉，免得留下指向已删文件的脏行
		_ = drv.Remove(joinRel(dir, IndexFileName))
		return 0, 0, nil
	}

	skip := map[string]bool{}
	if len(force) > 0 {
		// 只认目录里真实存在的名字：多选里可能混进已删/已改名的路径
		exist := make(map[string]bool, len(medias))
		for _, m := range medias {
			exist[m.Name] = true
		}
		for _, n := range force {
			if exist[n] {
				skip[n] = true
			}
		}
	}

	recomputed, err := writeIndexSkip(drv, dir, medias, skip)
	if err != nil {
		return 0, 0, err
	}
	return len(medias), recomputed, nil
}

// buildRecord 为一个媒体文件生成索引行。
// is_new 由存储根 `.mocca` 里该 sha1 的海报(.png)+简介(.meta)是否齐备推导：
// 两者都在 → 0（附加信息已就绪），否则 → 1。
//
// 顺带按类型提取元数据（错误一律非致命，失败不阻断索引）：
//   - 图片：EXIF 拍摄时间 → capture_time
//   - 音频：内嵌标签的作者/专辑 → artist/album，缺封面时把内嵌图写成海报
//   - 视频：不自动抽封面（见 MediaVideo 分支），只 best-effort 读容器标签 → artist/album
//
// 内容不变的增量扫描复用旧 sha1 与旧提取字段，不重复读文件重算。
// 第二个返回值 reused 表示**走了增量捷径**（没读文件内容），供调用方统计"重算了几条"。
func buildRecord(drv drivers.Driver, rel string, m drivers.Entry, prev map[string]Record) (Record, bool, error) {
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
			return Record{}, false, errors.Wrapf(err, "打开 %s 失败", rel)
		}
		shaTmp, herr := hashContent(f)
		closeStream(f)
		if herr != nil {
			return Record{}, false, herr
		}
		sha = shaTmp
	}

	rec := Record{Name: m.Name, SizeKB: sizeKB, Modified: modified, SHA1: sha, IsNew: 1}

	posterRel, _ := PosterRel(sha)
	summaryRel, _ := SummaryRel(sha)
	posterOK := statMeta(drv, posterRel)

	kind := localKind(m.Name)

	// 内容没变：沿用旧行里已提取的元数据，并且**不再重新解析**。
	//
	// 这里是"索引为什么慢"的另一半：原先的条件写成
	// `needExtract || rec.CaptureTime == ""`（音频还多几个 `|| rec.Album == ""`），
	// 于是**没有 EXIF 的图片、没有内嵌标签的音频，每次扫描都被整读一遍重新解析** ——
	// 截图/导出图这类"天生没有该元数据"的文件，让所谓"增量扫描"每次都把库重新读一遍。
	// 增量扫描就该是增量的：只有文件内容变了才重新解析。
	// 判据与 sha1 一致 —— size+modified 与旧行比；要强制重解析，走 RebuildFiles 的 force 口子。
	if !changed {
		if old, ok := prev[m.Name]; ok && old.SHA1 == sha {
			rec.CaptureTime, rec.Album, rec.Artist = old.CaptureTime, old.Album, old.Artist
		}
	}

	switch kind {
	case models.MediaImage:
		// 图片元数据：EXIF 拍摄时间
		if changed {
			_ = withMedia(drv, rel, func(f io.ReadSeeker) error {
				rec.CaptureTime = ImageCaptureTime(f)
				return nil
			})
		}
	case models.MediaAudio:
		// 音频：作者/专辑 + 缺封面时用内嵌图生成海报
		if changed {
			_ = withMedia(drv, rel, func(f io.ReadSeeker) error {
				album, artist, pic, _ := readAudioMeta(f)
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
				return nil
			})
		}
	case models.MediaVideo:
		// 启动/增量扫描**不**自动调 ffmpeg 抽封面：避免启动即重负载。
		// 缺封面由「补充截图」手动触发（见 handlers.FsPatch）。
		// 容器标签一般带标题/作者，best-effort 读一次（mp4 容器）
		if changed {
			_ = withMedia(drv, rel, func(f io.ReadSeeker) error {
				album, artist, _, _ := readAudioMeta(f)
				rec.Album, rec.Artist = album, artist
				return nil
			})
		}
	}

	if posterOK && statMeta(drv, summaryRel) {
		rec.IsNew = 0
	}
	return rec, !changed, nil
}

// localKind 按扩展名判定媒体类型（供索引时提取元数据用）。
// 实现统一走 MediaKindOf —— 这里曾经自带一份更宽的扩展名清单，
// 与索引收录用的 MediaExts 不一致，正是"文件进不了索引"那类问题的温床。
func localKind(name string) int {
	return MediaKindOf(name)
}

// closeStream 关掉驱动 Open 返回的流。
//
// Driver 接口只承诺 io.ReadSeeker，**不保证带 Close**。原先直接写
// `f.(interface{ Close() error }).Close()`（无检查断言），一旦某个驱动返回不带 Close
// 的流（或被包装过的流，如测试里的计数流），panic 会把**整轮扫描**打断，而不是只跳过
// 这一个文件。这里改成检查式断言：关不掉就算了，元数据是 best-effort。
func closeStream(f io.ReadSeeker) {
	if c, ok := f.(interface{ Close() error }); ok {
		_ = c.Close()
	}
}

// withMedia 打开一个媒体文件交给 fn 用，用完关闭。
//
// 给的是驱动返回的 io.ReadSeeker（不是整份字节）：解析标签/EXIF 只需在文件里定位几处，
// 而媒体文件动辄上 GB —— **整读进内存只为拿几百字节元数据**，既拖慢索引又可能 OOM。
// 打开失败或解析出错都返回 err，调用方按 best-effort 忽略即可（缺元数据不阻断索引）。
func withMedia(drv drivers.Driver, rel string, fn func(io.ReadSeeker) error) error {
	f, _, err := drv.Open(rel)
	if err != nil {
		return errors.Wrapf(err, "打开 %s 失败", rel)
	}
	defer closeStream(f)
	return fn(f)
}

// statMeta 元数据根（.mocca）下某相对路径是否存在；用作 is_new 的海报/简介判据。
func statMeta(drv drivers.Driver, rel string) bool {
	_, err := drv.MetaStat(rel)
	return err == nil
}
