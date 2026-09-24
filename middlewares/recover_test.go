package middlewares_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/azhai/mocca/helpers"
	"github.com/azhai/mocca/middlewares"
	"github.com/labstack/echo/v5"
)

// newPanicEcho 起一个只会在 handler 里 panic 的应用，错误日志指向临时目录。
func newPanicEcho(t *testing.T, route string) (*echo.Echo, string) {
	t.Helper()
	dir := t.TempDir()
	if err := helpers.OpenErrorLog(dir); err != nil {
		t.Fatalf("打开错误日志失败: %v", err)
	}
	t.Cleanup(func() { _ = helpers.CloseErrorLog() })

	e := echo.New()
	e.Use(middlewares.RecoverLog())
	e.GET(route, func(c *echo.Context) error {
		var nilMap map[string]string
		nilMap["boom"] = "写进 nil map 必 panic"
		return nil
	})
	return e, filepath.Join(dir, helpers.ErrorLogName)
}

func TestRecoverLogReturnsEnvelope500(t *testing.T) {
	e, logPath := newPanicEcho(t, "/api/fs/list")

	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/fs/list", nil))

	// 业务约定：HTTP 恒 200，成败看 body.code
	if rec.Code != http.StatusOK {
		t.Fatalf("HTTP 应为 200，got %d", rec.Code)
	}
	var resp struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("响应不是信封: %v, body=%s", err, rec.Body.String())
	}
	if resp.Code != helpers.CodeInternal {
		t.Errorf("code 应为 %d，got %d", helpers.CodeInternal, resp.Code)
	}
	if !strings.Contains(resp.Message, "内部错误") {
		t.Errorf("提示应说明是内部错误，got %q", resp.Message)
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("panic 未写进错误日志: %v", err)
	}
	got := string(data)
	if !strings.Contains(got, "PANIC [http GET /api/fs/list]") {
		t.Errorf("日志应记下方法与路径，got:\n%s", got)
	}
	if !strings.Contains(got, "assignment to entry in nil map") {
		t.Errorf("日志应含 panic 原因，got:\n%s", got)
	}
	if !strings.Contains(got, "TestRecoverLogReturnsEnvelope500") {
		t.Errorf("日志应含调用栈，got:\n%s", got)
	}
}

// 取流接口的失败必须回真实状态码：播放器不看信封，HTTP 200 + JSON 会被
// 当成媒体数据去解码，报出来的错与真实原因无关。
func TestRecoverLogStreamPathUsesRealStatus(t *testing.T) {
	e, logPath := newPanicEcho(t, "/d/*path")

	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/d/media/demo.mp4", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("取流路径应回 HTTP 500，got %d, body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(readFile(t, logPath), "PANIC [http GET /d/media/demo.mp4]") {
		t.Errorf("取流路径的 panic 同样要留痕:\n%s", readFile(t, logPath))
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读错误日志失败: %v", err)
	}
	return string(data)
}
