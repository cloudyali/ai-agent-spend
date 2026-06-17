# SQLite query layer (sqlc)

The Go in [`../sqlcgen/`](../sqlcgen) is **generated** from `schema.sql` +
`query.sql` by [sqlc](https://sqlc.dev) and **committed**. So you do *not* need
sqlc installed to build or use AgentSpend — only if you change the SQL.

## Regenerate (after editing `schema.sql` / `query.sql`)

```sh
sqlc generate      # run from the repo root; reads ../../../sqlc.yaml
```

## Installing sqlc (only needed to regenerate)

| Method | Command |
|---|---|
| macOS (Homebrew) | `brew install sqlc` |
| Go (any OS, 1.21+) | `go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest` |
| Prebuilt binaries | <https://github.com/sqlc-dev/sqlc/releases> (this repo used **v1.29.0**) |
| Docker | `docker run --rm -v "$(pwd):/src" -w /src sqlc/sqlc generate` |
| CI | the [`sqlc-dev/setup-sqlc`](https://github.com/sqlc-dev/setup-sqlc) GitHub Action |

Match your OS/arch to the download: the build sandbox used
`sqlc_1.29.0_linux_arm64`; on Apple-Silicon macOS use the **`darwin_arm64`** build
(or just `brew install sqlc`).

## Runtime note

The generated code is driver-agnostic (`database/sql`). Actually *running* the
SQLite backend (built with `-tags sqlite`) also needs the pure-Go driver:

```sh
go get modernc.org/sqlite
```
