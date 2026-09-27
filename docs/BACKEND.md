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

- **播放路径上不转码**：取流永远是「读原文件发字节」（HTTP Range）。播放时不做任何转码。
- **旁路 HLS 靠显式切分产生，不在请求时切片**：切分是**显式的后台动作** —— 管理后台的
  「视频切分」页，或命令行 `tools/gen_hls.sh`。两者用同一条命令（`ffmpeg -c copy`，**只切不转**，
  画质不变、CPU 极低），产物落在 `<目录>/.hls/<文件名>/index.m3u8` + 分片。服务端只把它当
  普通文件发（`/d` 上按扩展名显式钉 `Content-Type`）；`/fs/get` 检测到清单就多带一个 `hls_url`，
  客户端**优先走 HLS**，没有清单时仍走 Range 直链。不生成 `.mpd`，不做多码率（多码率 = 转码）。
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
│ BodyLimit       │           │ 请求绑定与校验    │          │ 页面 / 与 /admin/    │
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
| 挂载点聚合树 | `models/mounts.go` | 常驻内存：**只存挂载点层级**（真实目录与文件不进树），存储增删改时重建 |
| 驱动 | `drivers/` | 具体存储读写；新增一种存储 = 实现 `Driver` 接口，handler 不用改 |
| 工具 | `helpers/` | 统一响应信封（`OK` / `Fail` / `FailStatus`）与 JWT 签发解析 |
| 后台 | `web/` | 管理后台静态资源（唯一可选能力，可编译期剥离） |

### 一次请求的路径（以 `POST /api/fs/list` 为例）

```
BodyLimit(1GB) → RequestLogger → OptionalAuth(解析令牌，写 user_id)
   → handlers.FsList
        ├─ scopePath        普通用户收敛到 base_path，越界直接拒
        ├─ requireFolderPassword   目录密码（bcrypt 比对），不通过就不碰存储
        ├─ listDir          先取挂载点聚合树给出的虚拟挂载点（永远排最前）
        │    └─ realEntries 只有路径真的落在挂载点内才读存储（resolveStorageStrict）
        │         ├─ drivers.Open  建驱动实例（本地或 SMB 连接池）
        │         └─ drv.List      取真实条目；点开头与系统保留名在这里被过滤
        ├─ sortObjs         目录 → 文件（按类型分组），组内忽略大小写的名字序
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
- 播放逻辑与下载逻辑是同一条路径（同一个端点），没有区别对待；
- **带凭证的响应不许缓存**：请求里出现 `Authorization` 头、`?token=` 或 `?password=` 时，
  取流响应会带上 `Cache-Control: private, no-store` —— 地址上可能就挂着凭证，不能让它留在
  共享缓存或代理里。代价是浏览器也不再缓存这段流，来回拖进度条会重新发请求；
  不带任何凭证的游客请求不加这条指令，保持原本的可缓存行为。

### 3.4 挂载点聚合树与两种解析

内存里有一棵**只含挂载点**的层级树（`models/mounts.go`）：

- 由存储表的 `mount_path` 聚合而来，**不保存任何真实目录与文件** —— 它们每次列目录现读驱动，
  因此存储内容怎么变都不需要失效，只有挂载点增删改才会重建；
- 只挂 `/nas/movies` 时，树里会出现 `/` → `nas`、`/nas` → `movies` 两级（中间层是补出来的）；
- `disabled: true` 的挂载点不进树；挂载点在保存时就被规范化（`media/` → `/media`）；
- `CreateStorage` / `UpdateStorage` / `DeleteStorage` 会标记失效，下次读时惰性重建；
  `main.go` 启动时预热一次。

列目录时两种解析并存（`handlers/fs.go`）：

| 函数 | 语义 | 用在哪 |
| --- | --- | --- |
| `resolveStorageStrict` | 只认「路径真的落在某个挂载点之内」，最长前缀匹配（`/media/2026` 优先于 `/media`），**不兜底** | `fs/list`：判断这一层有没有真实内容 |
| `resolveStorage` | 同上，但匹配不上时退回「挂载点路径最长」的那个存储 | `fs/get`、`fs/remove`、`fs/rename`、`fs/move`、取流；以及 `fs/list` 里「既不在挂载点内、这一层也没有挂载点」的历史路径 |

于是 `fs/list` 的三种情形是：路径在挂载点内 → 挂载点条目 + 真实条目；
路径只是聚合树上的节点（例如 `/`、`/nas`）→ 只有挂载点条目；
两者都不是 → 走兜底解析。一个挂载点都没有时返回 `code: 404`、`尚未配置存储，请先添加挂载点`。

聚合树虚拟出来的条目会带 **`"mount": true`**（`handlers.MediaObj.Mount`）：它和普通目录一样是
`is_dir: true` / `type: 0`，但它是**另一个存储的入口**，客户端据此换图标（浏览应用用的是
「存储设备」图标与暖赭色，与文件夹明显区分）。真实条目不带这个键（`omitempty`）。

展示顺序（`sortObjs` / `fileRank`）：**挂载点 → 目录 → 文件按类型分组**
（视频 → 音频 → 图片 → 文本 → 其它），组内按名字排序且**忽略大小写**
（`models.LessName`，仅大小写不同时按原始字节序兜底）。详细规则见
[`API.md` §4.1](./API.md#41-列目录)。

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
| 构建命令 | `make full` / `make web` / `go build -tags nopgsql ./` | `make api` / `go build -tags noweb,nopgsql ./` |
| 产物 | `bin/mocca` | `bin/mocca-api` |
| APP 接口（初始化/登录、列目录、取详情、取流、弹幕、评论、收藏、目录密码状态） | 有 | 有（同一套路由，一字不改） |
| 管理接口（存储维护、上传/改名/移动/删除、媒体编辑、封面/截图/HLS/重建索引/补截图、TMDB 刮削、目录密码设置、全局设置、用户管理） | 有 | **无**：整组不注册，请求落到默认 404 |
| 取流与海报（`/d/*`、`/meta/poster`） | 有 | 有 |
| `passwd` 子命令 | 有 | 有 |
| 页面（浏览应用 `/` 与后台 `/admin/`） | 有（内嵌） | **无**：`web/public/` 不参与编译 |

### 5.1 标签落在哪些文件

| 文件 | 构建标签 | 作用 |
| --- | --- | --- |
| `web/embed.go` | `!noweb` | `//go:embed all:public`；`Embedded = true`；注册 `/`（浏览应用）与 `/admin/`（后台）路由 |
| `web/noweb.go` | `noweb` | `Embedded = false`；`FS()` 返回错误；`Register()` 是空操作 |
| `routes/build_full.go` | `!noweb` | `const AdminRoutes = true` |
| `routes/build_api.go` | `noweb` | `const AdminRoutes = false`：管理接口整组不注册 |
| `web/embed_test.go` | `!noweb` | 只对带后台的构建断言内嵌资源 |

装配侧有两个判断：`main.go` 里读编译期常量决定要不要挂页面，
`routes.SetupAPIRoutes` 里读 `AdminRoutes` 决定要不要调 `SetupAdminRoutes`：

```go
if web.Embedded {
	web.Register(root, "/admin") // 完整版：浏览应用挂 /，后台挂 /admin/
}

// routes.go 内：管理接口只在完整版注册
if AdminRoutes {
	SetupAdminRoutes(e)
}
```

**管理路由表只有一份**（`routes/admin.go` 的 `SetupAdminRoutes`，不带构建标签），
两种构建共用；差异只是一个布尔。这样不会出现「改了路由忘了同步另一个版本」，
而测试可以直接调用 `SetupAdminRoutes`，让管理接口的处理器在两种构建下都被覆盖到。
剥离效果本身由 `routes/routes_test.go` 断言：它比对路由注册表，
要求必需接口两种构建都在、管理接口只在 `AdminRoutes` 为真的构建里。

**`nopgsql`：两版都带，去掉 PostgreSQL 驱动。** 本服务只连 sqlite
（`models.Open` 里写死 `Type: "sqlite"`），而 goent 的驱动注册是把 pgsql 与 sqlite
两个驱动一起 import 的 —— pgsql 那条链会把 pgx 连同 puddle、x/text 共 18 个包、
约 3.6MB 一起链进二进制。标签成对落在**对端仓库** `/opt/repos/goent`
（本仓库以 `replace` 引入，见 `go.mod`）：

| 文件 | 构建标签 | 作用 |
| --- | --- | --- |
| `goent/drivers/dialect_pgsql.go` | `!nopgsql` | `openPgDialect` = `pgsql.OpenDSN`，即默认行为不变 |
| `goent/drivers/dialect_nopgsql.go` | `nopgsql` | `openPgDialect` 返回 `nil`：打不开，调用方本就判 nil |
| `goent/schema_ops_pgsql.go` | `!nopgsql` | `newPgSchemaDriver` = `pgsql.NewPgSchemaDriver` |
| `goent/schema_ops_nopgsql.go` | `nopgsql` | `newPgSchemaDriver` 返回 `nil`：该构建里这条分支取不到 |

默认构建（不带该标签）行为完全不变，goent 的其它使用者不受影响，只有本仓库的两个
产物显式加了它。要连 PostgreSQL 时把 Makefile 里的 `TAG_NOPG` 清空重建即可。

### 5.2 页面入口（完整版）

| 地址 | 内容 |
| --- | --- |
| `/` | 浏览应用首页（`index.html`，由 `Register` 直接写出，不经文件服务器） |
| `/admin/` | 管理后台首页（`admin/index.html`，由 `Register` 直接写出） |
| `/admin` | 302 到 `/admin/`（规范入口） |
| `/admin/index.html` | 被 `http.FileServer` 规范化 301 到 `/admin/` |
| `/css/*`、`/js/*`、`/logo*.png` | 静态资源：文件服务器从 `public/` 根提供 |

两个页面共用同一套 `css/` 与 `js/`，目录布局如下：

```text
public/
├── index.html          浏览应用（/）
├── admin/index.html    管理后台（/admin/）
├── css/                styles.css、home.css
├── js/                 app.js、home.js、mithril.js、static-hash.js
└── logo*.png           APP 图标
```

页面里一律用**绝对路径**引用资源（`/css/styles.css`、`/js/app.js`）：后台页在 `/admin/` 下，
写相对路径会被解析成 `/admin/css/styles.css` 而 404。

目录请求（结尾带 `/` 又不是 `/` 与 `/admin/` 这两个入口的）一律 404：否则 `css/` 与 `js/`
里没有 `index.html`，`http.FileServer` 会回一份**目录列表**，把内嵌资源全列出来。

### 5.3 实测体积（`-ldflags="-s -w" -trimpath`，macOS arm64）

| | 完整版 | 纯 API 版 |
| --- | --- | --- |
| 二进制体积（带 `nopgsql`） | 15.4 MB | 13.7 MB |
| 二进制体积（不带 `nopgsql`） | 19.0 MB | 17.4 MB |
| 含 HTML / CSS / JS | 是 | **否** |
| 可制作封面图 | 是 | **否** |

两个页面都是手写的轻量 SPA（mithril + `app.js` + `home.js` + `styles.css` + `home.css`
+ logo，`web/public/` 合计约 900KB），加上管理接口的机器码，两版相差约 1.7MB。

**体积的大头是运行库，不是本项目代码**：runtime 与 pclntab 约 6.4MB、其他标准库约 6MB、
`modernc.org/sqlite` 约 1.9MB，而 mocca 自己的代码只有 0.11MB。所以「纯 API 版应该小很多」
是错觉；剥离的价值在暴露面：二进制里没有任何 HTML/JS，也没有往数据目录写文件的封面图接口。
体积上真正有意义的一刀是 `nopgsql`（见 5.1 末），去掉 PostgreSQL 驱动即省 3.6MB。

### 5.4 怎么验证剥离确实生效

```bash
make api
./bin/mocca-api &     # 启动日志会自述「纯 API 构建（-tags noweb）」

curl -s -o /dev/null -w '%{http_code} %{content_type}\n' http://127.0.0.1:8000/admin/
# 期望：404 application/json —— 不是 200 text/html，也不是跳向白屏

curl -s -o /dev/null -w '%{http_code}\n' -X POST http://127.0.0.1:8000/api/storage/list
# 期望：404 —— 管理路由整组不存在（没有中间件拦，也没有信封，就是路由未注册）

curl -s -X POST http://127.0.0.1:8000/api/fs/list -H 'Content-Type: application/json' -d '{"path":"/"}'
# 期望：200 的信封 —— APP 接口照常在（这里可能回业务错误码，但路由是通的）
```

两种构建**共用同一份配置与数据目录**（`.env`、`mocca.db`、设备侧 `.mocca/` 都通用）：
可以直接把完整版换成纯 API 版（或反过来），不需要迁移任何数据。

---

## 6. 部署

### 6.1 从源码构建

仓库暂未提供 Dockerfile；产物是**静态链接的单文件**（`CGO_ENABLED=0`），
交叉编译已经覆盖 Linux / macOS / Windows：

```bash
make api          # 纯 API 版（本机平台）→ bin/mocca-api
make full         # 完整版（本机平台）  → bin/mocca
make all          # 两种版本 × 全平台，产物带版本号：bin/mocca-<版本>.linux-amd64 …（含 clean）
```

放一份配置再启动（配置见 6.2）：

```bash
cat > .env <<'EOF'
ADDR=:8000
DATA_DIR=/srv/mocca/data
JWT_SECRET=换成随机串
EOF
./bin/mocca        # 完整版：浏览应用 http://<host>:8000/ ，后台 http://<host>:8000/admin/
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
| `DATA_DIR` | `MOCCA_DATA_DIR` | `data` | 数据目录（`mocca.db`、`error.log`；封面与 `.index.jsonl` 在设备侧，见 §6.3） |
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
| `<data-dir>/mocca.db` | 业务数据：用户、挂载点、设置（全局选项）、收藏、评论、目录密码。媒体附加信息**不在库里**，在设备侧 `meta_dir`（见 API.md §6） |
| `<data-dir>/error.log` | 崩溃与致命错误（panic 值 + 调用栈），只在出事时增长；见 §7.4 |
| `<存储根>/.mocca/ab/cd/<sha1 余下部分>.png` | 封面图。按内容 sha1 寻址，存在各存储的 `meta_dir` 里（默认 `<root_folder_path>/.mocca`），**跟着媒体库走** |
| 各媒体目录下的 `.index.jsonl` | 索引：该目录每个媒体文件的大小、修改时间、sha1、是否缺封面。同样在设备侧 |
| `.env`（工作目录） | 配置；也可以放别处并用 `MOCCA_ENV_FILE` 指过去 |

**要持久化的是两处**：数据目录（`mocca.db`、`error.log`）与设备侧 `meta_dir`（封面、`.index.jsonl`）。
库里存的是相对路径，直接换盘搬走不丢记录；而服务本身没有别的状态，丢了数据目录等于丢了全部配置，
丢了 `meta_dir` 则封面与简介全没（`.index.jsonl` 会重建，封面不会自动重做，只能靠「补充截图」补）。

### 6.4 反向代理注意

| 项 | 要求 |
| --- | --- |
| `Range` 透传 | Nginx 默认透传；**切勿开启响应缓冲**（`proxy_buffering off`），否则拖动进度会卡顿 |
| 请求体大小 | 上传端点需放开 `client_max_body_size`（服务端单请求上限 1 GB） |
| 超时 | `proxy_read_timeout` 需大于最长播放时长（长连接持续读） |
| 子路径部署 | 目前路由前缀固定（API 为 `/api`，取流为 `/d`，页面为 `/` 与 `/admin`）；子路径场景请在反代上改写前缀 |

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
| 挂载点聚合树 | 常驻内存，**只含挂载点层级** | 存储增删改时重建；真实目录与文件不入树，所以存储内容变化不需要任何失效 |
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

### 7.4 崩溃与错误日志

崩溃痕迹单独落一份文件：**`<DATA_DIR>/error.log`**（追加写，一行一条，行首是 RFC3339 时间戳）。
与运行日志（标准 `log`，每个请求一行耗时）分开，是因为两者的读法不同：排障时打开
error.log，第一行就是原因，不用在几万行请求日志里翻。

| 崩溃面 | 行为 | 日志来源标签 |
| --- | --- | --- |
| HTTP 请求链里的 panic（handler / 中间件） | 记调用栈后进程继续；回 `code: 500` 信封；`/d/*` 取流按真实 HTTP 500 回 | `http <METHOD> <PATH>` |
| 后台 goroutine（启动索引、文件监控、http.Server） | 记调用栈后该 goroutine 结束，不带走进程 | `启动索引` / `文件监控` / `http 服务` |
| main goroutine panic | 记调用栈后以**退出码 1** 退出，是否重启交给守护进程 | `main` |
| 启动期致命错误（配置/建库/播种管理员/挂载页面/端口占用） | 记 `FATAL` 行后退出 | `启动` / `服务` |

要点：

- **只收异常**。业务失败（口令不对、文件不存在之类）不写这里 —— 它们在响应体的 `code` 里，
  写进来只会把真正的崩溃淹掉。
- panic 记录带 `debug.Stack()`，**含所有 goroutine 的栈**。goroutine 泄漏、死锁这类崩溃，
  只看出事那一个栈往往看不出因果。
- 文件打不开（磁盘满 / 权限）**不阻断启动**：此时崩溃信息退回标准错误，启动日志里会提醒一句。
- **不做轮转**：正常运行的 error.log 是 0 字节，只有崩溃才增长。确实要切割就用 logrotate，
  配 `copytruncate`（进程持有 fd，直接 rename 不会切走）。
- 看日志：`tail -n 50 <DATA_DIR>/error.log`。

### 7.5 排障清单

| 现象 | 先看哪里 |
| --- | --- |
| 服务莫名重启 / 请求回「服务内部错误」 | `<DATA_DIR>/error.log`：panic 值 + 调用栈在里面（见 §7.4） |
| 拖动进度卡顿 | 反向代理是否关了 `proxy_buffering` / `proxy_request_buffering` |
| 播放器报「格式不支持」 | 取流端点失败时回的是真实 HTTP 状态码，看状态码而不是播放器文案 |
| 上传大文件失败 | 反代的 `client_max_body_size`，以及单请求 1 GB 上限 |
| 登录后立刻失效 | `JWT_SECRET` 是否在两次启动之间变了 |
| 改了密码旧令牌还能用 | 预期行为（见 §4.2）：需要立刻失效就换 `JWT_SECRET` 重启 |
| 后台白屏 | 是不是用了纯 API 版（`/admin/` 在纯 API 版里按设计不存在） |
| 列表里看不到封面目录 | 预期行为：点开头的名字与 `.mocca` 都不下发 |
