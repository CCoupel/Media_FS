# Media_FS — Spécifications fonctionnelles et techniques

## Vue d'ensemble

Media_FS est un système de fichiers virtuel qui expose les bibliothèques de serveurs médias (Jellyfin, Emby, Plex) comme un point de montage navigable dans l'explorateur Windows ou Linux. Les médias apparaissent comme de vrais fichiers, lisibles directement par VLC, MPV ou tout lecteur, et copiables en local.

---

## Architecture générale

```
┌─────────────────────────────────────────────────┐
│         Explorateur Windows / Nautilus           │
└──────────────────┬──────────────────────────────┘
                   │ WinFSP (Windows) / FUSE (Linux)
┌──────────────────▼──────────────────────────────┐
│                Media_FS Core (Go)                │
│                                                  │
│  ┌──────────────┐   ┌───────────────────────┐   │
│  │  VFS Layer   │   │   Metadata Cache       │   │
│  │  (cgofuse)   │   │   (SQLite + BoltDB)    │   │
│  └──────┬───────┘   └───────────────────────┘   │
│         │                                        │
│  ┌──────▼──────────────────────────────────┐    │
│  │        Connector Interface               │    │
│  ├────────────┬────────────┬───────────────┤    │
│  │  Jellyfin  │    Emby    │     Plex       │    │
│  │ Connector  │ Connector  │  Connector     │    │
│  └────────────┴────────────┴───────────────┘    │
│                                                  │
│  ┌──────────────────────────────────────────┐   │
│  │        HTTP Client (parallel chunks)     │   │
│  └──────────────────────────────────────────┘   │
└─────────────────────────────────────────────────┘
                   │ HTTPS / REST
┌──────────────────▼──────────────────────────────┐
│           Serveurs médias distants               │
└─────────────────────────────────────────────────┘
```

---

## Structure du système de fichiers virtuel

Chaque serveur configuré apparaît comme un dossier racine `user@server` avec une icône spécifique au type de connecteur.

```
Z:\
├── cyril@HomeServer\          ← icône Jellyfin
│   ├── Films\
│   │   └── Inception (2010)\
│   │       ├── Inception.mkv        ← stream HTTP (lecture) ou téléchargement
│   │       ├── Inception.nfo        ← métadonnées XML générées à la volée
│   │       ├── poster.jpg           ← artwork depuis l'API
│   │       └── fanart.jpg
│   ├── Séries\
│   │   └── Breaking Bad\
│   │       ├── tvshow.nfo
│   │       ├── poster.jpg
│   │       └── Saison 01\
│   │           ├── S01E01 - Pilot.mkv
│   │           ├── S01E01 - Pilot.nfo
│   │           └── S01E01 - Pilot-thumb.jpg
│   └── Musique\
│       └── Pink Floyd\
│           └── The Wall\
│               ├── album.nfo
│               ├── folder.jpg
│               └── 01 - In the Flesh.flac
└── admin@NAS-Emby\            ← icône Emby
    └── ...
```

### Règles de nommage

- Dossier racine : `{username}@{server_alias}` — alias défini dans la config
- Icône du dossier racine : fichier `desktop.ini` + `{connector}.ico` embarqué dans le binaire
- Nommage des médias : repris tel quel depuis le nom fourni par l'API du serveur
- Nommage des saisons : `Saison XX` (localisé selon la langue du serveur)

---

## Fonctionnalités par priorité

### P0 — MVP

| Fonctionnalité | Détail | Issue |
|---|---|---|
| Montage WinFSP (Windows) | Lecteur `Z:` dans l'explorateur Windows | [#1](https://github.com/CCoupel/Media_FS/issues/1) |
| Montage FUSE (Linux) | Point de montage `/mnt/mediafs` | [#2](https://github.com/CCoupel/Media_FS/issues/2) |
| Connecteur Jellyfin | Auth, navigation, streaming | [#3](https://github.com/CCoupel/Media_FS/issues/3) |
| Connecteur Emby | Adapte Jellyfin, header auth différent | [#4](https://github.com/CCoupel/Media_FS/issues/4) |
| Lecture streaming | HTTP Range, seek depuis VLC/MPV | [#5](https://github.com/CCoupel/Media_FS/issues/5) |
| Copie locale parallèle | N chunks simultanés via HTTP Range | [#6](https://github.com/CCoupel/Media_FS/issues/6) |
| System tray + config UI | Tray menu, UI web localhost | [#7](https://github.com/CCoupel/Media_FS/issues/7) |
| Multi-serveurs + config.yaml | Plusieurs `user@server` simultanés | [#8](https://github.com/CCoupel/Media_FS/issues/8) |

### P1.1 — Métadonnées : attributs standard + sidecar

| Fonctionnalité | Détail | Issue |
|---|---|---|
| Attributs fichiers corrects | Taille réelle, mtime = date d'ajout | [#9](https://github.com/CCoupel/Media_FS/issues/9) |
| Fichiers `.nfo` générés à la volée | Format Kodi XML (movie, tvshow, episode) | [#10](https://github.com/CCoupel/Media_FS/issues/10) |
| Images sidecar | poster, fanart, thumb, folder selon le type | [#11](https://github.com/CCoupel/Media_FS/issues/11) |
| Cache SQLite | TTLs différenciés par type de donnée | [#12](https://github.com/CCoupel/Media_FS/issues/12) |

Format `.nfo` films (Kodi standard) :
```xml
<movie>
  <title>Inception</title>
  <year>2010</year>
  <rating>8.8</rating>
  <plot>...</plot>
  <director>Christopher Nolan</director>
  <genre>Science-Fiction</genre>
  <runtime>148</runtime>
  <uniqueid type="imdb">tt1375666</uniqueid>
</movie>
```

### P1.2 — Windows Shell Properties — [#13](https://github.com/CCoupel/Media_FS/issues/13)

| Fonctionnalité | Détail |
|---|---|
| `IPropertyStore` via WinFSP | Titre, année, réalisateur, note, genre visibles dans l'onglet "Détails" de l'explorateur |
| Filtrage/tri dans Explorer | Permet de trier la bibliothèque par année, note, etc. sans ouvrir les fichiers |
| Propriétés mappées | `System.Title`, `System.Media.Year`, `System.Rating`, `System.Keywords` (genres) |

### P2 — Contraintes

- **Read-only strict** : aucune opération d'écriture ne doit être propagée vers les serveurs distants
- Toute tentative d'écriture (rename, delete, create) retourne `EPERM` / `STATUS_ACCESS_DENIED`
- Les fichiers `.nfo` et images sont **virtuels** (générés en mémoire), jamais écrits sur le serveur

### P3 — Plex — [#14](https://github.com/CCoupel/Media_FS/issues/14)

Même fonctionnalités que Jellyfin/Emby. API différente (Plex Media Server API, authentification via `X-Plex-Token`). Scope isolé, développé séparément.

---

## Téléchargement parallèle (multi-chunk)

Lors d'une copie locale via l'explorateur, Media_FS utilise des HTTP Range requests pour télécharger le fichier en plusieurs chunks simultanés.

```
Fichier 4 Go
├── Chunk 1 : bytes 0      → 999MB    [goroutine 1]
├── Chunk 2 : bytes 1000MB → 1999MB   [goroutine 2]
├── Chunk 3 : bytes 2000MB → 2999MB   [goroutine 3]
└── Chunk 4 : bytes 3000MB → 4000MB   [goroutine 4]
                    ↓ assemblage en ordre
              fichier local final
```

Configuration :
```yaml
download:
  parallel_chunks: 4        # nombre de chunks simultanés (défaut: 4)
  chunk_size_mb: 64         # taille d'un chunk en MB (défaut: 64)
  buffer_size_kb: 256       # buffer de lecture HTTP
```

Le streaming (lecture directe sans copie) reste un flux unique avec support des range requests pour le seek.

---

## Connecteurs

### Interface commune (Go)

```go
type MediaConnector interface {
    // Auth
    Connect(config ServerConfig) error
    Ping() error

    // Navigation
    GetLibraries() ([]Library, error)
    GetItems(libraryID string, parentID string) ([]MediaItem, error)

    // Metadata
    GetItemMetadata(itemID string) (ItemMetadata, error)
    GetArtworkURL(itemID string, artType ArtworkType) (string, error)
    GetNFO(item MediaItem) ([]byte, error)

    // Streaming
    GetStreamURL(itemID string) (string, error)
    GetFileSize(itemID string) (int64, error)
}
```

### Jellyfin / Emby

Les APIs Jellyfin et Emby sont quasi identiques (Jellyfin est un fork d'Emby). Un seul connecteur de base avec des adaptateurs mineurs.

Endpoints clés :
- `GET /Users/{userId}/Items` — liste des éléments
- `GET /Items/{itemId}` — métadonnées d'un item
- `GET /Items/{itemId}/Images/{imageType}` — artwork
- `GET /Videos/{itemId}/stream` — stream vidéo (supporte Range)
- `GET /Audio/{itemId}/stream` — stream audio

Authentification : header `X-Emby-Token` ou `X-MediaBrowser-Token`.

### Plex (P3)

- `GET /library/sections` — bibliothèques
- `GET /library/sections/{id}/all` — contenu
- `GET /library/metadata/{id}` — métadonnées
- Stream via `/library/parts/{id}/file` avec `X-Plex-Token`

---

## Cache et performances

```
┌─────────────────────────────────────┐
│           Cache SQLite local         │
│  ┌──────────────┬──────────────────┐ │
│  │  items       │ TTL: 5 min       │ │
│  │  metadata    │ TTL: 1 heure     │ │
│  │  artwork     │ TTL: 24 heures   │ │
│  │  nfo         │ TTL: 1 heure     │ │
│  └──────────────┴──────────────────┘ │
└─────────────────────────────────────┘
```

- Le cache est local par machine, stocké dans `%APPDATA%\MediaFS\cache.db` (Windows) ou `~/.cache/mediafs/` (Linux)
- Invalidation manuelle : commande `mediafs refresh user@server`
- Les artwork sont mis en cache sur disque (pas en mémoire)

---

## Configuration

Fichier `config.yaml` (emplacement : `%APPDATA%\MediaFS\config.yaml` / `~/.config/mediafs/config.yaml`) :

```yaml
mount:
  drive_letter: "Z"          # Windows uniquement
  mount_point: "/mnt/mediafs" # Linux

servers:
  - alias: "HomeServer"
    type: jellyfin            # jellyfin | emby | plex
    url: "https://jellyfin.home.local:8096"
    username: "cyril"
    api_key: "xxxxxxxxxxxx"
    enabled: true

  - alias: "NAS-Emby"
    type: emby
    url: "http://192.168.1.50:8096"
    username: "admin"
    api_key: "xxxxxxxxxxxx"
    enabled: true

download:
  parallel_chunks: 4
  chunk_size_mb: 64

cache:
  ttl_items_sec: 300
  ttl_metadata_sec: 3600
  ttl_artwork_sec: 86400
```

---

## CLI — [#15](https://github.com/CCoupel/Media_FS/issues/15)

```bash
mediafs mount                     # monte tous les serveurs actifs
mediafs mount user@HomeServer     # monte un serveur spécifique
mediafs umount                    # démonte tout
mediafs umount user@HomeServer    # démonte un serveur
mediafs refresh user@HomeServer   # invalide le cache et rafraîchit
mediafs status                    # état des serveurs montés
mediafs config add                # assistant interactif pour ajouter un serveur
```

---

## Plateformes cibles

| Plateforme | Mécanisme VFS | Prérequis utilisateur |
|---|---|---|
| Windows 10/11 | WinFSP | Installer WinFSP (MSI fourni) |
| Linux | FUSE 3 | `libfuse3` (paquets distrib) |
| macOS | — | Hors scope |

---

## Hors scope

- Écriture vers les serveurs distants (rename, delete, upload)
- Transcoding (les streams sont servis tels quels)
- Lecture de médias DRM
- Interface graphique (CLI uniquement pour la V1)
- Synchronisation hors-ligne automatique
- macOS
