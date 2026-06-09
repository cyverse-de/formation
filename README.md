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

### Differences from the original Python service

The Go rewrite intentionally departs from the Python/FastAPI implementation in
a few places:

- **No analysis-details or job-type discovery tools.** The Python
  `GET /apps/analyses/{id}/details` (full submission record) and
  `GET /apps/job-types` endpoints have no MCP equivalents.
  `get_analysis_status` returns status and URL readiness only, and the valid
  job types (`VICE`, `DE`, `OSG`, `Tapis`) are documented in the `list_apps`
  schema.
- **Metadata replacement is per-attribute.** With `replace=true`,
  `set_metadata` and `upload_file` replace existing values only for the
  attributes being set. The Python service removed *all* existing AVUs first;
  the new behavior preserves unrelated and system-managed metadata (e.g.
  `ipc_UUID`, which users cannot recreate).
- **`SERVICE_ACCOUNTS_ONLY` covers every tool.** Authentication happens once at
  the `/mcp` endpoint, so the flag also gates the data-store tools; the Python
  service only enforced it on the apps routes.
- **Dry-run deletes respect the non-empty guard.** `delete_data` with
  `dry_run=true` on a non-empty directory without `recurse=true` returns the
  same error a real delete would, instead of a would-delete preview, so the
  preview always matches the real outcome.
- **File reads are capped.** `browse_data` returns at most 8 MiB of file
  content per call; page through larger files with `offset`/`limit`.
- **`list_apps` paginates the apps service.** Name searches and pagination are
  passed through server-side, and the client-side-only filters (description,
  integrator, dates, job type) page through the full corpus instead of
  truncating at the first 1000 apps as the Python service did.

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

## Connecting Claude Code

Formation is hosted centrally; clients connect to it over HTTPS and never run it
locally. The MCP endpoint is `<public-base-url>/mcp` — for example
`https://qa.cyverse.org/formation/mcp`.

### Quick start (OAuth)

```bash
claude mcp add --transport http formation https://qa.cyverse.org/formation/mcp
```

Then, inside Claude Code, run `/mcp`, select `formation`, and choose
**Authenticate**. Claude Code reads formation's protected-resource metadata,
discovers Keycloak, and runs the authorization-code + PKCE flow in your browser;
once you log in, the `mcp__formation__*` tools become available. Use
`claude mcp list` to confirm the connection.

Add `-s user` to make the server available in every project, or `-s project` to
write a shared `.mcp.json` into the current repository.

### Pre-registered OAuth client

The quick start relies on Keycloak allowing Dynamic Client Registration. If it
doesn't (Claude Code reports *"Incompatible auth server: does not support
dynamic client registration"*), register a client once in the realm and point
Claude Code at it. A public client is sufficient:

- **Client authentication:** off (public client — PKCE secures the flow)
- **Standard flow:** on (the authorization-code flow Claude Code uses)
- **Direct access grants:** off (Claude Code does not use the password grant)
- **Valid redirect URIs:** `http://localhost:8080/callback`

```bash
claude mcp add --transport http \
  --client-id <keycloak-client-id> \
  --callback-port 8080 \
  formation https://qa.cyverse.org/formation/mcp
```

The redirect URI is a loopback address on the *user's own machine*: Claude Code
briefly listens there to receive the OAuth callback (RFC 8252), so it is
unrelated to where formation is hosted. Keep the registered redirect URI and
`--callback-port` in sync; prefer an exact URI over a `*` wildcard. For a
confidential client, add `--client-secret` (or set `MCP_CLIENT_SECRET`).

### Static bearer token (quick tests)

Formation accepts any valid Keycloak bearer token, so for a throwaway session you
can skip the browser flow using the legacy login endpoint:

```bash
TOKEN=$(curl -s -u 'username:password' https://qa.cyverse.org/formation/login | jq -r .access_token)
claude mcp add --transport http formation https://qa.cyverse.org/formation/mcp \
  --header "Authorization: Bearer $TOKEN"
```

Keycloak access tokens are short-lived, so this suits quick checks rather than
ongoing use.

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
IRODS_CONN_IDLE_TTL=600                             # seconds an idle pooled connection is kept
IRODS_MAX_CONNS=100                                # max cached per-user connections (0 = unbounded)

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

# VICE URL readiness probe and subdomain resolution (seconds, except retries)
VICE_URL_CHECK_TIMEOUT=5
VICE_URL_CHECK_RETRIES=3
VICE_URL_CHECK_CACHE_TTL=5
VICE_SUBDOMAIN_RETRIES=5
VICE_SUBDOMAIN_RETRY_DELAY=1

# Server
LISTEN_ADDR=:8080
HTTP_TIMEOUT=30                                     # seconds, for downstream calls
CONFIG_FILE=config.json                            # optional JSON config path
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
