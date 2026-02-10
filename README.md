# EDRmount

EDRmount convierte **NZBs → una biblioteca FUSE** con MKVs “virtuales” que se **descargan on‑demand**, e incluye una **UI web** para **Importar / Subir / Health (reparar)**.
Pensado para que **Plex apunte a `library-auto`**.

## Quickstart (Docker Compose)

```yaml
services:
  edrmount:
    image: edrmount:dev
    container_name: edrmount
    restart: unless-stopped
    ports:
      - "1516:1516"
    privileged: true
    security_opt:
      - label=disable
    volumes:
      - ./edrmount-data/config:/config
      - ./edrmount-data/host:/host:rshared
      - ./edrmount-data/cache:/cache
      - ./edrmount-backups:/backups
```

UI: `http://<HOST>:1516/webui/`

## Volúmenes / Paths

- `/config`: `config.json` + SQLite
- `/host:rshared`: inbox + mounts FUSE
- `/cache`: staging + cache + backups locales de Health
- `/backups`: backups

Mounts (FUSE):
- `/host/mount/raw`
- `/host/mount/library-auto` (Plex)
- `/host/mount/library-manual`

## Funciones (UI)

- **Biblioteca**: navegar `library-auto` / `library-manual`
- **Subida**: upload (ngPost/Nyuu) → NZB a RAW (+ PAR2 local opcional)
- **Importar**: importar NZBs → aparecen MKVs virtuales
- **Health**: escaneo + reparación automática con **PAR2 local** (genera NZB limpio, sin `.par2`)
- **Ajustes**: config + restart
- **Logs**: logs de jobs

## Ajustes recomendados (rutas vigiladas + flujo)

En **Ajustes (Settings)** tienes dos carpetas vigiladas:

- **Watch media** (`watch.media.dir`): recomienda poner aquí **tus descargas** (donde caen los releases antes de subir/importar).
  - Ejemplo: `/host/inbox/media`
- **Watch NZB** (`watch.nzb.dir`): recomienda montar aquí tu **origen de NZBs** (por ejemplo OneDrive con los NZBs del grupo EDR).
  - Ejemplo: `/host/inbox/nzb`

> Idea: *media* = “lo que vas a procesar/subir”; *nzb* = “la cola/entrada de NZBs”.

## Library-auto (reglas tipo Filebot)

La vista `library-auto` se construye con plantillas configurables (estilo Filebot). Falta por completar la UI para editar todas las reglas, pero el backend ya soporta:

- `library.movie_dir_template`, `library.movie_file_template`
- `library.series_dir_template`, `library.season_folder_template`, `library.series_file_template`

Puedes personalizarlas en `config.json` (o vía UI cuando esté completa) para adaptar tu estructura de biblioteca.

## Primer arranque (first install)

Si `/config/config.json` no existe, EDRmount crea un **config.json mínimo** (sin secretos) para que el contenedor pueda arrancar y luego termines la configuración desde la UI.

## Notas importantes

- PAR2 se **guarda local** (no se sube al release).
- Health usa `.health.lock` para evitar doble reparación en RAW compartido.
- No publiques `config.json` con credenciales.
