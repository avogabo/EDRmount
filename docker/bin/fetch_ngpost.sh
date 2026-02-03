#!/usr/bin/env bash
set -euo pipefail

VERSION="v4.16"
ASSET="ngPost_v4.16_cmd-x86_64.AppImage"
BASE="https://github.com/mbruel/ngPost/releases/download/${VERSION}"
URL="${BASE}/${ASSET}"

mkdir -p docker/bin/.cache
cd docker/bin/.cache

if [ ! -f "${ASSET}" ]; then
  echo "Downloading ${URL}"
  curl -L -o "${ASSET}" "${URL}"
fi

echo "Extracting AppImage"
chmod +x "${ASSET}"
./"${ASSET}" --appimage-extract >/dev/null

# AppRun is the CLI entrypoint
cp -f squashfs-root/AppRun ../ngpost
chmod +x ../ngpost

echo "ngpost ready at docker/bin/ngpost"
