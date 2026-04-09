# Multi-stage build: small image for cheap VPS / home server.
FROM golang:1.22-alpine AS build
WORKDIR /src
RUN apk add --no-cache git ca-certificates
COPY go.mod ./
COPY . .
RUN go mod tidy && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/bot ./cmd/bot

FROM alpine:3.19
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=build /out/bot /app/bot
COPY configs/config.yaml /app/configs/config.yaml
ENV TZ=UTC
EXPOSE 9090
ENTRYPOINT ["/app/bot", "-config", "/app/configs/config.yaml"]
