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

Formation is configured via a JSON configuration file. Copy `config.example.json` to `config.json` and edit as needed:

```bash
cp config.example.json config.json
```

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
- `permissions_base_url`: Base URL of permissions service

**application**: Application behavior settings
- `user_suffix`: Username suffix to strip from integrator usernames
- `vice_domain`: Domain suffix for VICE applications
- `path_prefix`: URL path prefix for the service
- `vice_url_check_timeout`: Timeout for VICE URL checks in seconds
- `vice_url_check_retries`: Number of retries for VICE URL checks
- `vice_url_check_cache_ttl`: Cache TTL for VICE URL check results in seconds
- `service_accounts_only`: When true, disables regular user authentication and only accepts service account authentication (useful for testing)
- `service_account_usernames`: Map of service account role names to usernames used when calling backend services

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

See [API Endpoints Documentation](docs/API_ENDPOINTS.md) for detailed endpoint documentation including:
- Authentication (login, service accounts)
- Interactive Applications (`/apps`)
- File System Operations (`/data/browse`)
- Response formats

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

- [API Endpoints](docs/API_ENDPOINTS.md) - Complete API endpoint reference
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