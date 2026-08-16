FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Trim the debug symbol table: nothing here reads it, and it is roughly a
# third of the binary.
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /out/wollow-server ./cmd/server

FROM alpine:3.20
RUN apk add --no-cache ca-certificates

# The server needs no privileges beyond writing its own data directory, so it
# does not run as root. /data is chowned at build time because a named volume
# inherits the mount point's ownership on first use.
RUN adduser -D -u 10001 wollow \
 && mkdir -p /data \
 && chown wollow:wollow /data

COPY --from=build /out/wollow-server /usr/local/bin/wollow-server

USER wollow
VOLUME ["/data"]
ENV WOLLOW_DATA_DIR=/data
ENV WOLLOW_ADDR=:8080
EXPOSE 8080
ENTRYPOINT ["wollow-server"]
