# zenmoney-mcp

A [Model Context Protocol](https://modelcontextprotocol.io) server for
[ZenMoney](https://zenmoney.ru) — let Claude (or any MCP client) read your
personal finances and safely record new transactions.

> [!IMPORTANT]
> **Safety first.** This server intentionally exposes **no destructive
> operations**. There is no tool that can delete, archive, edit, or remove
> ZenMoney data — write tools only *add* new transactions.

Inspired by [`artarasov/zenmoney-mcp`](https://github.com/artarasov/zenmoney-mcp)
by Artem Tarasov — thanks for the idea! This implementation is an independent
Go rewrite.

## Tools

| Tool | Access | Purpose |
|---|---:|---|
| `sync_data` | read | Full or incremental API synchronization |
| `list_accounts` | read | Accounts, balances, currencies, and banks |
| `list_categories` | read | Category hierarchy |
| `list_merchants` | read | Search known merchants |
| `list_transactions` | read | Filter transactions by dates/account/category |
| `suggest_category` | read | ZenMoney category suggestion for a payee |
| `add_expense` | write | Add one expense transaction |
| `add_income` | write | Add one income transaction |
| `add_transfer` | write | Add one transfer transaction |

Read tools synchronize automatically. Account/category title resolution rejects
ambiguous matches rather than guessing.

## ZenMoney authentication

Obtain an API token from <https://zerro.app/token> (ZenMoney's wiki explicitly
recommends zerro.app tokens for personal/third-party use). The server uses the
classic ZenMoney synchronization API:

- `POST https://api.zenmoney.ru/v8/diff/`
- `POST https://api.zenmoney.ru/v8/suggest/`

The token is supplied only through `ZENMONEY_TOKEN`. Browser session cookies
are not used.

## Running

### Docker

```bash
docker run -d --name zenmoney-mcp -p 8080:8080 \
  -e ZENMONEY_TOKEN=<your-token> \
  ghcr.io/abrekhov/zenmoney-mcp:latest
```

### stdio mode (local MCP clients)

```bash
ZENMONEY_TOKEN=<your-token> zenmoney-mcp   # or set MCP_TRANSPORT=stdio
```

## Exposing to Claude.ai over HTTP

With `MCP_TRANSPORT=http` (the default in Docker) the server speaks Streamable
HTTP at `/mcp` and ships its own **OAuth 2.1 authorization server**:

- Dynamic Client Registration (RFC 7591) — Claude.ai registers itself on the fly
- Mandatory PKCE (S256), authorization-code + refresh-token flows
- Password consent gate (`MCP_OAUTH_PASSWORD`) — only you can authorize
- `iss` parameter in authorization responses (RFC 9207)
- 1-hour access tokens, rotating 30-day refresh tokens

| Environment variable | Purpose |
|---|---|
| `ZENMONEY_TOKEN` | ZenMoney API token (required) |
| `MCP_TRANSPORT` | `http` (default) or `stdio` |
| `HTTP_ADDR` | Listen address, default `:8080` |
| `MCP_BASE_URL` | Public base URL, e.g. `https://zm.example.com` (required for HTTP) |
| `MCP_OAUTH_PASSWORD` | Password required at the consent page |
| `MCP_SIGNING_KEY` | Stable random string (≥ 32 chars) used to sign tokens |
| `ZENMONEY_API_BASE_URL` | Override ZenMoney API base (advanced) |

### Connect in Claude.ai

1. **Settings → Connectors → Add custom connector**
2. URL: `https://<your-domain>/mcp`
3. Complete the OAuth consent page: enter `MCP_OAUTH_PASSWORD`, click Authorize

Refresh tokens live ~30 days; afterwards Claude asks you to re-authorize.

## Kubernetes (Helm)

The chart is published to GHCR as an OCI artifact:

```bash
helm install zenmoney-mcp oci://ghcr.io/abrekhov/charts/zenmoney-mcp \
  --set config.baseURL=https://zm.example.com \
  --set config.oauthPassword='…' \
  --set secrets.zenmoneyToken='…' \
  --set secrets.signingKey="$(openssl rand -hex 32)" \
  --set ingress.host=zm.example.com
```

See [`charts/zenmoney-mcp/values.yaml`](charts/zenmoney-mcp/values.yaml) for all
options (TLS via cert-manager, optional ingress IP allowlist, existing secrets,
resources, …).

## Development

```bash
go test -race ./...
docker build -t zenmoney-mcp .
```

## License

[MIT](LICENSE) © Anton Brekhov. Inspired by
[`artarasov/zenmoney-mcp`](https://github.com/artarasov/zenmoney-mcp) (MIT).
Not affiliated with ZenMoney.
