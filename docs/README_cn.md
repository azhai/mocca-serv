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
| REST API + 流媒体 | 有 | 有（路由完全一致） |
| `passwd` 命令行 | 有 | 有 |
| 管理后台 `/admin/` | 有（内嵌） | **无**：`web/public/` 不参与编译 |
| 封面图制作 | 有 | **无**：该接口只回一句「本构建不含此功能」 |

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
| `DATA_DIR` | `MOCCA_DATA_DIR` | `data` | 数据目录：库、封面、缩略图都在它下面 |
| `JWT_SECRET` | `MOCCA_JWT_SECRET` | `mocca-dev-secret` | 令牌签名密钥，正式部署必须换掉 |
| `ADMIN_PASSWORD` | `MOCCA_ADMIN_PASSWORD` | `@Mocca/1` | 首个管理员口令，仅在一个管理员都没有时使用 |

## 功能

- **存储挂载** —— 本地目录与 SMB（Samba）共享，挂载点数量不限。
- **字节范围流媒体** —— `GET /d/<路径>` 支持 `Range`、多段请求、`HEAD` 与条件请求，播放器可以拖动进度、断点续传。
- **媒体元数据** —— 标题、时长、封面、简介与多个作者；类型分视频 / 音频 / 图片。
- **封面图制作** —— 管理后台在画布上生成 1280×720 封面，存进 `<数据目录>/.mocca/covers/`（仅完整版）。
- **管理后台**（仅完整版）—— 内嵌、离线可用：挂载点、用户、元数据、目录密码，以及每个文件一条独立进度条的批量上传。
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
