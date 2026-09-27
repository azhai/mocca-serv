<div align="center">
  <img src="web/public/logo-64.png" width="96" height="96" alt="Mocca" />

  <p><em>Mocca is a self-hosted media server for your NAS: a REST API with byte-range streaming, plus an optional built-in admin console.</em></p>

  <img src="https://img.shields.io/badge/Go-1.27-00ADD8?logo=go&logoColor=white" alt="Go 1.27" />
  <a href="https://github.com/azhai/mocca"><img src="https://img.shields.io/badge/GitHub-azhai%2Fmocca-181717?logo=github" alt="Repository" /></a>
  <a href="./LICENSE"><img src="https://img.shields.io/badge/license-AGPL--3.0-blue" alt="License: AGPL-3.0" /></a>
</div>

---

- English | [中文](./docs/README_cn.md) | [日本語](./docs/README_ja.md) | [한국어](./docs/README_ko.md) | [Deutsch](./docs/README_de.md) | [Nederlands](./docs/README_nl.md) | [Français](./docs/README_fr.md) | [Español](./docs/README_es.md) | [Русский](./docs/README_ru.md) | [العربية](./docs/README_ar.md)
- Repo: **https://github.com/azhai/mocca**
- [API reference](./docs/API.md) · [Auth & media](./docs/API_AUTH_MEDIA.md) · [Architecture](./docs/BACKEND.md) · [License](./LICENSE)

## About

Mocca serves a media library over HTTP. It ships as a single Go binary — no CGO, no Node, no CDN.
`make full` builds the API plus an embedded admin console; `make api` builds the API alone.

## Two builds

One source tree, two binaries. The only difference is a build tag.

| | Full | API-only |
| --- | --- | --- |
| Build | `make full` (or `go build ./`) | `make api` (or `go build -tags noweb ./`) |
| Output | `bin/mocca` | `bin/mocca-api` |
| REST API + streaming | yes | yes — the app-facing routes are identical |
| `passwd` command | yes | yes |
| Admin console `/admin/` | yes, embedded | **no** — `web/public/` is not compiled in |
| Admin endpoints (storage, upload, edit, cover/screenshot, settings, users) | yes | **no** — the whole group is unregistered (HTTP 404) |

### What the API-only build serves

It is the backend for the app — and nothing else. The routes, the response envelope and the config/data files are the same as the full build:

- **health & init** — `GET /ping`, `ANY /api/ping`, `GET /api/init/status`
- **auth & account** — `POST /api/auth/register`, `POST /api/auth/login/hash`, `GET /api/me`, `POST /api/me/update`, avatars at `GET /static/avatars/:key`
- **browse & play** — `POST /api/fs/list`, `POST /api/fs/get`, `POST /api/fs/info`, `GET /d/*` (byte-range streaming), `GET /meta/poster`
- **social** — favorites `/api/favorites`, comments `/api/comments`, danmaku `/api/danmaku` plus its live stream `GET /api/danmaku/stream`
- **folder passwords** — `GET /api/folder/status`, so a client knows when to ask; setting one remains admin-only

The entire admin group is unregistered, so those paths answer with a plain `404` — no envelope, not `code: 404`:

`/api/storage/*`, `/api/fs/{upload,put,remove,rename,move,edit,cov,shot,hls,reindex,uncov,patch,scrape,scrape/apply}`, `POST /api/folder/password`, `/api/setting/*`, `/api/user/*`, and the pages `/` and `/admin/`.

Both builds read the same `.env`, the same `mocca.db` and the same device-side `.mocca/`, so you can swap one binary for the other without migrating anything.

### Running the API-only build

```bash
make api
./bin/mocca-api        # :8000, REST API only (make run-api to run it from source)
```

There is no console to create the first account with, so register it over the API — `POST /api/auth/register` makes the first account the admin — or put `ADMIN_PASSWORD` in `.env` and the first start seeds `admin`. The startup log self-identifies as `-tags noweb`, which is how you confirm which binary is running.

## Quick start

```bash
make full
./bin/mocca          # listens on :8000 by default
```

1. Open `http://localhost:8000/admin/` and register the first account — the first one becomes the admin.
2. Add a storage: mount path `/media`, driver `local`, root `/path/to/your/media`.
3. Play: `http://localhost:8000/d/media/movie.mp4?token=<your-token>`.

Lost the admin password, or wrote it wrong? The CLI gets you back in — it is included in **both** builds:

```bash
./bin/mocca-api passwd -u admin            # new password read from stdin
./bin/mocca-api passwd -u admin -p 'new-one'            # or pass it explicitly
```

## Configuration

Mocca reads `.env` from the working directory (point elsewhere with `MOCCA_ENV_FILE=/path/to/xxx.env`) and
system environment variables with a `MOCCA_` prefix. Precedence: built-in defaults < environment < `.env`.

| `.env` key | Environment | Default | Meaning |
| --- | --- | --- | --- |
| `ADDR` | `MOCCA_ADDR` | `:8000` | Listen address |
| `DATA_DIR` | `MOCCA_DATA_DIR` | `data` | Database lives here; covers and index files stay on the device, next to your media |
| `JWT_SECRET` | `MOCCA_JWT_SECRET` | `mocca-dev-secret` | Token signing key — change it in production |
| `ADMIN_PASSWORD` | `MOCCA_ADMIN_PASSWORD` | `@Mocca/1` | Seeds the first admin, only when no admin exists |

## Features

- **Storages** — mount local folders and SMB shares; any number of mount points.
- **Byte-range streaming** — `GET /d/<path>` handles `Range`, multi-range, `HEAD` and conditional requests, so players can seek and resume.
- **Media metadata** — title, duration, cover, summary and multiple authors per item; video / audio / image kinds.
- **Cover maker** (full build only) — a cover comes from an admin upload or an ffmpeg frame at a timestamp you pick (`/api/fs/shot` for one file, `/api/fs/patch` for a whole folder); it is resized to a 400×300 PNG and stored **on the device**, next to the library, at `<storage-root>/.mocca/ab/cd/<rest-of-sha1>.png`.
- **Admin console** (full build only) — embedded, works offline over plain HTTP on your LAN: storages, users, metadata, folder passwords and batch upload with one progress bar per file.
- **Browse app** (`/`) — collapsible directory tree, list/grid views, in-page audio and video player, and a full-screen image viewer with zoom; breadcrumbs plus in-folder search.
- **Accounts & permissions** — admin / user / guest; a user only ever sees its own `base_path`; 12 preset avatars (uploading images is not supported, by design).
- **Folder passwords** — set once on a parent folder and the whole subtree, streaming included, requires it; stored as bcrypt.
- **Comments, danmaku, favorites** — comments are unlimited, danmaku are capped at 50 characters and sorted by playback time, favorites are de-duplicated per user.
- **Operations** — `passwd` self-rescue command, single static binary, all configuration in `.env`.

## License

[AGPL-3.0](./LICENSE).

## Contributors

- [@azhai](https://github.com/azhai) — Mocca itself
- Everyone who reported an issue or sent a patch

Mocca's API conventions (static password hash, unified response envelope) and part of its documentation come from the original project [OpenList](https://github.com/OpenListTeam/OpenList) / [AlistGo/alist](https://github.com/AlistGo/alist).
Thanks to [Xhofe](https://github.com/Xhofe) and to all contributors of the original project:

[![Contributors](https://contrib.rocks/image?repo=OpenListTeam/OpenList)](https://github.com/OpenListTeam/OpenList/graphs/contributors)
