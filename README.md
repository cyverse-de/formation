# Formation

Formation is a FastAPI-based service that provides authenticated access to iRODS data storage with integrated Keycloak authentication. It serves as a bridge between web applications and iRODS file systems, offering RESTful APIs for file browsing, content retrieval, and metadata access.

## Features

- **Authentication**: Secure login via Keycloak OIDC with JWT token support
- **Service Account Support**: Service-to-service authentication with enforced role-based access control (requires "app-runner" role)
- **Interactive Apps**: List and filter VICE (Visual Interactive Computing Environment) applications accessible to authenticated users
- **File System Access**: Browse iRODS collections and retrieve file contents
- **Metadata Support**: Access iRODS AVU (Attribute-Value-Unit) metadata as HTTP headers
- **Content Type Detection**: Automatic MIME type detection for file responses
- **Asynchronous Operations**: Concurrent metadata retrieval and content type detection for improved performance
- **Pagination**: Support for offset/limit parameters when reading large files
- **Permission Checking**: Validates user read permissions before granting access
- **Advanced Filtering**: Filter apps by name, description, integrator, and date ranges

## Requirements

- Python 3.13+
- [uv](https://docs.astral.sh/uv/) - Fast Python package manager
- iRODS server access
- Keycloak server for authentication
- PostgreSQL database

### Development Requirements

- [jq](https://jqlang.github.io/jq/) - Command-line JSON processor (for hooks)
- [ruff](https://docs.astral.sh/ruff/) - Fast Python linter and formatter (installed via uv)

## Installation

This project uses [uv](https://docs.astral.sh/uv/) as the package manager for fast, reliable dependency management.

### Installing uv

```bash
# Install uv (if not already installed)
curl -LsSf https://astral.sh/uv/install.sh | sh

# Or using pip
pip install uv
```

### Project Setup

```bash
# Install dependencies and create virtual environment
uv sync

# Activate virtual environment (optional - uv run handles this automatically)
source .venv/bin/activate
```

## Configuration

Set the following environment variables:

### Database Configuration
- `DB_HOST`: PostgreSQL host
- `DB_PORT`: PostgreSQL port (default: 5432)
- `DB_USER`: Database username
- `DB_PASSWORD`: Database password
- `DB_NAME`: Database name

### iRODS Configuration
- `IRODS_HOST`: iRODS server hostname
- `IRODS_PORT`: iRODS server port
- `IRODS_USER`: iRODS username for service account
- `IRODS_PASSWORD`: iRODS password
- `IRODS_ZONE`: iRODS zone name

### Keycloak Configuration
- `KEYCLOAK_SERVER_URL`: Keycloak server URL
- `KEYCLOAK_REALM`: Keycloak realm name
- `KEYCLOAK_CLIENT_ID`: OAuth2 client ID
- `KEYCLOAK_CLIENT_SECRET`: OAuth2 client secret
- `KEYCLOAK_SSL_VERIFY`: Enable SSL verification (default: true)

### Permissions Service Configuration
- `PERMISSIONS_BASE_URL`: Base URL of permissions service (default: "http://permissions")
- `GROUPER_USER_GROUP_ID`: ID of the public user group (default: "de-users")

### Application Configuration
- `USER_SUFFIX`: Username suffix to strip from integrator usernames (default: "@iplantcollaborative.org")
- `APPS_BASE_URL`: Base URL of apps service (default: "http://apps")
- `APP_EXPOSER_BASE_URL`: Base URL of app-exposer service (default: "http://app-exposer")
- `SERVICE_ACCOUNTS_ONLY`: When set to "true", disables regular user authentication and only accepts service account authentication (default: false). Useful for testing service account authorization.
- `SERVICE_ACCOUNT_USERNAMES`: JSON map of service account role names to usernames used when calling backend services (default: `{}`). If a role is not in the map, the role name itself is used as the username. Example: `{"app-runner": "de-service-account"}`

## Usage

### Starting the Server

```bash
# Development mode with auto-reload (recommended for local development)
uv run fastapi dev main.py

# Production mode
uv run fastapi run main.py

# Custom host and port
uv run fastapi dev main.py --host 0.0.0.0 --port 8080

# Alternative: activate venv manually then run
source .venv/bin/activate
fastapi dev main.py
```

### API Endpoints

#### Authentication

**POST /login**
- Authenticate with username/password using HTTP Basic Auth
- Returns JWT access token for subsequent requests
- Example:
  ```bash
  curl -X POST "http://localhost:8000/login" \
    -u "username:password"
  ```

#### Service Account Authentication

Formation supports service account authentication for service-to-service API calls. Service accounts use Keycloak JWT bearer tokens with specific roles.

**How it Works:**
- Service accounts are identified by tokens where `preferred_username` starts with `"service-account-"`
- The `/apps/` endpoints support both regular user authentication and service account authentication
- Service accounts **must** have the `"app-runner"` role in their Keycloak realm roles (enforced)
- Service accounts without the `"app-runner"` role will receive a `403 Forbidden` error
- Service accounts with the proper role bypass user-level permissions checks

**Example Usage:**
```bash
# Obtain service account token from Keycloak
TOKEN=$(curl -X POST "https://keycloak.example.com/realms/myrealm/protocol/openid-connect/token" \
  -d "grant_type=client_credentials" \
  -d "client_id=my-service-account" \
  -d "client_secret=secret" | jq -r .access_token)

# Use service account token to call API
curl -H "Authorization: Bearer $TOKEN" \
  "http://localhost:8000/apps"
```

**Required Keycloak Configuration:**
1. Create a service account client in Keycloak
2. Enable "Client authentication" and "Service accounts roles"
3. Add the `"app-runner"` role to the service account's realm roles
4. The service account username will automatically have the `"service-account-"` prefix

**Service Account Username Mapping:**

Service accounts use a configurable username when making requests to backend services like the apps service. This allows you to control what username is used for authorization checks in downstream services:

```bash
# Configure username mapping in config.json
{
  "application": {
    "service_account_usernames": {
      "app-runner": "de-service-account"
    }
  }
}

# Or via environment variable
export SERVICE_ACCOUNT_USERNAMES='{"app-runner": "de-service-account"}'
```

**Mapping Behavior:**
- If `"app-runner"` has a mapping: Uses the mapped username (e.g., `"de-service-account"`)
- If no mapping exists: Uses the role name itself (e.g., `"app-runner"`)
- The mapped/fallback username is then sanitized and used in all calls to backend services

**Username Sanitization:**

Service account usernames are automatically sanitized before being sent to backend services. This sanitization applies to **service accounts only** (usernames with `"service-account-"` prefix) - regular user JWTs are not sanitized.

**Sanitization rules:**
- All special characters are removed (hyphens, underscores, dots, etc.)
- Converted to lowercase
- Only letters and numbers (alphanumeric) are retained

**Transformation examples:**
- `"de-service-account"` → `"deserviceaccount"`
- `"app-runner"` → `"apprunner"`
- `"Service_Account_123"` → `"serviceaccount123"`
- `"portal-conductor-service"` → `"portalconductorservice"`

**⚠️ CRITICAL: Implications for Downstream Services**

This sanitization affects how usernames appear in downstream services, particularly for whitelist-based access control:

1. **Apps Service**: Receives the sanitized username via the `user` query parameter
2. **App-Exposer**: Checks the sanitized username against the resource tracking bypass whitelist

**Example - App-Exposer Whitelist Configuration:**

If you configure Formation with:
```json
{
  "service_account_usernames": {
    "app-runner": "de-service-account"
  }
}
```

The sanitized username `"deserviceaccount"` (not `"de-service-account"`) will be sent to app-exposer. Therefore, your app-exposer whitelist must use the sanitized form:

```yaml
# app-exposer config.yml
resource_tracking:
  bypass_users:
    - deserviceaccount      # ✅ Correct - matches sanitized form
    # NOT "de-service-account" - that will not match!
```

**Username Flow:**
```
Formation config: "de-service-account"
    ↓ (sanitization)
Sent to apps/app-exposer: "deserviceaccount"
    ↓ (whitelist check)
App-exposer whitelist: "deserviceaccount" (must match sanitized form)
```

**Debugging Tip:**

Check app-exposer logs to see the actual username being checked:
```
Resource tracking disabled for user deserviceaccount (in bypass whitelist), skipping validation
```

This ensures compatibility with backend system username requirements while maintaining security through consistent username handling.

**Testing Service Account Authentication:**
To test service account authentication in isolation, you can disable regular user authentication:
```bash
# Set environment variable to enable service-accounts-only mode
export SERVICE_ACCOUNTS_ONLY=true

# Or add to config.json:
{
  "application": {
    "service_accounts_only": true
  }
}

# Now only service account tokens will be accepted
# Regular user authentication will return 403 Forbidden
```

#### Interactive Applications

**GET /apps**
- List interactive VICE applications accessible to authenticated user
- Requires Bearer token authentication
- Query parameters:
  - `limit`: Maximum apps to return (1-1000, default: 100)
  - `offset`: Pagination offset (default: 0)
  - `name`: Filter by app name (case-insensitive partial match)
  - `description`: Filter by description (case-insensitive partial match)
  - `integrator`: Filter by integrator username
  - `integration_date`: Filter by integration date (e.g., ">2025-09-29")
  - `edited_date`: Filter by edited date (e.g., "<=2024-12-31")
- See [Interactive Apps Endpoint Documentation](docs/INTERACTIVE_APPS_ENDPOINT.md) for detailed usage
- See [Date Filtering Documentation](docs/DATE_FILTERING.md) for date filter syntax

**Example:**
```bash
# List all accessible apps
curl -H "Authorization: Bearer <token>" \
  "http://localhost:8000/apps"

# Filter by name and date
curl -H "Authorization: Bearer <token>" \
  "http://localhost:8000/apps?name=jupyter&integration_date=>2025-01-01"
```

#### File System Operations

**GET /data/browse/{path:path}**
- Browse iRODS directory contents or retrieve file contents
- Requires Bearer token authentication
- **Note**: Leading slash is automatically added to paths - no need for double slashes in URLs
- Query parameters:
  - `offset`: Starting position for file reading (default: 0)
  - `limit`: Maximum bytes to read (optional)
  - `include_metadata`: Include iRODS metadata in response headers (default: false)
  - `avu_delimiter`: Separator for metadata value/unit pairs (default: ",")

**Examples:**

```bash
# List directory contents
curl -H "Authorization: Bearer <token>" \
  "http://localhost:8000/data/browse/cyverse/home/username"

# Read file with metadata
curl -H "Authorization: Bearer <token>" \
  "http://localhost:8000/data/browse/cyverse/home/username/file.txt?include_metadata=true"

# Read file with pagination
curl -H "Authorization: Bearer <token>" \
  "http://localhost:8000/data/browse/cyverse/home/username/largefile.txt?offset=1000&limit=500"
```

### Response Formats

#### Directory Listing
```json
{
  "path": "/cyverse/home/username",
  "type": "collection",
  "contents": [
    {"name": "file.txt", "type": "data_object"},
    {"name": "subdirectory", "type": "collection"}
  ]
}
```

#### File Content
- Returns raw file content with appropriate Content-Type header
- When `include_metadata=true`, includes `X-Datastore-{attribute}` headers

## Development

### Prerequisites

Development tools required:

```bash
# Install jq (for automated hooks)
# macOS
brew install jq

# Ubuntu/Debian
sudo apt-get install jq

# Fedora/RHEL
sudo dnf install jq

# Ruff is automatically installed via uv sync
# but can also be installed globally:
uv tool install ruff
```

### Code Style

The project uses [ruff](https://docs.astral.sh/ruff/) for linting and formatting:

```bash
# Format and lint code
uv run ruff format
uv run ruff check --fix

# Check for issues without fixing
uv run ruff check

# Format specific files
uv run ruff check --fix routes/apps.py
```

### Automated Linting (Optional)

For automatic linting after file edits, see `.claude/hooks-example.md` for Claude Code hook configuration. This requires `jq` to be installed.

### Working with uv

```bash
# Add new dependencies
uv add package-name

# Add development dependencies
uv add --dev package-name

# Update dependencies
uv sync --upgrade

# Run scripts with uv (automatically handles virtual environment)
uv run python main.py
uv run pytest
```

### Testing

```bash
# Run all tests
uv run pytest

# Run specific test file
uv run pytest tests/test_interactive_apps.py

# Run with verbose output
uv run pytest -v

# Run with coverage
uv run pytest --cov=. --cov-report=html
```

See [Testing Documentation](docs/TESTING.md) for comprehensive testing information.

## Documentation

- [Interactive Apps Endpoint](docs/INTERACTIVE_APPS_ENDPOINT.md) - Detailed documentation for the `/apps` endpoint
- [Date Filtering](docs/DATE_FILTERING.md) - Date filter syntax and usage examples
- [Implementation Status](docs/IMPLEMENTATION_STATUS.md) - Current implementation status and roadmap
- [Testing Guide](docs/TESTING.md) - Testing approach and guidelines

## API Documentation

Interactive API documentation is available when the server is running:

- **Swagger UI**: `http://localhost:8000/docs`
- **ReDoc**: `http://localhost:8000/redoc`

The API documentation is organized into three main categories:
- **Authentication** - User authentication and session management
- **Apps** - App discovery, job submission, and lifecycle management
- **Data Store** - iRODS file system operations and metadata access