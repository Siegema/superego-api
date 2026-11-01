# Superego API

Superego is a work-memory journal for recalling significant tasks, projects,
roles, and skills. This repository currently contains a buildable Go scaffold,
not a functioning API.

## Layout

```text
cmd/superego/main.go           Application entrypoint and future dependency wiring
internal/accounts/            Superego user identity, handlers, and persistence
internal/work/                Organizations, teams, roles, projects, entries, skills
internal/server/routes.go     HTTP route assembly and shared middleware
internal/database/database.go Shared PostgreSQL connection setup
migrations/                   Future database schema migrations
```

Files in each directory share a package. For example, `internal/work/entry.go`
and `internal/work/repository.go` both use `package work`.

Within a feature package:

- Domain files describe application concepts, such as projects and entries.
- `handler.go` handles HTTP requests and responses, including transport-specific types.
- `service.go`, where needed, coordinates application rules and ownership checks.
- `repository.go` handles persistence: CRUD and feature-specific queries. It is
  not an entrypoint to the source-code repository.

These are responsibilities for future implementation; the scaffold does not yet
define domain types, handlers, services, or repositories. No generic repository
framework or separate package per entity is required.

## Scope and next steps

The command currently exits without starting a server. Authentication, database
connections, endpoints, model fields, and schema migrations are not implemented.
The scaffold adds no dependencies.

When adding the first vertical slice, make private data ownership explicit and
scope reads and writes to the current user. Login may be deferred, but a temporary
development identity must remain local-only and must not be trusted from client
input. Work roles represent employment responsibilities, not authentication roles.

Entries should not require quantified outcomes or company artifact links. Skills
should be reusable across roles, projects, and entries. Exact relationships and
database constraints remain to be designed.

## Local checks

Use the Go version declared in `go.mod` (currently 1.27.3).

```sh
gofmt -l cmd internal
go vet ./...
go test ./...
go build ./...
go run ./cmd/superego
```

`gofmt -l` should produce no output. There are no behavioral tests yet because the
scaffold has no application behavior. Running the built command simply exits.
