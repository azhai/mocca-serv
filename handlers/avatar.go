package handlers

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/azhai/mocca/helpers"
	web "github.com/azhai/mocca/lib/web"
	"github.com/azhai/mocca/models"
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

// avatarArt 一个预设头像：name 是造型名（进 `<title>`，读屏、悬停提示与测试都拿它当锚点），
// shapes 是画在渐变底上的图元。
type avatarArt struct {
	name   string
	shapes string
}

// 头像 palette（写在下面每张卡通里的固定色，与 key 无关）：
//
//	#FDFBF4 奶油（毛色/亮部）  #2C2A24 墨（眼睛/鼻子/线条）  #FFFFFF 白（眼白/肚子）
//	#E7A2A6 #F0B9BD 猪粉系      #E8A33C 橙（小狐/喙/爪） #C97F22 深橙（耳内/纹） #D9873A 虎橙
//	#B07C4F 熊棕            #F0D063 小鸡黄与猫头鹰的虹膜  #7FB069 蛙绿  #6E96B8 #4E7391 蓝灰系（鲸/猫头鹰）
//
// 造型只用大色块 + 两三处特征（耳/喙/纹）：最小的使用场景是评论与弹幕旁 24–32px 的圆图，
// 细线和小面积细节在那个尺寸下会直接糊掉。也不用 emoji：那要依赖客户端字体，
// NAS 上常见的浏览器/系统没有彩色 emoji 字体，会退化成方块或线稿。
//
// 同系的两张必须靠**配色与轮廓**分开，不能只靠一个小细节（冒烟时 05 小狐与 12 小虎
// 因为共用一套橙色+白脸被认成同一张）：小狐用黑尖耳 + 两侧奶油颊毛 + 浅橙脸，
// 小虎用墨色圆耳（奶油内耳）+ 额头三道竖纹 + 深虎橙脸；猫头鹰用金黄虹膜 + 菱形喙，
// 与白眼圈三角喙的企鹅拉开；小熊换成熊棕脸 + 奶油口吻，不再和奶油脸的小猫混。
var avatarArts = map[string]avatarArt{
	"01": {"小猫", `<path d="M30 46 44 20 60 40Z" fill="#FDFBF4"/>` +
		`<path d="M98 46 84 20 68 40Z" fill="#FDFBF4"/>` +
		`<circle cx="64" cy="70" r="36" fill="#FDFBF4"/>` +
		`<path d="M38 42 46 27 55 39Z" fill="#E7A2A6" opacity=".75"/>` +
		`<path d="M90 42 82 27 73 39Z" fill="#E7A2A6" opacity=".75"/>` +
		`<ellipse cx="50" cy="66" rx="5" ry="7" fill="#2C2A24"/>` +
		`<ellipse cx="78" cy="66" rx="5" ry="7" fill="#2C2A24"/>` +
		`<path d="M60 80h8l-4 6Z" fill="#2C2A24"/>` +
		`<path d="M22 74h15M91 74h15" stroke="#2C2A24" stroke-width="4" stroke-linecap="round" opacity=".65"/>`},
	"02": {"小熊", `<circle cx="36" cy="42" r="15" fill="#B07C4F"/>` +
		`<circle cx="92" cy="42" r="15" fill="#B07C4F"/>` +
		`<circle cx="36" cy="42" r="7" fill="#2C2A24" opacity=".45"/>` +
		`<circle cx="92" cy="42" r="7" fill="#2C2A24" opacity=".45"/>` +
		`<circle cx="64" cy="72" r="36" fill="#B07C4F"/>` +
		`<ellipse cx="64" cy="88" rx="19" ry="14" fill="#FDFBF4"/>` +
		`<circle cx="50" cy="64" r="5" fill="#2C2A24"/>` +
		`<circle cx="78" cy="64" r="5" fill="#2C2A24"/>` +
		`<ellipse cx="64" cy="80" rx="7" ry="5" fill="#2C2A24"/>`},
	"03": {"熊猫", `<circle cx="34" cy="42" r="16" fill="#2C2A24"/>` +
		`<circle cx="94" cy="42" r="16" fill="#2C2A24"/>` +
		`<circle cx="64" cy="72" r="36" fill="#FFFFFF"/>` +
		`<ellipse cx="49" cy="66" rx="11" ry="13" fill="#2C2A24"/>` +
		`<ellipse cx="79" cy="66" rx="11" ry="13" fill="#2C2A24"/>` +
		`<circle cx="50" cy="64" r="4" fill="#FFFFFF"/>` +
		`<circle cx="78" cy="64" r="4" fill="#FFFFFF"/>` +
		`<ellipse cx="64" cy="84" rx="7" ry="5" fill="#2C2A24"/>`},
	"04": {"小兔", `<ellipse cx="50" cy="34" rx="10" ry="26" fill="#FDFBF4" transform="rotate(-14 50 34)"/>` +
		`<ellipse cx="78" cy="34" rx="10" ry="26" fill="#FDFBF4" transform="rotate(14 78 34)"/>` +
		`<ellipse cx="50" cy="36" rx="4" ry="17" fill="#E7A2A6" opacity=".7" transform="rotate(-14 50 36)"/>` +
		`<ellipse cx="78" cy="36" rx="4" ry="17" fill="#E7A2A6" opacity=".7" transform="rotate(14 78 36)"/>` +
		`<circle cx="64" cy="82" r="30" fill="#FDFBF4"/>` +
		`<circle cx="53" cy="78" r="5" fill="#2C2A24"/>` +
		`<circle cx="75" cy="78" r="5" fill="#2C2A24"/>` +
		`<path d="M60 88h8l-4 5Z" fill="#E7A2A6"/>`},
	"05": {"小狐", `<path d="M30 48 44 16 58 46Z" fill="#2C2A24"/>` +
		`<path d="M98 48 84 16 70 46Z" fill="#2C2A24"/>` +
		`<path d="M30 66 44 74 30 84Z" fill="#FDFBF4"/>` +
		`<path d="M98 66 84 74 98 84Z" fill="#FDFBF4"/>` +
		`<circle cx="64" cy="68" r="32" fill="#E8A33C"/>` +
		`<path d="M64 104C48 95 42 82 44 74h40c2 8-6 21-20 30Z" fill="#FDFBF4"/>` +
		`<ellipse cx="51" cy="62" rx="6" ry="4" fill="#2C2A24"/>` +
		`<ellipse cx="77" cy="62" rx="6" ry="4" fill="#2C2A24"/>` +
		`<path d="M60 84h8l-4 6Z" fill="#2C2A24"/>`},
	"06": {"小猪", `<circle cx="64" cy="72" r="34" fill="#F0B9BD"/>` +
		`<path d="M34 46 54 42 44 62Z" fill="#E7A2A6"/>` +
		`<path d="M94 46 74 42 84 62Z" fill="#E7A2A6"/>` +
		`<ellipse cx="64" cy="82" rx="16" ry="12" fill="#E7A2A6"/>` +
		`<ellipse cx="58" cy="82" rx="3" ry="4" fill="#2C2A24"/>` +
		`<ellipse cx="70" cy="82" rx="3" ry="4" fill="#2C2A24"/>` +
		`<circle cx="50" cy="62" r="4.5" fill="#2C2A24"/>` +
		`<circle cx="78" cy="62" r="4.5" fill="#2C2A24"/>`},
	"07": {"企鹅", `<ellipse cx="64" cy="74" rx="34" ry="38" fill="#2C2A24"/>` +
		`<ellipse cx="64" cy="86" rx="21" ry="25" fill="#FFFFFF"/>` +
		`<circle cx="52" cy="56" r="9" fill="#FFFFFF"/>` +
		`<circle cx="76" cy="56" r="9" fill="#FFFFFF"/>` +
		`<circle cx="52" cy="56" r="4" fill="#2C2A24"/>` +
		`<circle cx="76" cy="56" r="4" fill="#2C2A24"/>` +
		`<path d="M58 66h12l-6 9Z" fill="#E8A33C"/>`},
	"08": {"猫头鹰", `<path d="M38 46 54 28 58 48Z" fill="#4E7391"/>` +
		`<path d="M90 46 74 28 70 48Z" fill="#4E7391"/>` +
		`<circle cx="64" cy="72" r="34" fill="#6E96B8"/>` +
		`<circle cx="50" cy="66" r="13" fill="#FFFFFF"/>` +
		`<circle cx="78" cy="66" r="13" fill="#FFFFFF"/>` +
		`<circle cx="50" cy="66" r="8" fill="#F0D063"/>` +
		`<circle cx="78" cy="66" r="8" fill="#F0D063"/>` +
		`<circle cx="50" cy="66" r="4" fill="#2C2A24"/>` +
		`<circle cx="78" cy="66" r="4" fill="#2C2A24"/>` +
		`<path d="M64 80l6 8-6 8-6-8Z" fill="#E8A33C"/>`},
	"09": {"青蛙", `<ellipse cx="64" cy="80" rx="38" ry="28" fill="#7FB069"/>` +
		`<circle cx="44" cy="52" r="13" fill="#7FB069"/>` +
		`<circle cx="84" cy="52" r="13" fill="#7FB069"/>` +
		`<circle cx="44" cy="52" r="7" fill="#FFFFFF"/>` +
		`<circle cx="84" cy="52" r="7" fill="#FFFFFF"/>` +
		`<circle cx="44" cy="52" r="3.5" fill="#2C2A24"/>` +
		`<circle cx="84" cy="52" r="3.5" fill="#2C2A24"/>` +
		`<path d="M46 86q18 12 36 0" stroke="#2C2A24" stroke-width="4" fill="none" stroke-linecap="round"/>`},
	"10": {"小鸡", `<path d="M50 42q6-16 14-2 6-14 14 2Z" fill="#E7A2A6"/>` +
		`<circle cx="64" cy="74" r="32" fill="#F0D063"/>` +
		`<circle cx="52" cy="66" r="5" fill="#2C2A24"/>` +
		`<circle cx="76" cy="66" r="5" fill="#2C2A24"/>` +
		`<path d="M58 78h12l-6 8Z" fill="#E8A33C"/>` +
		`<ellipse cx="64" cy="106" rx="12" ry="4" fill="#E8A33C" opacity=".85"/>`},
	"11": {"鲸", `<path d="M104 66c8-6 14-18 12-26-9 5-16 13-18 22Z" fill="#4E7391"/>` +
		`<ellipse cx="62" cy="80" rx="42" ry="26" fill="#6E96B8"/>` +
		`<path d="M30 92q26 12 56-4" stroke="#FFFFFF" stroke-width="5" fill="none" opacity=".45"/>` +
		`<circle cx="40" cy="74" r="4.5" fill="#2C2A24"/>` +
		`<path d="M58 44q0-12 8-16M74 46q4-8 12-8" stroke="#FDFBF4" stroke-width="4" fill="none" stroke-linecap="round" opacity=".9"/>`},
	"12": {"小虎", `<circle cx="40" cy="44" r="13" fill="#2C2A24" opacity=".75"/>` +
		`<circle cx="88" cy="44" r="13" fill="#2C2A24" opacity=".75"/>` +
		`<circle cx="40" cy="44" r="7" fill="#FDFBF4"/>` +
		`<circle cx="88" cy="44" r="7" fill="#FDFBF4"/>` +
		`<circle cx="64" cy="72" r="33" fill="#D9873A"/>` +
		`<path d="M52 40v14M64 36v18M76 40v14" stroke="#2C2A24" stroke-width="5" stroke-linecap="round" opacity=".85"/>` +
		`<ellipse cx="64" cy="90" rx="16" ry="12" fill="#FDFBF4"/>` +
		`<circle cx="51" cy="68" r="4.5" fill="#2C2A24"/>` +
		`<circle cx="77" cy="68" r="4.5" fill="#2C2A24"/>` +
		`<path d="M60 86h8l-4 5Z" fill="#2C2A24"/>`},
}

// Avatar 返回预设头像：渐变底色 + 一枚卡通造型。
//
// 仓库不内置图片素材，这里按 key 现画一张 SVG：
// 好处是任何合法 key 都能拿到 200，不会因缺素材 404，
// 也顺带避免了「用户上传头像」带来的存储与越权读取问题。
func Avatar(c *web.Context) error {
	key := strings.TrimSuffix(c.Param("key"), ".png")
	if !models.IsValidAvatar(key) {
		return helpers.Fail(c, helpers.CodeNotFound, "头像不存在")
	}
	top := avatarColors[avatarIndex(key)%len(avatarColors)]
	bottom := shade(top, 0.82) // 底部压深一点，避免大色块太平

	// 兜底：万一以后加了预设却忘了画，就退化成画编号 —— 这个 key 仍然 200、
	// 仍然与其他头像不同，不会返回一张空白圆。
	name, art := "头像 "+key, `<text x="64" y="79" font-size="46" font-weight="600`+
		`" font-family="sans-serif" fill="#FDFBF4" fill-opacity="0.94" text-anchor="middle">`+key+`</text>`
	if a, ok := avatarArts[key]; ok {
		name, art = a.name, a.shapes
	}
	// key 只能来自 models.AvatarPresets 白名单，拼进 SVG 不需要转义。
	svg := fmt.Sprintf(
		`<svg xmlns="http://www.w3.org/2000/svg" width="128" height="128" viewBox="0 0 128 128" role="img">`+
			`<title>%s</title>`+
			`<defs><linearGradient id="g" x1="0" y1="0" x2="0" y2="1">`+
			`<stop offset="0" stop-color="%s"/><stop offset="1" stop-color="%s"/>`+
			`</linearGradient></defs>`+
			`<rect width="128" height="128" rx="64" fill="url(#g)"/>`+
			`%s</svg>`, name, top, bottom, art)
	c.Response().Header().Set("Content-Type", "image/svg+xml")
	// 头像可能被嵌在页面里：声明不嗅探，避免浏览器把 SVG 当别的类型处理
	c.Response().Header().Set("X-Content-Type-Options", "nosniff")
	return c.String(200, "%s", svg)
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
