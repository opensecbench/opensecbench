# Contributing to OpenSecBench

Thanks for your interest — contributions are welcome, whether that's a bug report, a feature idea,
a documentation fix, or a pull request. OpenSecBench is built by and for AppSec practitioners, so
real-world workflow feedback is as valuable as code.

This is an early-access project under active development, so some rough edges are expected and the
process below is deliberately lightweight. If anything here is unclear, open an issue and ask.

## Ways to contribute

- **Report a bug** — open an issue with enough detail to reproduce: what you did, what you expected,
  what happened, and your OS / provider setup. Redact any secrets, targets, or client data first.
- **Request a feature** — open an issue describing the workflow you're trying to support and why the
  current behaviour falls short. Motivation matters more than a proposed implementation.
- **Improve the docs** — README, ADRs, and inline docs all count.
- **Send a pull request** — see below.

## Read the design first

OpenSecBench is documented decision-first. The [`docs/adr/`](docs/adr/) directory holds architecture
decision records (ADRs) that explain *why* each subsystem works the way it does; see
[ADR-0057](docs/adr/adr-0057-decision-process.md) for when a change needs an ADR and how the process
works. Before a non-trivial change:

- Find the relevant ADR and align your change with it, **or**
- If your change alters a documented decision (or makes a new one), **propose it before building
  big** — open a `Proposed` ADR (copy [`adr-template.md`](docs/adr/adr-template.md)) or an issue
  first, so the design is reviewed before the code. New/changed ADRs land in the same PR; run
  `make adr-index` to refresh the generated index.

Many additions don't require touching the core at all: capabilities, methodologies, and report
templates ship as signed, open-format **extension packages**. See the extension-format ADRs
(`adr-0003`, `adr-0013`, `adr-0014`) if that's the shape of your contribution.

## Development setup

Full build instructions live in the [README](README.md#development). In short, you need the Go
toolchain declared in `go.mod` (currently Go 1.25, auto-managed by `GOTOOLCHAIN`):

```sh
go build ./...   # core packages (the desktop app is excluded behind a build tag)
go test ./...    # or: make test
```

The desktop app (Wails) and the browser frontend have extra prerequisites — see the README.

## Before you open a pull request

CI runs the following, and a PR won't merge until they're green. Run them locally first:

| Check          | Command                        |
| -------------- | ------------------------------ |
| Format         | `gofmt -l .` (must be empty) — or `make fmt` to fix |
| Vet            | `go vet ./...`                 |
| Build          | `go build ./...`               |
| Test (race)    | `go test -race ./...` — or `make test` |
| Lint           | `golangci-lint run` — or `make lint` (golangci-lint v2) |
| Secret scan    | gitleaks (CI runs it; just don't commit secrets) |

Additionally:

- **Keep PRs focused.** One logical change per PR is much easier to review than a mixed bag.
- **Write tests** for new behaviour and bug fixes where practical.
- **Never commit secrets, credentials, real targets, or client data** — not in code, fixtures, or
  test data. Mark intentional test-only secrets so the scanner ignores them (e.g. `//gitleaks:allow`).
- **Don't stub something out silently** — leave a `// TODO:` in code and a matching entry in
  [`TODO.md`](TODO.md).

## Commit and PR style

- This repo uses **Conventional Commits** — `feat(scope): …`, `fix(scope): …`, `docs: …`,
  `chore(ci): …`, `test(scope): …`. Match the existing history.
- Write commit messages that explain the *why*, not just the *what*.
- In the PR description, link the issue or ADR your change relates to.

## Security issues

**Do not** report security vulnerabilities through public issues or pull requests. See
[SECURITY.md](SECURITY.md) for how to report them privately.

## License

By contributing, you agree that your contributions are licensed under the project's
[Apache License 2.0](LICENSE).
