package helpers_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/azhai/mocca/helpers"
	"github.com/pkg/errors"
)

// openLog 把错误日志指向临时目录，返回日志文件路径。
// helpers 的错误日志是包级单例，所以这些用例不能 t.Parallel()。
func openLog(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := helpers.OpenErrorLog(dir); err != nil {
		t.Fatalf("打开错误日志失败: %v", err)
	}
	t.Cleanup(func() { _ = helpers.CloseErrorLog() })
	return filepath.Join(dir, helpers.ErrorLogName)
}

func readLog(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("错误日志未写出: %v", err)
	}
	return string(data)
}

func TestRecoverPanicWritesValueAndStack(t *testing.T) {
	path := openLog(t)

	func() {
		defer helpers.RecoverPanic("单元测试")
		panic("boom-marker")
	}()

	got := readLog(t, path)
	if !strings.Contains(got, "PANIC [单元测试]") {
		t.Errorf("应带 PANIC 级别与来源标签，got:\n%s", got)
	}
	if !strings.Contains(got, "boom-marker") {
		t.Errorf("应含 panic 值，got:\n%s", got)
	}
	// 栈里必须有本函数的名字，否则等于没记调用链
	if !strings.Contains(got, "TestRecoverPanicWritesValueAndStack") {
		t.Errorf("应含调用栈（函数名），got:\n%s", got)
	}
}

func TestRecoverPanicReturnsNilWithoutPanic(t *testing.T) {
	openLog(t)
	func() {
		defer func() {
			if r := helpers.RecoverPanic("单元测试"); r != nil {
				t.Errorf("没有 panic 时不该返回任何值，got %v", r)
			}
		}()
	}()
}

func TestGoSafeRecordsPanicAndProcessSurvives(t *testing.T) {
	path := openLog(t)

	done := make(chan struct{})
	helpers.GoSafe("后台任务", func() {
		defer close(done)
		panic("后台崩了")
	})

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("后台任务没跑完")
	}

	// 日志写入在同一 goroutine 里、close(done) 之前完成的（defer 顺序：先 close 再 recover），
	// 但 recover 发生在 GoSafe 外层，所以这里给文件一点落盘时间再读。
	deadline := time.Now().Add(time.Second)
	for {
		if got := readLog(t, path); strings.Contains(got, "后台崩了") {
			if !strings.Contains(got, "PANIC [后台任务]") {
				t.Errorf("标签不对，got:\n%s", got)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("后台 panic 未被记录:\n%s", readLog(t, path))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestErrorfWritesLine(t *testing.T) {
	path := openLog(t)

	helpers.Errorf("磁盘只剩 %d MB", 3)

	got := readLog(t, path)
	if !strings.Contains(got, "ERROR 磁盘只剩 3 MB") {
		t.Errorf("记录内容不对，got:\n%s", got)
	}
	// 时间戳要在行首，便于按时间排序与切片
	if !strings.HasPrefix(got, time.Now().Format("2006-01-02")) {
		t.Errorf("行首应是日期，got:\n%s", got)
	}
}

func TestErrorMessageIsOneLinePerCall(t *testing.T) {
	path := openLog(t)

	helpers.Errorf("第一件")
	helpers.Errorf("第二件")

	lines := strings.Split(strings.TrimRight(readLog(t, path), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("每次调用应恰好一行，got %d 行:\n%s", len(lines), strings.Join(lines, "\n"))
	}
}

// TestRecoverPanicInsideClosureDoesNotRecover 钉住那个坑：
// recover() 只在「被 defer 直接调用的函数」里有效，包一层匿名函数就会失效。
// 这里不是测我们的代码有多好，而是防止后人把 main 里的兜底改回这种写法——
// 那样代码看着像做了保护，实际 panic 照样打穿。
func TestRecoverPanicInsideClosureDoesNotRecover(t *testing.T) {
	openLog(t)

	recovered := false
	func() {
		defer func() {
			if r := helpers.RecoverPanic("包在匿名函数里"); r != nil {
				recovered = true
			}
			// 兜住继续上抛的 panic，否则整个测试进程挂掉
			_ = recover()
		}()
		panic("不该被 RecoverPanic 捕获")
	}()

	if recovered {
		t.Error("匿名函数里调用 RecoverPanic 不该生效：recover 必须被 defer 直接调用")
	}
}

// TestRecoverExitWritesThenExits 用子进程验证 RecoverExit：
// 它会 os.Exit，只能另起进程跑，再看退出码与日志文件。
func TestRecoverExitWritesThenExits(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=TestRecoverExitChild")
	cmd.Env = append(os.Environ(),
		"MOCCA_TEST_ERRLOG_DIR="+dir, "MOCCA_TEST_RECOVER_EXIT=1")
	err := cmd.Run()

	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("子进程应以非零码退出，got err=%v", err)
	}
	if exitErr.ExitCode() != 1 {
		t.Errorf("退出码应为 1，got %d", exitErr.ExitCode())
	}

	got := readLog(t, filepath.Join(dir, helpers.ErrorLogName))
	if !strings.Contains(got, "子进程崩了") {
		t.Errorf("main 兜底也应留痕，got:\n%s", got)
	}
	if !strings.Contains(got, "PANIC [main]") {
		t.Errorf("来源标签应为 main，got:\n%s", got)
	}
}

// TestRecoverExitChild 只作为上面那个用例的子进程运行。
func TestRecoverExitChild(t *testing.T) {
	if os.Getenv("MOCCA_TEST_RECOVER_EXIT") != "1" {
		t.Skip("子进程专用用例")
	}
	if err := helpers.OpenErrorLog(os.Getenv("MOCCA_TEST_ERRLOG_DIR")); err != nil {
		t.Fatalf("打开错误日志失败: %v", err)
	}
	defer helpers.RecoverExit("main", 1)
	panic("子进程崩了")
}
