// Package mediaindex 媒体文件的目录级索引与 sha1 元数据，作为「只做基础设施」的
// 一部分：本包只负责生成并维护 `.index.jsonl`，以及按 sha1 拼出海报/简介的存放路径；
// 海报图与简介正文由外部工具生成，写入后重新扫描即把 is_new 翻成 0。
package mediaindex

import (
	"bufio"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"io"
	"path/filepath"
	"strings"

	"github.com/pkg/errors"
)

// IndexFileName 每个目录一个的索引文件名。点开头，浏览列表天然隐去。
const IndexFileName = ".index.jsonl"

// MediaExts 当前纳入索引的媒体扩展名（小写，不区分大小写匹配）。
var MediaExts = []string{".mp4", ".mp3", ".png", ".jpg", ".jpeg"}

// Record 索引的一行：一个媒体文件。字段名与用户约定一致。
type Record struct {
	Name     string `json:"name"`     // 文件名
	SizeKB   int64  `json:"size_kb"`  // 大小，单位 KB
	Modified string `json:"modified"` // 最后修改时间，RFC3339(UTC)
	SHA1     string `json:"sha1"`     // 文件内容 sha1，40 个十六进制
	IsNew    int    `json:"is_new"`   // 1=待生成附加信息；0=已就绪
}

// isMediaName 是否是需要索引的媒体文件。
func isMediaName(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	for _, want := range MediaExts {
		if ext == want {
			return true
		}
	}
	return false
}

// IsSHA1 是否合法的 sha1（40 个十六进制字符）。大小写均可。
func IsSHA1(s string) bool {
	if len(s) != 40 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9') && !(c >= 'a' && c <= 'f') && !(c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}

// PosterRel 海报图的相对路径（相对存储根）。
// sha1 取首 2 字符作一级目录、次 2 字符作二级目录、其余作文件名。
// 例：sha1=abcd123...98 → .mocca/ab/cd/123...98.png
func PosterRel(sha1hex string) (string, error) {
	return metaRel(sha1hex, ".png")
}

// SummaryRel 附加信息 JSON 的相对路径（相对存储根）。
// 例：sha1=abcd123...98 → .mocca/ab/cd/123...98.json
func SummaryRel(sha1hex string) (string, error) {
	return metaRel(sha1hex, ".json")
}

func metaRel(sha1hex, ext string) (string, error) {
	sha1hex = strings.ToLower(strings.TrimSpace(sha1hex))
	if !IsSHA1(sha1hex) {
		return "", errors.Errorf("非法 sha1: %q", sha1hex)
	}
	return filepath.Join(".mocca", sha1hex[0:2], sha1hex[2:4], sha1hex[4:]+ext), nil
}

// jsonMarshal 压缩序列化一行，包一层错误上下文。
func jsonMarshal(rec Record) ([]byte, error) {
	b, err := json.Marshal(rec)
	if err != nil {
		return nil, errors.Wrapf(err, "序列化索引行 %q 失败", rec.Name)
	}
	return b, nil
}

// hashContent 对流求 sha1，返回小写十六进制。
func hashContent(r io.Reader) (string, error) {
	h := sha1.New()
	if _, err := io.Copy(h, r); err != nil {
		return "", errors.WithStack(err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// ReadIndex 读一份 .index.jsonl 流，按文件名归成 map；文件不存在/为空就当空。
// 供 handlers 列目录时顺带取 sha1 使用。
func ReadIndex(r io.Reader) (map[string]Record, error) {
	return readIndex(r)
}

// readIndex 读一份 .index.jsonl，按文件名归成 map，供增量用；文件不存在就当空。
func readIndex(r io.Reader) (map[string]Record, error) {
	out := make(map[string]Record)
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var rec Record
		if json.Unmarshal([]byte(line), &rec) == nil && rec.Name != "" {
			out[rec.Name] = rec
		}
	}
	if err := sc.Err(); err != nil {
		return nil, errors.WithStack(err)
	}
	return out, nil
}
