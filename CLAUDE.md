# Claude Development Guide for LFX V2 Newsletter Service

## Project Overview

The LFX V2 Newsletter Service is a Go microservice in the LFX v2 platform. All
APIs are project-scoped under `/projects/{project_uid}/...`. It owns:

- **Persistence** of newsletter drafts and send history in PostgreSQL (CloudNativePG-backed).
- **Recipient resolution** via NATS request/reply to lfx-v2-committee-service
  (`lfx.committee-api.list_members`), authorized against the request's project.
- **Email dispatch** by fanning out per-recipient `send_email` requests to
  lfx-v2-email-service over NATS, then transitioning the draft to `sent` only
  when at least one delivery is accepted.
- **State transitions** for drafts (draft → sent).
- **Open tracking** via a local pixel endpoint and the `newsletter_opens` table.

> AI content generation continues to live in the authoring UI; this service does
> not proxy AI calls.

## Key Technologies

- **Language**: Go 1.25+
- **HTTP**: stdlib `net/http` with Go 1.22+ mux pattern
- **Database**: PostgreSQL via [pgx](https://github.com/jackc/pgx) + [bun](https://bun.uptrace.dev), provisioned by [CloudNativePG](https://cloudnative-pg.io)
- **Schema**: single embedded `schema.sql` applied idempotently on startup (CREATE … IF NOT EXISTS), serialized across pods via a Postgres advisory transaction lock
- **Auth**: Heimdall-issued JWTs verified via JWKS (`MicahParks/keyfunc`)
- **Observability**: OpenTelemetry (traces, metrics, logs) + slog structured logging
- **Container**: Chainguard distroless images
- **Orchestration**: Kubernetes with Helm charts

## Architecture

```text
cmd/newsletter-api/
├── main.go                   # OTel bootstrap, DB pool, schema, HTTP server, graceful shutdown
└── service/
    ├── config.go             # ALL env var reads — no os.Getenv in other layers
    └── implementations.go    # Wires infrastructure into service structs

internal/domain/
├── model/                    # Pure data: Newsletter (project-scoped), Status, CommitteeMember
├── port/                     # Interfaces: NewsletterRepository, CommitteeClient, ProjectMetadataClient, EmailDispatcher
└── errors.go                 # Sentinel errors: ErrNotFound, ErrVersionMismatch, ErrInvalidRequest, ErrAlreadySent, ErrForbidden

internal/service/
├── newsletter.go             # CRUD + validation + state transitions
└── send_orchestrator.go      # Project-scoped recipient resolution, per-recipient
                              # email fan-out over NATS, open-pixel injection,
                              # durable send-intent + draft → sent transition

internal/repository/
└── postgres.go               # bun-backed NewsletterRepository with optimistic locking

internal/schema/
├── schema.go                 # //go:embed schema.sql + Apply()
└── schema.sql                # Consolidated DDL (CREATE … IF NOT EXISTS)

internal/handler/
├── http.go                   # Routes() + JSON helpers + error mapper
├── drafts.go                 # /newsletters/drafts CRUD
├── send.go                   # /send, /test-send, /recipients, /recipient-count
├── health.go                 # /livez, /readyz
└── middleware.go             # JWKS auth, request log

internal/infrastructure/
├── observability/
│   ├── log.go                # slog + OTel handler init
│   └── otel.go               # OTel SDK bootstrap
└── nats/                     # NATS request/reply clients to sibling services
    ├── client.go             # Shared NATS connection + IsReady (used by /readyz)
    ├── committee_client.go   # list_members + get_project (recipient resolution)
    ├── project_client.go     # get_name / get_slug (email chrome)
    ├── email_dispatcher.go   # send_email + engagement analytics
    └── subjects.go           # Upstream NATS subject constants

pkg/api/
└── newsletter.go             # Public contract: request/response DTOs
```

## Build Commands

```bash
make build       # Compile binary to bin/lfx-v2-newsletter-service/newsletter-api
make test        # Run tests with race detector
make check       # fmt + lint + license-check + go vet
make lint        # golangci-lint
```

## Conventions

### Config injection
All `os.Getenv` calls belong in `cmd/newsletter-api/service/config.go` →
`AppConfigFromEnv()`. Services receive a typed config struct, never call
`os.Getenv` themselves.

### Adding a new endpoint
1. Add the request/response DTO to `pkg/api/newsletter.go`.
2. Add the business-logic method to `internal/service/newsletter.go` or
   `send_orchestrator.go`.
3. Add the handler method to `internal/handler/`.
4. Register the route in `internal/handler/http.go`.

### Error handling
- Domain errors live in `internal/domain/errors.go` (`ErrNotFound`, `ErrVersionMismatch`, `ErrInvalidRequest`, `ErrAlreadySent`, `ErrForbidden`).
- Map domain errors to HTTP status codes in `internal/handler/http.go`.
- Always pass `ctx` for OTel trace correlation.

### Logging
- Use `slog.DebugContext`, `slog.InfoContext`, `slog.WarnContext`, `slog.ErrorContext`.
- Pass `ctx` so OTel trace correlation works.

### Optimistic concurrency control
Every draft row has a `version BIGINT` column. `Update` queries gate on
`id = $1 AND version = $2` and `version = version + 1`. If `RowsAffected = 0`,
follow up with an `Exists` check to distinguish `ErrNotFound` from
`ErrVersionMismatch`. Surface as `ETag: "<version>"` response header and
`If-Match: "<version>"` request header at the HTTP layer.

### License headers
Every `.go` file must start with:
```go
// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT
```

## Related Services

| Service                     | Relationship                                                                 |
| --------------------------- | ---------------------------------------------------------------------------- |
| `lfx-v2-committee-service`  | Recipient resolution over NATS (`list_members`, `get_project` for scoping)   |
| `lfx-v2-project-service`    | Project name/slug for email chrome over NATS (`get_name`, `get_slug`)        |
| `lfx-v2-email-service`      | Per-recipient email dispatch and engagement analytics over NATS              |
| Authoring UI                | HTTP client; proxies project-scoped UI requests to this service              |
