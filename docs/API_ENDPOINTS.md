# API Endpoints

## Authentication

**POST /login**
- Authenticate with username/password using HTTP Basic Auth
- Returns JWT access token for subsequent requests
- Example:
  ```bash
  curl -X POST "http://localhost:8000/login" \
    -u "username:password"
  ```

## Service Account Authentication

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

Service accounts act as a configurable username: formation exchanges the service-account token (RFC 8693 token exchange against Keycloak) for an impersonation token issued to the mapped username, and that token is forwarded to terrain. This controls which DE user the request acts as:

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
- The mapped/fallback username is sanitized, then impersonated via Keycloak token exchange; the sanitized user must exist in the realm and formation's client must be allowed to impersonate

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

1. **Keycloak**: The sanitized username must exist as a realm user for the token exchange to succeed
2. **Apps Service**: Sees the sanitized username (terrain extracts it from the impersonation token)
3. **App-Exposer**: Checks the sanitized username against the resource tracking bypass whitelist

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
Impersonated Keycloak user: "deserviceaccount"
    ↓ (token forwarded through terrain to apps/app-exposer)
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

## Interactive Applications

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
- See [Interactive Apps Endpoint Documentation](INTERACTIVE_APPS_ENDPOINT.md) for detailed usage
- See [Date Filtering Documentation](DATE_FILTERING.md) for date filter syntax

**Example:**
```bash
# List all accessible apps
curl -H "Authorization: Bearer <token>" \
  "http://localhost:8000/apps"

# Filter by name and date
curl -H "Authorization: Bearer <token>" \
  "http://localhost:8000/apps?name=jupyter&integration_date=>2025-01-01"
```

## File System Operations

**GET /data/{path}**
- Browse iRODS directory contents or stream file contents
- Requires Bearer token authentication and read permission on the path
- **Note**: Leading slash is automatically added to paths - no need for double slashes in URLs
- Query parameters:
  - `offset`: Starting position for file reading (default: 0)
  - `limit`: Maximum bytes to read (optional)
  - `include_metadata`: Include iRODS metadata in response headers (default: false)
  - `avu_delimiter`: Separator for metadata value/unit pairs (default: ",")

**PUT /data/{path}**
- Upload a file, create a directory, or set AVU metadata on an existing path
- Requires Bearer token authentication and write permission on the path (or its parent when creating)
- Operations:
  - Create or update a file: send the file content as the request body
  - Create a directory: use the `resource_type=directory` query parameter (no body)
  - Metadata-only update: send a request without a body to an existing path
- Metadata is supplied via `X-Datastore-{attribute}` request headers; header values are split into value and units on `avu_delimiter`
- Query parameters:
  - `resource_type`: Set to `directory` to create a collection
  - `avu_delimiter`: Separator for metadata value/unit pairs (default: ",")
  - `replace_metadata`: When true, replaces existing AVUs for the attributes being set (default: false, which adds)

**DELETE /data/{path}**
- Delete a file or directory
- Requires Bearer token authentication and write permission on the path
- Query parameters:
  - `recurse`: Allow deleting non-empty directories (default: false)
  - `dry_run`: Preview the deletion without executing it (default: false)
- Deleting a non-empty directory without `recurse=true` returns `400`, in both real and dry-run mode

**Examples:**

```bash
# List directory contents
curl -H "Authorization: Bearer <token>" \
  "http://localhost:8000/data/cyverse/home/username"

# Read file with metadata
curl -H "Authorization: Bearer <token>" \
  "http://localhost:8000/data/cyverse/home/username/file.txt?include_metadata=true"

# Read file with pagination
curl -H "Authorization: Bearer <token>" \
  "http://localhost:8000/data/cyverse/home/username/largefile.txt?offset=1000&limit=500"

# Upload a file with metadata
curl -X PUT -H "Authorization: Bearer <token>" \
  -H "X-Datastore-Project: myproject" \
  --data-binary @local-file.txt \
  "http://localhost:8000/data/cyverse/home/username/file.txt"

# Create a directory
curl -X PUT -H "Authorization: Bearer <token>" \
  "http://localhost:8000/data/cyverse/home/username/newdir?resource_type=directory"

# Preview a recursive delete
curl -X DELETE -H "Authorization: Bearer <token>" \
  "http://localhost:8000/data/cyverse/home/username/olddir?recurse=true&dry_run=true"
```

## Response Formats

### Directory Listing
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

### File Content
- Returns raw file content (streamed) with appropriate Content-Type header
- When `include_metadata=true`, includes `X-Datastore-{attribute}` headers

### PUT Result
```json
{
  "path": "/cyverse/home/username/file.txt",
  "type": "data_object",
  "created": true
}
```

### DELETE Result
```json
{
  "path": "/cyverse/home/username/olddir",
  "type": "collection",
  "would_delete": true,
  "deleted": true,
  "dry_run": false,
  "item_count": 15
}
```
`item_count` (immediate children) is present only when `recurse=true` and the directory is non-empty.

## Differences from the original Python implementation

Formation was rewritten in Go as a drop-in replacement. Intentional improvements:

- `GET /data` streams file contents instead of buffering whole files in memory.
- `PUT /data` with `replace_metadata=true` replaces only the AVU attributes being set, preserving unrelated AVUs (including system attributes such as `ipc_UUID`).
- `DELETE /data` dry runs report the same `400` a real delete would for non-empty directories without `recurse=true`.
- `GET /apps` uses real upstream pagination, so filtered results are no longer truncated at 1000 apps.

Small mechanical differences:

- Malformed query parameters return `400` with a `{"detail": ...}` body instead of FastAPI's `422` pydantic validation arrays.
- An invalid date filter returns `400` (the Python version raised an unhandled `500`).
- `GET /apps/analyses` (no trailing slash) is served directly instead of redirecting to `/apps/analyses/`.
- Health checks use `/` instead of `/docs`. `GET /` serves an HTML landing page (MCP client setup instructions plus a Swagger UI link) instead of the JSON string `"Hello from formation."`, so health checks should rely on the status code rather than the body. Swagger UI is served at `/docs` (generated with swaggo/swag rather than FastAPI, so the spec wording differs).
