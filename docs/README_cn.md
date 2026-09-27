<div align="center">
  <img src="../web/public/logo-64.png" width="96" height="96" alt="Mocca" />

  <p><em>Mocca 是自建的 NAS 媒体服务：提供支持 Range 的流媒体 REST API，并可选带一个内嵌的管理后台。</em></p>

  <img src="https://img.shields.io/badge/Go-1.27-00ADD8?logo=go&logoColor=white" alt="Go 1.27" />
  <a href="https://github.com/azhai/mocca"><img src="https://img.shields.io/badge/GitHub-azhai%2Fmocca-181717?logo=github" alt="仓库地址" /></a>
  <a href="../LICENSE"><img src="https://img.shields.io/badge/license-AGPL--3.0-blue" alt="许可证：AGPL-3.0" /></a>
</div>

---

- [English](../README.md) | 中文 | [日本語](./README_ja.md) | [한국어](./README_ko.md) | [Deutsch](./README_de.md) | [Nederlands](./README_nl.md) | [Français](./README_fr.md) | [Español](./README_es.md) | [Русский](./README_ru.md) | [العربية](./README_ar.md)
- 仓库地址：**https://github.com/azhai/mocca**
- [API 参考](./API.md) · [鉴权与多媒体](./API_AUTH_MEDIA.md) · [架构说明](./BACKEND.md) · [许可证](../LICENSE)

## 简介

Mocca 把媒体库通过 HTTP 提供出去。它是单个 Go 二进制文件 —— 没有 CGO、不需要 Node、不依赖 CDN。
`make full` 编译出 API 加内嵌的管理后台；`make api` 只编译 API。

## 两种构建

同一份源码编出两个产物，差异只在一个构建标签。

| | 完整版 | 纯 API 版 |
| --- | --- | --- |
| 构建 | `make full`（或 `go build ./`） | `make api`（或 `go build -tags noweb ./`） |
| 产物 | `bin/mocca` | `bin/mocca-api` |
| REST API + 流媒体 | 有 | 有（面向 APP 的路由完全一致） |
| `passwd` 命令行 | 有 | 有 |
| 管理后台 `/admin/` | 有（内嵌） | **无**：`web/public/` 不参与编译 |
| 管理接口（存储、上传、编辑、封面/截图、设置、用户） | 有 | **无**：整组不注册，请求落到 404 |

### 纯 API 版提供哪些接口

它是给 APP 当后端的，也只做这件事。路由、响应信封、配置与数据文件都与完整版一致：

- **探活与初始化** —— `GET /ping`、`ANY /api/ping`、`GET /api/init/status`
- **认证与账号** —— `POST /api/auth/register`、`POST /api/auth/login/hash`、`GET /api/me`、`POST /api/me/update`，头像 `GET /static/avatars/:key`
- **浏览与播放** —— `POST /api/fs/list`、`POST /api/fs/get`、`POST /api/fs/info`、`GET /d/*`（支持 Range 的取流）、`GET /meta/poster`
- **社交** —— 收藏 `/api/favorites`、评论 `/api/comments`、弹幕 `/api/danmaku` 及其实时流 `GET /api/danmaku/stream`
- **目录密码** —— `GET /api/folder/status` 让客户端知道何时该弹口令框；设置密码仍是管理员接口

管理接口整组不注册，这些路径会落到默认 `404`（没有信封，不是 `code: 404`）：

`/api/storage/*`、`/api/fs/{upload,put,remove,rename,move,edit,cov,shot,hls,reindex,uncov,patch,scrape,scrape/apply}`、`POST /api/folder/password`、`/api/setting/*`、`/api/user/*`，以及页面 `/` 与 `/admin/`。

两种构建读同一份 `.env`、同一个 `mocca.db`、同一份设备侧 `.mocca/`，换二进制不需要迁移任何数据。

### 跑纯 API 版

```bash
make api
./bin/mocca-api        # 监听 :8000，只有 REST API（从源码跑用 make run-api）
```

没有后台可以注册第一个账号，所以走接口注册 —— `POST /api/auth/register` 的第一个注册者就是管理员；也可以在 `.env` 里设 `ADMIN_PASSWORD`，首次启动自动播种 `admin`。启动日志会自述 `-tags noweb`，用来确认跑的到底是哪个二进制。

## 快速开始

```bash
make full
./bin/mocca          # 默认监听 :8000
```

1. 打开 `http://localhost:8000/admin/` 注册第一个账号 —— 第一个注册者自动成为管理员。
2. 添加挂载点：对外路径 `/media`、驱动 `local`、根目录填你的媒体目录。
3. 播放：`http://localhost:8000/d/media/movie.mp4?token=<你的令牌>`。

管理员口令忘了或写坏了？命令行是唯一的自救通道，**两种构建都带**：

```bash
./bin/mocca-api passwd -u admin            # 从标准输入读新口令
./bin/mocca-api passwd -u admin -p '新口令'            # 也可以直接传
```

## 配置

Mocca 读工作目录下的 `.env`（可用 `MOCCA_ENV_FILE=/path/to/xxx.env` 指到别处），以及带 `MOCCA_` 前缀的系统环境变量。
优先级：内置默认值 < 环境变量 < `.env`。

| `.env` 键 | 环境变量 | 默认值 | 含义 |
| --- | --- | --- | --- |
| `ADDR` | `MOCCA_ADDR` | `:8000` | 监听地址 |
| `DATA_DIR` | `MOCCA_DATA_DIR` | `data` | 库（mocca.db）放这里；封面与索引文件在设备侧、媒体所在目录旁 |
| `JWT_SECRET` | `MOCCA_JWT_SECRET` | `mocca-dev-secret` | 令牌签名密钥，正式部署必须换掉 |
| `ADMIN_PASSWORD` | `MOCCA_ADMIN_PASSWORD` | `@Mocca/1` | 首个管理员口令，仅在一个管理员都没有时使用 |

## 功能

- **存储挂载** —— 本地目录与 SMB（Samba）共享，挂载点数量不限。
- **字节范围流媒体** —— `GET /d/<路径>` 支持 `Range`、多段请求、`HEAD` 与条件请求，播放器可以拖动进度、断点续传。
- **媒体元数据** —— 标题、时长、封面、简介与多个作者；类型分视频 / 音频 / 图片。
- **封面制作**（仅完整版）—— 封面来自管理员上传，或在指定时间点用 ffmpeg 抽帧（单文件 `/api/fs/shot`、整个目录 `/api/fs/patch`）；统一缩成 400×300 PNG，存**在设备侧**媒体库旁的 `<存储根>/.mocca/ab/cd/<sha1 剩余部分>.png`。
- **管理后台**（仅完整版）—— 内嵌、离线可用：挂载点、用户、元数据、目录密码，以及每个文件一条独立进度条的批量上传。
- **浏览应用**（`/`）—— 可折叠的树形目录、列表/网格两种视图、页内音频视频播放器，以及支持缩放的全屏图片预览；另有面包屑导航与本目录搜索。
- **账号与权限** —— 管理员 / 普通用户 / 游客；普通用户只能看到自己的 `base_path`；12 个预设头像（出于安全考虑不支持上传图片）。
- **目录密码** —— 给父目录设一次，整棵子树（含取流）都要求密码，以 bcrypt 存储。
- **评论、弹幕与收藏** —— 评论不限字数；弹幕最长 50 字并按播放时刻排序；收藏按用户去重。
- **运维** —— `passwd` 自救命令、单个静态二进制、配置全部走 `.env`。

## 许可证

[AGPL-3.0](../LICENSE)。

## 贡献者

- [@azhai](https://github.com/azhai) —— Mocca 本身
- 所有提交问题与补丁的人

Mocca 的接口约定（静态口令哈希、统一响应信封）与部分文档来自原项目 [OpenList](https://github.com/OpenListTeam/OpenList) / [AlistGo/alist](https://github.com/AlistGo/alist)。
感谢原作者 [Xhofe](https://github.com/Xhofe) 以及原项目的全部贡献者：

[![贡献者](https://contrib.rocks/image?repo=OpenListTeam/OpenList)](https://github.com/OpenListTeam/OpenList/graphs/contributors)
