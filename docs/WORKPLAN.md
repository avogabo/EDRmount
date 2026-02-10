# EDRmount — WORKPLAN

Este documento es la “fuente de verdad” para continuar el trabajo sin depender de sesiones largas de chat.

## Objetivos

- Mantener sesiones cortas (evitar gastar tokens y evitar colapsos por contexto largo).
- Capturar decisiones y próximos pasos aquí (y en issues/PRs pequeños).

## Estado actual (2026-02-10)

### Repositorio / CI

- Repo: `avogabo/EDRmount`
- CI: GitHub Actions construye y publica imagen en GHCR.
- Imagen objetivo para Unraid/Portainer:
  - `ghcr.io/avogabo/edrmount:latest`

### Primer arranque

- El servicio debe arrancar incluso si falta `/config/config.json`.
- Comportamiento esperado: si falta, crear un config mínimo (sin secretos) y seguir.

### UI Ajustes (Settings)

- Watch folders (recomendación):
  - `watch.media.dir` → carpeta de descargas (media a procesar/subir)
  - `watch.nzb.dir` → carpeta de NZBs (p.ej. OneDrive con NZBs del grupo EDR)

- Library-auto (Filebot-ish):
  - Se muestran templates completos + variables disponibles.
  - Hay preview “ejemplos reales” vía endpoint.
  - Se puede editar templates desde Ajustes (inputs) y guardar.

> Nota: `library-manual` NO se toca (dos montajes FUSE: auto y manual).

## Decisiones de producto

- IDs TMDB: **ON por defecto** en templates.
- Carpeta/estructura base: editable, pero defaults actuales son los recomendados.

## Deploy en Unraid (Portainer)

Stack recomendado (FUSE):

```yaml
services:
  edrmount:
    image: ghcr.io/avogabo/edrmount:latest
    container_name: edrmount
    restart: unless-stopped
    ports:
      - "1516:1516"
    privileged: true
    security_opt:
      - label=disable
      - apparmor:unconfined
    volumes:
      - /mnt/user/appdata/edrmount/config:/config
      - /mnt/user/Nubes/USENET:/host:rshared
      - /mnt/cache/edrmount:/cache
      - /mnt/user/appdata/edrmount/backups:/backups
```

Checklist tras deploy:
- Portainer: activar “pull latest image” al redeploy.
- Browser: hard refresh (Ctrl+F5) si UI no cambia.
- Verificar que `config.json` se crea si la carpeta `/config` está vacía.

## Debug (rápido, sin pegar logs enormes)

- Logs (máximo 100–200 líneas):
  - `docker logs --tail 200 edrmount`
- Estado contenedor:
  - `docker ps`
- Si algo no cuadra en UI: hard refresh / incógnito.

## Backlog

### P0
- (UX) Hacer que el panel de Library-auto sea “Filebot-like” (normal vs avanzado) si hace falta.
- (Docs) Explicar claramente: inbox vs raw mount vs library-auto.

### P1
- Endpoint de preview con ejemplos reales (ya existe) → ampliar para múltiples ejemplos / mostrar variables y valores.
- Presets de templates (opcional).

## Notas de seguridad

- Nunca commitear secretos en `config.json`.
- Usar `config.sample.json` para ejemplos.
