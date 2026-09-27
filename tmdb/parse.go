package tmdb

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// dropWords 发布组/清晰度/编码/音轨类标记，刮削时必须从关键词里剔掉。
// 全部按小写比对；中英混排的常见叫法一并列出（片名里几乎不会出现这些词）。
var dropWords = map[string]bool{
	// 分辨率
	"240": true, "360": true, "480": true, "576": true, "720": true, "1080": true,
	"1440": true, "2160": true, "480p": true, "576p": true, "720p": true, "1080p": true,
	"1080i": true, "1440p": true, "2160p": true, "4k": true, "8k": true,
	"uhd": true, "hd": true, "fhd": true, "fullhd": true, "sd": true, "hdrip": true,
	// 来源
	"bluray": true, "blu-ray": true, "bdrip": true, "brrip": true, "bd": true,
	"web": true, "webdl": true, "web-dl": true, "webrip": true, "hdtv": true,
	"dvdrip": true, "dvd": true, "remux": true, "hdvd": true, "ts": true, "tc": true,
	// 编码
	"x264": true, "x265": true, "h264": true, "h265": true, "hevc": true,
	"avc": true, "xvid": true, "divx": true, "10bit": true, "8bit": true, "10bits": true,
	// 音频
	"aac": true, "ac3": true, "eac3": true, "dts": true, "dtshd": true, "truehd": true,
	"atmos": true, "flac": true, "mp3": true, "ddp": true, "dd5": true, "ddp5": true,
	// 版本/其它标记
	"hdr": true, "hdr10": true, "dovi": true, "dv": true, "sdr": true,
	"repack": true, "proper": true, "internal": true, "remastered": true,
	"extended": true, "uncut": true, "unrated": true, "imax": true,
	"multi": true, "dual": true, "chs": true, "cht": true, "gb": true, "big5": true,
	"chinese": true, "english": true, "subs": true, "sub": true, "idx": true,
	// 中文常见标记
	"国语": true, "国配": true, "中字": true, "中英": true, "双语": true,
	"字幕": true, "高清": true, "蓝光": true, "超清": true, "完整版": true,
	"收藏版": true, "导演剪辑版": true, "修复版": true, "加长版": true,
	"重制版": true, "未删减": true, "内嵌": true, "外挂": true,
}

var (
	// bracketRe 括号组：[..] (..) {..}，内容不含嵌套括号
	bracketRe = regexp.MustCompile(`[\[\(\{]([^\[\]\(\)\{\}]*)[\]\)\}]`)
	// urlRe 网址/域名特征：命中说明这段是发布组署名（如 www.ygdy8.com），整组丢掉
	urlRe = regexp.MustCompile(`(?i)(https?://|www\.|\.(com|net|org|cn|me|tv|io|cc|xyz|top)\b)`)
	// sepRe 字段分隔符：点/下划线/加号/半角与全角逗号一律视作空白
	// （连字符保留，Spider-Man 要留住）。\x{FF0C} 是全角逗号。
	sepRe = regexp.MustCompile(`[._+,\x{FF0C}]`)
	// dashYearRe 「短横 + 4 位数字」：`让子弹飞-2010` 的短横是分隔符，不是片名的一部分。
	// 只切短横后面紧跟 4 位数字的位置，`Spider-Man` 这类片名里的短横照旧保留。
	dashYearRe = regexp.MustCompile(`-(\d{4})`)
	// tailYearRe 检索词末尾的「空格/短横 + 年份」，供 SplitKeyword 拆「片名 + 年份」用。
	tailYearRe = regexp.MustCompile(`[\s\-]+(19\d{2}|20\d{2})$`)
	// keySepRe 比较片名时视作空白的装饰性标点：全角/半角冒号、间隔号、顿号、逗号、下划线。
	// 于是 `Spider-Man: No Way Home` 与用户输入的 `Spider-Man No Way Home` 视为同一个片名。
	keySepRe = regexp.MustCompile(`[:：·、,，_]`)
	// trimCut 词首尾的装饰性符号
	trimCut = " \t-—·:：;；\"'“”"
)

// GuessTitle 从媒体文件名猜出检索用的「片名 + 年份」。
//
// 刮削的第一步是把 `Interstellar.2014.1080p.BluRay.x264.mp4` 这类名字还原成
// 「Interstellar」+ 2014，规则按可靠性从高到低：
//
//  1. 括号组里出现网址/域名的一律丢弃（那是发布组署名）；其余括号组只留非标记词，
//     `让子弹飞(2010)` 的年份因此得以保留；
//  2. 独立的 4 位年份（1900-2099）当发行年份，取**最后一个**——片名本身常含数字
//     （`2001 A Space Odyssey 1968`），靠后的那个才是发行年份；
//  3. 命中 dropWords 的标记词丢弃，`BD-1080p` 这类分段标记整段丢弃；
//  4. 都不剩就退回原始主名（如 `1080p.BDRip`），关键词永远不为空——
//     否则用户点「刮削」只会得到一句「关键词为空」。
func GuessTitle(filename string) (title string, year int) {
	base := filename
	if i := strings.LastIndexByte(base, '.'); i > 0 {
		base = base[:i]
	}
	raw := base

	// 1) 括号组处理
	pre := bracketRe.ReplaceAllStringFunc(raw, func(m string) string {
		inner := strings.TrimSpace(m[1 : len(m)-1])
		if urlRe.MatchString(inner) {
			return " "
		}
		return " " + strings.Join(filterTokens(inner), " ") + " "
	})

	// 1.5) 短横年份当分隔符：`让子弹飞-2010`、`让子弹飞-2010-1080p` 里的短横不是片名的一部分，
	// 留着会让整个 `让子弹飞-2010` 变成一个词、年份也认不出来。只切短横后面紧跟 4 位数字的位置。
	pre = dashYearRe.ReplaceAllString(pre, " $1")

	// 2) 切词
	var fields []string
	for _, f := range strings.Fields(sepRe.ReplaceAllString(pre, " ")) {
		if f = strings.Trim(f, trimCut); f != "" {
			fields = append(fields, f)
		}
	}

	// 3) 定位年份（最后一个独立 4 位数），随后丢弃标记词
	yearIdx := -1
	for i, f := range fields {
		if y := asYear(f); y > 0 {
			yearIdx, year = i, y
		}
	}
	kept := make([]string, 0, len(fields))
	for i, f := range fields {
		if i == yearIdx || dropToken(f) {
			continue
		}
		kept = append(kept, f)
	}
	// 全被滤光的极端情况（名字本身就是一串标记）：退回原始主名
	if len(kept) == 0 {
		if yearIdx >= 0 {
			kept = append(kept, fields[yearIdx])
		} else if norm := filterTokens(strings.Join(fields, " ")); len(norm) > 0 {
			kept = norm
		} else {
			title = strings.Join(strings.Fields(sepRe.ReplaceAllString(raw, " ")), " ")
			return strings.TrimSpace(title), year
		}
	}
	return strings.Join(kept, " "), year
}

// filterTokens 切词并丢掉标记词，返回保留的词。
func filterTokens(s string) []string {
	var out []string
	for _, f := range strings.Fields(s) {
		if f = strings.Trim(f, trimCut); f != "" && !dropToken(f) {
			out = append(out, f)
		}
	}
	return out
}

// dropToken 是否该丢弃这个词：命中标记词表，或整词是若干标记词的连写（BD-1080p）。
func dropToken(tok string) bool {
	lower := strings.ToLower(tok)
	if dropWords[lower] {
		return true
	}
	if parts := strings.Split(lower, "-"); len(parts) > 1 {
		for _, p := range parts {
			if p != "" && !dropWords[p] {
				return false
			}
		}
		return true
	}
	return false
}

// asYear 独立的 4 位年份（1900-2099）才认，其余返回 0。
// 「2001太空漫游」这种数字与文字连体的不算年份，那是片名的一部分。
func asYear(tok string) int {
	if len(tok) != 4 {
		return 0
	}
	y, err := strconv.Atoi(tok)
	if err != nil || y < 1900 || y > 2099 {
		return 0
	}
	return y
}

// SplitKeyword 拆开用户手填的检索词「片名 + 年份」。
//
// 只认末尾的「空格+年份」或「短横+年份」（`无间道 2002`、`无间道-2002`）—— 前端输入框的
// 提示就是这么写的。要拆的原因是这两串直接丢给 TMDB 基本搜不到：年份得作为 year 参数下发，
// 而不是留在关键词里。夹在片名中间的数字（`2001太空漫游`）一律保持原样，那是片名的一部分。
func SplitKeyword(s string) (title string, year int) {
	s = strings.TrimSpace(s)
	m := tailYearRe.FindStringSubmatch(s)
	if m == nil {
		return s, 0
	}
	if title = strings.Trim(strings.TrimSpace(s[:len(s)-len(m[0])]), trimCut); title == "" {
		return s, 0
	}
	return title, asYear(m[1])
}

// rankCandidates 稳定重排候选，把「最可能是这一部」的排到最前（界面展示与默认填充都用它）：
//
//  1. 片名匹配度：完全相符 > 部分相符 > 其它（候选带译名与原名，两者任一相符都算）；
//  2. 同一档内，年份与查询相符的靠前 —— 年份来自文件名或用户填的「片名 年份」。
//
// 同级保持 TMDB 原有的相关度顺序（稳定排序），因此只在有更好的选择时才改变次序。
func rankCandidates(list []Candidate, query string, year int) {
	q := titleKey(query)
	if q == "" || len(list) < 2 {
		return
	}
	sort.SliceStable(list, func(i, j int) bool {
		if ti, tj := titleTier(list[i], q), titleTier(list[j], q); ti != tj {
			return ti < tj
		}
		if year > 0 {
			mi, mj := list[i].Year == year, list[j].Year == year
			if mi != mj {
				return mi
			}
		}
		return false
	})
}

// titleTier 片名匹配档位：0 完全相符，1 部分相符，2 其它（越小越靠前）。
func titleTier(c Candidate, q string) int {
	t, o := titleKey(c.Title), titleKey(c.OriginalTitle)
	if t == q || (o != "" && o == q) {
		return 0
	}
	if partlyMatches(t, q) || (o != "" && partlyMatches(o, q)) {
		return 1
	}
	return 2
}

// partlyMatches 双向包含即算部分相符。不足两字的片名不参与包含判断，
// 否则 `a` 这种短名会被任何关键词「包含」，把真正的结果挤到后面。
func partlyMatches(name, q string) bool {
	if len([]rune(name)) < 2 || len([]rune(q)) < 2 {
		return false
	}
	return strings.Contains(name, q) || strings.Contains(q, name)
}

// titleKey 归一化片名用于比较：转小写、把装饰性标点当空白、压缩空白。只用于比较，不改展示原文。
func titleKey(s string) string {
	s = keySepRe.ReplaceAllString(strings.ToLower(strings.TrimSpace(s)), " ")
	return strings.Join(strings.Fields(s), " ")
}
