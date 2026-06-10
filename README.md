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
    "zone": "iplant"
  },
  "keycloak": {
    "server_url": "https://keycloak.example.com",
    "realm": "cyverse",
    "client_id": "formation",
    "client_secret": "changeme",
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

**keycloak**: Keycloak authentication settings
- `server_url`: Keycloak server URL
- `realm`: Keycloak realm name
- `client_id`: OAuth2 client ID
- `client_secret`: OAuth2 client secret
- `ssl_verify`: Enable SSL verification (default: true)

**services**: Backend service URLs
- `apps_base_url`: Base URL of apps service
- `app_exposer_base_url`: Base URL of app-exposer service
- `permissions_base_url`: Base URL of permissions service (parsed for compatibility; unused)

**application**: Application behavior settings
- `user_suffix`: Username suffix to strip from integrator usernames
- `vice_domain`: Domain suffix for VICE applications
- `path_prefix`: URL path prefix stripped from incoming requests when present (the gateway forwards paths like `/formation/apps` unrewritten); all routes also serve at `/`
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

Small mechanical differences from FastAPI: malformed query parameters return `400` with a `{"detail": ...}` body instead of pydantic's `422` validation arrays, an invalid date filter returns `400` instead of an unhandled `500`, and `GET /apps/analyses` (without the trailing slash) is served directly instead of being redirected. The Swagger UI at `/docs` is no longer served; health checks should use `/`.

## Documentation

- [API Endpoints](docs/API_ENDPOINTS.md) - Complete API endpoint reference
- [Interactive Apps Endpoint](docs/INTERACTIVE_APPS_ENDPOINT.md) - Detailed documentation for the `/apps` endpoint
- [Date Filtering](docs/DATE_FILTERING.md) - Date filter syntax and usage examples
