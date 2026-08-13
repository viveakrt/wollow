FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/wollow-server ./cmd/server

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=build /out/wollow-server /usr/local/bin/wollow-server
VOLUME ["/data"]
ENV WOLLOW_DATA_DIR=/data
ENV WOLLOW_ADDR=:8080
EXPOSE 8080
ENTRYPOINT ["wollow-server"]
