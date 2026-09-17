# 鉴权与多媒体服务

本文只覆盖两块，都是与代码逐行核对过的：

1. **鉴权** —— 口令怎么算、令牌怎么发、用户能被限制到什么范围、目录密码怎么保护子树；
2. **多媒体** —— 浏览媒体库、拿播放地址、`/d` 取流（Range / 续传 / 拖拽）。

其余接口（注册、元数据、收藏评论、存储与用户管理）见 [`API.md`](./API.md)。

- [一、通用约定](#一通用约定)
- [二、鉴权](#二鉴权)
- [三、多媒体服务](#三多媒体服务)
- [四、完整播放流程](#四完整播放流程)
- [附：速查](#附速查)

---

## 一、通用约定

### Base URL 与端点分布

```
http://<host>:8000/api/...      业务接口
http://<host>:8000/d/<路径>      取流（不在 /api 之下）
http://<host>:8000/static/...   静态资源（预设头像）
```

监听地址来自 `.env` 的 `ADDR`，默认 `:8000`。

### 响应信封

除 `/ping` 与 `/d/*` 外，响应统一是：

```json
{ "code": 200, "message": "success", "data": { } }
```

**HTTP 状态码几乎恒为 200**，成败只看 `code`：

| 例外 | 状态码 | 原因 |
| --- | --- | --- |
| `GET /ping`、`ANY /api/ping` | 200 | 返回纯文本 `pong`，不是信封 |
| `GET /d/*` 失败 | 401 / 403 / 404 | 响应体要被播放器当字节流解析，必须靠状态码表达失败 |

取流端点是唯一保留真实状态码的业务端点，这是刻意的：若失败也回 200 + JSON，
播放器会把错误信封当媒体数据去解码，最终报出「格式不支持」之类与真实原因无关的错。

### 健康检查

```bash
curl -s http://127.0.0.1:8000/ping    # pong
```

---

## 二、鉴权

### 2.1 口令约定：客户端先做静态哈希

**所有**涉及口令的接口（注册、登录、改密、管理员建号/改号、目录密码）都只接受
**静态哈希**，服务端再过一次 bcrypt 落库。明文永远不会进数据库。

```
静态哈希 = sha256( 明文 + "-" + "https://github.com/alist-org/alist" )   // 小写十六进制，64 位
```

```bash
printf 'your-password-https://github.com/alist-org/alist' | shasum -a 256 | cut -d' ' -f1
```

为什么这么设计：

- 传输链路上没有明文口令（日志、抓包、反代日志都只看到哈希）；
- 服务端存的是 `bcrypt(静态哈希)`，**两层都是不可逆的**，库被拖走也无法直接用于登录；
- 与既有客户端（APP、内嵌后台）的算法保持一致，改盐会让老客户端全部登不进来。

服务端会**拒绝非静态哈希的输入**（`models.IsStaticHashText`：必须恰好 64 位小写十六进制），
返回 `code: 400`、`口令格式不正确：客户端须先做静态哈希再提交`。

> 这道闸门是拿事故换来的：历史上后台的用户编辑表单传的是明文，服务端照存成
> `bcrypt(明文)`，而登录是拿 `静态哈希` 去比对的 —— 那个账号从此**永远登不进去，且不报任何错**。
> 现在传错了会立刻被拒。

**浏览器端注意**：`crypto.subtle` 只在安全上下文（HTTPS 或 localhost）可用。
自建服务常用 `http://192.168.x.x:8000` 打开，此时 `crypto.subtle` 是 `undefined`，
直接调会抛 `TypeError`（表现为「点登录毫无反应」）。内嵌后台为此内置了一份纯 JS 的
SHA-256 兜底实现（`web/public/app.js`）。

### 2.2 注册、登录与首个管理员

| 端点 | 说明 |
| --- | --- |
| `POST /api/auth/register` | 注册；**库中还没有任何账号时，第一个注册者直接是管理员** |
| `POST /api/auth/login/hash` | 登录取 JWT |
| `GET /api/init/status` | 公开的初始化状态：`{ initialized, allow_register }` |

首启引导为什么这样设计：新装的服务如果没人有管理员权限，就配不了挂载点，
整个系统无从配置。注册开关存在 `setting` 表的 `allow_register` 键（缺省 `true`），
**当前没有修改它的接口**；第一个账号始终放行，否则会锁死。

登录的失败语义：

| 情况 | `code` | `message` |
| --- | --- | --- |
| 账号不存在 / 口令错误 | 401 | `用户名或密码错误`（**同一句**，不泄露用户名是否存在） |
| 账号被禁用 | 401 | `账号已被禁用` |
| 参数不是 JSON | 400 | `请求参数有误` |

**没有登录失败限流、没有图形验证码、没有账号锁定**：连续输错不会触发任何处置。
对外网暴露时请在反向代理或网关层自行限速。

### 2.3 令牌

登录返回 `data.token`，是一个 HS256 签名的 JWT：

| 载荷字段 | 说明 |
| --- | --- |
| `user_id` | 唯一自定义声明，其余信息一律查库 |
| `exp` / `iat` / `nbf` | 有效期 / 签发时间 / 生效时间 |

- 有效期由 `config.TokenExpiresIn` 决定，当前固定 **48 小时**（尚未开放成配置项）；
- 签名密钥是 `.env` 的 `JWT_SECRET`（默认 `mocca-dev-secret`，**正式部署必须换掉**）；
- 携带方式：

```http
Authorization: <token>
Authorization: Bearer <token>   # 前缀会被剥掉，两种写法等价
```

`?token=<JWT>` **只在取流端点 `/d/*` 上被接受**（播放器加不了自定义请求头），业务流程一律走请求头。

### 2.4 令牌的生命周期（重要，与旧版文档不同）

| 问题 | 实际行为 |
| --- | --- |
| 有登出接口吗？ | **没有**。JWT 无状态，客户端自己丢弃令牌即可 |
| 有刷新令牌吗？ | **没有**。到期重新登录 |
| 改密会让旧令牌失效吗？ | **不会**。令牌只带 `user_id`，服务端不校验口令时间戳，旧令牌可用到 `exp` |
| 禁用账号会立刻踢下线吗？ | 登录会被拒；但**已签发的令牌在 `authed` 组仍可用**到过期（中间件只解析签名，不查库）。只有 `admin` 组的接口会查库校验角色 |
| 账号被删了呢？ | `/api/me` 等查库的接口返回 `code: 401 账号不存在`；但签名仍有效的令牌在纯解析型接口上依然通过中间件 |
| 想立刻让所有令牌失效？ | 换掉 `JWT_SECRET` 并重启（全部令牌一起失效，用户需重新登录） |
| 有黑名单 / 单点登出吗？ | **没有**，服务端不保存任何会话状态 |

> 这些都是当前实现的真实边界，请据此设计运维流程：需要「立刻踢人」时，改 `JWT_SECRET`
> 是唯一手段；日常收紧只需给账号换口令并等待令牌自然过期。

### 2.5 角色与 `base_path` 隔离

角色取值与 APP 端 `Session.role` 严格对齐：

| 角色 | `role` | 含义 |
| --- | --- | --- |
| 普通用户 | `0` | 有账号，登录后按 `base_path` 收敛可见范围 |
| 游客 | `1` | 无账号，代表「没带令牌」的请求，行为由 `allow_guest` 决定 |
| 管理员 | `2` | 不受限，可调 `/api/**` 下的管理接口 |

**`base_path` 是普通用户的可见根**：服务端在读写前把请求路径拼到它后面。

```
最终路径 = path.Join( clean("/" + base_path), 请求路径 )
```

| `base_path` | 请求 `path` | 实际访问 |
| --- | --- | --- |
| `/media/home` | `/` | `/media/home` |
| `/media/home` | `/movies` | `/media/home/movies` |
| `/media/home` | `/../others.mp4` | **拒绝**（`code: 401`、`路径越出专属目录`） |
| （留空）或管理员 | 任意 | 原样，不收窄 |

要点：

- 拼接后**会再确认一次前缀**：`path.Join` 会把 `..` 归一化掉，只做字符串拼接的话
  `/../secret` 能跳出 `base_path`，这类越界请求被直接拒绝；
- 生效范围：`/api/fs/list`、`/api/fs/get`、`/d/*`（都经过 `scopePath`）；
- **写操作不受 `base_path` 限制**（`/api/fs/remove`、`/rename`、`/move`、上传），
  因为它们只有管理员能调；
- 客户端约定：**始终用相对自己根的路径**（如 `/movies`），不要自行拼接 `base_path`；
  服务端返回的 `name` 也是相对该根的。

### 2.6 游客（未登录）浏览

| 设置项 | 默认 | 作用 |
| --- | --- | --- |
| `allow_guest`（`setting` 表） | `true` | 未带令牌时是放行（按游客）还是拒绝 |

- 影响 `/api/fs/list`、`/api/fs/get` 与**取流** —— 三者用**同一个开关**。
  浏览放行而播放要求登录，会让游客点任何视频都报错；
- 关掉之后未登录请求返回 `code: 401`、`未登录不可浏览`（取流端点是 `HTTP 401`）；
- **当前没有接口能改这个开关**，只能直接改库（`setting` 表）或由代码播种。

### 2.7 目录密码

给**父目录设一次**，整棵子树自动受保护（校验时从路径逐级向上找最近的受保护目录）。

| 端点 | 权限 | 说明 |
| --- | --- | --- |
| `POST /api/folder/password` | 管理员 | `{ "path": "/media/私密", "password": "<静态哈希>" }`，`password` 为空即清除 |
| `GET /api/folder/status` | 公开 | `?path=...` → `{ protected, protected_dir }` |

| 约定 | 说明 |
| --- | --- |
| 存储 | 与账号同红线：只存 `bcrypt(静态哈希)`，不存明文（复用 `setting` 表，键前缀 `folder_pwd:`） |
| 传递 | `list` / `get` 放 JSON 字段 `password`；取流放 query（`?password=`），因为取流的 body 只能是被播放的字节 |
| 校验时机 | 在打开存储**之前**：密码不对时不会触碰后端 |
| 失败 | `code: 403`、`目录 /media/私密 需要密码`；取流端点回 `HTTP 403` |
| 与角色无关 | 管理员也要过这一关（它保护的是内容本身，不是操作权限） |

### 2.8 建议的最小权限模型

| 使用方 | 凭证 | 说明 |
| --- | --- | --- |
| 播放器 / 投屏设备 | `?token=<JWT>`（或游客放行） | 令牌在 URL 里，注意有效期与日志留存 |
| 前端 / 移动 App（普通用户） | JWT（`/api/auth/login/hash`） | 访问范围限于自身 `base_path` |
| 运维脚本 / 服务间 | JWT（管理员账号） | 48 小时过期；长期任务请自行重新登录 |
| 匿名浏览 | 无 | 由 `allow_guest` 决定，建议只在可信内网开启 |

---

## 三、多媒体服务

### 3.1 端点总览

| 端点 | 方法 | 凭证 | 作用 |
| --- | --- | --- | --- |
| `/api/fs/list` | POST | 公开（受游客开关约束） | 列目录，浏览媒体库 |
| `/api/fs/get` | POST | 公开（受游客开关约束） | 取详情，`raw_url` 可直接播放 |
| `/d/<路径>` | GET / HEAD | 请求头或 `?token=` | 取流，支持 Range |

**没有** `/p`（上游代理取流）端点，**没有**签名直链（`sign=`），**没有**缩略图端点。

### 3.2 取流鉴权（`middlewares.StreamAuth`）

```
取 Authorization 头（剥掉可选的 "Bearer "）
      │
      ├─ 空 ──► 再看 ?token=
      │            │
      │            ├─ 空 ──► allow_guest 为真则放行（游客），否则 HTTP 401
      │            └─ 有 ──► 解析校验
      │
      └─ 有 ──► 解析校验：成功写入用户 ID；失败 HTTP 401（**不降级成游客**）
```

不降级是刻意的：降级会把「登录已失效」表现为「静默变成游客」，
用户看到的现象（有些内容突然不见了）与真实原因对不上，排查成本极高。

### 3.3 Range 与响应

```http
GET /d/media/movie.mp4?token=<JWT> HTTP/1.1
Range: bytes=1048576-
```

取流由标准库 `http.ServeContent` 实现（`handlers/fs.go` 的 `Download`），语义完整：

| 能力 | 实现 |
| --- | --- |
| 拖拽进度（seek） | 播放器发 `Range: bytes=N-`，服务端回 `206` + 该片段 |
| 断点续传 | 客户端带 `Range: bytes=<已收字节>-` |
| 多段并发 | 一次请求多个区间 → `206` + `multipart/byteranges` |
| 条件请求 | `If-Match` / `If-None-Match` / `If-Modified-Since` / `If-Unmodified-Since` / `If-Range`；命中即 `304` / `412` |
| 范围越界 | `416` + `Content-Range: bytes */<size>` |
| 空文件 | 忽略 `Range` 直接 `200`（兼容给所有请求都附加 Range 的客户端） |
| `HEAD` | 只回响应头，不传体（播放前探测长度与类型） |
| `Content-Type` | 按扩展名推断（标准库 `mime`），`video/mp4` / `audio/mpeg` / `application/vnd.apple.mpegurl`… |

```http
HTTP/1.1 206 Partial Content
Accept-Ranges: bytes
Content-Range: bytes 1048576-734003199/734003200
Content-Length: 732954624
Content-Type: video/mp4
Last-Modified: Fri, 01 Aug 2026 12:00:00 GMT
```

要点：

- **不缓存内容**：每个请求（每个 Range）都重新打开文件/远端对象。省掉了缓存层与
  「缓存和真实文件不一致」这类长期维护成本；代价是高频 seek 会重复读存储。
- **不转码、不封装、不切片**：文件怎么写就怎么发。`.m3u8` / `.mpd` / `.ts` 与普通文件一样是字节流，
  播放器自己解析（多码率属于「旁路」：把清单和切片放进存储即可）。
- **断连即释放**：读句柄在请求返回时 `Close()`；SMB 的 `Close` 只是把连接**归还连接池**，不会断开会话。
- 写超时为 0（`main.go` 里的 `http.Server`）：否则正在播放的长连接会被掐断。
  `IdleTimeout` 120s，`ReadTimeout` 30s。

### 3.4 取流失败

失败一律 **真实 HTTP 状态码 + JSON 信封**：

| HTTP | 场景 | `message` |
| --- | --- | --- |
| 400 | 路径为空 | `路径为空` |
| 401 | 未登录且未开游客；令牌无效/过期；`base_path` 越界 | `未登录` / `登录已失效` / `路径越出专属目录` |
| 403 | 目录需要密码（`?password=` 缺失或不匹配） | `目录 /x 需要密码` |
| 404 | 路径不存在、存储未配置、文件打不开 | `文件不存在` / `尚未配置存储，请先添加挂载点` |
| 416 | Range 无法满足 | 无 JSON 体（标准库直接写头） |

### 3.5 分享与防盗链

本服务**没有**签名直链（没有 `sign=`、没有 HMAC、没有 `link_expiration`）。分享的方式只有两种：

1. 把 `?token=<JWT>` 一起发出去：48 小时后失效，且该令牌同时能访问其它接口 ——
   分享给外部人员时等于把账号权限一起给出去；
2. 打开 `allow_guest`：链接不带令牌、内网可用，但任何能访问该服务的人都能看全部（未设目录密码的）内容。

需要「可控时效的公开链接」时，请在反向代理层实现（例如签名的短链），服务端不提供该能力。

### 3.6 反向代理注意

| 项 | 要求 |
| --- | --- |
| `Range` 透传 | Nginx 默认透传；**切勿开启响应缓冲**（`proxy_buffering off`），否则拖动进度会卡顿 |
| 超时 | `proxy_read_timeout` 要大于最长播放时长（长连接持续读） |
| 请求体 | 上传接口需放开 `client_max_body_size`（服务端单请求上限 1 GB） |
| 子路径 | 路由前缀固定（`/api`、`/d`、`/static`、`/admin`），子路径部署请在反代改写 |

```nginx
location / {
    proxy_pass http://127.0.0.1:8000;
    proxy_http_version 1.1;
    proxy_set_header Host $host;
    proxy_set_header X-Real-IP $remote_addr;

    proxy_buffering off;            # 流媒体必需
    proxy_request_buffering off;
    proxy_read_timeout 3600s;
    client_max_body_size 0;
}
```

---

## 四、完整播放流程

```bash
BASE=http://127.0.0.1:8000
SALT='https://github.com/alist-org/alist'
hash() { printf '%s-%s' "$1" "$SALT" | shasum -a 256 | cut -d' ' -f1; }

# ① 登录取 JWT（首装先 /api/auth/register 建第一个管理员）
TOKEN=$(curl -s -X POST $BASE/api/auth/login/hash -H 'Content-Type: application/json' \
  -d "{\"username\":\"admin\",\"password\":\"$(hash 'your-password')\"}" | jq -r .data.token)

# ② 浏览：筛出视频（type=2）
curl -s -X POST $BASE/api/fs/list -H "Authorization: $TOKEN" -H 'Content-Type: application/json' \
  -d '{"path":"/media"}' | jq -r '.data.content[] | select(.type==2) | .name'

# ③ 取播放地址（raw_url 已含 token）
RAW=$(curl -s -X POST $BASE/api/fs/get -H "Authorization: $TOKEN" -H 'Content-Type: application/json' \
  -d '{"path":"/media/movie.mp4"}' | jq -r .data.raw_url)

# ④ 播放：现代播放器会自动发 Range 请求
mpv "$BASE$RAW"

# ⑤ 手动验证续传：从第 1 MB 处拉取
curl -r 1048576- -o part.bin "$BASE$RAW"

# ⑥ 受保护目录：列目录与取流都要带目录密码（同一个静态哈希）
PWD_HASH=$(hash 'dir-password')
curl -s -X POST $BASE/api/fs/list -H "Authorization: $TOKEN" -H 'Content-Type: application/json' \
  -d "{\"path\":\"/media/私密\",\"password\":\"$PWD_HASH\"}"
curl -s -o /dev/null -w '%{http_code}\n' \
  "$BASE/d/media/私密/a.mp4?token=$TOKEN&password=$PWD_HASH"
```

---

## 附：速查

```
# 鉴权
POST /api/auth/register           注册（首个账号自动成为管理员）
POST /api/auth/login/hash         登录取 JWT（口令须先做静态哈希）← 推荐
GET  /api/init/status             初始化状态（公开）
GET  /api/me                      当前用户 🔒
POST /api/me/update               改头像/改密 🔒（改密需带原口令）
GET  /api/folder/status?path=     目录是否受保护（公开）
POST /api/folder/password         设/清目录密码（管理员）

凭证：Authorization: <JWT>（"Bearer " 前缀可选）；取流可用 ?token=
口令：sha256(明文 + "-" + "https://github.com/alist-org/alist") 小写十六进制
令牌：HS256，载荷只有 user_id，默认 48 小时；无登出、无刷新、无黑名单
      改密不会使旧令牌失效；禁用账号不影响已签发令牌（除非走管理员接口）

# 多媒体
POST /api/fs/list                 列目录（type: 2=视频 3=音频 5=图片；只列过滤后条目）
POST /api/fs/get                  取详情 → data.raw_url 可直接播放
GET  /d/<路径>?token=<JWT>         取流（Range / 多段 / 续传 / 拖拽；失败回真实状态码）
HEAD /d/<路径>                    仅探测响应头
GET  /ping                        健康检查（纯文本 pong）

# 本服务没有的东西（旧文档里写过，但代码里不存在）
× /p 上游代理取流            × sign= 签名直链 / link_expiration
× /api/auth/login（明文）    × /api/auth/logout / 刷新令牌
× 登录失败限流               × 播放历史 / 稍后再看 / 播放进度（都在 APP 本地）
× 缩略图端点（thumb 恒为空）  × 缓存层 / 限速 / 并发上限
```
