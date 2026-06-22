# LFX One Newsletter Service

A Go microservice in the LFX v2 platform that owns project-scoped newsletter
persistence, recipient resolution, email dispatch, and the draft → sent state
transition. All APIs are scoped under `/projects/{project_uid}/...`.

## Responsibilities

- Persist newsletter drafts and sent history (CloudNativePG-backed Postgres).
- Resolve recipient lists from committees over NATS request/reply to
  lfx-v2-committee-service, authorized against the request's project.
- Dispatch newsletters by fanning out per-recipient `send_email` requests to
  lfx-v2-email-service over NATS, flipping the draft to `status=sent` only when
  at least one delivery is accepted.
- Track opens via a local pixel endpoint and aggregate engagement analytics.
- Expose a project-scoped HTTP REST API consumed by the authoring UI.

> AI content generation continues to live in the authoring UI; this service does
> not proxy AI calls.

## Quick Start

Two supported paths for running the service locally:

- **Path A — Go binary against host Postgres.** Fastest inner loop. The
  service runs on the host; Postgres is whatever you already have installed
  (Homebrew, Postgres.app, Docker, etc.).
- **Path B — Helm + CloudNativePG on OrbStack/kind.** Mirrors production:
  the CNPG operator provisions an in-cluster Postgres and the chart wires
  everything together.

### Prerequisites

- Go 1.25+
- A running PostgreSQL 16+ instance (Path A) **or** OrbStack/kind with `kubectl`,
  `helm` 3.8+, and [`ko`](https://ko.build) (Path B)
- A reachable NATS server (`NATS_URL`) exposing the committee, project, and
  email-service subjects — the service starts without them being live, but
  recipient resolution, project metadata, and email dispatch will fail

---

### Path A — run the binary against host Postgres

**1. Create the database.**

```bash
psql postgres://localhost/postgres -c 'CREATE DATABASE newsletters;'
```

The service applies its DDL idempotently at startup (`internal/schema/schema.sql`);
you do **not** need to run any SQL files manually.

**2. Set the required environment variables.**

```bash
export DATABASE_URL='postgres://<your-user>@localhost:5432/newsletters?sslmode=disable'
export NATS_URL='nats://localhost:4222'                # committee / project / email subjects
export PUBLIC_API_BASE_URL='http://localhost:8080'     # builds the open-tracking pixel URL
export REQUIRE_USER_AUTH=false                         # local only — production must verify JWTs
export LOG_LEVEL=debug
```

`sslmode=disable` is required for a vanilla Homebrew Postgres install, which
ships without TLS; pgx defaults to requiring SSL.

**3. Build and run.**

```bash
make run
```

On a successful start you should see something like:

```text
level=INFO msg="schema applied" tables=...
level=INFO msg="newsletter-api listening" addr=:8080
```

**4. Smoke-test.**

```bash
curl -s http://localhost:8080/livez && echo
# → ok
```

If you see `missing required env vars: DATABASE_URL`, the env vars above are not
set in the shell you ran `make run` from — `make` does **not** load your shell rc.

---

### Path B — Helm + CloudNativePG on OrbStack

**1. Install the CloudNativePG operator (once per cluster).**

```bash
make helm-install-operators
```

This installs the operator into `cnpg-system`. It is cluster-wide and only
needs to be installed once. Helm validates resources against installed CRDs
before applying any release, so the operator must exist before the umbrella
or standalone chart is installed — that's why it is not a subchart.

**2. Build the image with ko.**

```bash
make ko-build
```

This produces `ko.local/newsletter-api:local`. OrbStack shares the local
Docker image cache with Kubernetes, so no manual `kind load` or registry push
is needed.

**3. Create your local values override.**

```bash
cp charts/lfx-v2-newsletter-service/values.local.yaml.example \
   charts/lfx-v2-newsletter-service/values.local.yaml
```

The example file pins the chart to `database.mode=cluster+database`, points
`image.repository` at `ko.local/newsletter-api`, disables `requireUserAuth`,
and disables the NetworkPolicy for easier debugging. Adjust `app.nats.url` to
point at your local NATS server if needed.

**4. Install the chart.**

```bash
make helm-install-local
```

Watch the operator provision the cluster, then the deployment come up:

```bash
kubectl get cluster,database,pods -n lfx --context orbstack
```

Once the pod is `1/1 Running`, the service has already applied its schema.
Tail the logs to confirm:

```bash
kubectl logs -n lfx -l app.kubernetes.io/name=lfx-v2-newsletter-service \
  --tail=50 --context orbstack
# → level=INFO msg="schema applied" ...
# → level=INFO msg="newsletter-api listening" addr=:8080
```

**5. Smoke-test via port-forward.**

```bash
kubectl port-forward -n lfx svc/lfx-v2-newsletter-service 18080:8080 \
  --context orbstack &

curl -s localhost:18080/livez && echo
# → ok
```

---

### Other install modes

The chart supports three `database.mode` values; pick the right Make target:

| Target                        | `database.mode`     | When to use                                                              |
| ----------------------------- | ------------------- | ------------------------------------------------------------------------ |
| `make helm-install-local`     | (from values.local) | Local OrbStack/kind dev (defaults to `cluster+database`)                 |
| `make helm-install-cnpg`      | `cluster+database`  | Standalone CNPG install — chart provisions both the Cluster and Database |
| `make helm-install-external`  | `external`          | Connect to an existing Postgres via a Kubernetes Secret                  |

`external` mode requires a secret in the target namespace whose key (default
`url`) holds the `DATABASE_URL`. Set `database.external.secretName` to that
secret's name in your values override.

### Teardown

```bash
make helm-uninstall                           # remove the chart release
kubectl delete namespace lfx --context orbstack  # remove the CNPG cluster + PVCs
```

The CNPG operator itself is left installed (it is cluster-wide). To remove it:
`helm uninstall cnpg -n cnpg-system`.

For Path A, drop the local database:

```bash
psql postgres://localhost/postgres -c 'DROP DATABASE IF EXISTS newsletters;'
```

## Key Technologies

- **Language**: Go 1.25+
- **HTTP**: stdlib `net/http` with Go 1.22+ mux pattern
- **Database**: PostgreSQL via [pgx](https://github.com/jackc/pgx) + [bun](https://bun.uptrace.dev),
  provisioned by [CloudNativePG](https://cloudnative-pg.io) in cluster
- **Schema**: single embedded `schema.sql` applied idempotently on startup (CREATE … IF NOT EXISTS), serialized across pods via a Postgres advisory transaction lock
- **Observability**: OpenTelemetry (traces, metrics, logs) + slog structured logging
- **Container**: Chainguard distroless images
- **Orchestration**: Kubernetes with Helm charts

## Architecture

```text
cmd/newsletter-api/
├── main.go                   # bootstrap: OTel, DB pool, schema, HTTP, graceful shutdown
└── service/
    ├── config.go             # env var reads — single source of truth
    └── implementations.go    # wires infrastructure into service structs

internal/domain/
├── model/                    # Newsletter (project-scoped), Status, CommitteeMember
├── port/                     # interfaces: NewsletterRepository, CommitteeClient, ProjectMetadataClient, EmailDispatcher
└── errors.go                 # ErrNotFound, ErrVersionMismatch, ErrInvalidRequest, ErrAlreadySent, ErrForbidden

internal/service/
├── newsletter.go             # CRUD + validation + state transitions
└── send_orchestrator.go      # project-scoped recipient resolution, email fan-out
                              # over NATS, open-pixel injection, durable send intent

internal/repository/
└── postgres.go               # bun-backed NewsletterRepository with optimistic locking

internal/schema/
├── schema.go                 # //go:embed schema.sql + Apply()
└── schema.sql                # consolidated DDL (CREATE … IF NOT EXISTS)

internal/handler/
├── http.go                   # Routes(), JSON helpers
├── drafts.go                 # project-scoped newsletter CRUD handlers
├── send.go                   # send / test-send / recipients handlers
├── health.go                 # /livez and /readyz
└── middleware.go             # JWKS auth, request log

internal/infrastructure/
├── observability/            # OTel SDK + slog handler
└── nats/                     # NATS clients: committee, project, email dispatcher

pkg/api/
└── newsletter.go             # public DTOs (snake_case V2 contract)

charts/lfx-v2-newsletter-service/   # Helm chart with three database.mode options
```

## Build Commands

```bash
make build           # compile to bin/lfx-v2-newsletter-service/newsletter-api
make test            # go test -race
make check           # fmt + lint + license-check + go vet
make docker-build    # build OCI image
make helm-templates  # render Helm chart locally
```

## Database modes

The Helm chart supports three database modes (matching the upstream CloudNativePG
example):

| Mode               | Description                                                                                 |
| ------------------ | ------------------------------------------------------------------------------------------- |
| `external`         | Connect to an existing Postgres via a Kubernetes Secret containing `DATABASE_URL`           |
| `database`         | Create a CloudNativePG `Database` CR pointing at an existing `Cluster`                      |
| `cluster+database` | Create both a `Cluster` and a `Database` CR (standalone deployment without an umbrella)     |

`external` is the default for the standalone chart and the recommended mode for
production (per-service Postgres roles with least-privilege secrets).

## HTTP API

All newsletter routes are project-scoped under `/projects/{project_uid}`.

| Method | Path                                                          | Description                                  |
| ------ | ------------------------------------------------------------ | -------------------------------------------- |
| GET    | `/livez`                                                     | liveness probe                               |
| GET    | `/readyz`                                                    | readiness probe (DB ping + NATS)             |
| POST   | `/projects/{project_uid}/newsletters`                        | create draft                                 |
| GET    | `/projects/{project_uid}/newsletters`                        | list newsletters for the project            |
| GET    | `/projects/{project_uid}/newsletters/{newsletter_uid}`       | fetch newsletter (returns ETag)             |
| PUT    | `/projects/{project_uid}/newsletters/{newsletter_uid}`       | update draft (requires If-Match)            |
| DELETE | `/projects/{project_uid}/newsletters/{newsletter_uid}`       | delete draft                                 |
| POST   | `/projects/{project_uid}/newsletters/{newsletter_uid}/send`  | resolve recipients and dispatch the send    |
| POST   | `/projects/{project_uid}/newsletters/recipient-count`        | preview unique recipient count               |
| POST   | `/projects/{project_uid}/newsletters/recipients`             | preview recipient list                       |
| POST   | `/projects/{project_uid}/newsletters/test-send`              | dispatch a single test email                 |
| GET    | `/projects/{project_uid}/newsletter-analytics/{newsletter_uid}` | per-newsletter analytics (opens, recipients) |
| GET    | `/projects/{project_uid}/newsletter-opens/{newsletter_uid}`  | open-tracking pixel (unauthenticated GIF)    |

Optimistic concurrency control: every draft carries an integer `version`
column atomically incremented on each `UPDATE`. `GET` returns
`ETag: "<version>"`; `PUT` requires `If-Match: "<version>"` and returns
`412 Precondition Failed` on a mismatch.

## Related Services

| Service                          | Relationship                                                            |
| -------------------------------- | ----------------------------------------------------------------------- |
| `lfx-v2-committee-service`       | Recipient resolution over NATS (`list_members`, `get_project` scoping)  |
| `lfx-v2-project-service`         | Project name/slug for email chrome over NATS (`get_name`, `get_slug`)   |
| `lfx-v2-email-service`           | Per-recipient email dispatch and engagement analytics over NATS         |
| Authoring UI                     | HTTP client; proxies project-scoped UI requests to this service         |
