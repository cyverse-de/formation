# Formation

Formation is a FastAPI-based service that provides authenticated access to iRODS data storage with integrated Keycloak authentication. It serves as a bridge between web applications and iRODS file systems, offering RESTful APIs for file browsing, content retrieval, and metadata access.

## Features

- **Authentication**: Secure login via Keycloak OIDC with JWT token support
- **File System Access**: Browse iRODS collections and retrieve file contents
- **Metadata Support**: Access iRODS AVU (Attribute-Value-Unit) metadata as HTTP headers
- **Content Type Detection**: Automatic MIME type detection for file responses
- **Asynchronous Operations**: Concurrent metadata retrieval and content type detection for improved performance
- **Pagination**: Support for offset/limit parameters when reading large files
- **Permission Checking**: Validates user read permissions before granting access

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