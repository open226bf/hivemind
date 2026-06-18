# Contributing to Hivemind

Thanks for taking the time to contribute! This document explains how to get set
up, the conventions we follow, and how to propose changes.

Hivemind spans three repositories — [`hivemind`](https://github.com/open226/hivemind)
(control plane), [`hivemind-agent`](https://github.com/open226/hivemind-agent),
and [`hivemind-gui`](https://github.com/open226/hivemind-gui). This guide lives
in the control-plane repo and applies to all three; the others link back here.

## Code of Conduct

This project adheres to the [Contributor Covenant](CODE_OF_CONDUCT.md). By
participating you are expected to uphold it. Report unacceptable behaviour to the
maintainers (see [SECURITY.md](SECURITY.md) for contact).

## Getting started

```bash
git clone https://github.com/open226/hivemind.git
cd hivemind
cp .env.example .env          # edit DATABASE_URL, AES_KEY, ADMIN_*
go mod download
make run                      # or: go run ./cmd/server
```

You need **Go 1.25+**. A Postgres database is required; a Docker Swarm is
optional (the server falls back to a stub orchestrator). For the UI, see the
[`hivemind-gui`](https://github.com/open226/hivemind-gui) README.

## Development workflow

1. **Open an issue first** for anything non-trivial, so we can agree on the
   approach before you invest time.
2. Branch from `main`: `git checkout -b feat/short-description`.
3. Make focused commits — one logical change each.
4. Keep the build green: `make test && go vet ./... && staticcheck ./...`.
5. Open a pull request against `main` and fill in the template.

### Commit messages

We use [Conventional Commits](https://www.conventionalcommits.org/):

```
feat: add per-cluster name uniqueness
fix: surface real orchestrator cause instead of 'not configured'
refactor: centralise handler error mapping
docs: document the agent enrollment flow
test: cover tunnel reconnect resilience
```

Types: `feat`, `fix`, `refactor`, `docs`, `test`, `chore`, `perf`, `ci`, `build`.

## Coding standards

- **Format & lint:** code must pass `gofmt`, `go vet`, and `staticcheck` with no
  findings. Run `goimports` to keep imports tidy.
- **Architecture:** respect the hexagonal boundaries. The `domain` layer must not
  import adapters; business rules live in `domain`/`application`, I/O in
  `adapters`. New backends plug in behind the existing `ports` interfaces.
- **Tests:** add tests for new behaviour. Bug fixes should come with a regression
  test. Table-driven tests are preferred for pure logic.
- **Errors:** wrap with `%w`, map to HTTP in the handler layer via the shared
  `writeError` helper, never leak internal details to clients.
- **Comments:** explain *why*, not *what*. Don't restate the code.

## Tests

```bash
make test                 # go test ./...
go test ./internal/...    # a subset
```

The frontend uses Vitest via the Angular builder (`npm test`).

## Reporting bugs & requesting features

Use the issue templates. A good bug report includes the version/commit, steps to
reproduce, expected vs. actual behaviour, and relevant logs.

## License

By contributing, you agree that your contributions will be licensed under the
[Apache License 2.0](LICENSE).
