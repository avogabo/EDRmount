# syntax=docker/dockerfile:1

FROM golang:1.22 AS build
WORKDIR /src
COPY . .
RUN go build -o /out/edrmount ./cmd/edrmount

FROM ubuntu:jammy
RUN apt-get update && apt-get install -y ca-certificates fuse && rm -rf /var/lib/apt/lists/*
COPY --from=build /out/edrmount /usr/local/bin/edrmount

# runtime dirs
RUN mkdir -p /config /host /cache
EXPOSE 8080

# FUSE requires these at runtime:
# - /dev/fuse
# - --cap-add SYS_ADMIN
# - potentially apparmor:unconfined on Unraid

ENTRYPOINT ["/usr/local/bin/edrmount","-config","/config/config.json"]
