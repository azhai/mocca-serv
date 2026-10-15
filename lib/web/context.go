package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
)

// defaultMaxMemory 解析 multipart 时留在内存里的部分，超出落临时文件。
// 与 net/http 的默认值一致（32MB）；上传走流式拷贝，不靠它兜住整个文件。
const defaultMaxMemory int64 = 32 << 20

// Context 一次请求的上下文：请求/响应 + 路径参数 + 一个中间件与处理器之间的便签。
type Context struct {
	e      *Echo
	req    *http.Request
	res    *response
	params []Param
	store  map[string]any
	form   url.Values // FormValue/Bind 共用的一次性解析结果
}

// response 包一层 http.ResponseWriter，只为记「状态码是否已发出」：
// 处理器返回 error 时若已经写过（流媒体写到一半、SSE 已发头），就不能再改状态码。
// 内嵌原接口，所以 Header/Write/WriteHeader 的用法与裸 ResponseWriter 完全一致，
// MaxBytesReader、ServeContent 这些直接收得到。
type response struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (r *response) WriteHeader(code int) {
	if r.wrote {
		return // 已经发过状态码，重复调用只会被 net/http 警告，这里安静忽略
	}
	r.status = code
	r.wrote = true
	r.ResponseWriter.WriteHeader(code)
}

func (r *response) Write(b []byte) (int, error) {
	if !r.wrote {
		r.status = http.StatusOK
		r.wrote = true
	}
	return r.ResponseWriter.Write(b)
}

// Flush 让本包装对 http.Flusher 断言永远成立：内层支持就转下去，
// 不支持（或被某个中间件包成了不能刷新的 writer）就当空操作。
func (r *response) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Request 原始请求。
func (c *Context) Request() *http.Request { return c.req }

// Response 响应写入器（已包一层状态记录）。
func (c *Context) Response() http.ResponseWriter { return c.res }

// Status 已写出的状态码，没写过时为 0。
func (c *Context) Status() int { return c.res.status }

// Param 取路径里捕获的 :name 或 *name。没有这一段时回空串。
func (c *Context) Param(name string) string {
	for _, p := range c.params {
		if p.Name == name {
			return p.Value
		}
	}
	return ""
}

// PathParams 全部捕获（调试与日志用）。
func (c *Context) PathParams() []Param { return c.params }

// QueryParam 查询串里的值，没带时回空串。
func (c *Context) QueryParam(name string) string {
	if c.req.URL == nil {
		return ""
	}
	return c.req.URL.Query().Get(name)
}

// QueryValues 整个查询串（同名多值要用它）。
func (c *Context) QueryValues() url.Values {
	if c.req.URL == nil {
		return url.Values{}
	}
	return c.req.URL.Query()
}

// FormValue 表单值：urlencoded 与 multipart 都认，取不到时回落到查询串
// （与 net/http 的 Request.FormValue 同款语义，客户端怎么写表单都不用区分）。
func (c *Context) FormValue(name string) string { return c.formValues().Get(name) }

// formValues 解析一次表单并缓存。ParseForm 只吃 urlencoded；
// multipart 得走 ParseMultipartForm，这里都覆盖，失败就当没有。
func (c *Context) formValues() url.Values {
	if c.form != nil {
		return c.form
	}
	v := url.Values{}
	if isMultipart(c.req) {
		if mf, err := c.multipartForm(); err == nil && mf != nil {
			for k, s := range mf.Value {
				v[k] = s
			}
		}
	} else if err := c.req.ParseForm(); err == nil {
		for k, s := range c.req.PostForm {
			v[k] = s
		}
	}
	for k, s := range c.QueryValues() {
		if _, ok := v[k]; !ok {
			v[k] = s
		}
	}
	c.form = v
	return v
}

func isMultipart(req *http.Request) bool {
	return strings.HasPrefix(req.Header.Get("Content-Type"), "multipart/form-data")
}

// MultipartForm 解析并返回整个 multipart 表单（文件在 FileHeader 里）。
func (c *Context) MultipartForm() (*multipart.Form, error) {
	return c.multipartForm()
}

func (c *Context) multipartForm() (*multipart.Form, error) {
	if !isMultipart(c.req) {
		return nil, ErrNotMultipart
	}
	if err := c.req.ParseMultipartForm(defaultMaxMemory); err != nil {
		return nil, err
	}
	// MultipartForm 是字段不是方法：ParseMultipartForm 之后它才非 nil。
	return c.req.MultipartForm, nil
}

// ErrNotMultipart 请求不是 multipart 却调用了 MultipartForm。
var ErrNotMultipart = errors.New("请求不是 multipart/form-data")

// Set 在上下文里存一个值（中间件往下传东西用，本仓库只用来传 user_id）。
func (c *Context) Set(key string, v any) {
	if c.store == nil {
		c.store = map[string]any{}
	}
	c.store[key] = v
}

// Get 取 Set 存的值，没有时回 nil（调用方自己类型断言，取错类型得到零值）。
func (c *Context) Get(key string) any {
	if c.store == nil {
		return nil
	}
	return c.store[key]
}

// JSON 按 code 写一个 JSON 响应。Content-Type 已由处理器设过时不覆盖
// （后台的头像与海报接口就是这么自己指定 image/* 的）。
func (c *Context) JSON(code int, v any) error {
	h := c.res.Header()
	if h.Get("Content-Type") == "" {
		h.Set("Content-Type", "application/json; charset=UTF-8")
	}
	c.res.WriteHeader(code)
	return json.NewEncoder(c.res).Encode(v)
}

// String 按 code 写一段文本（format 有额外参数时才走 fmt，否则原样发）。
func (c *Context) String(code int, format string, args ...any) error {
	body := format
	if len(args) > 0 {
		body = fmt.Sprintf(format, args...)
	}
	h := c.res.Header()
	if h.Get("Content-Type") == "" {
		h.Set("Content-Type", "text/plain; charset=UTF-8")
	}
	c.res.WriteHeader(code)
	_, err := c.res.Write([]byte(body))
	return err
}

// Redirect 写一个重定向：Location 头 + 空体。
func (c *Context) Redirect(code int, location string) error {
	if code == 0 {
		code = http.StatusFound
	}
	c.res.Header().Set("Location", location)
	c.res.WriteHeader(code)
	return nil
}

// NoContent 只发状态码、不写体。
func (c *Context) NoContent(code int) error {
	c.res.WriteHeader(code)
	return nil
}

// bindContentJSON 请求体是否是 JSON（含 text/json 与带参数的写法）。
func bindContentJSON(req *http.Request) bool {
	mt, _, err := mime.ParseMediaType(req.Header.Get("Content-Type"))
	if err != nil {
		return strings.Contains(req.Header.Get("Content-Type"), "json")
	}
	return mt == "application/json" || mt == "text/json" || strings.HasSuffix(mt, "+json")
}
