# Single static binary (pure-Go SQLite and PostgreSQL drivers, no cgo).
FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=0.1.0-dev
# The server binary plus client builds for every platform /install.sh serves.
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o /hearth ./cmd/hearth && \
    scripts/build-dist.sh /dist "$VERSION" && mkdir /data

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /hearth /usr/local/bin/hearth
COPY --from=build /dist /usr/local/share/hearth/dist
COPY --from=build --chown=65532:65532 /data /data
# Default: SQLite + blob files in /data. Set HEARTH_DB / HEARTH_BLOBS
# for PostgreSQL / S3 (see deploy/docker-compose.scale.yml).
ENV HEARTH_DATA=/data HEARTH_ADDR=0.0.0.0:8787 HEARTH_DIST_DIR=/usr/local/share/hearth/dist
VOLUME /data
EXPOSE 8787
ENTRYPOINT ["hearth", "server"]
