#!/usr/bin/env bash
set -euo pipefail
CFG_DIR=${EDRMOUNT_CFG_DIR:-/home/pulgarcito/.openclaw/workspace/edrmount-data/config}
OUT_DIR=${EDRMOUNT_BACKUP_DIR:-/home/pulgarcito/.openclaw/workspace/edrmount-backups}
KEEP=${EDRMOUNT_CFG_KEEP:-30}

mkdir -p "$OUT_DIR"
TS=$(date +%Y%m%d-%H%M%S)

if [ -f "$CFG_DIR/config.json" ]; then
  cp -a "$CFG_DIR/config.json" "$OUT_DIR/config.json.$TS"
  gzip -f "$OUT_DIR/config.json.$TS"
  echo "OK: $OUT_DIR/config.json.$TS.gz"
fi

# rotate
ls -1t "$OUT_DIR"/config.json.*.gz 2>/dev/null | tail -n +$((KEEP+1)) | xargs -r rm -f
