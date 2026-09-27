<div align="center">
  <img src="../web/public/logo-64.png" width="96" height="96" alt="Mocca" />

  <p><em>Mocca is een zelfgehoste mediaserver voor je NAS: een REST-API met byte-range-streaming en optioneel een ingebouwde beheerconsole.</em></p>

  <img src="https://img.shields.io/badge/Go-1.27-00ADD8?logo=go&logoColor=white" alt="Go 1.27" />
  <a href="https://github.com/azhai/mocca"><img src="https://img.shields.io/badge/GitHub-azhai%2Fmocca-181717?logo=github" alt="Repository" /></a>
  <a href="../LICENSE"><img src="https://img.shields.io/badge/license-AGPL--3.0-blue" alt="Licentie: AGPL-3.0" /></a>
</div>

---

- [English](../README.md) | [中文](./README_cn.md) | [日本語](./README_ja.md) | [한국어](./README_ko.md) | [Deutsch](./README_de.md) | Nederlands | [Français](./README_fr.md) | [Español](./README_es.md) | [Русский](./README_ru.md) | [العربية](./README_ar.md)
- [API-referentie](./API.md) · [Auth & media](./API_AUTH_MEDIA.md) · [Architectuur](./BACKEND.md) · [Licentie](../LICENSE)

## Over Mocca

Mocca serveert een mediacollectie via HTTP. Het is één Go-binary —— geen CGO, geen Node, geen CDN.
`make full` bouwt de API plus een ingebouwde beheerconsole, `make api` alleen de API.

## Twee builds

Eén broncodeboom, twee binaries. Het enige verschil is een build-tag.

| | Volledig | Alleen API |
| --- | --- | --- |
| Build | `make full` (of `go build ./`) | `make api` (of `go build -tags noweb ./`) |
| Resultaat | `bin/mocca` | `bin/mocca-api` |
| REST-API + streaming | ja | ja (de app-routes zijn identiek) |
| `passwd`-commando | ja | ja |
| Beheerconsole `/admin/` | ja (ingebouwd) | **nee** —— `web/public/` wordt niet meegecompileerd |
| Beheer-endpoints (opslag, upload, bewerken, cover/screenshot, instellingen, gebruikers) | ja | **nee** —— de hele groep wordt niet geregistreerd (HTTP 404) |

## Snelstart

```bash
make full
./bin/mocca          # luistert standaard op :8000
```

1. Open `http://localhost:8000/admin/` en registreer het eerste account —— dat wordt automatisch beheerder.
2. Voeg opslag toe: mountpad `/media`, driver `local`, root naar je mediamap.
3. Afspelen: `http://localhost:8000/d/media/movie.mp4?token=<jouw-token>`.

Beheerderswachtwoord kwijt of verkeerd opgeslagen? De CLI is de weg terug —— die zit in **beide** builds:

```bash
./bin/mocca-api passwd -u admin            # nieuw wachtwoord wordt van stdin gelezen
./bin/mocca-api passwd -u admin -p 'nieuw-wachtwoord'
```

## Configuratie

Mocca leest `.env` uit de werkmap (andere locatie via `MOCCA_ENV_FILE=/path/to/xxx.env`) en
systeemomgevingsvariabelen met het voorvoegsel `MOCCA_`. Volgorde: ingebouwde standaardwaarden < omgeving < `.env`.

| `.env`-sleutel | Omgevingsvariabele | Standaard | Betekenis |
| --- | --- | --- | --- |
| `ADDR` | `MOCCA_ADDR` | `:8000` | Luisteradres |
| `DATA_DIR` | `MOCCA_DATA_DIR` | `data` | Hier staat de database; covers en indexbestanden staan apparaatzijde naast je media |
| `JWT_SECRET` | `MOCCA_JWT_SECRET` | `mocca-dev-secret` | Signatuursleutel voor tokens —— wijzig deze in productie |
| `ADMIN_PASSWORD` | `MOCCA_ADMIN_PASSWORD` | `@Mocca/1` | Wachtwoord van de eerste beheerder, alleen als er nog geen beheerder is |

## Functies

- **Opslag** —— koppel lokale mappen en SMB-(Samba-)shares; onbeperkt aantal mountpunten.
- **Byte-range-streaming** —— `GET /d/<pad>` ondersteunt `Range`, meerdere bereiken, `HEAD` en conditionele requests, dus spelers kunnen spoelen en hervatten.
- **Media-metadata** —— titel, duur, cover, omschrijving en meerdere auteurs per item; soorten: video / audio / afbeelding.
- **Covergenerator** (alleen volledige build) —— een cover komt van een admin-upload of een ffmpeg-frame op een gekozen tijdstip (`/api/fs/shot` voor één bestand, `/api/fs/patch` voor een hele map); die wordt naar 400×300 PNG geschaald en **apparaatzijde** naast de mediatheek bewaard onder `<opslagroot>/.mocca/ab/cd/<rest-van-sha1>.png`.
- **Beheerconsole** (alleen volledige build) —— ingebouwd, werkt offline: opslag, gebruikers, metadata, mapwachtwoorden en bulk-upload met een eigen voortgangsbalk per bestand.
- **Accounts & rechten** —— beheerder / gebruiker / gast; een gebruiker ziet uitsluitend het eigen `base_path`; 12 vooraf ingestelde avatars (afbeeldingen uploaden wordt bewust niet ondersteund).
- **Mapwachtwoorden** —— één keer instellen op een bovenliggende map en de hele substructuur (inclusief streaming) vraagt erom; opgeslagen als bcrypt.
- **Reacties, danmaku, favorieten** —— reacties zonder lengtelimiet, danmaku max. 50 tekens en gesorteerd op afspeeltijd, favorieten per gebruiker zonder duplicaten.
- **Beheer** —— `passwd` als noodoplossing, één statische binary, alle configuratie in `.env`.

## Licentie

[AGPL-3.0](../LICENSE).

## Bijdragers

- [@azhai](https://github.com/azhai) —— Mocca zelf
- Iedereen die een issue meldde of een patch stuurde

De API-conventies van Mocca (statische wachtwoordhash, uniforme response-envelope) en een deel van de documentatie komen uit het oorspronkelijke project [OpenList](https://github.com/OpenListTeam/OpenList) / [AlistGo/alist](https://github.com/AlistGo/alist).
Dank aan [Xhofe](https://github.com/Xhofe) en alle bijdragers van het oorspronkelijke project:

[![Contributors](https://contrib.rocks/image?repo=OpenListTeam/OpenList)](https://github.com/OpenListTeam/OpenList/graphs/contributors)
