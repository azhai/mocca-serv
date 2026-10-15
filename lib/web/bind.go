package web

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// Bind 把请求数据填进 i（须是指向 struct 的指针），字段名按 `json` tag 对齐。
//
// 三条来源按优先级：
//  1. Content-Type 是 JSON（含 +json）→ 直接解请求体；
//  2. 表单（urlencoded / multipart）→ 取表单字段，同名查询串作缺省；
//  3. 其余（GET/DELETE 或空体）→ 取查询串。
//
// 第 2、3 条走的是「先把值拼成 JSON 再解」这条路，两遍尝试：
// 先按看起来像 JSON 的原样发（`?id=5` 能进 int 字段），失败再全部当字符串
// （`?name=123` 该进 string 字段）。这样不必反射扫结构体，也不用给 binder 加
// 一套 `bind:""` 私有 tag —— 本仓库所有 c.Bind 都在 POST 上收 JSON body，
// 表单与查询串只是兜底。
func (c *Context) Bind(i any) error {
	req := c.req
	if bindContentJSON(req) {
		if req.Body == nil {
			return nil
		}
		// 空体不算错：结构体留在零值，由处理器自己的必填校验去回 400。
		if err := json.NewDecoder(req.Body).Decode(i); err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		return nil
	}

	var vals url.Values
	switch {
	case isMultipart(req), req.PostForm != nil && len(req.PostForm) > 0,
		req.Method == http.MethodPost && isURLEncoded(req):
		vals = c.formValues()
	default:
		vals = c.QueryValues()
	}
	if len(vals) == 0 {
		return nil
	}
	if err := bindValues(vals, i, true); err != nil {
		return bindValues(vals, i, false) // 第二遍：全部当字符串
	}
	return nil
}

func isURLEncoded(req *http.Request) bool {
	return strings.HasPrefix(req.Header.Get("Content-Type"), "application/x-www-form-urlencoded")
}

// bindValues 把 url.Values 编成 JSON 对象再解进 i。rawJSON 为真时，
// 长得像 JSON 的值（数字、布尔、对象、数组）原样嵌入，其余加引号当字符串。
func bindValues(vals url.Values, i any, rawJSON bool) error {
	m := make(map[string]json.RawMessage, len(vals))
	for k, s := range vals {
		if len(s) == 0 {
			continue
		}
		if len(s) == 1 {
			if rawJSON && json.Valid([]byte(s[0])) {
				m[k] = json.RawMessage(s[0])
				continue
			}
			if b, err := json.Marshal(s[0]); err == nil {
				m[k] = b
				continue
			}
			return errors.New("字段无法编码: " + k)
		}
		// 同名多值 → JSON 数组（对应 struct 里的 []string 字段）
		if b, err := json.Marshal(s); err == nil {
			m[k] = b
		} else {
			return errors.New("字段无法编码: " + k)
		}
	}
	body, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return json.Unmarshal(body, i)
}
