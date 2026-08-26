# Momo Poo

Momo Poo is a small Go web application for recording Momo's bathroom trips. Every event represents a trip outside and records whether the trip included a poo.

## Local Development Without Docker

Go 1.26 or newer is required.

Install `templ` and the Tailwind CSS v4.1+ standalone CLI, then regenerate UI assets after changing templates or styles:

```sh
go install github.com/a-h/templ/cmd/templ@latest
templ generate
tailwindcss -i ./web/static/globals.css -o ./web/static/shadcn.css --minify
```

```sh
mkdir -p data
LISTEN_ADDR=:8081 DATABASE_PATH=./data/momo-poo.db APP_TIMEZONE=Local go run .
```

This runs the same application directly on the host and is the quickest alternative to Docker Compose for local testing. Open <http://localhost:8080>. The health endpoint is available at <http://localhost:8080/healthz>. Stop the server with `Ctrl+C`; the local SQLite database remains in `./data/momo-poo.db`.

## iPhone Notifications

On iOS 16.4 or later, open the application from its public HTTPS URL in Safari, use **Share > Add to Home Screen**, and launch Momo from the new Home Screen icon. Tap **Enable notifications** in the application and accept the iOS permission prompt.

Every trip created from either the web page or API sends a notification to every subscribed iPhone. A trip remains successfully recorded if Apple's push service is temporarily unavailable. Expired subscriptions are removed automatically.

Web Push does not work from a normal Safari tab or over a plain LAN HTTP URL. Use the application's HTTPS Cloudflare Tunnel URL when installing it. Each iPhone must opt in separately.

The application generates its VAPID identity on first startup and stores it in the SQLite database. Keep the existing data-volume backup to retain notification subscriptions and identity; replacing the database requires each iPhone to enable notifications again.

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

Delete a trip by its ID:

```sh
curl --fail-with-body -X DELETE http://localhost:8080/api/v1/trips/123
```

A successful deletion returns `204 No Content`; an ID that does not exist returns `404 Not Found`.

`days` is the number of local calendar days to include, including today. For example, `days=1` returns only the current day and `days=7` returns today plus the previous six days. Calendar boundaries use `APP_TIMEZONE`.

The application has no authentication. Writes are limited to 50 requests per observed source address per local day and reads to 60 requests per observed source address per minute by default. Requests through one reverse proxy or Cloudflare Tunnel intentionally share a bucket. These limits are abuse protection, not access control; use a Cloudflare Access policy if the tunnel should be private.

## Configuration

| Variable | Default | Description |
| --- | --- | --- |
| `LISTEN_ADDR` | `:8080` | Address and port used by the HTTP server. |
| `DATABASE_PATH` | `./data/momo-poo.db` | SQLite database path. Compose sets this to `/data/momo-poo.db`. |
| `APP_TIMEZONE` | `Local` | IANA timezone used for display and `days` boundaries, such as `America/New_York`. |
| `WRITE_LIMIT_PER_DAY` | `50` | Trip creation and deletion attempts allowed per client IP and local day. |
| `READ_LIMIT_PER_MINUTE` | `60` | API list requests allowed per client IP and minute. |
| `VAPID_SUBJECT` | `https://github.com/leejayhsu/momo-poo` | Public HTTPS or `mailto:` contact URI used in Web Push authentication. |
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

## Published Docker Image

GitHub Actions tests and builds the image on pull requests. Pushes to `main` publish multi-platform `linux/amd64` and `linux/arm64` images to GitHub Container Registry:

```sh
docker pull ghcr.io/leejayhsu/momo-log:latest
```

The workflow publishes these tags:

- `latest` for the current `main` branch
- `sha-<commit>` for each published commit
- Semantic version tags when a Git tag such as `v1.2.3` is pushed (`1.2.3` and `1.2`)

The workflow uses the repository's automatic `GITHUB_TOKEN`; no registry credentials or Actions secrets need to be configured. If the package is not public after its first publication, change its visibility under the package's settings on GitHub.

For a home server using `/docker/appdata` for persistent storage, use the provided image-based Compose file:

```sh
docker compose -f compose.home-server.yaml up -d
```

It publishes the application at `http://host-address:8090` and stores SQLite data in `/docker/appdata/momo-log/data` on the host. Adjust the host port, timezone, or volume path in `compose.home-server.yaml` as needed.
