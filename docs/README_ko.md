<div align="center">
  <img src="../web/public/logo-64.png" width="96" height="96" alt="Mocca" />

  <p><em>Mocca는 NAS용 자체 호스팅 미디어 서버입니다. Range를 지원하는 스트리밍 REST API와 선택적으로 내장되는 관리 콘솔을 제공합니다.</em></p>

  <img src="https://img.shields.io/badge/Go-1.27-00ADD8?logo=go&logoColor=white" alt="Go 1.27" />
  <a href="https://github.com/azhai/mocca"><img src="https://img.shields.io/badge/GitHub-azhai%2Fmocca-181717?logo=github" alt="저장소" /></a>
  <a href="../LICENSE"><img src="https://img.shields.io/badge/license-AGPL--3.0-blue" alt="라이선스: AGPL-3.0" /></a>
</div>

---

- [English](../README.md) | [中文](./README_cn.md) | [日本語](./README_ja.md) | 한국어 | [Deutsch](./README_de.md) | [Nederlands](./README_nl.md) | [Français](./README_fr.md) | [Español](./README_es.md) | [Русский](./README_ru.md) | [العربية](./README_ar.md)
- 저장소: **https://github.com/azhai/mocca**
- [API 레퍼런스](./API.md) · [인증과 미디어](./API_AUTH_MEDIA.md) · [아키텍처](./BACKEND.md) · [라이선스](../LICENSE)

## 소개

Mocca는 미디어 라이브러리를 HTTP로 제공합니다. 단일 Go 바이너리로 동작합니다 —— CGO 없음, Node 불필요, CDN 불필요.
`make full`은 API와 내장 관리 콘솔을, `make api`는 API만 빌드합니다.

## 두 가지 빌드

같은 소스에서 두 개의 바이너리를 만듭니다. 차이는 빌드 태그 하나뿐입니다.

| | 전체 | API 전용 |
| --- | --- | --- |
| 빌드 | `make full` (또는 `go build ./`) | `make api` (또는 `go build -tags noweb ./`) |
| 산출물 | `bin/mocca` | `bin/mocca-api` |
| REST API + 스트리밍 | 있음 | 있음 (라우트가 완전히 동일) |
| `passwd` 명령 | 있음 | 있음 |
| 관리 콘솔 `/admin/` | 있음 (내장) | **없음** —— `web/public/`이 컴파일되지 않음 |
| 커버 이미지 제작 | 있음 | **없음** —— 핸들러는 "이 빌드에는 없음"만 응답 |

## 빠른 시작

```bash
make full
./bin/mocca          # 기본값으로 :8000 에서 대기
```

1. `http://localhost:8000/admin/` 을 열고 첫 계정을 등록합니다 —— 첫 등록자가 자동으로 관리자가 됩니다.
2. 스토리지 추가: 마운트 경로 `/media`, 드라이버 `local`, 루트에 미디어 폴더를 지정.
3. 재생: `http://localhost:8000/d/media/movie.mp4?token=<토큰>`.

관리자 비밀번호를 잊었거나 잘못 기록했다면 CLI가 유일한 복구 경로입니다 (**두 빌드 모두 포함**):

```bash
./bin/mocca-api passwd -u admin            # 새 비밀번호는 표준 입력에서 읽음
./bin/mocca-api passwd -u admin -p '새-비밀번호'
```

## 설정

Mocca는 작업 디렉터리의 `.env`(`MOCCA_ENV_FILE=/path/to/xxx.env`로 다른 위치 지정 가능)와 `MOCCA_` 접두사가 붙은 환경 변수를 읽습니다.
우선순위: 내장 기본값 < 환경 변수 < `.env`.

| `.env` 키 | 환경 변수 | 기본값 | 의미 |
| --- | --- | --- | --- |
| `ADDR` | `MOCCA_ADDR` | `:8000` | 대기 주소 |
| `DATA_DIR` | `MOCCA_DATA_DIR` | `data` | 데이터 디렉터리. 데이터베이스, 커버, 썸네일이 들어감 |
| `JWT_SECRET` | `MOCCA_JWT_SECRET` | `mocca-dev-secret` | 토큰 서명 키. 운영 환경에서는 반드시 변경 |
| `ADMIN_PASSWORD` | `MOCCA_ADMIN_PASSWORD` | `@Mocca/1` | 첫 관리자 비밀번호. 관리자가 하나도 없을 때만 사용됨 |

## 기능

- **스토리지** —— 로컬 폴더와 SMB(Samba) 공유를 마운트합니다. 마운트 지점 수에는 제한이 없습니다.
- **바이트 범위 스트리밍** —— `GET /d/<경로>`가 `Range`, 다중 범위, `HEAD`, 조건부 요청을 처리하므로 탐색(seek)과 이어보기가 가능합니다.
- **미디어 메타데이터** —— 제목, 재생 시간, 커버, 설명, 여러 저자. 종류는 비디오 / 오디오 / 이미지.
- **커버 이미지 제작** —— 관리 콘솔이 canvas로 1280×720 커버를 그려 `<데이터 디렉터리>/.mocca/covers/`에 저장합니다 (전체 빌드 전용).
- **관리 콘솔** (전체 빌드 전용) —— 바이너리에 내장되어 오프라인에서도 동작: 스토리지, 사용자, 메타데이터, 폴더 비밀번호, 파일마다 개별 진행 막대가 있는 일괄 업로드.
- **계정과 권한** —— 관리자 / 일반 사용자 / 게스트. 일반 사용자는 자신의 `base_path`만 볼 수 있습니다. 기본 아바타 12종 (이미지 업로드는 설계상 지원하지 않음).
- **폴더 비밀번호** —— 상위 폴더에 한 번 설정하면 하위 트리 전체(스트리밍 포함)에 비밀번호가 필요합니다. bcrypt로 저장.
- **댓글, 탄막, 즐겨찾기** —— 댓글은 글자 수 제한 없음, 탄막은 50자까지이며 재생 시각 순으로 정렬, 즐겨찾기는 사용자별로 중복 제거.
- **운영** —— `passwd` 복구 명령, 단일 정적 바이너리, 모든 설정은 `.env`.

## 라이선스

[AGPL-3.0](../LICENSE).

## 기여자

- [@azhai](https://github.com/azhai) —— Mocca 자체
- 이슈를 올리거나 패치를 보내 준 모든 분

Mocca의 API 규약(정적 비밀번호 해시, 통일된 응답 봉투)과 일부 문서는 원 프로젝트 [OpenList](https://github.com/OpenListTeam/OpenList) / [AlistGo/alist](https://github.com/AlistGo/alist)에서 왔습니다.
원작자 [Xhofe](https://github.com/Xhofe)와 원 프로젝트의 모든 기여자에게 감사드립니다:

[![Contributors](https://contrib.rocks/image?repo=OpenListTeam/OpenList)](https://github.com/OpenListTeam/OpenList/graphs/contributors)
