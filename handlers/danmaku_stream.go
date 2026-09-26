package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/azhai/mocca/helpers"
	"github.com/azhai/mocca/mediaindex"
	"github.com/labstack/echo/v5"
)

// DanmakuStream SSE：把某个媒体的**新**弹幕实时推给正在看它的人。
//
// 只推新增：连接建立前的历史由客户端先拉一次 `GET /api/danmaku`（它本来就要全量取，
// 顺手就拿到了，没必要在流里再发一遍）。
//
// 两个实现细节值得一提：
//   - **token 走查询串**：浏览器的 EventSource 不能带自定义请求头，所以鉴权只能靠
//     `?token=`（middlewares.TokenFrom 已经支持这种取法）；
//   - **心跳**：反向代理和浏览器都会掐掉长时间空闲的连接，15 秒一行注释帧足够保活。
func DanmakuStream(c *echo.Context) error {
	mediaPath := c.QueryParam("path")
	if mediaPath == "" {
		return helpers.Fail(c, helpers.CodeBadRequest, "缺少 path")
	}
	drv, sha, err := danmakuTarget(c, mediaPath)
	if err != nil {
		return helpers.Fail(c, helpers.CodeBadRequest, err.Error())
	}
	storageID := drv.Identity()
	_ = drv.Close() // 订阅只用到"哪块盘 + 哪个视频"，别把驱动（尤其 SMB 连接）占在整个长连接上

	ch, cancel := mediaindex.SubscribeDanmaku(storageID, sha)
	defer cancel()

	w := c.Response()
	// echo v5 的 Response() 是 http.ResponseWriter，刷新要自己断言成 http.Flusher。
	// 拿不到（比如被某个中间件包成了不支持刷新的 writer）就只能不刷 —— 弹幕会攒着一起到，
	// 但连接本身仍然正常。
	flush := func() {
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // 让 nginx 别攒着，否则弹幕会"堵一会儿再一起到"
	w.WriteHeader(http.StatusOK)
	flush()

	enc := json.NewEncoder(w)
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-c.Request().Context().Done():
			return nil // 客户端断开（关页面、切视频）
		case e, ok := <-ch:
			if !ok {
				return nil
			}
			if _, err := w.Write([]byte("event: danmaku\ndata: ")); err != nil {
				return nil
			}
			if err := enc.Encode(e); err != nil {
				return nil
			}
			if _, err := w.Write([]byte("\n")); err != nil {
				return nil
			}
			flush()
		case <-ticker.C:
			if _, err := w.Write([]byte(": ping\n\n")); err != nil {
				return nil
			}
			flush()
		}
	}
}
