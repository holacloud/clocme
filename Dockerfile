FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X github.com/holacloud/clocme/config.VERSION=${VERSION}" -o /clocme .

FROM alpine:3.22
# git is required at runtime: clones and blob reads shell out to it.
# rsvg-convert + a font render the PNG social previews (og:image).
# safe.directory: the data volume may be owned by a different uid than the
# one running the container (bind mounts) and git would refuse those repos.
RUN apk add --no-cache git ca-certificates rsvg-convert font-dejavu \
 && git config --system safe.directory '*' \
 && adduser -D -u 1000 clocme \
 && mkdir /data && chown clocme /data
COPY --from=build /clocme /app/clocme

# uid 1000 by default; docker-compose runs it with the host uid/gid instead.
USER clocme
WORKDIR /app
ENV DATADIR=/data \
    ADDR=:8080 \
    HOME=/tmp
VOLUME /data
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD wget -q -O /dev/null http://127.0.0.1:8080/healthz || exit 1
ENTRYPOINT ["/app/clocme"]
