# EDRmount — Spec (v0)

## Goals
- Unraid-friendly Docker service that mounts Usenet-backed content as a normal filesystem.
- Fast “ingest → visible” latency.
- Fast “playback start” for Plex/Kodi (Range-friendly streaming).
- Web UI for all configuration (providers, upload, rules, performance).

## Non-goals (initially)
- Strong security/privacy hardening (assumed LAN), but avoid obvious footguns (don’t log secrets).

## Core concepts
- **Catalog**: SQLite DB storing virtual paths and file references.
- **Two views**:
  - `raw` = 1:1 NZB/release view.
  - `library` = organized view driven by rules/templates.
- **Jobs**:
  - UploadJob (media → ngpost → nzb)
  - ImportJob (nzb → catalog)
  - OrganizeJob (match metadata → virtual naming)

## Ingest
### NZB Inbox
- Watch `/host/inbox/nzb` (inotify).
- On new `.nzb`:
  - parse and register entries
  - mark job status
  - file becomes visible immediately

### Media Inbox
- Watch `/host/inbox/media`.
- If directory:
  - detect TV vs Movie like existing bash:
    - TV if subfolders (e.g. Season/Temporada patterns) or multiple episode-like names.
    - Movie if single main video file.
- If single file:
  - classify using regex (SxxEyy → TV; (YYYY) → Movie; else NeedsReview).
- For TV: create per-subfolder UploadJobs; max concurrent uploads = 2.

## Upload (ngpost integration)
- ngpost bundled into image.
- UI config:
  - providers/servers for ngpost (or config generator)
  - connections, groups, naming templates
  - output nzb path (internal)
- Job runner captures logs and progress.
- Output NZB fed directly to importer.

## Mount (FUSE)
- Mountpoint: `/host/mount` (inside container), bind-mapped to host.
- Export directories:
  - `/host/mount/raw`
  - `/host/mount/library`
- FUSE operations needed for MVP:
  - Readdir, Getattr, Open, Read, Release.
- Read must support:
  - concurrent readers
  - byte ranges (offset/size)
  - cancellation

## Streaming performance
- Disk cache path is configurable in UI.
- Recommended container mount: `/cache`.
- Host example for your setup: `/mnt/vfs/EDRmount`.
- Read-ahead default: 256MB (tunable).
- Worker pools:
  - NNTP download workers (tunable)
  - Per-stream priority scheduling (active stream > read-ahead > prefetch)
- Provider fallback:
  - multiple providers supported
  - circuit breaker for timeouts

## Metadata matching
- TMDB (movies) and TVDB (series).
- Store IDs and normalized titles in DB.
- UI for manual match/override.

## Web UI
- Dashboard: streams, providers, cache, throughput.
- Library Rules: templates + regex + preview.
- Providers: add/test.
- Ingest: watch folders + job queue.
- Performance: read-ahead, workers, cache size.

## API
- REST endpoints for UI.
- Optional websocket for job progress/streams.

## Deliverables
- v0.1 MVP: NZB ingest + raw view + basic read path w/ cache skeleton + UI skeleton.
- v0.2: media inbox + ngpost integration.
- v0.3: library view + rules + TMDB/TVDB.
