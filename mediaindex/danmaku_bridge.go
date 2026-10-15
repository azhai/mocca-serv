package mediaindex

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/azhai/mocca/helpers"
	"github.com/azhai/mocca/lib/mq"
)

// 弹幕跨进程桥（默认关）：用 lib/mq.Journal 把本机多个 mocca 实例之间的实时弹幕打通。
//
// 为什么默认关、什么时候才开：单进程部署用不到（进程内 Bus 已经扇出好了）。只有把 mocca
// 放进反向代理后跑 2+ 个实例时，A 实例收到的弹幕才推不到连在 B 实例上的 SSE 客户端 ——
// 这正是 danmaku_hub.go 顶部注释写下的那条局限，桥就是它的解。main 里只有配了
// DANMAKU_JOURNAL_DIR 才 StartDanmakuBridge，没配就整条路径是 no-op，热路径零成本。
//
// 形状（受 Journal 的约束决定，不是随手选的）：
//   - Journal 是**单写者**的（见 lib/mq/journal.go：它不做文件锁，多写者会交错写坏记录头）。
//     所以每个实例只写**自己那一份**文件、只读**别人的**：既绕开多写者竞态，又天然无回环
//     —— 自己发的不会从自己的文件里被读回来，于是连 origin 标记都不必编进消息。
//   - 出站：PublishDanmaku 先投本地 Bus（本实例的 SSE 立刻收到），再 append 进本实例文件。
//   - 入站：后台每 bridgePoll 轮询一次目录，把别的实例文件里的新记录注入本地 Bus（只注入、
//     不再 append，否则会被别的实例读回后二次注入）。
//   - 只投新消息、不重放历史：首次发现某个文件就把游标移到末尾（seekToEnd）。后进来的观看者
//     要的历史弹幕仍从 .danmaku.jsonl 文件整读，桥只负责「此刻之后」的实时增量。
type danmakuBridge struct {
	bus      *mq.Bus // 注入目标；生产传全局 danmakuBus，测试传自己的
	dir      string  // 存放各实例 journal 文件的目录
	self     string  // 本实例的文件名（tail 时跳过，避免自读自）
	w        *mq.Journal
	mu       sync.Mutex
	readers  map[string]*mq.JournalReader // 已打开的其他实例文件 → 带游标的 reader
	stop     chan struct{}
	stopOnce sync.Once
}

const (
	bridgePoll       = 250 * time.Millisecond // 轮询间隔：弹幕不需要亚毫秒延迟，别把 CPU 空转出来
	bridgeFilePrefix = "danmaku-"
	bridgeFileSuffix = ".journal"
	// JournalReader 的 topic 过滤：空串＝读全部（桥自己按帧里的 topic 再分发到各视频）。
	bridgeAllTopics = ""
)

// bridge 是当前生效的桥；nil = 关闭。用 atomic.Pointer 而非裸包级变量：
// PublishDanmaku 从各 http handler goroutine 读它，main 在起服务前一次性写它，
// 逻辑上已有 happens-before，但测试会并发启停，原子量才站得住 -race。
var bridge atomic.Pointer[danmakuBridge]

// StartDanmakuBridge 开启桥并把本实例的 journal 文件准备好；dir 不存在会创建。
// 只在 main 里 DANMAKU_JOURNAL_DIR 配了才调用。返回错误则调用方应放弃启用桥（不阻断启动）。
func StartDanmakuBridge(dir string) error {
	b, err := newBridge(dir, danmakuBus)
	if err != nil {
		return err
	}
	bridge.Store(b)
	helpers.GoSafe("弹幕跨进程桥", b.run)
	return nil
}

// StopDanmakuBridge 停后台轮询、关所有文件句柄，并摘掉全局桥（此后 PublishDanmaku 不再落盘）。
func StopDanmakuBridge() {
	if b := bridge.Swap(nil); b != nil {
		b.Close()
	}
}

// newBridge 造一个桥实例，注入目标总线 bus。与包级全局解耦，方便单测用两只 Bus 模拟两进程。
func newBridge(dir string, bus *mq.Bus) (*danmakuBridge, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	b := &danmakuBridge{
		bus:     bus,
		dir:     dir,
		self:    fmt.Sprintf("%s%d-%s%s", bridgeFilePrefix, os.Getpid(), randHex(4), bridgeFileSuffix),
		readers: map[string]*mq.JournalReader{},
		stop:    make(chan struct{}),
	}
	w, err := mq.OpenJournal(filepath.Join(dir, b.self))
	if err != nil {
		return nil, err
	}
	b.w = w
	return b, nil
}

// publishOut 把一条已编码好的弹幕落到本实例文件（供别的实例读）。
// body 直接进帧，不再 marshal —— PublishDanmaku 已经编过一次，整条链路只编一次。
func (b *danmakuBridge) publishOut(topic string, body []byte) error {
	_, err := b.w.Append(topic, body)
	return err
}

// run 后台轮询循环；收到 stop 就收尾并释放文件句柄。
func (b *danmakuBridge) run() {
	t := time.NewTicker(bridgePoll)
	defer t.Stop()
	for {
		select {
		case <-b.stop:
			b.closeFiles()
			return
		case <-t.C:
			b.tailOnce()
		}
	}
}

// tailOnce 扫一遍目录：为每个「别人的」弹幕文件确保有 reader，然后把它们的新记录注入本地 Bus。
// 目录读失败就等下一轮（别的实例可能刚建目录/正在写），不退出。
func (b *danmakuBridge) tailOnce() {
	entries, err := os.ReadDir(b.dir)
	if err != nil {
		return
	}
	for _, de := range entries {
		name := de.Name()
		if de.IsDir() || name == b.self ||
			!strings.HasPrefix(name, bridgeFilePrefix) || !strings.HasSuffix(name, bridgeFileSuffix) {
			continue
		}
		b.ensureReader(name)
		b.drain(name)
	}
}

// ensureReader 首次见到某实例文件就打开一个只读游标，并立刻移到末尾（只关心之后的新消息）。
func (b *danmakuBridge) ensureReader(name string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.readers[name]; ok {
		return
	}
	r, err := mq.OpenJournalReader(filepath.Join(b.dir, name), bridgeAllTopics)
	if err != nil {
		return // 文件可能刚被对方删了（进程退出清理），下轮再说
	}
	_ = seekReaderToEnd(r)
	b.readers[name] = r
}

// drain 把一个 reader 当前可读的全部新记录注入本地 Bus。Next 读到末尾返回 false 就退出等下轮。
// 注意只 Publish、不 Append：注入的消息已经在作者实例落过一次盘，再落一遍会被别人重复读到。
func (b *danmakuBridge) drain(name string) {
	b.mu.Lock()
	r := b.readers[name]
	b.mu.Unlock()
	if r == nil {
		return
	}
	for {
		m, ok := r.Next()
		if !ok {
			return
		}
		_, _ = b.bus.Publish(m.Topic, m.Body)
	}
}

// closeFiles 关本实例写文件与所有读游标（幂等，供 Stop 及 run 退出时调）。
func (b *danmakuBridge) closeFiles() {
	_ = b.w.Close()
	b.mu.Lock()
	for name, r := range b.readers {
		_ = r.Close()
		delete(b.readers, name)
	}
	b.mu.Unlock()
}

// Close 通知后台轮询退出。句柄由 run 的收尾分支释放。
func (b *danmakuBridge) Close() {
	b.stopOnce.Do(func() { close(b.stop) })
}

// seekReaderToEnd 把读游标移到当前文件末尾，从而只读之后的新消息。
// 借道 SeekTo：给一个实际永不达到的 want（seq 从 0 递增，2^64-1 到不了），
// scanJournal 会一路扫到底、把 clean 停在最后一个完整记录末尾＝文件大小。空文件则回到 0，
// 后续 append 从 0 起被正常读到。
func seekReaderToEnd(r *mq.JournalReader) error {
	return r.SeekTo(uint64(math.MaxUint64))
}

// randHex 生成 n 字节随机数的十六进制串，用作实例文件名后缀（同 pid 多开的情况也能区分）。
func randHex(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		// crypto/rand 失败极罕见；用时间戳兜底，保证文件名在本目录里唯一即可。
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}
