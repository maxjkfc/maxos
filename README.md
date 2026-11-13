# maxos

Go + PostgreSQL + MCP server, exposed through a Cloudflare Tunnel and protected by Cloudflare Access.

M0 provides the `core_inbox` table and two MCP tools: `capture` and `inbox_recent`.

## Run

```sh
cp .env.example .env     # set POSTGRES_PASSWORD (and the Access values for `serve`)
make up                  # start PostgreSQL (127.0.0.1:55432)
make migrate             # apply migrations to the dev database
make check               # go vet + golangci-lint + go test against a real Postgres (maxos_test)
make sqlc                # regenerate internal/db/dbgen (Docker)
```

Commands (`maxos <command>`), configured through environment variables:

| Command     | Purpose                                                             | Required env                                                              |
|-------------|---------------------------------------------------------------------|---------------------------------------------------------------------------|
| `migrate`   | Apply migrations                                                    | `MAXOS_DATABASE_URL`                                                      |
| `serve`     | HTTP MCP endpoint at `/mcp`, every request verified by Access JWT   | `MAXOS_DATABASE_URL`, `MAXOS_ACCESS_TEAM_DOMAIN`, `MAXOS_ACCESS_AUD`      |
| `mcp-stdio` | MCP over stdio for local clients (actor `local`, source `mcp-stdio`) | `MAXOS_DATABASE_URL`                                                      |

`MAXOS_HTTP_ADDR` defaults to `:8080`. `make tunnel-up` additionally starts `cloudflared` and needs `TUNNEL_TOKEN`.
