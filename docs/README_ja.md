<div align="center">
  <img src="../web/public/logo-64.png" width="96" height="96" alt="Mocca" />

  <p><em>Mocca は自宅 NAS 向けのセルフホスト型メディアサーバーです。Range 対応のストリーミング REST API と、任意で組み込める管理コンソールを提供します。</em></p>

  <img src="https://img.shields.io/badge/Go-1.27-00ADD8?logo=go&logoColor=white" alt="Go 1.27" />
  <a href="https://github.com/azhai/mocca"><img src="https://img.shields.io/badge/GitHub-azhai%2Fmocca-181717?logo=github" alt="リポジトリ" /></a>
  <a href="../LICENSE"><img src="https://img.shields.io/badge/license-AGPL--3.0-blue" alt="ライセンス: AGPL-3.0" /></a>
</div>

---

- [English](../README.md) | [中文](./README_cn.md) | 日本語 | [한국어](./README_ko.md) | [Deutsch](./README_de.md) | [Nederlands](./README_nl.md) | [Français](./README_fr.md) | [Español](./README_es.md) | [Русский](./README_ru.md) | [العربية](./README_ar.md)
- リポジトリ：**https://github.com/azhai/mocca**
- [API リファレンス](./API.md) · [認証とメディア](./API_AUTH_MEDIA.md) · [アーキテクチャ](./BACKEND.md) · [ライセンス](../LICENSE)

## 概要

Mocca はメディアライブラリを HTTP 経由で配信します。単一の Go バイナリで動作します —— CGO 不要、Node 不要、CDN 不要。
`make full` は API と内蔵の管理コンソールを、`make api` は API のみを生成します。

## 2 つのビルド

同じソースツリーから 2 つのバイナリを生成します。違いはビルドタグだけです。

| | フル | API のみ |
| --- | --- | --- |
| ビルド | `make full`（または `go build ./`） | `make api`（または `go build -tags noweb ./`） |
| 生成物 | `bin/mocca` | `bin/mocca-api` |
| REST API + ストリーミング | あり | あり（ルートは完全に同一） |
| `passwd` コマンド | あり | あり |
| 管理コンソール `/admin/` | あり（内蔵） | **なし** —— `web/public/` はコンパイル対象外 |
| カバー画像の作成 | あり | **なし** —— ハンドラは「このビルドには含まれない」と返すだけ |

## クイックスタート

```bash
make full
./bin/mocca          # 既定では :8000 で待ち受けます
```

1. `http://localhost:8000/admin/` を開き、最初のアカウントを登録します —— 最初の登録者が自動的に管理者になります。
2. ストレージを追加：マウントパス `/media`、ドライバ `local`、ルートにメディアのフォルダを指定。
3. 再生：`http://localhost:8000/d/media/movie.mp4?token=<トークン>`。

管理者パスワードを忘れた／書き壊してしまった場合、CLI が唯一の復旧手段です（**どちらのビルドにも含まれます**）：

```bash
./bin/mocca-api passwd -u admin            # 新しいパスワードは標準入力から
./bin/mocca-api passwd -u admin -p '新しいパスワード'
```

## 設定

Mocca は作業ディレクトリの `.env`（`MOCCA_ENV_FILE=/path/to/xxx.env` で別の場所を指定可能）と、`MOCCA_` 接頭辞付きの環境変数を読みます。
優先順位：組み込みの既定値 < 環境変数 < `.env`。

| `.env` のキー | 環境変数 | 既定値 | 意味 |
| --- | --- | --- | --- |
| `ADDR` | `MOCCA_ADDR` | `:8000` | 待ち受けアドレス |
| `DATA_DIR` | `MOCCA_DATA_DIR` | `data` | データディレクトリ。データベース、カバー、サムネイルが入ります |
| `JWT_SECRET` | `MOCCA_JWT_SECRET` | `mocca-dev-secret` | トークン署名鍵。本番では必ず変更してください |
| `ADMIN_PASSWORD` | `MOCCA_ADMIN_PASSWORD` | `@Mocca/1` | 最初の管理者のパスワード。管理者が 1 人もいないときだけ使われます |

## 機能

- **ストレージ** —— ローカルフォルダと SMB（Samba）共有をマウントできます。マウントポイントの数に制限はありません。
- **バイト範囲ストリーミング** —— `GET /d/<パス>` が `Range`・複数レンジ・`HEAD`・条件付きリクエストに対応。シークとレジュームが可能です。
- **メディアメタデータ** —— タイトル、再生時間、カバー、説明、複数の作者。種類は動画 / 音声 / 画像。
- **カバー画像の作成** —— 管理コンソールが canvas で 1280×720 のカバーを描き、`<データディレクトリ>/.mocca/covers/` に保存します（フルビルドのみ）。
- **管理コンソール**（フルビルドのみ）—— バイナリに内蔵され、オフラインでも動作：ストレージ、ユーザー、メタデータ、フォルダパスワード、ファイルごとに個別の進捗バーが出る一括アップロード。
- **ブラウズアプリ**（`/`）—— 折りたたみ可能なディレクトリツリー、リスト／グリッド表示、ページ内の音声・動画プレーヤー、ズーム対応の全画面画像ビューア。パンくずとフォルダ内検索つき。
- **アカウントと権限** —— 管理者 / 一般ユーザー / ゲスト。一般ユーザーは自分の `base_path` しか見えません。プリセットのアバターは 12 種類（画像アップロードは設計上サポートしません）。
- **フォルダパスワード** —— 親フォルダに一度設定すれば、サブツリー全体（ストリーミングを含む）でパスワードが必要になります。bcrypt で保存します。
- **コメント・弾幕・お気に入り** —— コメントは文字数無制限、弾幕は 50 文字までで再生時刻順、お気に入りはユーザーごとに重複を排除します。
- **運用** —— `passwd` による復旧、単一の静的バイナリ、設定はすべて `.env`。

## ライセンス

[AGPL-3.0](../LICENSE)。

## コントリビューター

- [@azhai](https://github.com/azhai) —— Mocca 本体
- Issue やパッチを送ってくれたすべての人

Mocca の API の取り決め（静的なパスワードハッシュ、統一レスポンスエンベロープ）と一部のドキュメントは、原プロジェクト [OpenList](https://github.com/OpenListTeam/OpenList) / [AlistGo/alist](https://github.com/AlistGo/alist) 由来です。
原作者 [Xhofe](https://github.com/Xhofe) と原プロジェクトのすべてのコントリビューターに感謝します：

[![Contributors](https://contrib.rocks/image?repo=OpenListTeam/OpenList)](https://github.com/OpenListTeam/OpenList/graphs/contributors)
