// Package web 是本仓库自用的最小 HTTP 框架：路由 + 中间件 + 响应封装。
//
// 为什么不用 echo：整个后台只用到 5 个类型、12 个 Context 方法（实测），
// 而框架层要的性质很窄 —— 「HTTP 恒 200、成败看 body.code」是业务约定，
// 绑定只用 JSON body，鉴权是自己写的 JWT（见 helpers/jwt.go）。
// 自带这层换来的是：没有第三方 HTTP 依赖、行为可测，`-tags noweb` 的剥离
// 与 http.Server 的超时/优雅关闭都握在自己手里（见 main.go）。
//
// 形状与 echo v5 有意保持一致（New/Use/Group/GET/POST/Any + Context 同名方法），
// 所以调用点只是换个包名；唯一刻意不同的是错误出口：处理器返回 error 交给
// Echo.ErrorHandler，不回 echo 那种 *echo.HTTPError —— 本仓库一处都没用过它。
package web

import (
	"net/http"
)

// HandlerFunc 一个请求处理器。返回非 nil 的 error 交给 Echo.ErrorHandler。
type HandlerFunc func(*Context) error

// MiddlewareFunc 洋葱式中间件：包住下一层处理器，并返回它的结果。
type MiddlewareFunc func(HandlerFunc) HandlerFunc

// Echo 路由表 + 全局中间件。实现了 http.Handler，可以直接交给 http.Server。
type Echo struct {
	root   *node
	all    []*route // 注册顺序的清单，供 Router().Routes() 断言用
	mws    []MiddlewareFunc
	bottom *Group // 实例层注册等价于在这个空前缀组上注册

	// NotFound 未命中任何路由时走它。默认回 404。
	NotFound HandlerFunc
	// ErrorHandler 处理器返回 error 时走它。默认：没写过响应就回 500。
	// 走到这里说明是 bug —— 业务失败一律该用 helpers.Fail 回信封。
	ErrorHandler func(*Context, error)
}

// New 一个空实例，NotFound 与 ErrorHandler 用默认值。
func New() *Echo {
	e := &Echo{}
	e.bottom = &Group{e: e}
	e.NotFound = defaultNotFound
	e.ErrorHandler = defaultErrorHandler
	e.root = newNode()
	return e
}

func defaultNotFound(c *Context) error {
	http.NotFound(c.Response(), c.Request())
	return nil
}

// defaultErrorHandler 只在响应还没开始写时补一个 500：
// 流媒体写到一半、SSE 已发过响应头的情况下，再写任何东西都是脏数据。
func defaultErrorHandler(c *Context, err error) {
	if c.res.wrote {
		return
	}
	// 错误细节不外泄（响应体可能被播放器当字节流解析），状态码足够定位问题。
	_ = err
	c.res.WriteHeader(http.StatusInternalServerError)
}

// Use 追加全局中间件。顺序即洋葱层次：先注册的在最外面，
// 所以 panic 兜底必须第一个 Use（见 main.go）。
func (e *Echo) Use(mw ...MiddlewareFunc) { e.mws = append(e.mws, mw...) }

// register 落一条路由：拼上组前缀，记进路由树与清单。
func (e *Echo) register(method, path string, h HandlerFunc, mw []MiddlewareFunc) {
	r := &route{method: method, path: path, handler: h, mw: mw}
	e.root.insert(method, path, r)
	e.all = append(e.all, r)
}

// Router 路由登记表（只为拿 Routes()，注册请直接走 Group/Echo 的方法）。
func (e *Echo) Router() *Router { return &Router{routes: e.all} }

// Router 已注册路由的只读视图。测试用它断言「这条接口到底注册没」，
// 比发请求看状态码更直接：不依赖数据库，也不被中间件的鉴权响应混淆。
type Router struct{ routes []*route }

// RouteInfo 一条路由：方法（Any 记作 "*"）与注册时的完整路径（含组前缀）。
type RouteInfo struct {
	Method string
	Path   string
}

// Routes 按注册顺序返回路由清单。
func (rt *Router) Routes() []RouteInfo {
	out := make([]RouteInfo, 0, len(rt.routes))
	for _, r := range rt.routes {
		m := r.method
		if m == anyMethod {
			m = "*"
		}
		out = append(out, RouteInfo{Method: m, Path: r.path})
	}
	return out
}

// Group 一个带前缀与自带中间件的注册组。前缀多一个少一个斜杠都不影响命中
// （路由表按段切分，见 router.go 的 split）。
func (e *Echo) Group(prefix string, mw ...MiddlewareFunc) *Group {
	return &Group{e: e, prefix: prefix, mws: append([]MiddlewareFunc{}, mw...)}
}

// Group 带前缀与自带中间件的注册组。
type Group struct {
	e      *Echo
	prefix string
	mws    []MiddlewareFunc
}

// Group 在组上再开子组：中间件继承父组，前缀拼接。
func (g *Group) Group(prefix string, mw ...MiddlewareFunc) *Group {
	all := append(append([]MiddlewareFunc{}, g.mws...), mw...)
	return &Group{e: g.e, prefix: g.prefix + prefix, mws: all}
}

// Use 只给这个组追加中间件（不影响全局）。
func (g *Group) Use(mw ...MiddlewareFunc) { g.mws = append(g.mws, mw...) }

// register 组级注册：拼前缀，把「组级中间件 + 这一条的中间件」接成一条链。
// 顺序是组级在外 —— 与 echo 一致，所以给组挂的鉴权能包住每条路由。
func (g *Group) register(method, path string, h HandlerFunc, mw ...MiddlewareFunc) {
	all := append(append([]MiddlewareFunc{}, g.mws...), mw...)
	g.e.register(method, g.prefix+path, h, all)
}

func (g *Group) Route(method, path string, h HandlerFunc, mw ...MiddlewareFunc) {
	g.register(method, path, h, mw...)
}

// 各方法的注册入口。变参 mw 是只作用于这一条路由的中间件，跑在组级中间件里面。
func (g *Group) GET(path string, h HandlerFunc, mw ...MiddlewareFunc) {
	g.register(http.MethodGet, path, h, mw...)
}
func (g *Group) POST(path string, h HandlerFunc, mw ...MiddlewareFunc) {
	g.register(http.MethodPost, path, h, mw...)
}
func (g *Group) PUT(path string, h HandlerFunc, mw ...MiddlewareFunc) {
	g.register(http.MethodPut, path, h, mw...)
}
func (g *Group) PATCH(path string, h HandlerFunc, mw ...MiddlewareFunc) {
	g.register(http.MethodPatch, path, h, mw...)
}
func (g *Group) DELETE(path string, h HandlerFunc, mw ...MiddlewareFunc) {
	g.register(http.MethodDelete, path, h, mw...)
}
func (g *Group) HEAD(path string, h HandlerFunc, mw ...MiddlewareFunc) {
	g.register(http.MethodHead, path, h, mw...)
}
func (g *Group) OPTIONS(path string, h HandlerFunc, mw ...MiddlewareFunc) {
	g.register(http.MethodOptions, path, h, mw...)
}

// Any 对该路径的所有方法生效（路由表里记成 anyMethod，见 router.go）。
func (g *Group) Any(path string, h HandlerFunc, mw ...MiddlewareFunc) {
	g.register(anyMethod, path, h, mw...)
}

// 实例层注册：转发到空前缀的根组，路径原样进表。
func (e *Echo) GET(path string, h HandlerFunc, mw ...MiddlewareFunc) {
	e.bottom.GET(path, h, mw...)
}
func (e *Echo) POST(path string, h HandlerFunc, mw ...MiddlewareFunc) {
	e.bottom.POST(path, h, mw...)
}
func (e *Echo) PUT(path string, h HandlerFunc, mw ...MiddlewareFunc) {
	e.bottom.PUT(path, h, mw...)
}
func (e *Echo) PATCH(path string, h HandlerFunc, mw ...MiddlewareFunc) {
	e.bottom.PATCH(path, h, mw...)
}
func (e *Echo) DELETE(path string, h HandlerFunc, mw ...MiddlewareFunc) {
	e.bottom.DELETE(path, h, mw...)
}
func (e *Echo) HEAD(path string, h HandlerFunc, mw ...MiddlewareFunc) {
	e.bottom.HEAD(path, h, mw...)
}
func (e *Echo) OPTIONS(path string, h HandlerFunc, mw ...MiddlewareFunc) {
	e.bottom.OPTIONS(path, h, mw...)
}
func (e *Echo) Any(path string, h HandlerFunc, mw ...MiddlewareFunc) {
	e.bottom.Any(path, h, mw...)
}

// ServeHTTP 实现 http.Handler。
//
// 每请求做一次中间件套叠（全局层数固定是个位数），不做对象池：
// 本仓库的瓶颈在磁盘与网络 IO，不在这里。
func (e *Echo) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rt, params := find(e.root, split(r.URL.Path), r.Method, nil)

	h := e.NotFound
	if rt != nil {
		h = rt.chain()
	}
	for i := len(e.mws) - 1; i >= 0; i-- {
		h = e.mws[i](h)
	}

	c := &Context{e: e, req: r, res: &response{ResponseWriter: w}, params: params}
	if err := h(c); err != nil && e.ErrorHandler != nil {
		e.ErrorHandler(c, err)
	}
}
