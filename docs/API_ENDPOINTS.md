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
- Returns raw file content with appropriate Content-Type header
- When `include_metadata=true`, includes `X-Datastore-{attribute}` headers
