<div align="center">
  <img src="../web/public/logo-64.png" width="96" height="96" alt="Mocca" />

  <p><em>Mocca ist ein selbst gehosteter Medienserver für dein NAS: eine REST-API mit Byte-Range-Streaming und optional einer eingebauten Verwaltungskonsole.</em></p>

  <img src="https://img.shields.io/badge/Go-1.27-00ADD8?logo=go&logoColor=white" alt="Go 1.27" />
  <a href="https://github.com/azhai/mocca"><img src="https://img.shields.io/badge/GitHub-azhai%2Fmocca-181717?logo=github" alt="Repository" /></a>
  <a href="../LICENSE"><img src="https://img.shields.io/badge/license-AGPL--3.0-blue" alt="Lizenz: AGPL-3.0" /></a>
</div>

---

- [English](../README.md) | [中文](./README_cn.md) | [日本語](./README_ja.md) | [한국어](./README_ko.md) | Deutsch | [Nederlands](./README_nl.md) | [Français](./README_fr.md) | [Español](./README_es.md) | [Русский](./README_ru.md) | [العربية](./README_ar.md)
- [API-Referenz](./API.md) · [Auth & Medien](./API_AUTH_MEDIA.md) · [Architektur](./BACKEND.md) · [Lizenz](../LICENSE)

## Über Mocca

Mocca stellt eine Mediathek über HTTP bereit. Es läuft als einzelne Go-Binärdatei —— kein CGO, kein Node, kein CDN.
`make full` erzeugt die API samt eingebauter Verwaltungskonsole, `make api` nur die API.

## Zwei Builds

Ein Quellbaum, zwei Binärdateien. Der einzige Unterschied ist ein Build-Tag.

| | Vollständig | Nur API |
| --- | --- | --- |
| Build | `make full` (oder `go build ./`) | `make api` (oder `go build -tags noweb ./`) |
| Ergebnis | `bin/mocca` | `bin/mocca-api` |
| REST-API + Streaming | ja | ja (die App-Routen sind identisch) |
| `passwd`-Kommando | ja | ja |
| Verwaltungskonsole `/admin/` | ja (eingebaut) | **nein** —— `web/public/` wird nicht kompiliert |
| Admin-Endpunkte (Speicher, Upload, Bearbeiten, Cover/Screenshot, Einstellungen, Benutzer) | ja | **nein** —— die ganze Gruppe wird nicht registriert (HTTP 404) |

## Schnellstart

```bash
make full
./bin/mocca          # lauscht standardmäßig auf :8000
```

1. `http://localhost:8000/admin/` öffnen und das erste Konto registrieren —— das erste Konto wird automatisch Administrator.
2. Einen Speicher hinzufügen: Mount-Pfad `/media`, Treiber `local`, Wurzelverzeichnis auf deinen Medienordner setzen.
3. Abspielen: `http://localhost:8000/d/media/movie.mp4?token=<dein-token>`.

Admin-Passwort vergessen oder falsch geschrieben? Die CLI ist der Weg zurück —— sie ist in **beiden** Builds enthalten:

```bash
./bin/mocca-api passwd -u admin            # neues Passwort wird von stdin gelesen
./bin/mocca-api passwd -u admin -p 'neues-passwort'
```

## Konfiguration

Mocca liest `.env` aus dem Arbeitsverzeichnis (anderer Ort über `MOCCA_ENV_FILE=/path/to/xxx.env`) sowie
Systemumgebungsvariablen mit dem Präfix `MOCCA_`. Rangfolge: eingebaute Standardwerte < Umgebung < `.env`.

| `.env`-Schlüssel | Umgebungsvariable | Standard | Bedeutung |
| --- | --- | --- | --- |
| `ADDR` | `MOCCA_ADDR` | `:8000` | Listen-Adresse |
| `DATA_DIR` | `MOCCA_DATA_DIR` | `data` | Hier liegt die Datenbank; Cover und Indexdateien liegen geräteseitig neben den Medien |
| `JWT_SECRET` | `MOCCA_JWT_SECRET` | `mocca-dev-secret` | Signaturschlüssel für Tokens —— im Produktivbetrieb ändern |
| `ADMIN_PASSWORD` | `MOCCA_ADMIN_PASSWORD` | `@Mocca/1` | Passwort des ersten Administrators, nur wenn noch keiner existiert |

## Funktionen

- **Speicher** —— lokale Ordner und SMB-(Samba-)Freigaben einbinden; beliebig viele Mount-Punkte.
- **Byte-Range-Streaming** —— `GET /d/<pfad>` beherrscht `Range`, mehrere Bereiche, `HEAD` und bedingte Requests; Player können spulen und fortsetzen.
- **Medien-Metadaten** —— Titel, Dauer, Cover, Beschreibung und mehrere Autoren pro Eintrag; Arten: Video / Audio / Bild.
- **Cover-Generator** (nur vollständiger Build) —— ein Cover kommt aus einem Admin-Upload oder aus einem ffmpeg-Frame zu einem gewählten Zeitpunkt (`/api/fs/shot` für eine Datei, `/api/fs/patch` für einen ganzen Ordner); es wird auf 400×300 PNG skaliert und **geräteseitig** neben der Mediathek unter `<Speicherwurzel>/.mocca/ab/cd/<Rest-der-sha1>.png` abgelegt.
- **Verwaltungskonsole** (nur vollständiger Build) —— eingebaut, funktioniert offline: Speicher, Benutzer, Metadaten, Ordner-Passwörter und Massen-Upload mit eigenem Fortschrittsbalken je Datei.
- **Konten & Rechte** —— Administrator / Benutzer / Gast; ein Benutzer sieht ausschließlich sein eigenes `base_path`; 12 vorgegebene Avatare (Bild-Upload wird bewusst nicht unterstützt).
- **Ordner-Passwörter** —— einmal beim übergeordneten Ordner gesetzt, verlangt der gesamte Teilbaum (inklusive Streaming) das Passwort; gespeichert als bcrypt.
- **Kommentare, Danmaku, Favoriten** —— Kommentare ohne Längenlimit, Danmaku bis 50 Zeichen, nach Abspielzeit sortiert, Favoriten je Benutzer ohne Duplikate.
- **Betrieb** —— `passwd` zur Selbstrettung, einzelne statische Binärdatei, alle Konfiguration in `.env`.

## Lizenz

[AGPL-3.0](../LICENSE).

## Mitwirkende

- [@azhai](https://github.com/azhai) —— Mocca selbst
- Alle, die ein Issue gemeldet oder einen Patch geschickt haben

Die API-Konventionen von Mocca (statischer Passwort-Hash, einheitlicher Response-Envelope) und ein Teil der Dokumentation stammen aus dem ursprünglichen Projekt [OpenList](https://github.com/OpenListTeam/OpenList) / [AlistGo/alist](https://github.com/AlistGo/alist).
Dank an [Xhofe](https://github.com/Xhofe) und alle Mitwirkenden des ursprünglichen Projekts:

[![Contributors](https://contrib.rocks/image?repo=OpenListTeam/OpenList)](https://github.com/OpenListTeam/OpenList/graphs/contributors)
