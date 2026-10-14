package mediaindex

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/azhai/mocca/drivers"
	"github.com/pkg/errors"
)

// 弹幕与评论。
//
// 落盘位置：元数据根（设备根下的 `.mocca`）里，按 sha1 内容寻址，与海报/简介同构：
//
//	<元数据根>/ab/cd/<sha1[4:]>.danmaku.jsonl
//
// 为什么是文件、不是数据库：
//   - 这是**设备侧**的数据：换机器、重装服务、把硬盘拔下来带走，弹幕应该跟着盘走，
//     而不是躺在某台机器的 sqlite 里（附加信息 `.mocca/xx/xx/<sha1>.meta` 就是这么设计的）；
//   - 按 sha1 锚定 → 文件改名/移动，弹幕不丢；
//   - 一个媒体一个文件，读一次就能拿到全部，不用 join 用户表（所以作者信息必须快照进 JSON）。
//
// 为什么是 jsonl + 追加：发送是"高频加一行"的高并发写。若每次都整读全文再整体覆写，
// 代价是 O(整个文件)；追加写是 O(一行)，且单行不超过 PIPE_BUF(4096) 时内核保证原子。
// **单条长度上限因此是并发安全的前提，不是体验限制** —— 放宽会让半行写坏文件。

// 单条内容上限（按 Unicode 码点计）。
const (
	// MaxDanmakuLength 弹幕。屏幕上一行放不下就不叫弹幕了。
	MaxDanmakuLength = 50
	// MaxCommentLength 评论。上限的真正作用是保证一行 JSON 编码后仍在 4KB 内：
	// 500 字按最坏情况（每字 4 字节）约 2KB，加上其余字段仍在原子写的安全区内。
	MaxCommentLength = 500
)

// 弹幕文件后缀。与海报(.png)/简介(.meta)同构，只是它一行一条。
const danmakuExt = ".danmaku.jsonl"

// 类型：评论就是「时间点为 0 的弹幕」，两者共用一份存储。
const (
	DanmakuTypeComment = iota // 评论：Offset 恒为 0，挂在整部作品上
	DanmakuTypeDanmaku        // 弹幕：Offset 是它在时间轴上出现的时刻（毫秒）
)

// DanmakuRel 弹幕文件的相对路径（相对元数据根）。
func DanmakuRel(sha1hex string) (string, error) {
	return metaRel(sha1hex, danmakuExt)
}

// Entry 一条弹幕或评论。
//
// UserName / UserAvatar 是**发送那一刻的快照**：
//   - 文件存储没有用户表可 join，列表接口必须能直接渲染出昵称与头像；
//   - 快照语义正是要的：用户后来改了昵称、换了头像，历史弹幕仍显示当时的样子，
//     不会让几年前的发言"集体变脸"。
type Entry struct {
	ID         string    `json:"id"`
	UserID     uint      `json:"user_id"`
	UserName   string    `json:"user_name"`   // 当时的昵称
	UserAvatar string    `json:"user_avatar"` // 当时的头像（预设 key，前端拼 /static/avatars/<key>.png）
	Type       int       `json:"type"`        // 0 评论 / 1 弹幕
	Offset     int       `json:"offset"`      // 毫秒；弹幕为出现时刻，评论恒为 0
	Content    string    `json:"content"`
	ParentID   string    `json:"parent_id,omitempty"` // 回复的目标；顶层为空
	CreatedAt  time.Time `json:"created_at"`
}

// IsDanmaku 是否弹幕。
func (e *Entry) IsDanmaku() bool { return e.Type == DanmakuTypeDanmaku }

// Validate 落库前校验，并顺手把评论的时刻归零、生成 ID。
func (e *Entry) Validate() error {
	e.Content = strings.TrimSpace(e.Content)
	switch {
	case e.Content == "":
		return errors.New("内容为空")
	case e.Type == DanmakuTypeComment:
		e.Offset = 0 // 评论不挂时刻，这就是"评论 = 时间点为 0 的弹幕"
		if n := utf8.RuneCountInString(e.Content); n > MaxCommentLength {
			return errors.Errorf("评论 %d 字，上限 %d", n, MaxCommentLength)
		}
	case e.Type == DanmakuTypeDanmaku:
		if n := utf8.RuneCountInString(e.Content); n > MaxDanmakuLength {
			return errors.Errorf("弹幕 %d 字，上限 %d", n, MaxDanmakuLength)
		}
		if e.Offset < 0 {
			return errors.New("弹幕时间点不能为负")
		}
	default:
		return errors.Errorf("未知类型 %d", e.Type)
	}
	if e.ID == "" {
		e.ID = newDanmakuID()
	}
	if e.CreatedAt.IsZero() {
		e.CreatedAt = time.Now()
	}
	return nil
}

// danmakuSeq 同一毫秒内的递增序号。
var danmakuSeq uint32

// newDanmakuID 生成可排序的唯一 ID：毫秒时间戳在前（定长 → 字典序即时间序），
// 后接同一毫秒内的递增序号。
//
// 后缀**不能用随机数**：同一毫秒里并发发出的两条会按随机后缀排序，
// 于是评论列表刷新一次顺序就变一次（看着像"评论在跳"）。序号能保证：
// 谁先拿到号谁在前。定长也很关键 —— 否则字典序会乱，按 ID 增量拉取就没法用。
func newDanmakuID() string {
	seq := atomic.AddUint32(&danmakuSeq, 1) & 0xffff
	return fmt.Sprintf("%014d%04x", time.Now().UnixMilli(), seq)
}

// ---------- 写入 ----------

// danmakuMu 串行化对弹幕文件的**写与整读**（写在这一段，读在 ReadDanmaku）。
//
// 为什么写要排队：追加本身在内核层是原子的，但「确认文件以换行结尾」+「追加」是两步，
// 两步之间插入另一个写者就会拼出 `...}{...}` 这种坏行。
//
// 为什么读也要排队：整体重写（删一条 / 清一类）是 MetaCreate 截断 + 缓冲 Flush，
// 中间必然有个「空文件 / 半文件」的窗口；读如果不排队，那个窗口里的残缺会被连 stat
// 一起写进缓存，而它事后与真实文件完全吻合 —— 缓存就再也发现不了自己错了。
var danmakuMu sync.Mutex

// AppendDanmaku 追加一条弹幕/评论到该媒体的 jsonl 文件。
func AppendDanmaku(drv drivers.Driver, sha1hex string, e *Entry) error {
	if err := e.Validate(); err != nil {
		return err
	}
	rel, err := DanmakuRel(sha1hex)
	if err != nil {
		return err
	}

	danmakuMu.Lock()
	defer danmakuMu.Unlock()

	// 自愈：上一次写若被打断（进程被杀、盘满），文件末尾会留下半行。
	// 不补换行就直接追加，等于把新记录粘在坏行后面，整条都读不出来。
	if err := ensureTrailingNewline(drv, rel); err != nil {
		return errors.Wrap(err, "整理弹幕文件失败")
	}
	if err := drv.MetaMkdirAll(path.Dir(rel)); err != nil {
		return errors.Wrapf(err, "创建 %s 失败", path.Dir(rel))
	}
	line, err := jsonMarshalDanmaku(e)
	if err != nil {
		return err
	}
	f, err := drv.MetaAppend(rel)
	if err != nil {
		return errors.Wrapf(err, "打开 %s 失败", rel)
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		_ = f.Close()
		return errors.Wrapf(err, "写 %s 失败", rel)
	}
	if err := f.Close(); err != nil {
		return errors.Wrapf(err, "写 %s 失败", rel)
	}

	// 写后把这一行并进缓存（并刷新 stat），避免下一次读又把整个文件读一遍。
	danmakuCacheAppend(drv, sha1hex, e, rel)
	return nil
}

// ensureTrailingNewline 保证文件以换行结尾：非空的最后一个字节不是 '\n' 时补一个。
func ensureTrailingNewline(drv drivers.Driver, rel string) error {
	st, err := drv.MetaStat(rel)
	if err != nil || st.Size == 0 {
		return nil // 不存在或空文件：不用补
	}
	f, size, err := drv.MetaOpen(rel)
	if err != nil {
		return err
	}
	if size == 0 {
		closeStream(f)
		return nil
	}
	// 要看的是**最后一**字节，不是文件开头：从当前位置 Read 拿到的是 '{'，
	// 于是每次追加都会多补一个换行（读侧靠空行跳过兼容，但文件会一路虚胖）。
	if _, err := f.Seek(size-1, io.SeekStart); err != nil {
		closeStream(f)
		return err
	}
	var last [1]byte
	if _, err := f.Read(last[:]); err != nil && err != io.EOF {
		closeStream(f)
		return err
	}
	closeStream(f)
	if last[0] == '\n' {
		return nil
	}
	a, err := drv.MetaAppend(rel)
	if err != nil {
		return err
	}
	_, err = a.Write([]byte{'\n'})
	_ = a.Close()
	return err
}

// jsonMarshalDanmaku 序列化一行（含末尾不带换行）。
func jsonMarshalDanmaku(e *Entry) ([]byte, error) {
	b, err := json.Marshal(e)
	if err != nil {
		return nil, errors.Wrapf(err, "序列化弹幕 %q 失败", e.ID)
	}
	return b, nil
}

// ---------- 读取 ----------

// danmakuFile 一份已解析的弹幕文件 + 它当时的 stat，用来判断要不要重读。
type danmakuFile struct {
	modTime time.Time
	size    int64
	rows    []*Entry
}

var (
	// 读缓存：弹幕是「写少读多」，列表/播放器时间轴每次进来都要全量取。
	// 按 (modTime,size) 判断是否需要重读 —— 只比对 mtime 不够：同一秒内的两次
	// 追加 mtime 可能相同，size 一定变。
	cacheMu      sync.RWMutex
	danmakuCache = map[string]*danmakuFile{}
)

// cacheKey 缓存键 = 存储标识 + sha1。
//
// **只按 sha1 是不够的**：两块盘上放着同一个视频（内容相同 → sha1 相同），
// 缓存就会串 —— A 盘的弹幕被 B 盘读到。存储标识来自 Driver.Identity()。
func cacheKey(drv drivers.Driver, sha1hex string) string {
	return drv.Identity() + "\x00" + sha1hex
}

// ReadDanmaku 读某媒体的全部弹幕与评论（按 ID 排序，即时间序）。
func ReadDanmaku(drv drivers.Driver, sha1hex string) ([]*Entry, error) {
	rel, err := DanmakuRel(sha1hex)
	if err != nil {
		return nil, err
	}
	key := cacheKey(drv, sha1hex)
	cacheMu.RLock()
	cached := danmakuCache[key]
	cacheMu.RUnlock()

	st, serr := drv.MetaStat(rel)
	if serr != nil && os.IsNotExist(errors.Cause(serr)) {
		// 只有真「不存在」才算这个媒体没人发过东西。以前这里还要再走一次
		// MetaOpen（必然失败）+ MetaStat，等于每部片子两次元数据往返；
		// 后台列目录逐条探弹幕数时，这个差别会被放大到整个目录。
		//
		// MetaStat 报其它错（权限、I/O、SMB 会话抖一下）则**不能**走这条路：
		// 把一次抖动洗成「这部片子没人说过话」，后台的徽标会整层消失且不留痕迹；
		// 那种错落到下面的整读里 —— 文件打得开就照读，打不开才当作没有。
		// （本地盘仍只花一次 Stat；SMB 若认不出 not-exist，就退回改前的两次往返。）
		return []*Entry{}, nil
	}
	// st 在 serr != nil 时是零值，不能拿它比缓存。
	if serr == nil && cached != nil && cached.modTime.Equal(st.Modified) && cached.size == st.Size {
		return cached.rows, nil // 缓存命中：一次 stat 就够，不读文件
	}

	// 未命中：整读。**读必须和写共用 danmakuMu** —— 逐条删与按类型清空都是
	// MetaCreate（立即截断）+ 缓冲重写，读者撞进那个窗口就会把「空文件 / 半文件」
	// 连同它当时的 (mtime,size) 一起写进缓存；等重写完成，那份 stat 反而与真实
	// 文件完全吻合，缓存再也自校不回来（叠上 danmakuCacheAppend 的乐观合并，
	// 后面的新记录会接到残缺列表尾巴上）。弹幕文件很薄，读者排在一次写后面不疼。
	danmakuMu.Lock()
	defer danmakuMu.Unlock()

	rows, stat, err := readDanmakuFile(drv, rel)
	if err != nil {
		return nil, err
	}
	cacheMu.Lock()
	danmakuCache[key] = &danmakuFile{modTime: stat.Modified, size: stat.Size, rows: rows}
	cacheMu.Unlock()
	return rows, nil
}

// danmakuCacheAppend 写后把新行并进缓存，并顺手刷新 stat。
func danmakuCacheAppend(drv drivers.Driver, sha1hex string, e *Entry, rel string) {
	key := cacheKey(drv, sha1hex)
	st, err := drv.MetaStat(rel)
	if err != nil {
		cacheMu.Lock()
		delete(danmakuCache, key) // 拿不到 stat：不敢复用缓存，下次读重新整读
		cacheMu.Unlock()
		return
	}
	cacheMu.Lock()
	defer cacheMu.Unlock()
	if c, ok := danmakuCache[key]; ok {
		rows := make([]*Entry, 0, len(c.rows)+1)
		rows = append(rows, c.rows...)
		rows = append(rows, e)
		danmakuCache[key] = &danmakuFile{modTime: st.Modified, size: st.Size, rows: rows}
	}
}

// InvalidateDanmakuCache 让某媒体的弹幕缓存失效（删除/重写文件后调用）。
func InvalidateDanmakuCache(drv drivers.Driver, sha1hex string) {
	cacheMu.Lock()
	delete(danmakuCache, cacheKey(drv, sha1hex))
	cacheMu.Unlock()
}

// readDanmakuFile 整读并解析一份弹幕文件。
//
// 末尾**不完整的一行**直接丢弃：那是被打断的半行写（见 ensureTrailingNewline），
// 留着只会让解析失败。它不会被修好，但也不会污染前面的正常记录。
func readDanmakuFile(drv drivers.Driver, rel string) ([]*Entry, drivers.Entry, error) {
	empty := drivers.Entry{}
	f, _, err := drv.MetaOpen(rel)
	if err != nil {
		if _, serr := drv.MetaStat(rel); serr != nil {
			return []*Entry{}, empty, nil // 没有弹幕文件 = 没有弹幕，不是错误
		}
		return nil, empty, errors.Wrapf(err, "读 %s 失败", rel) // 有文件却打不开：真错误
	}
	defer closeStream(f)

	var rows []*Entry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var e Entry
		if err := json.Unmarshal([]byte(line), &e); err != nil || e.ID == "" {
			continue // 坏行（含末尾半行）跳过，不影响其余记录
		}
		rows = append(rows, &e)
	}
	if err := sc.Err(); err != nil {
		return nil, empty, errors.Wrapf(err, "读 %s 失败", rel)
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })

	st, _ := drv.MetaStat(rel)
	return rows, st, nil
}

// ---------- 查询 ----------

// DanmakuCounts 某媒体的弹幕与评论各几条（后台列目录的计数探测用）。
// 没有文件就是 0,0：ReadDanmaku 对这种情形只花一次 MetaStat。
func DanmakuCounts(drv drivers.Driver, sha1hex string) (danmaku, comments int, err error) {
	rows, err := ReadDanmaku(drv, sha1hex)
	if err != nil {
		return 0, 0, err
	}
	for _, e := range rows {
		switch e.Type {
		case DanmakuTypeDanmaku:
			danmaku++
		case DanmakuTypeComment:
			comments++
		}
	}
	return danmaku, comments, nil
}

// DanmakuInRange 取 [from, to] 毫秒区间内的弹幕（含端点），按出现时刻排序。
// 播放器按时间轴只取当前窗口用；from<0 视为 0，to<0 表示不设上界。
func DanmakuInRange(rows []*Entry, from, to int) []*Entry {
	if from < 0 {
		from = 0
	}
	out := make([]*Entry, 0, len(rows))
	for _, e := range rows {
		if e.Type != DanmakuTypeDanmaku {
			continue
		}
		if e.Offset < from {
			continue
		}
		if to >= 0 && e.Offset > to {
			continue
		}
		out = append(out, e)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Offset < out[j].Offset })
	return out
}

// CommentThread 一条顶层评论 + 它的回复（回复只支持一层：再套下去的缩进在
// 播放器那块小地方根本放不下，而"能回复"这个需求一层就够了）。
type CommentThread struct {
	*Entry
	Replies []*Entry `json:"replies"`
}

// CommentThreads 把评论整理成「顶层 + 回复」。弹幕不参与。
func CommentThreads(rows []*Entry) []*CommentThread {
	byID := make(map[string]*Entry, len(rows))
	var tops []*Entry
	for _, e := range rows {
		if e.Type != DanmakuTypeComment {
			continue
		}
		byID[e.ID] = e
		if e.ParentID == "" {
			tops = append(tops, e)
		}
	}
	// 顶层评论**倒序**（最新在最上面）：用户明确要求"按撰写时间倒序展示评论"。
	// ID 是「毫秒时间戳 + 同毫秒序号」且定长，所以字典序即时间序 —— 按 ID 排就是按撰写时间排。
	// 回复保持正序：它是一段对话，顺着读才接得上。
	sort.SliceStable(tops, func(i, j int) bool { return tops[i].ID > tops[j].ID })

	out := make([]*CommentThread, 0, len(tops))
	for _, t := range tops {
		out = append(out, &CommentThread{Entry: t, Replies: []*Entry{}})
	}
	idx := make(map[string]*CommentThread, len(out))
	for i, t := range out {
		idx[t.ID] = out[i]
	}
	for _, e := range rows {
		if e.Type != DanmakuTypeComment || e.ParentID == "" {
			continue
		}
		// 回复指向的顶层评论若已被删除，回复也随之不展示（避免孤儿回复浮在外面）
		if parent, ok := idx[e.ParentID]; ok {
			parent.Replies = append(parent.Replies, e)
		}
	}
	return out
}

// ---------- 删除 ----------

// ErrDanmakuNotFound 要删的弹幕/评论不存在（或已被别人删掉）。
var ErrDanmakuNotFound = errors.New("弹幕或评论不存在")

// DeleteDanmaku 删除一条弹幕/评论。uid 用于越权校验；isAdmin 为真时跳过。
// 追加写的文件删不了单行，只能整体重写 —— 所以删除是 O(文件)，但它很罕见。
func DeleteDanmaku(drv drivers.Driver, sha1hex, id string, uid uint, isAdmin bool) error {
	rel, err := DanmakuRel(sha1hex)
	if err != nil {
		return err
	}

	danmakuMu.Lock()
	defer danmakuMu.Unlock()

	rows, _, err := readDanmakuFile(drv, rel)
	if err != nil {
		return err
	}
	kept := make([]*Entry, 0, len(rows))
	found := false
	for _, e := range rows {
		if e.ID == id {
			found = true
			if !isAdmin && e.UserID != uid {
				return errors.New("不能删除别人的弹幕")
			}
			continue
		}
		kept = append(kept, e)
	}
	if !found {
		return ErrDanmakuNotFound
	}
	if err := writeDanmakuRows(drv, rel, kept); err != nil {
		return err
	}
	// 失效放在重写**之后**：以前是先 delete 再截断重写，等于主动把「读到空文件」
	// 的机会留给并发读者（读者现在也在同一把锁里，两头都堵上才说得过去）。
	InvalidateDanmakuCache(drv, sha1hex)
	return nil
}

// writeDanmakuRows 把剩下的条目整体重写回文件；一条不剩就把文件删掉，别留一个空壳。
//
// 逐条删与按类型清空共用这一份：两处各写一遍覆写逻辑，将来改格式（加字段、改编码）必漏一处。
func writeDanmakuRows(drv drivers.Driver, rel string, kept []*Entry) error {
	if len(kept) == 0 {
		_ = drv.MetaRemove(rel)
		return nil
	}
	f, err := drv.MetaCreate(rel)
	if err != nil {
		return errors.Wrapf(err, "重写 %s 失败", rel)
	}
	defer func() { _ = f.Close() }()
	bw := bufio.NewWriter(f)
	for _, e := range kept {
		line, err := jsonMarshalDanmaku(e)
		if err != nil {
			return err
		}
		if _, err := bw.Write(append(line, '\n')); err != nil {
			return errors.Wrapf(err, "重写 %s 失败", rel)
		}
	}
	return errors.WithStack(bw.Flush())
}

// DanmakuAll 给 ClearDanmaku 表示「两类都清」。
const DanmakuAll = -1

// ClearDanmaku 清空某媒体的弹幕或评论：onlyType 取 DanmakuTypeDanmaku /
// DanmakuTypeComment / DanmakuAll，返回清掉的条数（本来就没有就是 0）。
//
// 为什么不循环调 DeleteDanmaku：那是「每删一条重写一次整个文件」，O(条数×文件)；
// 这里一次读 + 一次重写就够了。
func ClearDanmaku(drv drivers.Driver, sha1hex string, onlyType int) (int, error) {
	rel, err := DanmakuRel(sha1hex)
	if err != nil {
		return 0, err
	}

	danmakuMu.Lock()
	defer danmakuMu.Unlock()

	rows, _, err := readDanmakuFile(drv, rel)
	if err != nil {
		return 0, err
	}
	kept := make([]*Entry, 0, len(rows))
	cleared := 0
	for _, e := range rows {
		if onlyType == DanmakuAll || e.Type == onlyType {
			cleared++
			continue
		}
		kept = append(kept, e)
	}
	if cleared == 0 {
		return 0, nil // 没有要清的东西：别把一个不存在的文件创建出来
	}
	if err := writeDanmakuRows(drv, rel, kept); err != nil {
		return 0, err
	}
	// 与 DeleteDanmaku 同一条规矩：重写完成才让缓存失效。
	InvalidateDanmakuCache(drv, sha1hex)
	return cleared, nil
}
