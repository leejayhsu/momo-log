FROM golang:1.26-alpine AS build

WORKDIR /src

COPY go.* ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/momo-poo .

FROM alpine:latest AS runtime

RUN apk add --no-cache ca-certificates su-exec tzdata \
    && addgroup -S momo-poo \
    && adduser -S -G momo-poo -h /nonexistent -s /sbin/nologin momo-poo \
    && mkdir /data \
    && chown momo-poo:momo-poo /data

COPY --from=build /out/momo-poo /usr/local/bin/momo-poo

ENV LISTEN_ADDR=:8080 \
    DATABASE_PATH=/data/momo-poo.db \
    APP_TIMEZONE=Local \
    GO_ENV=production

EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD wget --quiet --spider http://127.0.0.1:8080/healthz || exit 1

ENTRYPOINT ["sh", "-c", "chown -R momo-poo:momo-poo /data && exec su-exec momo-poo:momo-poo /usr/local/bin/momo-poo"]
