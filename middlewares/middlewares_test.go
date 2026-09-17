package middlewares_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/azhai/mocca/middlewares"
	"github.com/labstack/echo/v5"
)

// newEcho 挂一个受 BodyLimit 保护的回显接口。
func newEcho(limit int64) *echo.Echo {
	e := echo.New()
	e.Use(middlewares.BodyLimit(limit))
	e.POST("/echo", func(c *echo.Context) error {
		return c.JSON(200, map[string]any{"ok": true})
	})
	return e
}

func TestBodyLimitRejectsTooLargeByContentLength(t *testing.T) {
	e := newEcho(10) // 10 字节上限

	body := strings.Repeat("x", 50)
	req := httptest.NewRequest(http.MethodPost, "/echo", strings.NewReader(body))
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("响应不是信封: %v, body=%s", err, rec.Body.String())
	}
	if code, _ := resp["code"].(float64); int(code) != 400 {
		t.Errorf("超限应返回 400，got %v", resp["code"])
	}
	if msg, _ := resp["message"].(string); !strings.Contains(msg, "过大") {
		t.Errorf("提示应说明体积超限，got %q", msg)
	}
}

func TestBodyLimitAllowsNormalBody(t *testing.T) {
	e := newEcho(1024)
	req := httptest.NewRequest(http.MethodPost, "/echo", strings.NewReader("small"))
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	// 这里的回显接口直接返回业务 JSON，不是统一信封，所以看 HTTP 状态码
	if rec.Code != http.StatusOK {
		t.Errorf("正常体积应放行，got HTTP %d", rec.Code)
	}
}

func TestRequestLoggerPassesThrough(t *testing.T) {
	e := echo.New()
	e.Use(middlewares.RequestLogger(50 * time.Millisecond))
	e.GET("/ok", func(c *echo.Context) error { return c.String(200, "ok") })

	req := httptest.NewRequest(http.MethodGet, "/ok", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Body.String() != "ok" {
		t.Errorf("日志中间件不应改动响应，got %q", rec.Body.String())
	}
}
