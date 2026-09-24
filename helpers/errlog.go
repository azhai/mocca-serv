package helpers

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime/debug"
	"sync"
	"time"

	"github.com/pkg/errors"
)

// ErrorLogName 错误日志文件名。落在数据目录下，与 mocca.db、.mocca 同级。
const ErrorLogName = "error.log"

var (
	errMu     sync.Mutex
	errOut    io.Writer = os.Stderr // 未打开文件时退回标准错误：宁可少一份文件，也不能丢了信息
	errCloser io.Closer             // 非 nil 表示错误日志文件已就绪
)

// OpenErrorLog 打开（追加）数据目录下的错误日志。
//
// 和运行日志分开：运行日志（标准 log）记录每个请求的耗时，量大且正常；
// 错误日志只收 panic、致命错误——出事时打开它，第一行就是原因，不用在海量
// 请求日志里翻。写失败不阻断启动：错误日志本身打不开，也不该让服务起不来。
func OpenErrorLog(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return errors.Wrapf(err, "failed create %s", dir)
	}
	path := filepath.Join(dir, ErrorLogName)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return errors.Wrapf(err, "failed open %s", path)
	}

	errMu.Lock()
	defer errMu.Unlock()
	if errCloser != nil {
		_ = errCloser.Close() // 重复打开（测试里常见）时换掉旧句柄，不泄漏 fd
	}
	errOut, errCloser = f, f
	return nil
}

// CloseErrorLog 关闭错误日志文件，写回标准错误。进程退出前调用。
func CloseErrorLog() error {
	errMu.Lock()
	defer errMu.Unlock()
	if errCloser == nil {
		return nil
	}
	err := errCloser.Close()
	errCloser = nil
	errOut = os.Stderr
	return err
}

// Errorf 记一条错误日志。用于「不该发生但发生了」的分支。
func Errorf(format string, args ...any) {
	writeRecord("ERROR", "", fmt.Sprintf(format, args...))
}

// Fatalf 记一条错误日志后退出，语义同 log.Fatalf。
//
// where 是分组标签（如 "启动"、"索引"），便于事后按来源过滤。
// 文件未打开时不再往 stderr 补写——下面那句 log.Fatalf 已经打了，避免同一件事两行。
func Fatalf(where, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	errMu.Lock()
	if errCloser != nil {
		_, _ = errOut.Write([]byte(recordLine("FATAL", where, msg)))
	}
	errMu.Unlock()
	log.Fatalf("%s: %s", where, msg)
}

// Panicf 记录一次 panic 及其调用栈。
//
// 栈用 debug.Stack()：它连**其它 goroutine** 的栈一起抓。goroutine 泄漏、
// 死锁这类崩溃，只看出事的那个栈往往看不出因果。
func Panicf(where string, v any) {
	body := fmt.Sprintf("%v\n%s", v, debug.Stack())
	writeRecord("PANIC", where, body)
}

// RecoverPanic 供 defer 使用：捕获 panic 写入错误日志，返回捕获到的值。
//
// 用法必须是 `defer helpers.RecoverPanic("来源")` —— **直接** defer 本函数。
//
//	go func() {
//		defer helpers.RecoverPanic("启动索引")   // 对
//		...
//	}()
//	defer func() { _ = helpers.RecoverPanic("启动") }()  // 错：recover 会失效
//
// 原因：recover() 只在「被 defer 直接调用的那个函数」里有效。包一层匿名函数，
// 本函数就成了被普通调用的普通函数，recover() 返回 nil、panic 照旧往上传播，
// 错得悄无声息（表观看代码像是做了保护）。要退出进程请用 RecoverExit。
//
// 返回值只用来判断「是否发生过 panic」。不重新 panic：调用方想终止进程就
// os.Exit，想继续就把这个 goroutine 结束掉。
func RecoverPanic(where string) any {
	r := recover()
	if r == nil {
		return nil
	}
	Panicf(where, r)
	return r
}

// RecoverExit 供 main 用：捕获 panic、记进错误日志，然后以给定码退出。
//
//	defer helpers.RecoverExit("main", 1)
//
// 和 RecoverPanic 一样必须直接 defer。崩溃退出留非零码，是否重启交给
// systemd / gorch 这类守护进程决定。
func RecoverExit(where string, code int) {
	if r := recover(); r != nil {
		Panicf(where, r)
		os.Exit(code)
	}
}

// GoSafe 启动后台 goroutine，并把其中的 panic 记进错误日志。
//
// 裸 `go func()` 里的 panic 会直接带走整个进程——一个后台索引任务的越界
// 就能让正在播放的流全断。统一走这里：记录后该 goroutine 结束，进程继续服务。
func GoSafe(where string, fn func()) {
	go func() {
		defer RecoverPanic(where)
		fn()
	}()
}

func recordLine(level, where, body string) string {
	tag := level
	if where != "" {
		tag = level + " [" + where + "]"
	}
	return time.Now().Format(time.RFC3339) + " " + tag + " " + body + "\n"
}

// writeRecord 落一条记录：文件已打开写文件，否则写标准错误。
func writeRecord(level, where, body string) {
	line := recordLine(level, where, body)
	errMu.Lock()
	defer errMu.Unlock()
	if _, err := errOut.Write([]byte(line)); err != nil {
		_, _ = os.Stderr.WriteString("写错误日志失败: " + err.Error() + "\n")
	}
}
