package web

import (
	"strings"
)

// anyMethod 是 `e.Any(...)` 在路由表里的占位方法名：真实 HTTP 方法名不会长这样，
// 查不到具体方法时回落到它。
const anyMethod = "*ANY"

// route 一条已注册的路由：方法 + 注册时的完整路径（含组前缀）+ 处理器
// + 注册时挂在单条路由上的中间件。method/path 只为了 Routes() 能报出登记表。
type route struct {
	method  string
	path    string
	handler HandlerFunc
	mw      []MiddlewareFunc
}

// chain 把路由级中间件套到处理器上（全局中间件在 ServeHTTP 时再包一层，
// 顺序才是「全局在外、路由在内」—— 与 echo 一致，也让 BodyLimit 能包住鉴权）。
func (r *route) chain() HandlerFunc {
	h := r.handler
	for i := len(r.mw) - 1; i >= 0; i-- {
		h = r.mw[i](h)
	}
	return h
}

// node 路由树的一个节点。三种子边按优先级排：静态 > 参数(:name) > 通配(*name)。
//
// 为什么不用 echo 那种字节级 radix tree：这里的路由表是「目录层 + 参数 + 一个
// 兜底 /*」的形态，静态/参数/通配三段就够，且匹配可读、可回溯（通配优先级最低，
// 所以 /static/avatars/:key 与 /api/* 抢 /css/* 那种事不会发生）。
type node struct {
	static map[string]*node
	param  *node  // 段形如 :name
	wild   *node  // 段形如 *name（吃掉剩余全部路径）
	name   string // param/wild 捕获名
	routes map[string]*route
}

func newNode() *node { return &node{routes: map[string]*route{}} }

func (n *node) child(seg string) *node {
	if n.static == nil {
		n.static = map[string]*node{}
	}
	c, ok := n.static[seg]
	if !ok {
		c = newNode()
		n.static[seg] = c
	}
	return c
}

// split 把路径切成匹配用的段：
//   - 首部与中间的连续斜杠忽略（/api//ping 视作 /api/ping），少一处无谓的 404，
//     也没有客户端会发这种路径。
//   - 尾斜杠保留语义：追加一个空段作标记，让 /admin 与 /admin/ 是两条路由。
//     echo 默认不合并尾斜杠，web.Register 就靠「/admin 重定向 / /admin/ 出页面」。
//     通配 *path 会吃掉包括这个空尾段在内的全部剩余段，值仍用 TrimSuffix 去掉尾斜杠。
//   - 纯根路径 "/"（及 "//"）视作空切片，交给根节点静态路由与兜底 /*。
func split(path string) []string {
	core := strings.Trim(path, "/")
	if core == "" {
		return nil
	}
	segs := strings.Split(core, "/")
	out := segs[:0:0]
	for _, s := range segs {
		if s != "" {
			out = append(out, s)
		}
	}
	if strings.HasSuffix(path, "/") {
		out = append(out, "") // 尾斜杠标记段
	}
	return out
}

// insert 注册一条路由进树。同 method 同路径重复注册以最后一次为准（不报错：
// 本仓库的路由表是手写清单，重复是笔误而非需要中断启动的错误）。
// 注：树里只按段存引用，登记表清单在 Echo.all 上（见 web.go）。
func (root *node) insert(method, path string, r *route) {
	n := root
	for _, seg := range split(path) {
		switch {
		case seg == "": // 尾斜杠标记段：当普通静态子节点，与不带斜杠的路径分开
			n = n.child(seg)
		case seg[0] == ':': // 参数段：:key
			if n.param == nil {
				n.param = newNode()
			}
			n.param.name = seg[1:]
			n = n.param
		case seg[0] == '*': // 通配段：*path（含裸 /*）
			if n.wild == nil {
				n.wild = newNode()
			}
			if name := seg[1:]; name != "" {
				n.wild.name = name
			} else {
				n.wild.name = "*"
			}
			n = n.wild
		default:
			n = n.child(seg)
		}
	}
	if n.routes == nil {
		n.routes = map[string]*route{}
	}
	n.routes[method] = r
}

// Param 一段捕获：名字 + 值。路由上的参数个数是个位数，
// 用切片线性扫比每请求建 map 省。
type Param struct {
	Name  string
	Value string
}

// find 按「静态 > 参数 > 通配」的深度优先匹配，命中即返回（回溯只在当前层
// 三种边之间发生，顺序保证高优先级先赢）。
func find(n *node, segs []string, method string, params []Param) (*route, []Param) {
	if len(segs) == 0 {
		if r := lookup(n, method); r != nil {
			return r, params
		}
		// 段用完了但当前节点挂着通配子节点：/d/ 与 / 都算命中 /d/*path 与 /*，
		// 捕获值为空串（处理器的必填校验会回 400，不靠路由报错）。
		if n.wild != nil {
			if r := lookup(n.wild, method); r != nil {
				return r, append(params, Param{Name: n.wild.name, Value: ""})
			}
		}
		return nil, params
	}
	seg := segs[0]

	// 通配优先级最低，所以先递归静态与参数两条边，当前层都没命中才轮到它。
	if c, ok := n.static[seg]; ok {
		if r, p := find(c, segs[1:], method, params); r != nil {
			return r, p
		}
	}
	if n.param != nil {
		p := append(params, Param{Name: n.param.name, Value: seg})
		if r, pp := find(n.param, segs[1:], method, p); r != nil {
			return r, pp
		}
	}
	if n.wild != nil {
		// 通配能吃掉当前段及其后所有段：先试完更具体的两条边才轮到它。
		if r := lookup(n.wild, method); r != nil {
			// 尾斜杠不写进值里：/d/a/b/ 取到的还是 a/b（播放器给的路径带不带斜杠都一样）
			v := strings.TrimSuffix(strings.Join(segs, "/"), "/")
			return r, append(params, Param{Name: n.wild.name, Value: v})
		}
	}
	return nil, params
}

func lookup(n *node, method string) *route {
	if n == nil || n.routes == nil {
		return nil
	}
	if r, ok := n.routes[method]; ok {
		return r
	}
	return n.routes[anyMethod]
}
