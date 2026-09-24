package mediaindex

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/azhai/mocca/drivers"
	"github.com/azhai/mocca/helpers"
	"github.com/azhai/mocca/models"
)

// Watch 全局文件监控控制器。默认不启动（开关在库里，缺省关），
// 后台「全局选项」改 `fs_watch` 时调 Start/Stop 即时生效，无需重启进程。
var Watch = &WatchController{}

// WatchController 让阻塞式 WatchStorages 变成可反复启停的能力。
// 用包级单例而不是把句柄从 main 传进 handler：两者分属不同包，
// 传参会把 main 的启动顺序耦合进 handler 的签名。
type WatchController struct {
	mu      sync.Mutex
	cancel  context.CancelFunc
	running bool
	gen     uint64 // 每次 Start 自增；goroutine 退出时只有自己那一代还匹配才复位状态
}

// Start 拉起监控；已在跑则原样返回（幂等）。
func (wc *WatchController) Start(logf func(string, ...any)) error {
	wc.mu.Lock()
	defer wc.mu.Unlock()
	if wc.running {
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	wc.cancel = cancel
	wc.running = true
	wc.gen++
	gen := wc.gen
	helpers.GoSafe("文件监控", func() {
		defer func() {
			// 只复位自己那一代：Stop 后紧接着 Start 时，旧 goroutine 的退出
			// 不能把新一次监控的 running/cancel 清掉（否则状态与实际相反）。
			wc.mu.Lock()
			if wc.gen == gen {
				wc.running = false
				wc.cancel = nil
			}
			wc.mu.Unlock()
		}()
		if err := WatchStorages(ctx, logf); err != nil {
			logf("文件监控退出: %v", err)
		}
	})
	return nil
}

// Stop 停掉监控；没在跑时是空操作（幂等）。只发取消信号不等待，
// WatchStorages 收到 ctx.Done 后自己归还驱动连接。
// 状态先置为「未运行」：调用方（后台开关）拿到响应时，语义上监控已停。
func (wc *WatchController) Stop() {
	wc.mu.Lock()
	defer wc.mu.Unlock()
	if wc.cancel != nil {
		wc.cancel()
	}
	wc.cancel = nil
	wc.running = false
	wc.gen++ // 作废在途 goroutine 的复位权
}

// Running 当前是否在监控。
func (wc *WatchController) Running() bool {
	wc.mu.Lock()
	defer wc.mu.Unlock()
	return wc.running
}

// WatchStorages 监控所有「本地(Local)」存储的媒体文件变化：变化落盘后增量重扫
// 所在目录的 `.index.jsonl`。阻塞直到 ctx 取消。
//
// fsnotify 走的是 OS 文件系统通知（inotify/kqueue/FSEvents），只对本地磁盘有效，
// 且只能逐个目录 Add（没有现成的递归 Add）：启动时把每个根下所有子目录递归登记，
// 运行期遇到新建目录再补登记。SMB 等远程驱动拿不到本地事件，跳过并打日志说明。
func WatchStorages(ctx context.Context, logf func(string, ...any)) error {
	storages, err := models.ListStorages()
	if err != nil {
		return err
	}
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer func() { _ = watcher.Close() }()

	// watched：已经登记进 watcher 的绝对目录名，守卫避免重复 Add
	watched := map[string]bool{}
	// root(归一绝对路径) -> 已打开的 Local 驱动。事件名是绝对路径，按根归算相对目录
	roots := map[string]*drivers.Local{}
	for _, s := range storages {
		if s.Disabled {
			continue
		}
		drv, err := drivers.Open(s)
		if err != nil {
			continue
		}
		ld, ok := drv.(*drivers.Local)
		if !ok {
			_ = drv.Close()
			logf("挂载点 %s 不是本地存储，跳过文件监控", s.MountPath)
			continue
		}
		root := canonicalPath(ld.Root)
		if err := addRecursive(watcher, watched, root); err != nil {
			_ = drv.Close()
			logf("监控目录 %s 失败: %v", root, err)
			continue
		}
		roots[root] = ld
	}
	if len(roots) == 0 {
		logf("没有可监控的本地存储，跳过文件监控")
		return nil
	}
	defer func() {
		for _, ld := range roots {
			_ = ld.Close()
		}
	}()
	logf("文件监控就绪：登记 %d 个本地存储根、共 %d 个子目录", len(roots), len(watched))

	// 收集一小段时间内变化的目录，去抖后一次性重扫，避免拷贝/连续写入时反复重算
	const debounce = 600 * time.Millisecond
	pending := map[string]bool{} // key = root + "\x00" + 存储内相对目录
	timer := time.NewTimer(time.Hour)
	if !timer.Stop() {
		<-timer.C
	}
	defer timer.Stop()

	for {
		select {
		case ev, ok := <-watcher.Events:
			if !ok {
				return nil
			}
			// 新建目录后把它整棵子树补登记进 watcher，否则子孙目录的变化收不到
			if ev.Op&fsnotify.Create != 0 {
				if _, rel, within := relWithin(roots, ev.Name); within && !hasHiddenSegment(rel) {
					if fi, statErr := os.Stat(ev.Name); statErr == nil && fi.IsDir() {
						_ = addRecursive(watcher, watched, ev.Name)
					}
				}
			}
			scheduleFromEvent(roots, ev, pending)
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(debounce)
		case <-watcher.Errors:
		case <-timer.C:
			flushPending(roots, pending, logf)
		case <-ctx.Done():
			return nil
		}
	}
}

// canonicalPath 归一绝对路径：先解析符号链接，失败则原样 Clean。
func canonicalPath(p string) string {
	p = filepath.Clean(p)
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

// addRecursive 把一个本地目录及其全部子目录逐个登记进 watcher（fsnotify 无递归 Add，
// 需逐目录登记）。watched 守卫已登记的目录，幂等。子项读不到就跳过，不影响已登记部分。
func addRecursive(w *fsnotify.Watcher, watched map[string]bool, dir string) error {
	return filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() {
			return nil
		}
		p = filepath.Clean(p)
		if watched[p] {
			return nil
		}
		if werr := w.Add(p); werr != nil {
			return werr
		}
		watched[p] = true
		return nil
	})
}

// relWithin 事件绝对路径落在哪个受监控根内，返回根与存储内相对路径。
func relWithin(roots map[string]*drivers.Local, name string) (root, rel string, ok bool) {
	for r := range roots {
		rel, err := filepath.Rel(r, name)
		if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
			continue
		}
		return r, rel, true
	}
	return "", "", false
}

// scheduleFromEvent 判断一次文件事件是否需要重扫，需要就把所在目录记进 pending。
// 只认「媒体文件直接变化」：我们自己的 `.index.jsonl` 写入、`.mocca` 等隐藏路径
// 都不触发，否则会陷入「扫→写索引→再触发→再扫」的自循环。
func scheduleFromEvent(roots map[string]*drivers.Local, ev fsnotify.Event, pending map[string]bool) {
	root, rel, ok := relWithin(roots, ev.Name)
	if !ok {
		return
	}
	if hasHiddenSegment(rel) || !isMediaName(filepath.Base(rel)) {
		return
	}
	pending[root+"\x00"+filepath.Dir(rel)] = true
}

// hasHiddenSegment 相对路径里是否含点开头的段（.mocca、隐藏目录）。
func hasHiddenSegment(rel string) bool {
	for _, seg := range strings.Split(rel, "/") {
		if strings.HasPrefix(seg, ".") {
			return true
		}
	}
	return false
}

// flushPending 把去抖期内存下的待重扫目录逐个跑 ScanDir。
func flushPending(roots map[string]*drivers.Local, pending map[string]bool, logf func(string, ...any)) {
	if len(pending) == 0 {
		return
	}
	dirs := pending
	pending = map[string]bool{}
	for key := range dirs {
		root, rel, _ := strings.Cut(key, "\x00")
		if ld := roots[root]; ld != nil {
			if err := ScanDir(ld, rel); err != nil {
				logf("重扫 %s/%s 失败: %v", root, rel, err)
			}
		}
	}
}
