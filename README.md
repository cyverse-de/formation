# Formation

Formation is a FastAPI-based service that provides authenticated access to iRODS data storage with integrated Keycloak authentication. It serves as a bridge between web applications and iRODS file systems, offering RESTful APIs for file browsing, content retrieval, and metadata access.

## Features

- **Authentication**: Secure login via Keycloak OIDC with JWT token support
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

### Code Style

```bash
# Format and lint code using uv
uv run ruff format
uv run ruff check --fix

# Or install ruff globally and run directly
uv tool install ruff
ruff format
ruff check --fix
```

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