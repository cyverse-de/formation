# Formation

Formation is an MCP (Model Context Protocol) server for the CyVerse Discovery
Environment. It is an MCP-optimized alternative to the Terrain API: a thin,
flat-JSON bridge over the DE's `apps` and `app-exposer` services and the iRODS
data store, exposed as MCP tools for use by AI agents and automated tooling.

It is written in Go using the official
[MCP Go SDK](https://github.com/modelcontextprotocol/go-sdk) and serves tools
over the **streamable HTTP** transport. It acts as an OAuth 2.0 resource server:
it validates Keycloak bearer tokens and publishes
[RFC 9728](https://datatracker.ietf.org/doc/rfc9728) protected-resource metadata
so MCP clients can run the standard OIDC authorization-code flow against
Keycloak. The legacy Keycloak password-grant login is preserved as a plain REST
endpoint.

## Tools

| Tool | Purpose |
|------|---------|
| `list_apps` | List DE apps available to the user, with filters (job type, name, description, integrator, date ranges). |
| `get_app_parameters` | Get the parameter group definitions needed to launch an app. |
| `launch_app_and_wait` | Launch an app and, for interactive apps, resolve and return its access URL. |
| `get_analysis_status` | Get an analysis's status and, for interactive apps, whether its URL is ready. |
| `list_running_analyses` | List the user's analyses filtered by status (default `Running`). |
| `stop_analysis` | Control a running analysis: `save_and_exit`, `exit`, or `extend_time`. |
| `open_in_browser` | Resolve the access URL for an interactive analysis. |
| `browse_data` | List a directory or read a file in the iRODS data store. |
| `create_directory` | Create a directory in the iRODS data store. |
| `upload_file` | Create or overwrite a file in the iRODS data store. |
| `delete_data` | Delete a file or directory (supports dry-run and recursive deletion). |
| `set_metadata` | Set AVU metadata on an iRODS file or directory. |

## Endpoints

| Path | Description |
|------|-------------|
| `POST /mcp` | The MCP streamable-HTTP endpoint. Requires a Keycloak bearer token. |
| `GET /.well-known/oauth-protected-resource` | OAuth 2.0 protected-resource metadata (RFC 9728). Public. |
| `POST /login` | Legacy password-grant login (HTTP Basic). Returns Keycloak's token JSON. |
| `GET /` | Unauthenticated health check. |

All paths are served under `PATH_PREFIX` when one is configured.

## Authentication

Formation supports two complementary flows against the same Keycloak realm:

- **OIDC authorization-code flow (recommended for MCP clients).** Formation is a
  resource server. An unauthenticated request to `/mcp` returns `401` with a
  `WWW-Authenticate` header pointing at the protected-resource metadata. The MCP
  client discovers Keycloak from that metadata and runs the standard
  authorization-code + PKCE flow itself, then calls `/mcp` with the resulting
  bearer token. Formation never hosts an `/authorize` or callback endpoint.
- **Password grant (legacy).** `POST /login` with HTTP Basic credentials
  exchanges a username and password for a Keycloak token via the
  resource-owner-password-credentials grant.

Tokens are validated against the realm's JWKS (RS256, issuer-checked; audience
is not enforced, matching the original service). **Service accounts** — Keycloak
principals whose `preferred_username` begins with `service-account-` — must hold
the `app-runner` realm role; that role maps to a configurable, sanitized
downstream username used when calling backend services.

## Requirements

- Go 1.25+
- Access to the DE `apps` and `app-exposer` services
- An iRODS server (for the data-store tools)
- A Keycloak realm for authentication

## Configuration

Configuration is resolved with environment variables taking precedence over an
optional JSON file (`CONFIG_FILE`, default `config.json`); see
`config.example.json`. Key variables:

```bash
# iRODS
IRODS_HOST=data.cyverse.org
IRODS_PORT=1247
IRODS_USER=service-account
IRODS_PASSWORD=secret
IRODS_ZONE=iplant

# Keycloak
KEYCLOAK_SERVER_URL=https://auth.example.com
KEYCLOAK_REALM=CyVerse
KEYCLOAK_CLIENT_ID=formation
KEYCLOAK_CLIENT_SECRET=secret
KEYCLOAK_SSL_VERIFY=true

# Backend services
APPS_BASE_URL=http://apps:8080
APP_EXPOSER_BASE_URL=http://app-exposer:8080
PERMISSIONS_BASE_URL=http://permissions:8080

# Application
USER_SUFFIX=@iplantcollaborative.org
VICE_DOMAIN=.cyverse.run
PATH_PREFIX=/formation
PUBLIC_BASE_URL=https://de.cyverse.org/formation   # advertised in OAuth metadata
SERVICE_ACCOUNTS_ONLY=false
SERVICE_ACCOUNT_USERNAMES='{"app-runner":"de-service-account"}'

# Server
LISTEN_ADDR=:8080
```

Secrets (`KEYCLOAK_CLIENT_SECRET`, `IRODS_PASSWORD`) should be supplied via the
environment or the JSON config secret, not via CLI flags.

## Building and running

```bash
# Run locally (reads config from the environment / config.json)
go run ./cmd/formation

# Build a binary
go build -o formation ./cmd/formation

# Build the container image (uses build.sh -> Dockerfile)
./build.sh -i harbor.cyverse.org/de/formation -t v1.0.0
```

## Development

```bash
go build ./...        # compile
go vet ./...          # vet
golangci-lint run     # lint
go test ./...         # unit tests
```

Tests are table-driven and run against in-memory and `httptest` servers; the
OIDC verifier is tested with a stub discovery/JWKS server and signed tokens. A
live iRODS server is not required for the unit tests.
