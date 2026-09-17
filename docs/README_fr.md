<div align="center">
  <img src="../web/public/logo-64.png" width="96" height="96" alt="Mocca" />

  <p><em>Mocca est un serveur multimédia auto-hébergé pour votre NAS : une API REST avec streaming par plages d'octets, et une console d'administration intégrée en option.</em></p>

  <img src="https://img.shields.io/badge/Go-1.27-00ADD8?logo=go&logoColor=white" alt="Go 1.27" />
  <a href="https://github.com/azhai/mocca"><img src="https://img.shields.io/badge/GitHub-azhai%2Fmocca-181717?logo=github" alt="Dépôt" /></a>
  <a href="../LICENSE"><img src="https://img.shields.io/badge/license-AGPL--3.0-blue" alt="Licence : AGPL-3.0" /></a>
</div>

---

- [English](../README.md) | [中文](./README_cn.md) | [日本語](./README_ja.md) | [한국어](./README_ko.md) | [Deutsch](./README_de.md) | [Nederlands](./README_nl.md) | Français | [Español](./README_es.md) | [Русский](./README_ru.md) | [العربية](./README_ar.md)
- [Référence API](./API.md) · [Auth & médias](./API_AUTH_MEDIA.md) · [Architecture](./BACKEND.md) · [Licence](../LICENSE)

## À propos

Mocca sert une médiathèque en HTTP. C'est un unique binaire Go —— pas de CGO, pas de Node, pas de CDN.
`make full` produit l'API et une console d'administration intégrée, `make api` produit l'API seule.

## Deux compilations

Une seule base de code, deux binaires. La seule différence est un tag de compilation.

| | Complète | API seule |
| --- | --- | --- |
| Compilation | `make full` (ou `go build ./`) | `make api` (ou `go build -tags noweb ./`) |
| Résultat | `bin/mocca` | `bin/mocca-api` |
| API REST + streaming | oui | oui (routes identiques) |
| Commande `passwd` | oui | oui |
| Console d'administration `/admin/` | oui (intégrée) | **non** —— `web/public/` n'est pas compilé |
| Générateur de couvertures | oui | **non** —— le handler répond seulement « absent de cette compilation » |

## Démarrage rapide

```bash
make full
./bin/mocca          # écoute sur :8000 par défaut
```

1. Ouvrez `http://localhost:8000/admin/` et créez le premier compte —— il devient automatiquement administrateur.
2. Ajoutez un stockage : chemin de montage `/media`, pilote `local`, racine vers votre dossier de médias.
3. Lecture : `http://localhost:8000/d/media/movie.mp4?token=<votre-jeton>`.

Mot de passe administrateur oublié ou mal saisi ? La ligne de commande est le seul recours —— elle est incluse dans **les deux** compilations :

```bash
./bin/mocca-api passwd -u admin            # le nouveau mot de passe est lu sur stdin
./bin/mocca-api passwd -u admin -p 'nouveau'
```

## Configuration

Mocca lit le fichier `.env` du répertoire de travail (autre emplacement via `MOCCA_ENV_FILE=/path/to/xxx.env`) ainsi que
les variables d'environnement préfixées par `MOCCA_`. Priorité : valeurs par défaut < environnement < `.env`.

| Clé `.env` | Variable d'environnement | Défaut | Signification |
| --- | --- | --- | --- |
| `ADDR` | `MOCCA_ADDR` | `:8000` | Adresse d'écoute |
| `DATA_DIR` | `MOCCA_DATA_DIR` | `data` | Répertoire de données : base, couvertures et vignettes |
| `JWT_SECRET` | `MOCCA_JWT_SECRET` | `mocca-dev-secret` | Clé de signature des jetons —— à changer en production |
| `ADMIN_PASSWORD` | `MOCCA_ADMIN_PASSWORD` | `@Mocca/1` | Mot de passe du premier administrateur, uniquement s'il n'en existe aucun |

## Fonctionnalités

- **Stockages** —— montez des dossiers locaux et des partages SMB (Samba) ; autant de points de montage que voulu.
- **Streaming par plages d'octets** —— `GET /d/<chemin>` gère `Range`, les plages multiples, `HEAD` et les requêtes conditionnelles : les lecteurs peuvent se déplacer et reprendre.
- **Métadonnées média** —— titre, durée, couverture, description et plusieurs auteurs par élément ; types vidéo / audio / image.
- **Générateur de couvertures** —— la console dessine une couverture 1280×720 sur un canvas et l'enregistre dans `<répertoire-de-données>/.mocca/covers/` (compilation complète uniquement).
- **Console d'administration** (compilation complète uniquement) —— intégrée, fonctionne hors ligne : stockages, utilisateurs, métadonnées, mots de passe de dossiers et envoi par lots avec une barre de progression par fichier.
- **Comptes & permissions** —— administrateur / utilisateur / invité ; un utilisateur ne voit que son propre `base_path` ; 12 avatars prédéfinis (l'envoi d'images n'est volontairement pas pris en charge).
- **Mots de passe de dossiers** —— défini une fois sur un dossier parent, tout le sous-arbre (streaming compris) l'exige ; stocké en bcrypt.
- **Commentaires, danmaku, favoris** —— commentaires sans limite de longueur, danmaku limités à 50 caractères et triés par instant de lecture, favoris dédupliqués par utilisateur.
- **Exploitation** —— commande de secours `passwd`, binaire statique unique, configuration entièrement dans `.env`.

## Licence

[AGPL-3.0](../LICENSE).

## Contributeurs

- [@azhai](https://github.com/azhai) —— Mocca lui-même
- Toutes celles et ceux qui ont signalé un problème ou envoyé un correctif

Les conventions d'API de Mocca (hachage statique du mot de passe, enveloppe de réponse unifiée) et une partie de la documentation proviennent du projet d'origine [OpenList](https://github.com/OpenListTeam/OpenList) / [AlistGo/alist](https://github.com/AlistGo/alist).
Merci à [Xhofe](https://github.com/Xhofe) et à tous les contributeurs du projet d'origine :

[![Contributors](https://contrib.rocks/image?repo=OpenListTeam/OpenList)](https://github.com/OpenListTeam/OpenList/graphs/contributors)
