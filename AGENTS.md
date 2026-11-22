# Repository Guidelines

## Project Structure & Module Organization

`cmd/` contains service entry points; `internal/` contains application, service,
repository, model and shared-package layers. Protobuf sources live in `proto/`;
`pkg/proto/` and mock directories are generated. Tests sit beside Go source.
`dashboard/` contains the application UI; `static/` contains the documentation
site. Deployment configuration lives in `compose*.yaml` and `infra/`.

## Build, Test, and Development Commands

- `go build ./...` and `go vet ./...`: compile and inspect Go packages.
- `make test/short`: run tests without infrastructure dependencies.
- `make test/integration`: run infrastructure-backed integration tests.
- `make tools` installs pinned tooling; `make lint` runs configured checks.
- `docker compose -f compose.dev.yaml up --build`: start development services.
- In `static/`, run `npm run validate:docs` and `npm run validate:llms`;
  `npm run build` validates and builds the site.

## Coding Style & Naming Conventions

Use configured gofumpt/goimports via repository-pinned golangci-lint. Go uses tabs;
imports form three groups: standard library, external dependencies, then
`github.com/hitesh22rana/chronoverse`, separated by blank lines.

Wrap long signatures into multiline parameter/result groups with trailing commas;
keep short signatures compact. Use named constants for meaningful states and
operations; new internal operation strings use uppercase values such as `BEGIN`.

Write substantial SQL as multiline raw strings: uppercase keywords, separate
clauses, indented CTEs/subqueries, and one item per line for long lists. Compact
scalar queries and projection fragments are acceptable. Preserve placeholders,
quoted data, comments, predicates and optimizer-sensitive expressions.

Comments explain invariants and trade-offs. Remove obsolete tracking-only labels,
retaining security rationale and lint directives. Regenerate generated files.
Keep standalone documentation and corresponding MDX synchronized; regenerate
LLM content with `npm run generate:llms` in `static/`.

## Testing Guidelines

Use `Test...` names, table-driven cases and existing fixtures. Test behavior rather
than source layout; use `testkit.NormalizeSQL` only for known query shapes.
Run affected database/concurrency tests with `TESTKIT_STRICT=1 go test -race`.
Investigate coverage gaps; coverage alone does not prove concurrency safety.

## Commit & Pull Request Guidelines

Use descriptive prefixes (`fix:`, `style:`, `docs:`). Sign and verify every commit.
Use an existing PR branch/worktree; create new PRs only when requested. Describe
behavior changes, linked issues, validation and limitations. Do not monitor CI
continuously after pushing unless asked.

## Local Records & Safety

Keep private research in `docs/local/`; consult its index/running notes when present.
Exclude it through `.git/info/exclude`, not shared `.gitignore`; never publish
without permission. Preserve cancellation, transaction authority and exact cleanup
receipts. Storage pressure or age does not authorize proof deletion.
