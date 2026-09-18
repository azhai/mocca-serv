# Mocca 后端 API 参考

本文与代码同步：所有端点都来自 `routes/routes.go`，字段来自各 handler 的请求/响应结构体。
**只列出实际存在的接口**——早期版本里那些「规划中的接口」（`/api/public/info`、任务队列、扫描、
设置、驱动列表、`/p` 代理取流、`sign=` 签名直链等）在本服务中并不存在，已删除。

> **两种构建共用这一套 API**：完整版（`make full`）额外带管理后台 `/admin/`；
> 纯 API 版（`make api`，`-tags noweb`）把后台整体剥离。
> 两者的路由表一致，唯一差别是 `POST /api/meta/cover`（封面图制作）
> 在纯 API 版里返回 `code: 404` 并说明原因。详见 [`BACKEND.md` §5](./BACKEND.md#5-两种构建完整版与纯-api-版)。

- [1. 通用约定](#1-通用约定)
- [2. 认证与账号](#2-认证与账号)
- [3. 探活与初始化](#3-探活与初始化)
- [4. 文件系统](#4-文件系统)
- [5. 流媒体取流](#5-流媒体取流)
- [6. 媒体元数据与封面](#6-媒体元数据与封面)
- [7. 收藏、评论与弹幕](#7-收藏评论与弹幕)
- [8. 目录密码](#8-目录密码)
- [9. 存储与用户管理（管理员）](#9-存储与用户管理管理员)
- [10. 错误码](#10-错误码)
- [附：一次完整调用](#附一次完整调用)

---

## 1. 通用约定

### 1.1 Base URL

```
http://<host>:8000/api/...     业务接口（前缀 /api，由 config.APIBaseURL 固定）
http://<host>:8000/d/<路径>     取流（不在 /api 之下）
http://<host>:8000/admin/      管理后台（仅完整版）
http://<host>:8000/static/...  静态资源（头像）
```

监听地址由 `.env` 的 `ADDR` 决定，默认 `:8000`。下文路径均省略 host。

### 1.2 响应信封

除 `/ping` 与 `/d/*` 外，所有接口都返回统一信封：

```json
{ "code": 200, "message": "success", "data": { } }
```

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `code` | int | 业务状态码，**200 表示成功** |
| `message` | string | 提示信息；失败时是可直接展示给用户的中文原因 |
| `data` | any | 业务数据；失败时为 `null`（`helpers.Fail` 不带 data） |

**HTTP 状态码几乎恒为 200**，成败只看 `code`。只有两处例外：

1. `GET /ping`、`ANY /api/ping` 返回 `text/plain`（`pong`）；
2. `GET /d/*` 取流失败时回**真实 HTTP 状态码**，因为响应体要被播放器当字节流解析（见 §5）。

### 1.3 请求编码

| 方法 | 约定 |
| --- | --- |
| `GET` | 参数走 query string |
| `POST` / `DELETE` | `application/json` body（`echo` 的 `Bind`） |
| 上传、封面图 | `multipart/form-data`；目标目录用 query 参数 `path` 传 |

时间字段（`created_at`、`modified` 等）是 RFC3339 字符串，如 `2026-09-17T14:03:00+08:00`。

### 1.4 权限分组

路由分组即鉴权策略，一眼能看出每个接口的要求：

| 分组 | 中间件 | 含义 |
| --- | --- | --- |
| 公开 | 无 | 任何人可调 |
| `optional` | `OptionalAuth` | 带令牌就按该用户处理，不带按游客；游客开关关掉后返回 401 |
| `authed` | `AuthMiddleware` | 必须带有效 JWT，否则 `code: 401` |
| `admin` | `AdminMiddleware` | 必须登录**且**是管理员，否则 `401` / `403` |

---

## 2. 认证与账号

鉴权机制、口令约定与安全边界的完整说明见 [`API_AUTH_MEDIA.md`](./API_AUTH_MEDIA.md)。
这里只列接口。

### POST /api/auth/register

注册。**库里一个账号都没有时，第一个注册者直接成为管理员**（否则新装的服务没人配得了存储）。

```json
{ "username": "alice", "password": "<静态哈希>" }
```

`password` 必须是**静态哈希**：`sha256(明文 + "-" + "https://github.com/alist-org/alist")` 的 64 位小写十六进制。
传明文会被拒（`code: 400`，`口令格式不正确：客户端须先做静态哈希再提交`）——
这是刻意设的闸门，见鉴权文档。

响应：

```json
{ "code": 200, "message": "success",
  "data": { "id": 1, "username": "alice", "role": 2, "is_admin": true } }
```

| `code` | 场景 |
| --- | --- |
| 400 | 参数有误 / 口令不是静态哈希 / 用户名已存在 |
| 403 | 已有账号且服务端关闭了注册（`allow_register=false`） |
| 500 | 初始化检查或建号失败 |

### POST /api/auth/login/hash

登录，返回 JWT。

```json
{ "username": "alice", "password": "<静态哈希>" }
```

```json
{ "code": 200, "message": "success", "data": { "token": "eyJhbGciOiJIUzI1NiIs..." } }
```

- 账号不存在与口令错误返回**同一句** `用户名或密码错误`（`code: 401`），不泄露用户名是否存在；
- 账号被禁用返回 `code: 401`、`账号已被禁用`；
- **没有** `/api/auth/login`（明文入口）、**没有** `/api/auth/logout`、**没有**刷新令牌接口；
- **没有**登录失败限流：连续失败不会被锁。

### GET /api/me 🔒

当前用户（需登录）。

```json
{ "code": 200, "message": "success",
  "data": { "id": 1, "username": "alice", "avatar": "03", "role": 2, "base_path": "/media/home" } }
```

`avatar` 是预设头像的 key（`01`–`12`），前端拼 `/static/avatars/<key>.png`。

### POST /api/me/update 🔒

本人改资料。**只能改头像与密码**，改不了角色、账号名与 `base_path`。

```json
{ "avatar": "05", "password": "<原口令静态哈希>", "new_password": "<新口令静态哈希>" }
```

| 字段 | 说明 |
| --- | --- |
| `avatar` | 可选；必须是 `01`–`12` |
| `password` / `new_password` | 改密时**必须成对**出现：先校验原口令（错则 `403`），再设新口令 |

改密后**已签发的旧令牌仍然有效直到过期**（令牌只带 `user_id`，不校验口令时间戳）。详见鉴权文档 §2.4。

### GET /static/avatars/:key

预设头像，返回 `image/svg+xml`（服务端按 key 现画，仓库里没有图片素材）。
`key` 为 `01`–`12`（可带 `.png` 后缀），其它值返回 `code: 404` 的 JSON 信封。
响应带 `X-Content-Type-Options: nosniff`。

---

## 3. 探活与初始化

### GET /ping 与 ANY /api/ping

健康检查，返回**纯文本** `pong`（不是 JSON）。适合容器探针与反向代理存活检测。

```bash
curl -s http://127.0.0.1:8000/ping          # pong
curl -s http://127.0.0.1:8000/api/ping      # pong
```

### GET /api/init/status

初始化状态，公开。客户端据此决定「引导注册管理员」还是「走登录」。

```json
{ "code": 200, "message": "success",
  "data": { "initialized": false, "allow_register": true } }
```

`initialized` = 库里已有账号；`allow_register` 读设置项 `allow_register`（缺省 `true`）。

> 注册开关存在库表 `setting` 里（键 `allow_register`），**当前没有对外修改它的接口**，
> 只能直接改库或由代码播种。

---

## 4. 文件系统

### 4.1 列目录

#### POST /api/fs/list （公开，登录可选）

```json
{ "path": "/media", "password": "<目录密码的静态哈希，可选>", "page": 1, "per_page": 50 }
```

| 字段 | 必填 | 说明 |
| --- | --- | --- |
| `path` | 否 | 目录路径，省略或空串按 `/` 处理；由**挂载点最长匹配**决定用哪个存储 |
| `password` | 否 | 目标目录（或其任一上层目录）设了密码时必填 |
| `page` / `per_page` | 否 | **当前实现不分页**：字段被解析但未使用，永远一次返回全部（已过滤）条目 |

响应：

```json
{
  "code": 200, "message": "success",
  "data": {
    "content": [
      { "name": "demo.mp4", "size": 734003200, "is_dir": false,
        "modified": "2026-08-01T12:00:00+08:00", "thumb": "", "type": 2, "sign": "" },
      { "name": "S01", "size": 0, "is_dir": true,
        "modified": "2026-07-30T09:12:00+08:00", "thumb": "", "type": 0, "sign": "" }
    ],
    "total": 2
  }
}
```

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `name` | string | 名字 |
| `size` | int64 | 字节数（目录为 0，取决于驱动） |
| `is_dir` | bool | 是否目录 |
| `modified` | string | RFC3339 |
| `type` | int | 见 §4.3 |
| `mount` | bool | `true` 表示这一条是**挂载点**（虚拟目录）。`omitempty`，真实目录/文件没有这个键 |
| `thumb` | string | **恒为空**：本服务不生成缩略图 |
| `sign` | string | **恒为空**：本服务没有签名直链机制（访问控制见鉴权文档） |

`mount` 是给界面用的标记：挂载点同样是 `is_dir: true`、`type: 0`，但它是**另一个存储的入口**
（点进去换一个存储根），不是当前存储里的目录，所以客户端应当给它换一个图标。

`total` 是**过滤后**的条数，与 `content` 长度一致。

#### 展示顺序

`content` 由服务端排好序，客户端不需要再排：

1. **挂载点**（由挂载点列表聚合出的虚拟目录，见下）永远最前，其内部按名字排序；
2. **目录**；
3. **文件**按类型分组：视频 → 音频 → 图片 → 文本 → 其它，组内按名字排序。

第 2、3 步的名字排序**忽略大小写**（`apple` 排在 `Banana` 之前），仅当忽略大小写后同名时才按原始字节序兜底。

#### 挂载点聚合树

服务端在内存里维护一棵**只含挂载点**的层级树（`models/mounts.go`），存储增删改时才重建；
真实目录与文件不进这棵树（每次列目录都现读驱动）。它决定三种路径的行为：

| 请求的 `path` | 行为 |
| --- | --- |
| 落在某个挂载点之内（如 `/media/movies`） | 读该挂载点的存储，相对路径 = 去掉挂载点前缀 |
| 不在任何挂载点内、但这一层有挂载点（只挂了 `/media` 与 `/nas` 时的 `/`） | **只回虚拟挂载点条目，不读任何存储** |
| 两者都不满足（如没有前缀的 `/movies`） | 退回「最长挂载点」兜底，保持历史路径可用 |

要点：

- 根路径 `/` **不再是某个存储的根**，而是挂着各挂载点的虚拟层：配了 `/media` 与 `/nas` 时，
  `path: "/"` 返回两个条目 `media`、`nas`（`is_dir: true`、`type: 0`、**`mount: true`**）；
- 只挂了 `/nas/movies` 时，`/` 返回 `nas`、`/nas` 返回 `movies` —— 中间层由聚合树补出来；
- 虚拟挂载点条目与存储里的真实条目**同名时合并成一条**（挂载点优先），一个名字在一层里只出现一次；
- `disabled: true` 的挂载点不进聚合树。

**列表过滤规则**（只影响列表，按显式路径取流不受影响）：

1. 名字以 `.` 开头 —— 隐藏文件/目录，含 `.DS_Store`、`._xxx`、`.git`，以及本服务自己的 `.mocca`（封面/缩略图目录）；
2. 命中系统保留名（不区分大小写）：`$RECYCLE.BIN`、`System Volume Information`、`RECYCLER`、
   `Thumbs.db`、`ehthumbs.db`、`desktop.ini`、`lost+found`、`found.000`、`hiberfil.sys`、
   `pagefile.sys`、`swapfile.sys`、`Network Trash Folder`、`Temporary Items`。

名字里其它符号（`!` `#` `%` `&` 等）一律保留 —— 不按首字符做「特殊符号」这类宽规则。

### 4.2 取详情

#### POST /api/fs/get （公开，登录可选）

```json
{ "path": "/media/demo.mp4", "password": "" }
```

```json
{
  "code": 200, "message": "success",
  "data": {
    "name": "demo.mp4", "size": 734003200, "is_dir": false, "type": 2,
    "thumb": "", "sign": "", "modified": "2026-08-01T12:00:00+08:00",
    "raw_url": "/d/media/demo.mp4?token=eyJhbGciOi...",
    "header": "", "provider": "Local",
    "title": "演示片", "duration": 7325000,
    "cover": ".mocca/covers/demo.png", "description": "简介", "authors": ["甲", "乙"]
  }
}
```

| 字段 | 说明 |
| --- | --- |
| `raw_url` | 可直接交给播放器/下载器的地址。**带 `Authorization` 请求时**会把该令牌附到 `?token=`；不带则只有路径 |
| `provider` | 当前只有 `Local` 与 `SMB`（由存储的 `driver` 决定） |
| `header` | 恒为空字符串（APP 会解析该字段，故保留） |
| `title` / `duration` / `cover` / `description` / `authors` | 有元数据时才出现（`omitempty`） |

### 4.3 类型枚举

`type` 取值与 APP 端 `MediaKind` 严格对齐（`models/media.go`）：**不是 `iota` 顺排**，
错位会让 APP 把视频判成「未知」而不可播放，且静默无报错。

| 值 | 含义 | 判定方式 |
| --- | --- | --- |
| `0` | 目录 | 目录一律 0，不看扩展名（`movies.mp4` 这样的目录名不会被当成视频） |
| `1` | 未知 | 扩展名不在下面几类里 |
| `2` | 视频 | `.mp4 .mkv .mov .avi .webm .flv .m3u8 .ts` |
| `3` | 音频 | `.mp3 .flac .wav .aac .ogg .m4a .wma` |
| `4` | 文本 | `.txt .md .srt .ass .vtt .json .nfo` |
| `5` | 图片 | `.jpg .jpeg .png .gif .webp .bmp .heic .avif` |

### 4.4 写操作（管理员）

| 端点 | 方法 | 请求体 | 说明 |
| --- | --- | --- | --- |
| `/api/fs/remove` | POST | `{ "path": "/media/a.mp4" }` | 删除文件或**递归**删除目录，调用方自行确认 |
| `/api/fs/rename` | POST | `{ "path": "/media/a.mp4", "name": "b.mp4" }` | 同目录改名（`name` 只取基名） |
| `/api/fs/move` | POST | `{ "path": "/media/a.mp4", "dst_dir": "/media/2026" }` | 移动并保留原文件名 |

`/api/fs/move` **不支持跨存储**：来源与目标必须落在同一个挂载点（`code: 400`、`不支持跨存储移动`）。
跨存储没有廉价实现（要整份复制再删），宁可直接拒绝，也不做「看起来成功其实是慢速拷贝」。

三者都不经过目录密码校验，也不受 `base_path` 限制 —— 只有管理员能调，这一点由中间件保证。

### 4.5 上传（管理员）

目标目录用 query 参数传：`?path=/media/uploads`。

#### POST /api/fs/upload?path=/media/uploads

`multipart/form-data`，字段名 `files`（可多个）。逐个写入，返回每个文件的结果：

```json
{ "code": 200, "message": "success",
  "data": [ { "name": "a.mp4", "size": 12345 },
            { "name": "b.mp4", "size": 0, "message": "错误原因" } ] }
```

#### POST /api/fs/put?path=/media/uploads

`multipart/form-data`，字段名 `file`（兼容 `files`）。单文件上传，失败时整个请求 `code: 500`。

```json
{ "code": 200, "message": "success", "data": { "name": "a.mp4", "size": 12345 } }
```

单请求体积上限 1 GB，超限返回 `code: 400`（`请求体过大，上限 1GB`）。
后台对每个文件单独调 `/api/fs/put`，这样才能拿到**每个文件各自的进度**。

---

## 5. 流媒体取流

### GET / HEAD /d/\<路径\>

```http
GET /d/media/movie.mp4?token=<JWT> HTTP/1.1
Range: bytes=1048576-
```

| 项 | 说明 |
| --- | --- |
| 鉴权 | 由 `StreamAuth` 负责：`Authorization` 头或 `?token=` 二者其一（播放器常常加不了自定义头） |
| 游客 | 没带令牌时按设置项 `allow_guest`（默认 `true`）放行；关掉后回 **HTTP 401** |
| 令牌无效 | 直接 **HTTP 401**，不降级成游客（降级会让「登录失效」表现为静默变游客） |
| 目录密码 | 用 `?password=<静态哈希>` 传（取流端点的 body 只能是被播放的字节，所以走 query） |
| Range | 由标准库 `http.ServeContent` 实现：单段 `206` + `Content-Range`、多段 `multipart/byteranges`、`If-*` 条件请求、`416`、`HEAD` |
| 失败响应 | **真实 HTTP 状态码 + JSON 信封**（401 / 403 / 404），不是 200 |

响应示例：

```http
HTTP/1.1 206 Partial Content
Accept-Ranges: bytes
Content-Range: bytes 1048576-734003199/734003200
Content-Length: 732954624
Content-Type: video/mp4
Last-Modified: Fri, 01 Aug 2026 12:00:00 GMT
```

要点：

- 不写 `Content-Disposition`、不做转码、不切片：文件怎么写就怎么发；
- 写超时设为 0（`main.go`），否则正在播放的长连接会被掐断；
- **没有 `/p` 反向代理端点**，也没有 HLS/DASH 清单的特殊处理 —— `.m3u8` 与 `.ts`
  和其它文件一样按字节流发，播放器自己去解析（`/d` 的 `Content-Type` 由扩展名推断）。
- **没有 `sign=` 签名直链**：分享给外部播放器时直接带上 `?token=<JWT>`，
  令牌到期即失效（或把 `allow_guest` 打开）。

---

## 6. 媒体元数据与封面

### GET /api/meta?path=/media/demo.mp4 （公开）

```json
{ "code": 200, "message": "success",
  "data": { "meta": { "id": 1, "path": "/media/demo.mp4", "kind": 2, "size": 734003200,
                      "title": "演示片", "duration": 7325000,
                      "cover": ".mocca/covers/demo.png", "description": "简介",
                      "created_at": "...", "updated_at": "..." },
            "authors": ["甲", "乙"] } }
```

没有元数据时返回 `code: 404`、`尚无元数据`。

### POST /api/meta/save （管理员）

有则更新、无则新建。

```json
{ "path": "/media/demo.mp4", "kind": 2, "size": 734003200,
  "title": "演示片", "duration": 7325000,
  "cover": ".mocca/covers/demo.png", "description": "简介",
  "authors": ["甲", "乙"] }
```

| 字段 | 说明 |
| --- | --- |
| `kind` | **必填**，只能是 `2`（视频）/ `3`（音频）/ `5`（图片）；其它值 `code: 400`、`媒体类型不合法` |
| `size` / `duration` | 字节数 / 毫秒 |
| `cover` | 相对**数据目录**的路径（`models.CoverRelPath` 口径：`.mocca/covers/<文件名>`） |
| `authors` | 整体替换，顺序即展示顺序 |

图片（`kind=5`）落库前会**强制清空** `duration` / `cover` / `description`，避免脏数据。

### POST /api/meta/cover （管理员，仅完整版）

把管理后台用 canvas 画好的封面图落盘，返回可直接写进元数据的 `cover`。
纯 API 构建（`-tags noweb`）返回 `code: 404` 与一句说明。

`multipart/form-data`：

| 字段 | 必填 | 说明 |
| --- | --- | --- |
| `file` | 是 | 图片内容，≤ 2 MB；只接受 PNG / JPEG / WebP（**按内容嗅探**，文件名与声明的 MIME 都不作数） |
| `name` | 是 | 文件名片段，通常直接传媒体文件名（`a.mp4` → 落盘 `a.png`） |

```bash
curl -s -X POST -H "Authorization: $TOKEN" \
  -F "name=a.mp4" -F "file=@cover.png" \
  http://127.0.0.1:8000/api/meta/cover
```

```json
{ "code": 200, "message": "success", "data": { "cover": ".mocca/covers/a.png" } }
```

文件落在 `<数据目录>/.mocca/covers/`（与媒体库解耦，数据目录搬家也不丢）。
除了路径穿越（`../`、`\`）会被收敛成基名，非法字符也会换成 `-`，主文件名最长 64 个字符。

拿到路径后仍需写进元数据才会生效：

```bash
curl -s -X POST -H "Authorization: $TOKEN" -H 'Content-Type: application/json' \
  -d '{"path":"/media/a.mp4","kind":2,"cover":".mocca/covers/a.png"}' \
  http://127.0.0.1:8000/api/meta/save
```

---

## 7. 收藏、评论与弹幕

### 收藏（🔒 需登录）

服务端**只落「收藏」这一项跨设备数据**；播放历史、稍后再看、看到第几秒都留在 APP 本地。

| 端点 | 方法 | 请求体 | 说明 |
| --- | --- | --- | --- |
| `/api/favorites` | GET | — | 我的收藏，按收藏时间倒序 |
| `/api/favorites` | POST | `{ "path": "/media/a.mp4", "name": "a.mp4", "kind": 2, "thumb": "" }` | 重复收藏不产生第二条 |
| `/api/favorites` | DELETE | `{ "path": "/media/a.mp4" }` | 取消收藏（按路径匹配） |

收藏对象：`{ "id": 1, "user_id": 1, "path": "/media/a.mp4", "name": "a.mp4", "kind": 2, "thumb": "", "created_at": "..." }`
（`thumb` 存的是相对路径口径，见 `models.ThumbRelPath`；本服务不生成缩略图。）

### 评论与弹幕（读取公开，写入需登录）

评论与弹幕**共用一张表**，靠 `type` 区分。

| 端点 | 方法 | 权限 | 参数 |
| --- | --- | --- | --- |
| `/api/comments` | GET | 公开 | `?path=/media/a.mp4`，按时间正序 |
| `/api/danmaku` | GET | 公开 | `?path=/media/a.mp4`，按出现时刻正序 |
| `/api/comments` | POST | 🔒 | `{ "path": "...", "type": 0, "offset": 0, "content": "..." }` |
| `/api/comments` | DELETE | 🔒 | `{ "id": 12 }`，**只能删自己发的** |

规则（`models.Comment.Validate`）：

| 项 | 评论（`type=0`） | 弹幕（`type=1`） |
| --- | --- | --- |
| 字数 | 不限 | ≤ **50 个字符**（按 Unicode 码点计，中文算 1 字） |
| `offset` | 强制归零 | 出现时刻（毫秒），必须 ≥ 0 |
| 排序 | 创建时间正序 | `offset` 正序 |

`content` 会先 `TrimSpace`；空内容 / 空 `path` / 未知 `type` 一律 `code: 400`。
删除别人的评论返回 `code: 400`、`cannot delete other's comment`。

---

## 8. 目录密码

给**父目录设一次**，整棵子树（含取流）都要求密码。

| 端点 | 方法 | 权限 | 参数 |
| --- | --- | --- | --- |
| `/api/folder/password` | POST | 管理员 | `{ "path": "/media/私密", "password": "<新密码的静态哈希>" }`；`password` 为空即**清除** |
| `/api/folder/status` | GET | 公开 | `?path=...` |

```json
// POST 设置
{ "code": 200, "message": "success", "data": { "path": "/media/私密", "protected": true } }
// GET 查询
{ "code": 200, "message": "success", "data": { "protected": true, "protected_dir": "/media/私密" } }
```

- 密码**只存 bcrypt 哈希**，与账号口令同一红线；
- 校验顺序：`scopePath`（用户目录收敛）→ 目录密码 → 打开存储。密码不对时不触碰后端存储；
- 需要密码的接口：`/api/fs/list`、`/api/fs/get`、`/d/*`。三者的失败码分别是
  `403 目录 /x 需要密码`（前两者，JSON）与 `HTTP 403`（取流，真实状态码）。

---

## 9. 存储与用户管理（管理员）

### 存储

| 端点 | 方法 | 参数 |
| --- | --- | --- |
| `/api/storage/list` | GET | — |
| `/api/storage/create` | POST | `mount_path`, `order`, `driver`, `addition`, `disabled` |
| `/api/storage/update` | POST | `id` + 待改字段（字段为空表示不改） |
| `/api/storage/delete` | POST | `id` |

`mount_path` 在**保存时**就被规范化：开头补斜线、末尾去斜线、合并重复斜线（`media/`、`media`、`/media//`
一律存成 `/media`）。返回体与后续列表里拿到的都是规范形态，客户端不需要自己纠。改动挂载点会立即
刷新内存里的挂载点聚合树，无需重启。

`addition` 是驱动私有配置的 **JSON 字符串**，不是对象：

```json
// 本地目录
{ "mount_path": "/media", "driver": "local",
  "addition": "{\"root_folder_path\":\"/mnt/media\"}" }

// Samba / SMB
{ "mount_path": "/nas", "driver": "smb",
  "addition": "{\"address\":\"192.168.1.9\",\"username\":\"u\",\"password\":\"p\",\"share_name\":\"media\",\"root_folder_path\":\"movies\"}" }
```

| 驱动 | `driver` 取值 | `addition` 字段 |
| --- | --- | --- |
| 本地目录 | `local`（空值也按 local） | `root_folder_path`（空则 `.`） |
| SMB | `smb` / `samba` | `address`（`host` 或 `host:445`，缺端口补 445）、`username`、`password`、`share_name`（必填）、`root_folder_path`（共享内根目录） |

存储对象（列表项）：

```json
{ "id": 1, "mount_path": "/media", "order": 0, "driver": "local", "status": "",
  "addition": "{\"root_folder_path\":\"/mnt/media\"}", "cache_expiration": 0,
  "custom_cache_policies": "", "disabled": false, "modified": "..." }
```

> `status` / `cache_expiration` / `custom_cache_policies` 是从上游继承的字段，
> **当前实现不使用**（本服务没有缓存层与状态机），保留是为了数据库与客户端字段兼容。

挂载点解析规则：**最长前缀匹配**；没有任何挂载点匹配时退回到「路径最长的那个挂载点」，
因此完全不匹配的路径也会落到某个存储里（这是刻意的兜底，便于单挂载点部署）。
一个挂载点都没有时返回 `code: 404`、`尚未配置存储，请先添加挂载点`。

`disabled` 为真的挂载点**不参与匹配**（`/api/storage/list` 仍会列出）。

### 用户

| 端点 | 方法 | 参数 |
| --- | --- | --- |
| `/api/user/list` | GET | — （不含任何口令字段） |
| `/api/user/create` | POST | `username`, `password`（静态哈希）, `role`, `base_path`, `avatar`, `disabled` |
| `/api/user/update` | POST | `id` + 待改字段；`password` 为空表示不改密 |
| `/api/user/delete` | POST | `id` |

| 规则 | 说明 |
| --- | --- |
| `role` | `0` 普通用户 / `1` 游客 / `2` 管理员 |
| `base_path` | 该用户可见资源的根；留空 = 不受限 |
| 最后一个管理员 | **不能被降级，也不能被删除**（`code: 400`），否则没人再进得了管理后台 |
| 改密 | 同样必须传静态哈希；传明文会被拒 |
| 用户名 | 统一转小写并去空白（`NormalizeUsername`），避免大小写绕过唯一约束 |

用户对象：`{ "id": 1, "username": "alice", "role": 0, "base_path": "/media/home", "permission": 0, "avatar": "01", "disabled": false, "created_at": "...", "updated_at": "...", "last_login_at": "..." }`

> `permission` 是上游遗留的位掩码字段，**当前实现不参与任何判定**。

---

## 10. 错误码

| `code` | 含义 | 典型场景 |
| --- | --- | --- |
| 200 | 成功 | — |
| 400 | 参数/状态错误 | 缺 `path`、口令不是静态哈希、`kind` 不合法、母目录已存在、跨存储移动、非图片封面、封面超 2 MB |
| 401 | 未认证 | 未带令牌、令牌过期/无效、游客被禁用、`base_path` 越界 |
| 403 | 无权限 | 普通用户调管理员接口、目录密码不对、原密码不正确、注册已关闭 |
| 404 | 目标不存在 | 文件/目录不存在、尚无元数据、挂载点不存在、纯 API 构建下的封面图接口 |
| 500 | 服务端错误 | 读写存储失败、建号失败、签发令牌失败 |

中间件的失败一律走信封（HTTP 仍 200）：

| 中间件 | 失败 | `code` |
| --- | --- | --- |
| `AuthMiddleware` | 没带令牌 / 解析失败 | 401（`未登录` / `登录已失效`） |
| `AdminMiddleware` | 同上，或角色不是管理员 | 401 / 403（`需要管理员权限`） |
| `OptionalAuth` | 令牌无效不报错，按游客放行 | — |

---

## 附：一次完整调用

```bash
BASE=http://127.0.0.1:8000
SALT='https://github.com/alist-org/alist'
hash() { printf '%s-%s' "$1" "$SALT" | shasum -a 256 | cut -d' ' -f1; }

# ① 探活 + 初始化状态
curl -s $BASE/ping
curl -s $BASE/api/init/status

# ② 首启：注册第一个账号（自动成为管理员）
curl -s -X POST $BASE/api/auth/register -H 'Content-Type: application/json' \
  -d "{\"username\":\"admin\",\"password\":\"$(hash 'your-password')\"}"

# ③ 登录取 JWT
TOKEN=$(curl -s -X POST $BASE/api/auth/login/hash -H 'Content-Type: application/json' \
  -d "{\"username\":\"admin\",\"password\":\"$(hash 'your-password')\"}" | jq -r .data.token)

# ④ 配一个本地挂载点
curl -s -X POST $BASE/api/storage/create -H "Authorization: $TOKEN" -H 'Content-Type: application/json' \
  -d '{"mount_path":"/media","driver":"local","addition":"{\"root_folder_path\":\"/mnt/media\"}"}'

# ⑤ 列目录（游客也可，取决于 allow_guest）
curl -s -X POST $BASE/api/fs/list -H 'Content-Type: application/json' \
  -d '{"path":"/media"}' | jq -r '.data.content[] | "\(.type)\t\(.name)"'

# ⑥ 取播放地址（raw_url 已带 token）
curl -s -X POST $BASE/api/fs/get -H "Authorization: $TOKEN" -H 'Content-Type: application/json' \
  -d '{"path":"/media/movie.mp4"}' | jq -r .data.raw_url

# ⑦ 交给播放器（会自动发 Range 请求）
mpv "$BASE/d/media/movie.mp4?token=$TOKEN"

# ⑧ 续传第 1 MB 之后的片段
curl -r 1048576- -o part.bin "$BASE/d/media/movie.mp4?token=$TOKEN"
```
