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
| `serve`     | HTTP MCP endpoint at `/mcp` and the read-only pages under `/app/`, every request verified by Access JWT | `MAXOS_DATABASE_URL`, `MAXOS_ACCESS_TEAM_DOMAIN`, `MAXOS_ACCESS_AUD`      |
| `mcp-stdio` | MCP over stdio for local clients (actor `local`, source `mcp-stdio`) | `MAXOS_DATABASE_URL`                                                      |

The database URL carries no password: it is read from `PGPASSWORD` (`make` sets it from `POSTGRES_PASSWORD`, compose sets it for the `maxos` service), so `/`, `@` and `%` are safe in the password. Single-quote the value in `.env` (`POSTGRES_PASSWORD='...'`): `.env` is read by both bash and compose, and unquoted `&`, `$`, spaces or `#` are interpreted differently by each. When running binaries by hand, use `MAXOS_DATABASE_URL=postgres://maxos@127.0.0.1:55432/maxos?sslmode=disable` and export `PGPASSWORD`.

`MAXOS_HTTP_ADDR` defaults to `:8080`. `make tunnel-up` additionally starts `cloudflared` and needs `TUNNEL_TOKEN`.

`MAXOS_TIMEZONE` is optional: an IANA zone name (default `Asia/Taipei`) for where you live. It decides what day it is, so a confirmed transaction dated in the future counts in balances only from its day, and each period of an instalment plan counts from the day it falls due. A budget is compared with what has been spent by that day in the same way: an expense counts toward it from its day on. A name that is not a zone stops the command at start.

## Web pages

`maxos serve` also serves a read-only view of the ledger under `/app/`, in Traditional Chinese: the balances (總覽), transactions, drafts waiting to be confirmed, credit card statements, budgets and spending, with charts drawn on the server as inline SVG. There is no JavaScript, no CDN and no request to any other site; every response carries `Content-Security-Policy: default-src 'self'`, `X-Content-Type-Options: nosniff`, `Referrer-Policy: no-referrer` and `Cache-Control: no-store`, and only `GET` and `HEAD` are served (anything else is `405`). The page header shows the email of the signed-in user.

The pages need the same Cloudflare Access setup as `/mcp` (issue #2): they sit behind the same Access verifier, so a request without a valid `Cf-Access-Jwt-Assertion` header gets the same `401` as `/mcp`, the `CF_Authorization` cookie alone is not honoured, and there is no switch that turns verification off, locally included. The verifier and the security headers are in front of the whole server, including what its router answers on its own: without a token even `/`, `//app`, `/app/../mcp` or `OPTIONS *` is a `401` and never a redirect, a `404` or a bare `200`, so the paths that exist are not told to anyone who is not signed in. `/mcp` carries the same headers, with `Cache-Control: no-store, no-transform`. The public hostname of the tunnel has to route `/app/*` to the app as well as `/mcp`, and the Access application has to cover both paths. The tests sign their own tokens with a self-signed key (`internal/webtest`); real Access is not reachable from them.

Design, routes and how a write action would be added later: [docs/design/web-readonly.md](docs/design/web-readonly.md).
