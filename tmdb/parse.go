package tmdb

import (
	"regexp"
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
