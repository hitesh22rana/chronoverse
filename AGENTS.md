# Chronoverse

Chronoverse is a self-hosted distributed job scheduler and orchestrator. Go HTTP
and gRPC services, Kafka workers and Docker-backed execution nodes run scheduled
and manual workflows. The Next.js dashboard and documentation site are separate
frontend workspaces.

## Engineering Principles & Change Scope

Prefer the smallest complete change. Preserve replay safety, tenant isolation,
lease authority and operational clarity across both Compose and Kubernetes.
Understand the owner and callers before changing a contract; avoid parallel
implementations of existing behavior.

For each change, check the affected HTTP/gRPC contracts, services, workers,
dashboard, configuration, deployment topologies, tests and documentation. State
which surfaces apply and update them together. See [architecture](docs/architecture.md)
and [service boundaries](static/content/docs/engineering/service-boundaries.mdx).
Transport code handles protocol concerns; services own domain authorization and
behavior, repositories own persistence, and runtime adapters own Docker evidence.
Keep dashboard routes thin and domain code under `dashboard/src/features/`.

## Project Structure & Module Organization

`cmd/` contains service entry points; `internal/` contains application, service,
repository, model and shared-package layers. Protobuf sources live in `proto/`;
`pkg/proto/` and mock directories are generated. Tests sit beside Go source.
`dashboard/` contains the application UI; `static/` contains the documentation
site. Deployment configuration lives in `compose*.yaml` and `infra/`.

## Setup, Commands & Validation

Use the Go version in `go.mod`, Node/toolchain versions in CI, and committed npm
lockfiles (`npm ci` separately in `dashboard/` and `static/`). Install Buf on
`PATH`; `make tools` installs Go generators and pinned lint into `.bin/`.
Docker is required for infrastructure tests; integration launchers also use Python.
See the [development workflow](static/content/docs/contributing/development.mdx)
for setup and generation commands.

`make lint`, `make test/short` and `make test/integration` invoke `dependencies`:
they regenerate protobuf output, update `buf.lock` and run `go mod tidy`. Review
resulting diffs. For focused checks with dependencies already synchronized, use
the pinned binary and direct Go commands below.

| Change | Validation from the indicated directory |
| --- | --- |
| Go | Root: `go build ./...`, `go vet ./...`, `make test/short`, `./.bin/golangci-lint run ./...` |
| Database/concurrency | Root: e.g. `TESTKIT_STRICT=1 go test -race ./internal/repository/jobs -count=1`; full infrastructure gate: `make test/integration` |
| Dashboard | `dashboard/`: `npm test`, `npm run lint`, `npm run build` |
| Docs/HTTP reference | `static/`: `npm run generate:llms`, `npm run validate:docs`, `npm run validate:llms`; site changes also warrant `npm test` and `npm run build` |
| Deployment | Root: `make compose/validate`, both `make k8s/render/local` and `make k8s/render/production`; follow the development guide for cluster-dependent dry runs |

Kustomize rendering is offline; kubectl/setup dry runs can inspect the selected
cluster. Check its context first. Run checks appropriate to the change and report
which were skipped; do not describe unavailable infrastructure as a passing gate.

## Worktree & Environment Safety

A Git worktree does not isolate running infrastructure. Compose currently uses a
fixed project name and explicit container names; changing only the project name
does not isolate those resources. Prefer disposable Testcontainers for tests.
Do not stop shared services, remove volumes, reset databases or apply cluster
changes without authorization. Track and stop only processes you started.

Use `.env.example` for configuration names; never commit credentials, private keys,
real session tokens or secret-bearing logs. Preserve authorization and tenant
boundaries. Update configuration references when defaults or validation change.
Recreate only explicitly disposable development databases; deployment data needs
an appropriate migration/rollback plan, not an assumed reset.

## Go Style & Naming Conventions

Use configured gofumpt/goimports via repository-pinned golangci-lint. Go uses tabs;
imports form three groups: standard library, external dependencies, then
`github.com/hitesh22rana/chronoverse`, separated by blank lines.

Wrap long signatures into multiline parameter/result groups with trailing commas;
keep short signatures compact. Use named constants for meaningful states and
operations; new internal operation strings use uppercase values such as `BEGIN`.

## SQL Formatting

Write substantial SQL as multiline raw strings: uppercase keywords, separate
clauses, indented CTEs/subqueries, and one item per line for long lists. Compact
scalar queries and projection fragments are acceptable. Preserve placeholders,
argument order, quoted data/identifiers, casts, null behavior, predicates, lock
order and optimizer-sensitive expressions. Whitespace inside literals is data;
keep line boundaries after `--` comments. Formatting must preserve behavior;
query/index changes require database-backed plan evidence.

## Comments & Generated Files

Comments explain invariants and trade-offs. Remove obsolete tracking-only labels,
retaining security rationale and lint directives. Regenerate generated files.
Change protobuf sources in `proto/` and use `make generate`; regenerate mocks
from their `//go:generate` owners with `make mockgen`. Do not hand-edit outputs.

## Writing & Maintaining Documentation

Update an existing page before adding a new one. Explain user/operator-visible
behavior, prerequisites, limits and recovery steps using runnable examples with
safe placeholders. Separate implemented behavior from proposals; keep private
review evidence and tracking history out of public documentation.

Public documentation lives in `static/content/docs/`. Register new pages and
source references in `static/docs.config.ts`. The public HTTP contract is maintained
in `static/content/openapi.yaml`; it is not generated by the gRPC protobuf target.
Keep both contracts accurate when a change affects both surfaces.

Keep repository references (`docs/`, `README.md`, `infra/k8s/README.md`) and matching
MDX aligned. Explicitly compare mirrored SQL/prose: static validators do not enforce
standalone-guide/MDX equality. Validate executable SQL changes against disposable
infrastructure. Regenerate LLM content from canonical MDX/OpenAPI with
`npm run generate:llms` in `static/`; never edit generated LLM files manually.

## Testing Guidelines

Use `Test...` names, table-driven cases and existing fixtures. Test behavior rather
than source layout; use `testkit.NormalizeSQL` only for known query shapes, never
SQL semantic equivalence or quoted-value assertions. Extend existing tests rather
than adding duplicate coverage or test-only production seams.
Use Go’s testing package, existing assertion helpers, and Vitest in the frontend
workspaces. Database integration tests use `TestIntegration...` names and existing
`internal/pkg/testkit` fixtures.
Investigate coverage gaps; coverage alone does not prove concurrency safety.

## Commit & Pull Request Guidelines

Read the implementation and callers before editing; prefer `rg` for searches.
Preserve unrelated local changes. Use descriptive prefixes (`fix:`, `style:`,
`docs:`). Sign and verify every commit; never bypass signing. Use an existing PR
branch/worktree; create new PRs only when requested. For new work, use descriptive
`feat/`, `fix/`, `refactor/`, `docs/` or `chore/` branches. Describe
behavior changes, linked issues, validation and limitations. Do not monitor CI
continuously after pushing unless asked. Merge or delete branches only when
authorized. Report checks actually run, failures and skipped validation.

## Local Records & Safety

Keep private research in `docs/local/`; consult its index/running notes when present.
Exclude it through `.git/info/exclude`, not shared `.gitignore`; never publish
without permission. Preserve cancellation, transaction authority and exact cleanup
receipts. Timeouts do not prove remote side effects never happened. Storage
pressure, age or a missing daemon does not authorize proof deletion or forced
success; proof GC and accepting-side fencing need their own safety design.
