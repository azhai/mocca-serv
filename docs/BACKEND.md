# Mocca 后端服务架构

面向「NAS 点播」场景的服务：提供 REST API 与支持 Range 的流媒体传输。管理后台是**可选能力**——
完整版构建里带，纯 API 版构建里被整体剥离（见 [§5](#5-两种构建完整版与纯-api-版)），
两种构建共用同一份源码与同一套 API。

本文与代码同步：包名、函数名、配置项都来自当前仓库；早期版本里那些「规划中的设计」
（`internal/fs`、`internal/net`、混合缓存、限速中间件、上游代理取流等）在本服务中并不存在，已删除。

- [1. 设计目标与边界](#1-设计目标与边界)
- [2. 分层架构](#2-分层架构)
- [3. 流媒体实现](#3-流媒体实现)
- [4. 认证与安全](#4-认证与安全)
- [5. 两种构建：完整版与纯 API 版](#5-两种构建完整版与纯-api-版)
- [6. 部署](#6-部署)
- [7. 并发与扩展](#7-并发与扩展)

---

## 1. 设计目标与边界

### 目标

| 目标 | 落地方式 |
| --- | --- |
| API 不依赖界面 | 管理后台只是 API 的消费者，可编译期整体剥离（build tag `noweb`，见 §5） |
| 大文件流式播放 | 标准库 `http.ServeContent`：Range、多段、条件请求、`HEAD` 全部可用（见 §3） |
| 多存储后端统一 | 驱动抽象层（`drivers.Driver`），同一套 API 覆盖本地盘与 SMB |
| 单二进制部署 | 无 CGO（`modernc.org/sqlite` 纯 Go）、后台资源走 `go:embed`，运行时不依赖任何外部目录 |
| 口令可自救 | 首启自动播种管理员；口令写坏时用 `passwd` 子命令重置 |
| 库结构自动演进 | `goent` 的 `AutoMigrate` + 热点索引（`models/tables.go`） |

### 明确的非目标

- **不做转码**：不引入 ffmpeg，不改变媒体编码。多码率靠旁路 HLS/DASH 清单，服务端只发字节。
- **不做切片封装**：不生成 `.m3u8` / `.mpd`，只按普通文件发。
- **不做内容缓存层**：不缓存文件数据，每个 Range 现读存储（理由见 §7.3）。
- **不做签名直链**：没有 `sign=` / HMAC / 链接有效期；访问控制只有 JWT、目录密码与游客开关。
- **不做多租户**：只有管理员 / 普通用户 / 游客三种角色，用户靠 `base_path` 隔离目录。

---

## 2. 分层架构

真实的包结构（不是规划图）：

```
                        ┌──────────────────────────────────────┐
   HTTP 请求  ────────► │  routes/routes.go（echo v5 路由装配） │
                        └───────────────┬──────────────────────┘
                                        │  分组即鉴权：admin > authed > optional > 公开
        ┌───────────────────────────────┼───────────────────────────────┐
        ▼                               ▼                               ▼
┌─────────────────┐           ┌──────────────────┐          ┌─────────────────────┐
│ middlewares/    │           │ handlers/        │          │ web/（仅完整版）     │
│ BodyLimit       │           │ 请求绑定与校验    │          │ /admin/ 静态后台     │
│ RequestLogger   │           │ 调用 models/     │          │ go:embed all:public │
│ AuthMiddleware  │           │ drivers，封装信封 │          └─────────────────────┘
│ AdminMiddleware │           └────────┬─────────┘
│ OptionalAuth    │                    │
│ StreamAuth      │                    ▼
└─────────────────┘           ┌──────────────────┐        ┌──────────────────────┐
                              │ models/          │        │ helpers/             │
                              │ goent + SQLite   │        │ 信封 OK/Fail         │
                              │ 表与业务规则      │        │ JWT 签发/解析        │
                              └────────┬─────────┘        └──────────────────────┘
                                       ▼
                              ┌──────────────────┐
                              │ drivers/         │  List / Stat / Open / Create /
                              │ local · smb      │  MkdirAll / Remove / Rename / Move
                              └──────────────────┘
```

### 各层职责

| 层 | 目录 | 职责 |
| --- | --- | --- |
| 入口 | `main.go` | 载入配置 → 建隐藏目录 → 开库 → 播种管理员 → 装配路由 → 优雅关闭；`passwd` 子命令也在这里 |
| 路由 | `routes/routes.go` | 端点注册与分组（分组即鉴权策略），两种构建共用 |
| 中间件 | `middlewares/` | 请求体上限、访问日志、登录/管理员校验、取流鉴权 |
| 处理器 | `handlers/` | 绑定与校验请求、调用 models 与 drivers、用 `helpers.OK/Fail` 封装信封 |
| 领域模型 | `models/` | 表结构与业务规则：用户、存储、设置、收藏、元数据、作者、评论 |
| 驱动 | `drivers/` | 具体存储读写；新增一种存储 = 实现 `Driver` 接口，handler 不用改 |
| 工具 | `helpers/` | 统一响应信封（`OK` / `Fail` / `FailStatus`）与 JWT 签发解析 |
| 后台 | `web/` | 管理后台静态资源（唯一可选能力，可编译期剥离） |

### 一次请求的路径（以 `POST /api/fs/list` 为例）

```
BodyLimit(1GB) → RequestLogger → OptionalAuth(解析令牌，写 user_id)
   → handlers.FsList
        ├─ scopePath        普通用户收敛到 base_path，越界直接拒
        ├─ requireFolderPassword   目录密码（bcrypt 比对），不通过就不碰存储
        ├─ resolveStorage   按挂载点最长匹配选出存储 + 存储内相对路径
        ├─ drivers.Open     建驱动实例（本地或 SMB 连接池）
        ├─ drv.List         取条目；点开头与系统保留名在这里被过滤
        └─ helpers.OK       { "content": [...], "total": N }
```

请求体上限、日志、鉴权都在中间件链上；业务错误一律 HTTP 200 + `code`，
唯一例外是取流端点（见 §3.5）。

---

## 3. 流媒体实现

### 3.1 取流路径

```
GET /d/<路径>?token=<JWT>
   │
   ├─ StreamAuth     Authorization 头 → ?token= → allow_guest 开关
   │                 失败用 FailStatus 回**真实 HTTP 状态码**
   ├─ scopePath      普通用户收敛到 base_path；越界拒绝
   ├─ requireFolderPassword  目录密码从 query 取（播放器带不了 JSON body）
   ├─ openStorage    最长匹配挂载点 → drivers.Open
   ├─ drv.Open(rel)  返回 io.ReadSeeker（本地是 *os.File，SMB 是共享上的随机读句柄）
   └─ http.ServeContent(Response, Request, basename, modtime, seeker)
```

### 3.2 驱动接口

```go
type Driver interface {
	List(rel string) ([]Entry, error)
	Stat(rel string) (Entry, error)
	Open(rel string) (io.ReadSeeker, int64, error) // 必须可 Seek：Range 依赖它
	MkdirAll(rel string) error
	Create(rel string) (io.WriteCloser, error)
	Remove(rel string) error   // 目录递归删
	Rename(rel, newName string) error
	Move(rel, dstDirRel string) error
	Close() error
}
```

- 新增存储 = 实现这个接口 + 在 `drivers.Open` 里注册，handler 一行都不用改；
- **`Open` 必须返回可随机定位的流**：Range 需要知道文件大小并能任意定位；
- SMB 实例来自连接池（`drivers/smbpool.go`），`Close` 是**归还引用**而不是断开会话；
  进程退出时 `main.go` 调 `drivers.CloseAllSMB()` 收尾。

### 3.3 为什么用 `http.ServeContent`

Range 的全部语义都白送，且是被广泛验证过的实现：

| 行为 | 效果 |
| --- | --- |
| 单段 `Range` | `206` + `Content-Range` |
| 多段 `Range` | `206` + `multipart/byteranges` |
| 条件请求 | `If-Match` / `If-None-Match` / `If-Modified-Since` / `If-Unmodified-Since` / `If-Range` → `304` / `412` |
| 越界 | `416` + `Content-Range: bytes */<size>` |
| 空文件 | 忽略 `Range` 回 `200` |
| `HEAD` | 只写头 |
| `Content-Type` | 按扩展名推断 |

代价与取舍：

- **每个请求重新 `Open`**：不缓存文件内容，一次播放里的每个 seek 都会重新打开存储对象；
- **没有断点续传的服务端状态**：续传完全由客户端的 `Range` 表达，服务端无状态；
- 播放逻辑与下载逻辑是同一条路径（同一个端点），没有区别对待。

### 3.4 挂载点解析

`resolveStorage` 的规则（`handlers/fs.go`）：

1. 跳过 `disabled` 的挂载点；
2. 取**最长前缀匹配**的挂载点（`/media/2026` 优先于 `/media`）；
3. 没有匹配时退回到「挂载点路径最长的那个」，这样单挂载点部署下任意路径都能落到它上面；
4. 一个挂载点都没有 → `code: 404`、`尚未配置存储，请先添加挂载点`。

移动（`/api/fs/move`）要求来源与目标解析到**同一个存储**：跨存储没有廉价实现，
宁可直接拒绝，也不做「看起来成功其实是慢速拷贝」。

### 3.5 取流失败用真实状态码

`helpers.Fail` 恒回 HTTP 200 + `code`；取流走 `helpers.FailStatus`，HTTP 状态码与 `code` 一致。

原因：取流响应体要被播放器当成字节流解析。失败若回 `200 OK` + JSON，
播放器会把那段 JSON 当媒体数据去解码，最终报「格式不支持」之类与真实原因无关的错，
排查时会被彻底带偏（401 / 403 / 404 是有意义的信号：重试、换令牌、还是提示目录密码）。

### 3.6 长连接的服务器超时

```go
srv := &http.Server{
	ReadTimeout:  30 * time.Second,
	WriteTimeout: 0,                 // 关键：流媒体是长连接，设了会把正在播放的视频掐断
	IdleTimeout:  120 * time.Second,
}
```

`echo` v5 的 `Echo` 只提供 `Start`（没有 `Shutdown`），所以 `main.go` 自己包了一层
`http.Server`：超时可控，且收到 `SIGINT` / `SIGTERM` 时能优雅关闭。

---

## 4. 认证与安全

### 4.1 口令：两级不可逆哈希

```
客户端：  静态哈希 = sha256(明文 + "-" + "https://github.com/alist-org/alist")
服务端：  bcrypt(静态哈希)  → 落库（models/user.go 的 SetPassword）
```

- 链路上没有明文（日志、抓包、反代日志都只见到哈希）；
- 库里躺着的是 `bcrypt(静态哈希)`，两层都不可逆；
- 服务端**拒绝**非静态哈希的输入（必须是 64 位小写十六进制），
  否则会存成 `bcrypt(明文)` 而该账号**永远登不进去且不报错**；
- 客户端（含内嵌后台）必须先算哈希：浏览器在 `http://192.168.x.x` 这类非安全上下文下
  `crypto.subtle` 不可用，后台内置了纯 JS SHA-256 兜底。

### 4.2 令牌与它的边界

| 项 | 实现 |
| --- | --- |
| 形态 | JWT HS256，自定义声明只有 `user_id`（其余信息查库） |
| 有效期 | `config.TokenExpiresIn` = 48 小时（尚未开放成配置项） |
| 密钥 | `.env` 的 `JWT_SECRET`（默认 `mocca-dev-secret`，正式部署必须换） |
| 携带 | `Authorization: <token>`（`Bearer ` 前缀会被剥掉）；`?token=` **仅取流端点支持** |
| 登出 / 刷新 | **都没有**：无状态令牌，客户端丢弃即可 |
| 改密使旧令牌失效 | **不会**：令牌里没有口令时间戳，旧令牌可用到过期 |
| 禁用账号 | 登录被拒；已签发令牌在 `authed` 组仍可用；只有 `admin` 组会查库校验角色 |
| 黑名单 | 没有；要让全部令牌立刻失效只能换 `JWT_SECRET` 并重启 |

这些是当前实现的**真实边界**，不是设计理想。需要更强的会话控制时，应引入会话表或黑名单
（并接受随之而来的状态一致性问题），而不是假设现有令牌会随改密失效。

### 4.3 授权：三层叠起来看

```
第一层  路由分组        admin / authed / optional / 公开（routes/routes.go，一眼可读）
第二层  中间件          AdminMiddleware 查库确认角色；AuthMiddleware 只校验签名
第三层  base_path 收敛  scopePath：普通用户的每个路径都收敛到自己的目录，越界直接拒
```

`scopePath` 的要点：

- `path.Join(clean(base_path), reqPath)` 之后再**确认前缀** —— `path.Join` 会把 `..`
  归一化掉（`/media/home` + `/../others.mp4` → `/others.mp4`），只做拼接是挡不住越界的；
- 越界返回 `code: 401`、`路径越出专属目录`；取流端点是 `HTTP 401`；
- 管理员与 `base_path` 为空的用户不受限；
- 写操作（删除/改名/移动/上传）不受 `base_path` 限制 —— 它们只有管理员能调。

### 4.4 目录密码

- 存 `bcrypt(静态哈希)`，复用 `setting` 表，键前缀 `folder_pwd:`（`models/folderpwd.go`）；
- 校验时**从请求路径逐级向上**找最近的受保护目录，所以父目录设一次即可保护整棵子树；
- 校验发生在打开存储之前；`list` / `get` 用 JSON 字段 `password`，取流用 query；
- 它保护的是内容而不是操作权限，**管理员也要过这一关**。

### 4.5 其他安全措施与现状

| 项 | 现状 |
| --- | --- |
| 请求体上限 | `middlewares.BodyLimit` 默认 1 GB，超限 `413`（业务码 400），并包一层 `http.MaxBytesReader` 防「不报 Content-Length」的绕过 |
| 上传文件名 | 只取 `filepath.Base`，挡掉 `../` 穿越 |
| 封面文件名 | 取基名 → 剥开头的点 → 非法字符换 `-` → 截到 64 字符；类型按**内容嗅探**（不信任文件名与声明的 MIME） |
| 列表过滤 | 点开头的名字（含 `.mocca` 自身）与系统保留名不下发，避免把封面/系统文件当素材混进列表 |
| 头像 | 服务端按 key 现画 SVG，不读用户文件；带 `X-Content-Type-Options: nosniff` |
| 访问日志 | `middlewares.RequestLogger` 记录方法、路径、耗时，慢请求（≥2s）单独标注 |
| 未做的部分 | **没有** CORS 配置（后台与 API 同源部署，不需要）、**没有**登录失败限流、**没有** IP 白名单 / SSRF 防护（因为不存在上游代理端点）、**没有** HTTPS（交给反向代理） |

> 「未做的部分」同样是刻意的现状说明：对外网暴露时，限速、防爆破、TLS 都应在反向代理
> 或网关层解决，而不是假设服务自带。

---

## 5. 两种构建：完整版与纯 API 版

同一份源码编出两个产物，**差异只在构建标签**——不靠删文件，也不靠分支：

| | 完整版 | 纯 API 版 |
| --- | --- | --- |
| 构建命令 | `make full` / `go build ./` | `make api` / `go build -tags noweb ./` |
| 产物 | `bin/mocca` | `bin/mocca-api` |
| REST API（`/api/*`）与取流（`/d/*`） | 有 | 有（同一套路由，一字不改） |
| `passwd` 子命令 | 有 | 有 |
| 管理后台 `/admin/` | 有（内嵌，含封面图制作） | **无**：`web/public/` 不参与编译 |
| `POST /api/meta/cover`（封面图落盘） | 有 | **无**：处理器被换成一句说明 |

### 5.1 标签落在哪些文件

| 文件 | 构建标签 | 作用 |
| --- | --- | --- |
| `web/embed.go` | `!noweb` | `//go:embed all:public`；`Embedded = true`；注册 `/admin` 路由 |
| `web/noweb.go` | `noweb` | `Embedded = false`；`FS()` 返回错误；`Register()` 是空操作 |
| `handlers/cover_admin.go` | `!noweb` | 封面图落盘：写入 `<data-dir>/.mocca/covers/` |
| `handlers/cover_api.go` | `noweb` | 同名处理器，只回「本构建不含此功能」 |
| `web/embed_test.go` | `!noweb` | 只对带后台的构建断言内嵌资源 |
| `handlers/cover_test.go` | `!noweb` | 只对带后台的构建断言封面接口 |

装配侧只有一个判断，`main.go` 里读编译期常量：

```go
if web.Embedded {
	web.Register(root, "/admin") // 完整版：挂上静态后台
} else {
	// 纯 API 版：什么都不挂，/admin/* 与其它未命中路径一样走进默认 404
}
```

**路由表在两种构建里完全一致**：`/api/meta/cover` 无条件注册，差异收在处理器实现里。
于是 `routes/` 不需要任何 build tag，也不会出现「改了路由忘了同步另一个版本」。

### 5.2 实测差异（`-ldflags="-s -w"`，macOS arm64）

| | 完整版 | 纯 API 版 |
| --- | --- | --- |
| 二进制体积 | 17.3 MB | 17.1 MB |
| 含 HTML / CSS / JS | 是 | **否** |
| 可制作封面图 | 是 | **否** |

后台是手写的轻量 SPA（mithril + `app.js` + `styles.css`，合计约 200KB），
**因此纯 API 版省下的体积很小**。它的价值在暴露面：二进制里没有任何 HTML/JS，
也没有往数据目录写文件的封面图接口。

### 5.3 怎么验证剥离确实生效

```bash
make api
./bin/mocca-api &     # 启动日志会自述「纯 API 构建（-tags noweb）」

curl -s -o /dev/null -w '%{http_code} %{content_type}\n' http://127.0.0.1:8000/admin/
# 期望：404 application/json —— 不是 200 text/html，也不是跳向白屏

curl -s -X POST http://127.0.0.1:8000/api/meta/cover
# 期望：{"code":404,"message":"当前为纯 API 构建（-tags noweb），未包含封面图制作；…"}
```

两种构建**共用同一份配置与数据目录**（`.env`、`mocca.db`、`.mocca/` 都通用）：
可以直接把完整版换成纯 API 版（或反过来），不需要迁移任何数据。

---

## 6. 部署

### 6.1 从源码构建

仓库暂未提供 Dockerfile；产物是**静态链接的单文件**（`CGO_ENABLED=0`），
交叉编译已经覆盖 Linux / macOS / Windows：

```bash
make api          # 纯 API 版（本机平台）→ bin/mocca-api
make full         # 完整版（本机平台）  → bin/mocca
make dist         # 两种版本 × 全平台，产物带版本号：bin/mocca-<版本>.linux-amd64 …
```

放一份配置再启动（配置见 6.2）：

```bash
cat > .env <<'EOF'
ADDR=:8000
DATA_DIR=/srv/mocca/data
JWT_SECRET=换成随机串
EOF
./bin/mocca        # 完整版：后台在 http://<host>:8000/admin/
```

| 特性 | 说明 |
| --- | --- |
| 静态链接 | `CGO_ENABLED=0`，SQLite 用纯 Go 实现（`modernc.org/sqlite`），可跑在任意 Linux（含 Alpine） |
| 体积裁剪 | `-ldflags "-s -w"` + `-trimpath` |
| 健康检查 | `/ping` 返回纯文本 `pong`，可直接当容器探针 |
| 前端资源 | 完整版把后台 `go:embed` 进二进制，运行时不需要任何外部目录 |

### 6.2 关键环境变量

配置优先级：**内置默认值 < 系统环境变量 < `.env` 文件**（`.env` 是权威来源）。
系统环境变量的键名要加 `MOCCA_` 前缀，`.env` 里写短名，两者等价：

| `.env` 短名 | 环境变量 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `ADDR` | `MOCCA_ADDR` | `:8000` | 监听地址 |
| `DATA_DIR` | `MOCCA_DATA_DIR` | `data` | 数据目录（库与封面/缩略图都在它下面） |
| `JWT_SECRET` | `MOCCA_JWT_SECRET` | `mocca-dev-secret` | JWT 签名密钥；**正式部署必须换掉** |
| `ADMIN_PASSWORD` | `MOCCA_ADMIN_PASSWORD` | `@Mocca/1` | 首个管理员口令，仅在库里一个管理员都没有时使用 |

`MOCCA_ENV_FILE=/path/to/xxx.env` 可以把配置文件指到别处（这个键名不加前缀，
因为得先找到文件才谈得上读里面的键）。文件不存在不算错误（只认环境变量），
但文件读不动会**直接启动失败** —— 静默降级会演成「配了新口令却在用默认口令」。

忘了口令时用 CLI 自救（两种构建都带）：

```bash
./bin/mocca passwd -u admin            # 从标准输入读新口令
./bin/mocca passwd -u admin -p '新口令'
```

### 6.3 必须持久化的路径

| 路径 | 内容 |
| --- | --- |
| `<data-dir>/mocca.db` | 全部业务数据：用户、存储、设置、收藏、元数据、作者、评论 |
| `<data-dir>/.mocca/covers/` | 封面图（库里存的是相对数据目录的路径，整个目录搬家后依然有效） |
| `<data-dir>/.mocca/thumbs/` | 缩略图目录（当前实现不生成缩略图，目录仅预留） |
| `.env`（工作目录） | 配置；也可以放别处并用 `MOCCA_ENV_FILE` 指过去 |

**务必持久化数据目录**：库里存的是相对路径，直接换盘搬走不丢记录；
但服务本身没有别的状态，丢了它等于丢了全部配置。

### 6.4 反向代理注意

| 项 | 要求 |
| --- | --- |
| `Range` 透传 | Nginx 默认透传；**切勿开启响应缓冲**（`proxy_buffering off`），否则拖动进度会卡顿 |
| 请求体大小 | 上传端点需放开 `client_max_body_size`（服务端单请求上限 1 GB） |
| 超时 | `proxy_read_timeout` 需大于最长播放时长（长连接持续读） |
| 子路径部署 | 目前路由前缀固定（API 为 `/api`，取流为 `/d`，后台为 `/admin`）；子路径场景请在反代上改写前缀 |

Nginx 片段：

```nginx
location / {
    proxy_pass http://127.0.0.1:8000;   # 默认监听 :8000，由 .env 的 ADDR 决定
    proxy_http_version 1.1;
    proxy_set_header Host $host;
    proxy_set_header X-Real-IP $remote_addr;

    # 流媒体必需：关闭缓冲，透传 Range
    proxy_buffering off;
    proxy_request_buffering off;
    proxy_read_timeout 3600s;

    client_max_body_size 0;
}
```

---

## 7. 并发与扩展

### 7.1 单实例的实际边界

| 资源 | 现状 | 说明 |
| --- | --- | --- |
| 请求体 | 单请求上限 1 GB | `middlewares.DefaultMaxBody`，超限返回 413 |
| 取流 | 每个请求一个句柄 | 本地存储 = 1 个文件描述符；SMB 走连接池，按需复用 |
| 上传 | 每个文件一次请求 | 前端最多 3 个并发，逐个文件给独立进度 |
| 数据库 | **单个 SQLite 文件** | 纯 Go 驱动（`modernc.org/sqlite`，无 CGO）；写操作是串行的 |
| 内容缓存 | **没有** | 不缓存文件内容：每个 Range 都直接向存储取数据，靠存储自身的随机读能力 |
| 限速 / 连接数上限 | **没有** | 需要的话请在反向代理或操作系统层面做 |

> 这一节刻意只写现状。旧版本里的 `auto_memory_limit` / `max_connections` /
> `ClientDownloadLimit` 等配置项在当前实现中并不存在，写进文档只会误导部署。

### 7.2 水平扩展

会话状态是自包含的（JWT + 库里的用户记录），所以进程本身可以多开；
**瓶颈在数据层**：`mocca.db` 是一个本地 SQLite 文件。

- **推荐**：单实例 + 反向代理。SQLite 的读性能对媒体服务这种「读多写少」的场景足够；
- 需要多副本时，先把数据层换成所有实例都能访问的关系库；把 SQLite 放到网络文件系统上
  并发写并不安全，不要这么做。
- 长连接的视频流不要设置过短的负载均衡空闲超时，否则会周期性掐断正在播放的流。

### 7.3 为什么不做内容缓存

每个 Range 请求都直接读存储，不引入内存/磁盘缓存层：

- 拖拽进度只取所需片段，对本地盘与 SMB 都是「读多少是多少」，缓存收益有限；
- 缓存层会自动带来淘汰策略、内存上限、以及「缓存与真实文件不同步」这类需要长期维护的问题；
- 真正的瓶颈通常在网络与存储侧，不在这一层。

若确定需要，优先在反向代理上做（`proxy_cache`），比在服务里实现一套淘汰逻辑更可控。

### 7.4 排障清单

| 现象 | 先看哪里 |
| --- | --- |
| 拖动进度卡顿 | 反向代理是否关了 `proxy_buffering` / `proxy_request_buffering` |
| 播放器报「格式不支持」 | 取流端点失败时回的是真实 HTTP 状态码，看状态码而不是播放器文案 |
| 上传大文件失败 | 反代的 `client_max_body_size`，以及单请求 1 GB 上限 |
| 登录后立刻失效 | `JWT_SECRET` 是否在两次启动之间变了 |
| 改了密码旧令牌还能用 | 预期行为（见 §4.2）：需要立刻失效就换 `JWT_SECRET` 重启 |
| 后台白屏 | 是不是用了纯 API 版（`/admin/` 在纯 API 版里按设计不存在） |
| 列表里看不到封面目录 | 预期行为：点开头的名字与 `.mocca` 都不下发 |
