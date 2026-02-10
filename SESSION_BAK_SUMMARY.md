# sessionsBAK — distilled summary (EDRmount)

This file captures high-signal context from the previous long development session logs (sessionsBAK) without importing the whole chat.

## Why the old agent stopped responding
- The session log contains provider errors like:
  - `You have hit your ChatGPT usage limit (plus plan). Try again in ~XXXX min.`
- There were also occasional `context_length_exceeded` and intermittent provider `server_error` entries.

## Repo + image publishing outcome
- Repo created and pushed to GitHub.
- SSH key had to be added to GitHub (initial `Permission denied (publickey)` until key was registered).
- A GitHub Actions workflow was added to build+push Docker image to GHCR.
- Result: image published to GHCR so Unraid can install via `image: ghcr.io/<owner>/edrmount:latest`.

## Runtime paths / concepts that caused confusion
- `/host/inbox/nzb` is an **input** directory for `.nzb` files.
- `/host/mount/raw` is a **FUSE virtual output** view (not the same as the inbox).
- FUSE mount points in code:
  - `/host/mount/raw`
  - `/host/mount/library-auto`
  - `/host/mount/library-manual`

## Requested improvements
- V1.1: first-run should generate a minimal `/config/config.json` if missing (so container boots, then user configures via UI).
- Improve README to explain mapping and avoid `raw/raw` confusion.

## Safety note
- The old session contains pasted secrets (tokens/passwords). Do not copy them into durable notes or commit them.
