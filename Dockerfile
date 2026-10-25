# syntax=docker/dockerfile:1
# Один образ сборки — два бинарника: api (конвейер + веб) и mocks (внешние источники).
FROM golang:1.24-alpine AS build
WORKDIR /src
ENV CGO_ENABLED=0 GOFLAGS=-trimpath
COPY go.mod go.sum ./
RUN go mod download && go mod verify
COPY . .
RUN go build -ldflags="-s -w" -o /out/api ./cmd/api \
 && go build -ldflags="-s -w" -o /out/mocks ./cmd/mocks

FROM alpine:3.20 AS runtime
RUN apk add --no-cache ca-certificates tzdata wget \
 && addgroup -S app && adduser -S -G app -H app
ENV TZ=Europe/Moscow
USER app

FROM runtime AS api
COPY --from=build /out/api /usr/local/bin/api
EXPOSE 8080
HEALTHCHECK --interval=10s --timeout=3s --retries=5 CMD wget -qO- http://localhost:8080/readyz || exit 1
ENTRYPOINT ["/usr/local/bin/api"]

FROM runtime AS mocks
COPY --from=build /out/mocks /usr/local/bin/mocks
EXPOSE 8090
HEALTHCHECK --interval=10s --timeout=3s --retries=5 CMD wget -qO- http://localhost:8090/healthz || exit 1
ENTRYPOINT ["/usr/local/bin/mocks"]
