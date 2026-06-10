# Formation

Formation is a Go service that provides authenticated access to CyVerse Discovery Environment apps and iRODS data storage with integrated Keycloak authentication. It serves as a bridge between web applications and the DE backend services, offering RESTful APIs for app discovery and launching, analysis management, file browsing, content retrieval, and metadata access.

## Features

- **Authentication**: Secure login via Keycloak OIDC with JWT token support
- **Service Account Support**: Service-to-service authentication with enforced role-based access control (requires "app-runner" role)
- **Interactive Apps**: List and filter VICE (Visual Interactive Computing Environment) applications accessible to authenticated users
- **App Launching**: Submit analyses and control running VICE analyses (extend time, save and exit, exit)
- **File System Access**: Browse iRODS collections and stream file contents
- **Metadata Support**: Access and set iRODS AVU (Attribute-Value-Unit) metadata as HTTP headers
- **Content Type Detection**: Automatic MIME type detection for file responses
- **Pagination**: Support for offset/limit parameters when reading large files and listing apps
- **Permission Checking**: Validates user read/write permissions before granting access
- **Advanced Filtering**: Filter apps by name, description, integrator, job type, and date ranges
- **MCP Server**: Hosted Model Context Protocol server at `/mcp` with OAuth 2.1 (PKCE) login via Keycloak (see [MCP Server](#mcp-server))

## Requirements

- Go 1.25+
- iRODS server access
- Keycloak server for authentication
- apps and app-exposer services

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

The config file path defaults to `config.json` in the working directory and can be overridden with the `CONFIG_FILE` environment variable. Every setting can also be overridden by an environment variable (e.g. `IRODS_HOST`, `KEYCLOAK_SERVER_URL`, `APPS_BASE_URL`); environment variables take precedence over the config file.

### Configuration File Structure

```json
{
  "irods": {
    "host": "irods.example.com",
    "port": "1247",
    "user": "rods",
    "password": "changeme",
    "zone": "iplant",
    "cache_ttl": 0
  },
  "keycloak": {
    "server_url": "https://keycloak.example.com",
    "realm": "cyverse",
    "client_id": "formation",
    "client_secret": "changeme",
    "mcp_client_id": "formation-mcp",
    "mcp_scopes": "openid profile email",
    "ssl_verify": true
  },
  "services": {
    "apps_base_url": "http://apps:8080",
    "app_exposer_base_url": "http://app-exposer:8080",
    "permissions_base_url": "http://permissions:8080"
  },
  "application": {
    "user_suffix": "@iplantcollaborative.org",
    "vice_domain": ".cyverse.run",
    "path_prefix": "/formation",
    "public_base_url": "https://de.example.org/formation",
    "mcp_enabled": true,
    "mcp_launch_max_wait": 540,
    "mcp_browse_byte_limit": 1048576,
    "vice_url_check_timeout": 5.0,
    "vice_url_check_retries": 3,
    "vice_url_check_cache_ttl": 5.0,
    "service_accounts_only": false,
    "service_account_usernames": {
      "app-runner": "de-service-account"
    }
  }
}
```

### Configuration Sections

**irods**: iRODS server connection settings
- `host`: iRODS server hostname
- `port`: iRODS server port
- `user`: iRODS username for service account
- `password`: iRODS password
- `zone`: iRODS zone name
- `cache_ttl`: iRODS client metadata cache lifetime in seconds (default: 0, caching disabled). Leave disabled when running more than one replica — a cached (or cached-negative) entry on one replica hides writes made through another until it expires (env: `IRODS_CACHE_TTL`)

**keycloak**: Keycloak authentication settings
- `server_url`: Keycloak server URL
- `realm`: Keycloak realm name
- `client_id`: OAuth2 client ID
- `client_secret`: OAuth2 client secret
- `mcp_client_id`: Public (no-secret) Keycloak client shared by all MCP users; required when the MCP server is enabled (env: `MCP_CLIENT_ID`)
- `mcp_scopes`: Space-separated scopes advertised in the MCP OAuth metadata (default: `openid profile email`, env: `MCP_SCOPES`)
- `ssl_verify`: Enable SSL verification (default: true)

**services**: Backend service URLs
- `apps_base_url`: Base URL of apps service
- `app_exposer_base_url`: Base URL of app-exposer service
- `permissions_base_url`: Base URL of permissions service (parsed for compatibility; unused)

**application**: Application behavior settings
- `user_suffix`: Username suffix to strip from integrator usernames
- `vice_domain`: Domain suffix for VICE applications
- `path_prefix`: URL path prefix stripped from incoming requests when present (the gateway forwards paths like `/formation/apps` unrewritten); all routes also serve at `/`
- `public_base_url`: Formation's externally visible base URL including the path prefix (e.g. `https://de.cyverse.org/formation`); required when the MCP server is enabled and used to build the OAuth resource identifier and discovery documents (env: `PUBLIC_BASE_URL`)
- `mcp_enabled`: Mount the MCP server at `/mcp` (default: true, env: `MCP_ENABLED`)
- `mcp_launch_max_wait`: Hard cap in seconds on `launch_app_and_wait`'s `max_wait` (default: 540); keep it below the gateway's idle timeout
- `mcp_browse_byte_limit`: Maximum bytes the `browse_data` tool reads from a file (default: 1048576)
- `vice_url_check_timeout`: Timeout for VICE URL checks in seconds
- `vice_url_check_retries`: Number of retries for VICE URL checks
- `vice_url_check_cache_ttl`: Cache TTL for VICE URL check results in seconds
- `service_accounts_only`: When true, disables regular user authentication and only accepts service account authentication (useful for testing)
- `service_account_usernames`: Map of service account role names to usernames used when calling backend services

## Usage

### Starting the Server

```bash
# Default port 8000
go run ./cmd/formation

# Custom port and log level
go run ./cmd/formation --listen-port 8080 --log-level debug
```

### API Endpoints

See [API Endpoints Documentation](docs/API_ENDPOINTS.md) for detailed endpoint documentation including:
- Authentication (login, service accounts)
- Applications (`/apps`, `/app/launch`)
- Analyses (`/apps/analyses`)
- File System Operations (`/data`)
- Response formats

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

Authorization at `/mcp` mirrors the REST API: any valid realm token is accepted, the
apps/analysis tools require the `app-runner` realm role for service accounts, and the data
tools act as the token's user.

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

Formation was originally implemented in Python with FastAPI and rewritten in Go as a drop-in replacement: the REST API, response shapes, and configuration are unchanged. Intentional behavior improvements over the Python version:

- `GET /data` streams file contents instead of buffering whole files in memory.
- `PUT /data` with `replace_metadata=true` replaces only the AVU attributes being set, preserving unrelated AVUs (including system attributes such as `ipc_UUID`).
- `DELETE /data` dry runs report the same error a real delete would for non-empty directories without `recurse=true`.
- `GET /apps` uses real upstream pagination, so results are no longer truncated at 1000 apps when filtering.

Small mechanical differences from FastAPI: malformed query parameters return `400` with a `{"detail": ...}` body instead of pydantic's `422` validation arrays, an invalid date filter returns `400` instead of an unhandled `500`, and `GET /apps/analyses` (without the trailing slash) is served directly instead of being redirected. `GET /` serves an HTML landing page (MCP client setup instructions plus a Swagger UI link) instead of the JSON string `"Hello from formation."`; health checks should use `/` rather than `/docs` and rely on the status code, not the body.

## Documentation

- [API Endpoints](docs/API_ENDPOINTS.md) - Complete API endpoint reference
- [Date Filtering](docs/DATE_FILTERING.md) - Date filter syntax and usage examples

## API Documentation

Interactive Swagger UI is available at `/docs` when the server is running (e.g. `http://localhost:8000/docs`). To authenticate in the UI: open the Authorize dialog, fill in the BasicAuth username/password, execute `POST /login`, then paste the returned `access_token` into the BearerAuth value as `Bearer <token>`.

The OpenAPI spec is generated from [swaggo/swag](https://github.com/swaggo/swag) annotations on the handlers into the committed `apidocs` package. After changing annotations, regenerate with:

```bash
go run github.com/swaggo/swag/cmd/swag@latest init -g cmd/formation/main.go -o apidocs --outputTypes go,json
```
