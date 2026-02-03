# EDRmount

EDRmount is a Docker-first Unraid service that:

- Watches an inbox for **NZB** files and/or **media files (MKV/dirs)**
- Optionally **uploads media to Usenet (ngpost integrated)** and generates NZBs
- Indexes NZBs into a local catalog
- Exposes content as a **FUSE-mounted filesystem** suitable for Plex/Kodi/etc.
  - `/mount/raw/...`  (release/NZB view)
  - `/mount/library/...` (organized view)

## Library layout (defaults)

### Movies
`Peliculas/{1080|4K}/{Inicial}/{Titulo} ({Año}) tmdb-{tmdbId}.{ext}`

Example:
`Peliculas/1080/A/Avatar (2009) tmdb-19995.mkv`

### Series
`SERIES/{Emision|Finalizadas}/{Inicial}/{Titulo} ({Año}) tvdb-{tvdbId}/Temporada {NN}/{NN}x{NN} - {TituloEpisodio}.{ext}`

## Unraid volumes (recommended)

**Config/state (appdata):**
- `/mnt/user/appdata/EDRmount`  → container: `/config`

**Host data root (user selectable in template):**
- e.g. `/mnt/user/Nubes/USENET` → container: `/host`

Suggested subpaths inside host root:
- `/host/mount` (FUSE mountpoint)
- `/host/inbox/nzb` (NZB inbox)
- `/host/inbox/media` (media inbox)
- `/host/cache` (cache directory, configurable from UI)

## Cache
Cache path is **configurable from the Web UI**. For your setup, default recommendation:
- host: `/mnt/vfs/EDRmount` → container: `/cache`

## Status
Scaffold created:
- minimal Go HTTP server (UI placeholder)
- sample config
- Dockerfile + compose example

Next: jobs (watchers), ngpost integration, SQLite catalog, and FUSE mount.
