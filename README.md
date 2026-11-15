# maxos

Go + PostgreSQL + MCP server, exposed through a Cloudflare Tunnel and protected by Cloudflare Access.

M0 provides the `inbox_item` table and two MCP tools: `capture` and `inbox_recent`.

## Run

```sh
cp .env.example .env     # set POSTGRES_PASSWORD (and the Access values for `serve`)
make up                  # start PostgreSQL (127.0.0.1:55432)
make migrate             # apply migrations to the dev database
make check               # go vet + golangci-lint + go test against a real Postgres (maxos_test) + sqlc drift check
make sqlc                # regenerate app/*/adapter/out/persistence/postgres/dbgen (Docker)
```

Commands (`maxos <command>`), configured through environment variables:

| Command     | Purpose                                                             | Required env                                                              |
|-------------|---------------------------------------------------------------------|---------------------------------------------------------------------------|
| `migrate`   | Apply migrations                                                    | `MAXOS_DATABASE_URL`                                                      |
| `serve`     | HTTP MCP endpoint at `/mcp`, every request verified by Access JWT   | `MAXOS_DATABASE_URL`, `MAXOS_ACCESS_TEAM_DOMAIN`, `MAXOS_ACCESS_AUD`      |
| `mcp-stdio` | MCP over stdio for local clients (actor `local`, source `mcp-stdio`) | `MAXOS_DATABASE_URL`                                                      |

The database URL carries no password: it is read from `PGPASSWORD` (`make` sets it from `POSTGRES_PASSWORD`, compose sets it for the `maxos` service), so `/`, `@` and `%` are safe in the password. Single-quote the value in `.env` (`POSTGRES_PASSWORD='...'`): `.env` is read by both bash and compose, and unquoted `&`, `$`, spaces or `#` are interpreted differently by each. When running binaries by hand, use `MAXOS_DATABASE_URL=postgres://maxos@127.0.0.1:55432/maxos?sslmode=disable` and export `PGPASSWORD`.

`MAXOS_HTTP_ADDR` defaults to `:8080`. `make tunnel-up` additionally starts `cloudflared` and needs `TUNNEL_TOKEN`.

`MAXOS_TIMEZONE` is optional: an IANA zone name (default `Asia/Taipei`) for where you live. It decides what day it is, so a confirmed transaction dated in the future counts in balances only from its day, and each period of an instalment plan counts from the day it falls due. A name that is not a zone stops the command at start.
