# Superego API

Superego is a work-memory journal for recalling significant tasks, projects,
roles, and skills. The first API slice provides PostgreSQL-backed entry CRUD.

**Development only:** authentication is not implemented. The command binds to
`127.0.0.1:8080` by default and uses one fixed development user. Do not expose it through a
public proxy, tunnel, or shared environment. Ownership-scoped queries are in place,
but they are not a substitute for authentication.

## Layout

```text
cmd/superego/main.go           Configuration, dependency wiring, server lifecycle
internal/accounts/            Superego user identity, handlers, and persistence
internal/work/                Entry types, HTTP handlers, persistence; other model placeholders
internal/server/routes.go     HTTP route assembly and shared middleware
internal/database/database.go Shared PostgreSQL connection setup
migrations/                   Explicit, reversible SQL schema migrations
build/                        API/PostgreSQL Containerfiles and Podman Compose stack
```

Files in each directory share a package. For example, `internal/work/entry.go`
and `internal/work/repository.go` both use `package work`.

Within a feature package:

- Domain files describe application concepts, such as projects and entries.
- `handler.go` handles HTTP requests and responses, including transport-specific types.
- `service.go`, where needed, coordinates application rules and ownership checks.
- `repository.go` handles persistence: CRUD and feature-specific queries. It is
  not an entrypoint to the source-code repository.

Entry handlers currently call the repository directly: there is no separate
service layer until application rules need coordination. PostgreSQL access uses
`pgx`; routing uses Go's standard `net/http` package.

## Run locally

Use the Go version declared in `go.mod` (currently 1.27.3) and a PostgreSQL database
(verified with PostgreSQL 17). Create a dedicated empty database, then set an
explicit connection URL and apply the initial migration once:

```sh
export DATABASE_URL='postgres://USER:PASSWORD@127.0.0.1:5432/superego?sslmode=disable'
psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f migrations/0001_entries.up.sql
go run ./cmd/superego
```

Replace the example credentials. `sslmode=disable` is only appropriate for this
local database connection. Do not commit real connection strings. Migrations are
manual, not run automatically at startup; see [migrations](migrations/README.md).
Startup verifies database connectivity and creates the development user
idempotently. SIGINT/SIGTERM initiate graceful shutdown.

### Build and run with Podman Compose

Install Podman and a Compose provider such as `podman-compose`. Run these commands
from the repository root. Both Containerfiles and the Compose configuration live
in `build/`; build contexts point to the repository root.

```sh
# Example credentials for local development only; choose your own password.
export POSTGRES_PASSWORD='superego-local-only'
podman compose -f build/compose.yaml build
podman compose -f build/compose.yaml up -d
podman compose -f build/compose.yaml ps
curl http://127.0.0.1:8080/api/entries
```

The API image uses a multi-stage Go build (matching `go.mod`) and runs a static
binary as a non-root user. PostgreSQL 17 stores its data in a named volume and
applies the initial schema automatically **only when that volume is empty**. The
API waits for PostgreSQL's health check before starting. Both published ports
(`8080` and `5432`) bind only to `127.0.0.1` on the host.

Inside Compose, the API connects to `postgres:5432`, not `localhost`. The password
is passed through `PGPASSWORD`, avoiding URL-encoding issues. `HTTP_ADDR` is set
to `0.0.0.0:8080` inside the API container so port forwarding works; the default
outside Compose remains `127.0.0.1:8080`. Do not publish the API on a public host
interface: authentication is not implemented.

If reusing a volume from the earlier database-only setup, apply the initial
migration **only if it has not already been applied**, then start the API:

```sh
podman compose -f build/compose.yaml up -d postgres
# Wait for PostgreSQL to become healthy before applying the migration.
podman compose -f build/compose.yaml exec -T postgres psql -U superego -d superego -v ON_ERROR_STOP=1 < migrations/0001_entries.up.sql
podman compose -f build/compose.yaml up -d api
```

The explicit Compose project name `superego-api` preserves the previous volume
name when moving the files into `build/`. Later migrations remain manual.
`POSTGRES_PASSWORD` initializes a new volume only; changing the variable later
does not change an existing database password. Keep it set when running Compose
commands.

Useful commands:

```sh
podman compose -f build/compose.yaml logs -f api postgres
podman compose -f build/compose.yaml exec postgres psql -U superego -d superego
podman compose -f build/compose.yaml down
```

`down` retains database data. Adding `--volumes` permanently deletes the database.
If `podman compose` cannot connect to its socket, start the rootless socket with
`systemctl --user start podman.socket`, or use `podman-compose` directly with the
same `-f build/compose.yaml` arguments.

## Entry API

All paths below use `http://127.0.0.1:8080`. `localhost` is also accepted. Other
Host names and unsafe cross-origin browser requests are rejected; CORS is not
enabled. Requests that write entries must use `Content-Type: application/json`.

| Method | Path | Result |
| --- | --- | --- |
| POST | `/api/entries` | Create; `201` with entry and `Location` |
| GET | `/api/entries` | List; `200` with paginated entries |
| GET | `/api/entries/{id}` | Read; `200` with entry |
| PUT | `/api/entries/{id}` | Replace writable fields; `200` with entry |
| DELETE | `/api/entries/{id}` | Delete; `204` with no body |

Create and replace accept:

```json
{
  "title": "Simplified deployment troubleshooting",
  "notes": "Documented the failure modes so teammates could investigate independently.",
  "occurred_at": "2026-01-02T17:00:00Z"
}
```

- `title`: required, trimmed, 1–200 Unicode characters.
- `notes`: optional, defaults to `""`, at most 20,000 Unicode characters.
- `occurred_at`: optional RFC3339 timestamp or `null`; stored as an instant and
  returned in UTC with PostgreSQL microsecond precision.
- Title and notes cannot contain null characters. Unknown fields, multiple JSON
  values, and bodies over 1 MiB are rejected.
- IDs and creation/update timestamps are server-assigned. Entry IDs are positive
  integers represented as **JSON strings** to preserve precision for web clients.
- PUT is a full replacement: omitted notes become empty and an omitted date
  becomes `null`. It preserves `created_at` and advances `updated_at`. Concurrent
  updates are last-write-wins; version checks are not implemented yet.
- Metrics, outcomes, and evidence links are not required. Ownership is supplied
  by the server, never accepted from the client.

```sh
curl -i http://127.0.0.1:8080/api/entries \
  -H 'Content-Type: application/json' \
  --data '{"title":"Mentored a teammate","notes":"Helped them investigate their first production incident."}'
```

Lists are ordered by descending ID (newest-created first, not occurrence date).
Use `limit` (1–100, default 50) and the returned `next_before_id` cursor:

```json
{"entries": [], "next_before_id": null}
```

For a nonempty page, each element is an entry containing `id`, `title`, `notes`,
`occurred_at`, `created_at`, and `updated_at`. Fetch the next page with
`GET /api/entries?limit=50&before_id=CURSOR`. A null cursor means there are no more
results. Unsupported or repeated query parameters are rejected.

Entry-handler errors use `{"error":"message"}`. Invalid input returns `400`,
oversized bodies `413`, unsupported media types `415`, and unexpected persistence
failures `500` without database details. Missing entries and entries belonging to
another owner both return `404`. Routing and browser-protection errors use the
standard HTTP server's error responses. Responses use `Cache-Control: no-store`.

Organizations, teams, roles, projects, skills, their relationships, account APIs,
and login remain deferred. Before adding public access, replace the fixed owner
with a trusted per-request identity and retain all ownership checks.

## Local checks

```sh
gofmt -l cmd internal
go vet ./...
go test -race ./...
go build ./...
```

`gofmt -l` should produce no output. Unit tests do not need a database. To also run
the PostgreSQL integration test:

```sh
export SUPEREGO_TEST_DATABASE_URL='postgres://USER:PASSWORD@127.0.0.1:5432/superego_test?sslmode=disable'
go test -race -count=1 ./...
```

Use a dedicated test database. Integration tests create a uniquely named schema,
apply the up/down migrations, exercise CRUD, pagination, persistence, and
cross-owner isolation, then drop only that schema. The test user needs permission
to create schemas. Without the test URL, the integration test is explicitly
skipped. An optional security check is
`go run golang.org/x/vuln/cmd/govulncheck@latest ./...`.
