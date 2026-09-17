package handlers

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/azhai/mocca/helpers"
	"github.com/azhai/mocca/models"
	"github.com/labstack/echo/v5"
)

// avatarColors 预设头像的底色，按 key 取模命中：同一个 key 永远同一张图。
//
// 12 个 key 配 12 个色，保证每个头像一眼可辨；色相是莫奈式的低饱和中间调
// （苔绿/睡莲蓝/藕紫/陶土…），与后台的奶油+抹茶主题同一套语言，
// 且都足够深，白字压得住。
var avatarColors = []string{
	"#6E8B3D", "#7E9A4E", "#96A55C", "#A8A85A",
	"#5C7A6B", "#4E7A6E", "#6B8A7A", "#5F7E96",
	"#6B7A8F", "#7E6E8F", "#8F7A9A", "#A2685A",
}

// Avatar 返回预设头像。
//
// 仓库不内置图片素材，这里按 key 现画一张 SVG：
// 好处是任何合法 key 都能拿到 200，不会因缺素材 404，
// 也顺带避免了「用户上传头像」带来的存储与越权读取问题。
func Avatar(c *echo.Context) error {
	key := strings.TrimSuffix(c.Param("key"), ".png")
	if !models.IsValidAvatar(key) {
		return helpers.Fail(c, helpers.CodeNotFound, "头像不存在")
	}
	top := avatarColors[avatarIndex(key)%len(avatarColors)]
	bottom := shade(top, 0.82) // 底部压深一点，避免大色块太平
	svg := fmt.Sprintf(
		`<svg xmlns="http://www.w3.org/2000/svg" width="128" height="128" viewBox="0 0 128 128">`+
			`<defs><linearGradient id="g" x1="0" y1="0" x2="0" y2="1">`+
			`<stop offset="0" stop-color="%s"/><stop offset="1" stop-color="%s"/>`+
			`</linearGradient></defs>`+
			`<rect width="128" height="128" rx="64" fill="url(#g)"/>`+
			`<text x="64" y="79" font-size="46" font-weight="600" font-family="sans-serif" `+
			`fill="#FDFBF4" fill-opacity="0.94" text-anchor="middle">%s</text>`+
			`</svg>`, top, bottom, key)
	c.Response().Header().Set("Content-Type", "image/svg+xml")
	// 头像可能被嵌在页面里：声明不嗅探，避免浏览器把 SVG 当别的类型处理
	c.Response().Header().Set("X-Content-Type-Options", "nosniff")
	return c.String(200, svg)
}

// avatarIndex 把 "01".."12" 折算成稳定下标。
func avatarIndex(key string) int {
	n := 0
	for _, r := range key {
		if r < '0' || r > '9' {
			continue
		}
		n = n*10 + int(r-'0')
	}
	return n
}

// shade 把 #RRGGBB 按 factor 整体压亮/压暗，用于做渐变的下半段。
// 解析失败时原样返回，不因为配色问题让头像接口报错。
func shade(hex string, factor float64) string {
	if len(hex) != 7 || hex[0] != '#' {
		return hex
	}
	v, err := strconv.ParseUint(hex[1:], 16, 32)
	if err != nil {
		return hex
	}
	r, g, b := int(v>>16), int(v>>8)&0xFF, int(v)&0xFF
	clamp := func(x int) int {
		if x < 0 {
			return 0
		}
		if x > 255 {
			return 255
		}
		return x
	}
	return fmt.Sprintf("#%02X%02X%02X",
		clamp(int(float64(r)*factor)),
		clamp(int(float64(g)*factor)),
		clamp(int(float64(b)*factor)))
}
