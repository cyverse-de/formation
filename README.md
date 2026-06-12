# Formation

Formation is the CyVerse Discovery Environment's hosted [Model Context Protocol](https://modelcontextprotocol.io/) server. It exposes app discovery and launching, analysis management, and iRODS data access as MCP tools at `/mcp`, with OAuth 2.1 (PKCE) login via Keycloak. All backend operations go through the [terrain](https://github.com/cyverse-de/terrain) API gateway, forwarding each caller's Keycloak token so terrain enforces authorization.

## Features

- **MCP Server**: Streamable HTTP transport at `/mcp`, stateless so any replica can serve any request (see [MCP Server](#mcp-server))
- **OAuth 2.1 Login**: Authorization code flow with PKCE against the existing Keycloak realm, with standard MCP discovery and a dynamic client registration shim
- **Interactive Apps**: List VICE (Visual Interactive Computing Environment) applications, launch them, and wait for the URL to become ready
- **Analysis Management**: Check status, list running analyses, and stop analyses with or without saving outputs
- **Data Store Access**: Browse iRODS collections, read and upload files, create directories, manage AVU metadata, and delete paths with dry-run support

## Requirements

- Go 1.25+
- Keycloak server for authentication
- terrain service (the DE API gateway; it talks to apps, app-exposer, and the data store)

### Development Requirements

- [golangci-lint](https://golangci-lint.run/) - Go linter aggregator

## Building

```bash
go build ./cmd/formation
```

A container image can be built with the multi-stage `Dockerfile`:

```bash
./build.sh --runtime podman
```

## Configuration

Formation is configured via a JSON configuration file. Copy `config.example.json` to `config.json` and edit as needed:

```bash
cp config.example.json config.json
```

The config file path defaults to `config.json` in the working directory and can be overridden with the `CONFIG_FILE` environment variable. Every setting can also be overridden by an environment variable (e.g. `KEYCLOAK_SERVER_URL`, `TERRAIN_BASE_URL`, `OUTPUT_ZONE`); environment variables take precedence over the config file.

### Configuration File Structure

```json
{
  "keycloak": {
    "server_url": "https://keycloak.example.com",
    "realm": "cyverse",
    "mcp_client_id": "formation-mcp",
    "mcp_scopes": "openid profile email",
    "ssl_verify": true
  },
  "services": {
    "terrain_base_url": "http://terrain"
  },
  "application": {
    "output_zone": "iplant",
    "user_suffix": "@iplantcollaborative.org",
    "vice_domain": ".cyverse.run",
    "path_prefix": "/formation",
    "public_base_url": "https://de.example.org/formation",
    "mcp_launch_max_wait": 540,
    "mcp_browse_byte_limit": 1048576,
    "vice_url_check_timeout": 5.0,
    "vice_url_check_retries": 3,
    "vice_url_check_cache_ttl": 5.0
  }
}
```

### Configuration Sections

**keycloak**: Keycloak authentication settings
- `server_url`: Keycloak server URL (required)
- `realm`: Keycloak realm name (required)
- `mcp_client_id`: Public (no-secret) Keycloak client shared by all MCP users (required, env: `MCP_CLIENT_ID`)
- `mcp_scopes`: Space-separated scopes advertised in the MCP OAuth metadata (default: `openid profile email`, env: `MCP_SCOPES`)
- `ssl_verify`: Enable SSL verification (default: true)

**services**: Backend service URLs
- `terrain_base_url`: Base URL of the terrain API gateway (default: `http://terrain`, env: `TERRAIN_BASE_URL`)

**application**: Application behavior settings
- `output_zone`: iRODS zone used when generating analysis output directories (required; env: `OUTPUT_ZONE`; the legacy `irods.zone` JSON key still works as a fallback)
- `user_suffix`: Username suffix to strip from integrator usernames
- `vice_domain`: Domain suffix for VICE applications
- `path_prefix`: URL path prefix stripped from incoming requests when present (the gateway forwards paths like `/formation/mcp` unrewritten); all routes also serve at `/`
- `public_base_url`: Formation's externally visible base URL including the path prefix (e.g. `https://de.cyverse.org/formation`); used to build the OAuth resource identifier and discovery documents (required, env: `PUBLIC_BASE_URL`)
- `mcp_launch_max_wait`: Hard cap in seconds on `launch_app_and_wait`'s `max_wait` (default: 540); keep it below the gateway's idle timeout
- `mcp_browse_byte_limit`: Maximum bytes the `browse_data` tool reads from a file (default: 1048576)
- `vice_url_check_timeout`: Timeout for VICE URL checks in seconds
- `vice_url_check_retries`: Number of retries for VICE URL checks
- `vice_url_check_cache_ttl`: Cache TTL for VICE URL check results in seconds

## Usage

### Starting the Server

```bash
# Default port 8000
go run ./cmd/formation

# Custom port and log level
go run ./cmd/formation --listen-port 8080 --log-level debug
```

Besides `/mcp` and its OAuth discovery routes, the server exposes only an HTML landing page
at `/` (with MCP client setup instructions); Kubernetes probes hit `/` and rely on the status
code.

## MCP Server

Formation hosts a [Model Context Protocol](https://modelcontextprotocol.io/) server at `/mcp`
(streamable HTTP transport, stateless), replacing the retired stdio-based
[formation-mcp](https://github.com/cyverse-de/formation-mcp) project. MCP clients such as
Claude Code and Claude.ai connectors authenticate with the OAuth 2.1 authorization code flow
(PKCE) against the existing Keycloak realm, using a single shared **public** client.

```bash
# Claude Code
claude mcp add --transport http formation https://de.example.org/formation/mcp
```

### Tools

| Tool | Description |
|------|-------------|
| `whoami` | Report the caller's account info and data-store paths (home, trash, default output folder) from terrain's bootstrap |
| `list_apps` | List available applications, optionally filtered by name |
| `get_app_parameters` | Get an app's parameters, types, and defaults |
| `launch_app_and_wait` | Launch an app; for VICE apps, wait for the URL to become ready. Reports missing required parameters instead of launching blind |
| `get_analysis_status` | Check an analysis's status and VICE URL readiness |
| `list_running_analyses` | List the user's running analyses |
| `stop_analysis` | Stop an analysis, optionally saving outputs |
| `browse_data` | List an iRODS directory or read a file |
| `create_directory` | Create an iRODS collection with optional metadata |
| `upload_file` | Upload text content to an iRODS path with optional metadata |
| `set_metadata` | Set or replace AVU metadata on a path |
| `delete_data` | Delete a file or directory, with dry-run support |

The Python server's `open_in_browser` tool was dropped: a hosted server cannot open a local
browser, and the launch/status tools already return the VICE URL as a link.

### OAuth discovery

Unauthenticated requests to `/mcp` get a `401` with a `WWW-Authenticate` header pointing at
the RFC 9728 protected resource metadata. From there, clients discover the authorization
server metadata (an RFC 8414 facade whose authorize/token endpoints are Keycloak's real realm
endpoints) and a dynamic client registration shim (`POST /oauth/register`) that accepts any
registration and always returns the shared public `mcp_client_id`, so no per-user client
setup is needed. Nothing is stored; Keycloak validates redirect URIs at authorization time.

Discovery documents are served at the prefixed and bare paths (e.g.
`/formation/.well-known/oauth-protected-resource/mcp`) as well as the RFC root forms
(e.g. `/.well-known/oauth-protected-resource/formation/mcp`). The root forms require gateway
rules routing `/.well-known/{oauth-protected-resource,oauth-authorization-server,openid-configuration}/formation*`
to formation; clients that follow the `WWW-Authenticate` header (including the Claude family)
work without them.

### Keycloak setup

Create a public client in the realm (suggested id `formation-mcp`):

1. **Client authentication:** off (public client). **Standard flow:** on. **Direct access grants:** off.
2. **Advanced settings → Proof Key for Code Exchange:** `S256`.
3. **Valid redirect URIs:** `https://claude.ai/api/mcp/auth_callback`,
   `https://claude.com/api/mcp/auth_callback`, plus local-client callbacks. Claude Code uses a
   random localhost port, which needs the prefix wildcard `http://localhost*` — note that this
   is broad; enumerate exact URIs instead if your policy requires it. The registration shim
   logs every redirect URI clients ask for, to help maintain this list.
4. **Web origins:** `+` (or explicit origins).

Authorization at `/mcp` accepts any valid realm **user** token; every tool acts as the
token's user, and the user's own token is forwarded to terrain. Service-account tokens are
rejected with a 401 — without a real user behind them there is no data-store identity to act
as.

## Development

### Code Style

The project uses standard Go tooling:

```bash
# Format code
gofmt -w .

# Lint
golangci-lint run ./...

# Vet
go vet ./...
```

### Testing

```bash
# Run all tests
go test ./...

# Run tests for one package
go test ./internal/handlers/

# Run tests matching a pattern
go test -run TestLaunch ./internal/handlers/
```

## History

Formation was originally a Python/FastAPI REST API for app launching and data access, rewritten in Go as a drop-in replacement, then retargeted from calling the apps/app-exposer services and iRODS directly to fronting the terrain API gateway: each caller's bearer token is forwarded so the DE services enforce authorization, and formation holds no rodsadmin credentials. Consequences of the terrain retargeting that remain visible through the MCP tools:

- A submission's `email` field is stripped instead of forwarded; the notification email always comes from the token's claims.
- Metadata reads exclude system AVUs (attributes starting with `ipc`), and writes to them are rejected with "Access denied" — enforced by data-info behind terrain, not by formation itself.
- Deletions move items to the DE trash instead of permanently removing them.

The REST API was removed once its last consumer (portal-conductor) moved to calling terrain directly; the MCP server is now formation's sole interface. With it went `/login`, `/user`, `/apps*`, `/app/launch/*`, `/data/*`, the Swagger UI at `/docs`, and service-account authentication (Keycloak token-exchange impersonation is no longer used or required). `GET /` remains as an HTML landing page and the health-check target — probes should rely on the status code, not the body.
