# Interactive Apps Endpoint

## Overview

Added `GET /apps` endpoint to list interactive applications accessible to authenticated users.

## Implementation Details

### Files Added

1. **`permissions.py`** - Permissions service client
   - `PermissionsClient` class for interacting with the permissions service
   - `get_public_app_ids()` - Retrieves public apps (accessible to all users)
   - `get_user_accessible_app_ids()` - Retrieves apps accessible to specific user
   - `get_accessible_app_ids()` - Combines public and user-specific apps

2. **`tests/test_interactive_apps.py`** - Unit test structure
   - Test cases for permissions filtering
   - Test cases for pagination validation
   - Test cases for error handling

### Files Modified

1. **`main.py`**
   - Imported `permissions` module
   - Added `permissions_client` initialization
   - Added `get_interactive_apps()` helper function with database query
   - Added `GET /apps` endpoint with comprehensive Swagger docs
   - Fixed async database connection usage

## Endpoint Specification

### Request

```
GET /apps?limit=100&offset=0
Authorization: Bearer <token>
```

**Query Parameters:**
- `limit` (optional): Maximum apps to return (1-1000, default: 100)
- `offset` (optional): Pagination offset (default: 0)
- `name` (optional): Filter apps by name (case-insensitive partial match)
- `description` (optional): Filter apps by description (case-insensitive partial match)
- `integrator` (optional): Filter apps by integrator username (case-insensitive partial match)
- `integration_date` (optional): Filter by integration date with operator (e.g., ">2025-09-29")
- `edited_date` (optional): Filter by edited date with operator (e.g., "<=2024-12-31")

### Response

```json
{
  "total": 150,
  "apps": [
    {
      "id": "c7f05682-23c8-4182-b9a2-e09650a5f49b",
      "name": "JupyterLab Datascience",
      "description": "JupyterLab with scientific Python packages",
      "version": "3.0.0",
      "integrator_username": "wregglej",
      "integration_date": "2023-01-15T10:30:00",
      "edited_date": "2023-06-20T14:45:00"
    }
  ]
}
```

**Response Fields:**
- `id` - App UUID (needed for job submissions)
- `name` - Display name
- `description` - Detailed description
- `version` - Latest version string
- `integrator_username` - Username of integrator
- `integration_date` - ISO 8601 timestamp of initial integration
- `edited_date` - ISO 8601 timestamp of last edit (nullable)

### Status Codes

- **200 OK** - Successfully retrieved apps
- **400 Bad Request** - Invalid pagination parameters
- **401 Unauthorized** - Missing or invalid authentication token
- **500 Internal Server Error** - Database query failed
- **503 Service Unavailable** - Permissions service not accessible

## Database Query

The endpoint queries the `app_versions_listing` view with the following filters:

1. `deleted = false` - Exclude deleted apps
2. `disabled = false` - Exclude disabled apps
3. `overall_job_type = 'interactive'` - Only interactive apps
4. App ID in set of accessible apps (public OR user-specific permissions)
5. Latest version only (max `version_order` per app)

Results ordered alphabetically by app name.

## Permissions Model

Apps are accessible to a user if:
1. **Public**: App has read permission granted to the grouper user group (typically "de-users")
2. **User-specific**: User has explicit read (or higher) permission on the app

The endpoint queries the permissions service to get both sets and combines them.

## Environment Variables Required

- `PERMISSIONS_BASE_URL` - Base URL of permissions service (default: "http://permissions")
- `GROUPER_USER_GROUP_ID` - ID of the public user group (default: "de-users")
- All existing DB_* variables for database connectivity

## Testing

Run unit tests:
```bash
uv run pytest tests/test_interactive_apps.py
```

## Usage Example

```python
import httpx

# Authenticate
login_response = httpx.post(
    "https://formation/login",
    auth=("username", "password")
)
token = login_response.json()["access_token"]

# Get interactive apps
apps_response = httpx.get(
    "https://formation/apps?limit=50&offset=0",
    headers={"Authorization": f"Bearer {token}"}
)
apps_data = apps_response.json()

print(f"Total apps: {apps_data['total']}")
for app in apps_data['apps']:
    print(f"  - {app['name']} ({app['id']})")
```

## Swagger Documentation

The endpoint includes comprehensive OpenAPI/Swagger documentation accessible at:
```
GET /docs
```

The documentation includes:
- Detailed description of what "interactive apps" means
- Complete parameter specifications
- Response schema with examples
- All possible error responses with examples
