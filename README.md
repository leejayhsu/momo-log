# Momo Poo

Momo Poo is a small Go web application for recording Momo's bathroom trips. Every event represents a trip outside and records whether the trip included a poo.

## Local Development

Go 1.26 or newer is required.

```sh
mkdir -p data
LISTEN_ADDR=:8080 DATABASE_PATH=./data/momo-poo.db APP_TIMEZONE=Local go run .
```

Open <http://localhost:8080>. The health endpoint is available at <http://localhost:8080/healthz>.

## API

Create a trip that included a poo:

```sh
curl --fail-with-body \
  -X POST http://localhost:8080/api/v1/trips \
  -H 'Content-Type: application/json' \
  -d '{"has_poo":true}'
```

Create a trip that did not include a poo:

```sh
curl --fail-with-body \
  -X POST http://localhost:8080/api/v1/trips \
  -H 'Content-Type: application/json' \
  -d '{"has_poo":false}'
```

List today's trips:

```sh
curl --fail-with-body 'http://localhost:8080/api/v1/trips?days=1'
```

`days` is the number of local calendar days to include, including today. For example, `days=1` returns only the current day and `days=7` returns today plus the previous six days. Calendar boundaries use `APP_TIMEZONE`.

The application has no authentication. Writes are limited to 50 requests per observed source address per local day and reads to 60 requests per observed source address per minute by default. Requests through one reverse proxy or Cloudflare Tunnel intentionally share a bucket. These limits are abuse protection, not access control; use a Cloudflare Access policy if the tunnel should be private.

## Configuration

| Variable | Default | Description |
| --- | --- | --- |
| `LISTEN_ADDR` | `:8080` | Address and port used by the HTTP server. |
| `DATABASE_PATH` | `./data/momo-poo.db` | SQLite database path. Compose sets this to `/data/momo-poo.db`. |
| `APP_TIMEZONE` | `Local` | IANA timezone used for display and `days` boundaries, such as `America/New_York`. |
| `WRITE_LIMIT_PER_DAY` | `50` | Trip creation attempts allowed per client IP and local day. |
| `READ_LIMIT_PER_MINUTE` | `60` | API list requests allowed per client IP and minute. |
| `HOST_PORT` | `8090` | Compose-only host port published for the application. |

## Docker Compose

Start the application:

```sh
docker compose up --build -d
```

Set `APP_TIMEZONE` to your household's IANA timezone when using Docker; a container's `Local` timezone is normally UTC:

```sh
APP_TIMEZONE=America/New_York docker compose up --build -d
```

The service publishes port `8090` on the host, making it reachable at `http://localhost:8090` and from a Cloudflare Tunnel configured to use `http://host-address:8090`. Set `HOST_PORT` to publish a different host port:

```sh
HOST_PORT=8091 docker compose up --build -d
```

Compose bind-mounts `./data` to `/data` in the container. The SQLite database therefore remains on the Docker host at `./data/momo-poo.db` when the container is replaced or removed. Back up the `data` directory to preserve event history.

The container fixes ownership of `/data` at startup, then runs the application process as a non-root user.
