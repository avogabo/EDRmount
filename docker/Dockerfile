# syntax=docker/dockerfile:1

FROM golang:1.24 AS build
WORKDIR /src
COPY . .

# Build metadata (set by CI via build args)
ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown
RUN go build -ldflags "-X github.com/gaby/EDRmount/internal/version.Version=${VERSION} -X github.com/gaby/EDRmount/internal/version.Commit=${COMMIT} -X github.com/gaby/EDRmount/internal/version.Date=${BUILD_DATE}" -o /out/edrmount ./cmd/edrmount

# Health tool (included in image)
# Pin version for reproducible builds (avoid cache bust / upstream breakage)
RUN go install github.com/javi11/nzb-repair@v0.0.3

FROM ubuntu:jammy AS runtime
RUN apt-get update && apt-get install -y ca-certificates fuse3 curl libglib2.0-0 par2 openjdk-17-jre-headless xz-utils mediainfo libmediainfo0v5 libmms0 locales && rm -rf /var/lib/apt/lists/*

# ---- par2cmdline-turbo (faster PAR2 create/verify/repair) ----
FROM ubuntu:jammy AS par2build
RUN apt-get update && apt-get install -y --no-install-recommends \
    git build-essential autoconf automake libtool pkg-config ca-certificates \
    && rm -rf /var/lib/apt/lists/*
ARG PAR2_TURBO_REF=v1.4.0
RUN git clone --depth 1 --branch "${PAR2_TURBO_REF}" https://github.com/animetosho/par2cmdline-turbo.git /src/par2cmdline-turbo
WORKDIR /src/par2cmdline-turbo
RUN test -f configure || ./automake.sh
RUN ./configure --prefix=/usr/local
RUN make -j"$(nproc)"
RUN make install DESTDIR=/out

FROM runtime

RUN locale-gen en_US.UTF-8 || true
ENV LANG=en_US.UTF-8
ENV LC_ALL=en_US.UTF-8

COPY --from=build /out/edrmount /usr/local/bin/edrmount
COPY --from=build /go/bin/nzb-repair /usr/local/bin/nzb-repair
# Install turbo par2 tools (par2create/par2/...) overriding base package when present
COPY --from=par2build /out/usr/local/bin/ /usr/local/bin/

# ---- FileBot (portable) ----
ARG FILEBOT_VERSION=5.1.6
RUN mkdir -p /opt/filebot \
  && curl -L -o /tmp/filebot.tar.xz "https://get.filebot.net/filebot/FileBot_${FILEBOT_VERSION}/FileBot_${FILEBOT_VERSION}-portable.tar.xz" \
  && tar -xJf /tmp/filebot.tar.xz -C /opt/filebot \
  && rm -f /tmp/filebot.tar.xz \
  && chmod +x /opt/filebot/filebot.sh \
  && ln -sf /opt/filebot/filebot.sh /usr/local/bin/filebot

# ---- nyuu (Antidote2151/Nyuu-Obfuscation) ----
# Nyuu is node-based and OPTIONAL for local builds/tests.
ARG INSTALL_NYUU=1
RUN if [ "$INSTALL_NYUU" = "1" ]; then \
    set -e; \
    for i in 1 2 3; do \
      echo "apt attempt $i"; \
      apt-get update && \
      apt-get install -y --no-install-recommends --fix-missing nodejs npm git python3 make g++ && \
      break; \
      sleep 5; \
    done; \
    rm -rf /var/lib/apt/lists/*; \
    npm install -g --production --unsafe-perm "https://github.com/Antidote2151/Nyuu-Obfuscation.git"; \
    rm -rf /usr/local/bin/ngpost /opt/ngpost; \
  else \
    echo "Skipping nyuu install (INSTALL_NYUU=0)"; \
  fi

# runtime dirs
RUN mkdir -p /config /host /cache /backups
EXPOSE 1516

# FUSE requires these at runtime:
# - /dev/fuse
# - --cap-add SYS_ADMIN
# - potentially apparmor:unconfined on Unraid

ENTRYPOINT ["/usr/local/bin/edrmount","-fuse","-config","/config/config.json"]
