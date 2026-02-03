# syntax=docker/dockerfile:1

FROM golang:1.24 AS build
WORKDIR /src
COPY . .
RUN go build -o /out/edrmount ./cmd/edrmount

FROM ubuntu:jammy
RUN apt-get update && apt-get install -y ca-certificates fuse curl libglib2.0-0 && rm -rf /var/lib/apt/lists/*

# ngpost's bundled Qt expects OpenSSL 1.1 (libcrypto.so.1.1/libssl.so.1.1).
# Ubuntu jammy ships OpenSSL 3, so we temporarily add focal to install libssl1.1.
RUN set -e; \
  echo 'deb http://archive.ubuntu.com/ubuntu focal main' > /etc/apt/sources.list.d/focal.list; \
  apt-get update; \
  apt-get install -y --no-install-recommends libssl1.1; \
  rm -f /etc/apt/sources.list.d/focal.list; \
  rm -rf /var/lib/apt/lists/*
COPY --from=build /out/edrmount /usr/local/bin/edrmount

# ---- ngpost (bundled AppImage contents) ----
# We bundle the extracted squashfs-root to avoid system Qt deps.
ARG NGPOST_VERSION=v4.16
ARG NGPOST_ASSET=ngPost_v4.16_cmd-x86_64.AppImage
RUN mkdir -p /opt/ngpost
RUN curl -L -o /tmp/ngpost.AppImage "https://github.com/mbruel/ngPost/releases/download/${NGPOST_VERSION}/${NGPOST_ASSET}" \
    && chmod +x /tmp/ngpost.AppImage \
    && /tmp/ngpost.AppImage --appimage-extract >/dev/null \
    && mv squashfs-root/* /opt/ngpost/ \
    && rm -f /tmp/ngpost.AppImage

# Wrapper so runner can call /usr/local/bin/ngpost
RUN printf '%s\n' \
  '#!/usr/bin/env bash' \
  'set -euo pipefail' \
  'export LD_LIBRARY_PATH="/opt/ngpost/lib:${LD_LIBRARY_PATH:-}"' \
  'exec /opt/ngpost/AppRun "$@"' \
  > /usr/local/bin/ngpost \
  && chmod +x /usr/local/bin/ngpost

# runtime dirs
RUN mkdir -p /config /host /cache
EXPOSE 1516

# FUSE requires these at runtime:
# - /dev/fuse
# - --cap-add SYS_ADMIN
# - potentially apparmor:unconfined on Unraid

ENTRYPOINT ["/usr/local/bin/edrmount","-config","/config/config.json"]
