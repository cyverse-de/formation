import asyncio
import mimetypes
import os
import re
import sys
from datetime import datetime, timezone
from typing import Any, Awaitable, Callable, TypedDict
from uuid import UUID

import httpx
import psycopg
from fastapi import Depends, FastAPI, HTTPException, Request, Response
from fastapi.responses import JSONResponse
from fastapi.responses import Response as FastAPIResponse
from fastapi.security import (
    HTTPAuthorizationCredentials,
    HTTPBasic,
    HTTPBasicCredentials,
    HTTPBearer,
)
from starlette.exceptions import HTTPException as StarletteHTTPException

import auth
import clients
import ds
import permissions


class ResourceRequirements(TypedDict, total=False):
    """
    Resource requirements for an analysis step.

    Attributes:
        step_number: Pipeline step number (for multi-step analyses)
        min_cpu_cores: Minimum CPU cores required
        max_cpu_cores: Maximum CPU cores allowed
        min_memory_limit: Minimum memory in bytes
        min_disk_space: Minimum disk space in bytes (optional)
    """

    step_number: int
    min_cpu_cores: float
    max_cpu_cores: float
    min_memory_limit: int
    min_disk_space: int


class AnalysisSubmission(TypedDict, total=False):
    """
    Type definition for analysis submission payload.

    Attributes:
        name: Display name for the analysis
        app_id: UUID of the application to launch
        system_id: System identifier for the execution environment
        debug: Enable debug mode for the analysis
        output_dir: iRODS path where analysis outputs will be stored
        notify: Whether to send notifications on analysis completion
        config: Configuration parameters for the analysis
        requirements: List of resource requirements for each analysis step
    """

    name: str
    app_id: str
    system_id: str
    debug: bool
    output_dir: str
    notify: bool
    config: dict[str, Any]
    requirements: list[ResourceRequirements]


app = FastAPI()

# Initialize service clients
apps_base_url = os.environ.get("APPS_BASE_URL") or "http://apps"
if not apps_base_url:
    print(
        "Warning: APPS_BASE_URL not set, app endpoints will not be available",
        file=sys.stderr,
    )

app_exposer_base_url = os.environ.get("APP_EXPOSER_BASE_URL") or "http://app-exposer"
if not app_exposer_base_url:
    print(
        "Warning: APP_EXPOSER_BASE_URL not set, app endpoints will not be available",
        file=sys.stderr,
    )

apps_client = None
app_exposer_client = None
permissions_client = None

if apps_base_url:
    apps_client = clients.AppsClient(base_url=apps_base_url)
if app_exposer_base_url:
    app_exposer_client = clients.AppExposerClient(base_url=app_exposer_base_url)

# Initialize permissions client
permissions_base_url = os.environ.get("PERMISSIONS_BASE_URL") or "http://permissions"
if permissions_base_url:
    permissions_client = permissions.PermissionsClient(base_url=permissions_base_url)


@app.exception_handler(StarletteHTTPException)
async def http_exception_handler(
    _request: Request, exc: StarletteHTTPException
) -> JSONResponse:
    print(exc, file=sys.stderr)
    return JSONResponse(content={"detail": exc.detail}, status_code=exc.status_code)


@app.middleware("http")
async def exception_handling_middleware(
    request: Request, call_next: Callable[[Request], Awaitable[Response]]
) -> Response:
    try:
        return await call_next(request)
    except Exception as e:
        print(str(e), file=sys.stderr)
        return JSONResponse(content=str(e), status_code=500)


db_host = os.environ.get("DB_HOST") or ""
if db_host == "":
    print("DB_HOST must be set", file=sys.stderr)
    sys.exit(1)

db_port = os.environ.get("DB_PORT") or "5432"

db_user = os.environ.get("DB_USER") or ""
if db_user == "":
    print("DB_USER must be set", file=sys.stderr)
    sys.exit(1)

db_password = os.environ.get("DB_PASSWORD") or ""
if db_password == "":
    print("DB_PASSWORD must be set", file=sys.stderr)
    sys.exit(1)

db_name = os.environ.get("DB_NAME") or ""
if db_name == "":
    print("DB_NAME must be set", file=sys.stderr)
    sys.exit(1)

irods_host = os.environ.get("IRODS_HOST")
if irods_host is None:
    print("Environment variable IRODS_HOST is not set.", file=sys.stderr)
    sys.exit(1)

irods_port = os.environ.get("IRODS_PORT")
if irods_port is None:
    print("Environment variable IRODS_PORT is not set.", file=sys.stderr)
    sys.exit(1)

irods_user = os.environ.get("IRODS_USER")
if irods_user is None:
    print("Environment variable IRODS_USER is not set.", file=sys.stderr)
    sys.exit(1)

irods_password = os.environ.get("IRODS_PASSWORD")
if irods_password is None:
    print("Environment variable IRODS_PASSWORD is not set.", file=sys.stderr)
    sys.exit(1)

irods_zone = os.environ.get("IRODS_ZONE")
if irods_zone is None:
    print("Environment variable IRODS_ZONE is not set.", file=sys.stderr)
    sys.exit(1)

keycloak_server_url = os.environ.get("KEYCLOAK_SERVER_URL") or ""
if keycloak_server_url == "":
    print("Environment variable KEYCLOAK_SERVER_URL is not set.", file=sys.stderr)
    sys.exit(1)

keycloak_realm = os.environ.get("KEYCLOAK_REALM") or ""
if keycloak_realm == "":
    print("Environment variable KEYCLOAK_REALM is not set.", file=sys.stderr)
    sys.exit(1)

keycloak_client_id = os.environ.get("KEYCLOAK_CLIENT_ID") or ""
if keycloak_client_id == "":
    print("Environment variable KEYCLOAK_CLIENT_ID is not set.", file=sys.stderr)
    sys.exit(1)

keycloak_client_secret = os.environ.get("KEYCLOAK_CLIENT_SECRET") or ""
if keycloak_client_secret == "":
    print("Environment variable KEYCLOAK_CLIENT_SECRET is not set.", file=sys.stderr)
    sys.exit(1)

keycloak_ssl_verify = os.environ.get("KEYCLOAK_SSL_VERIFY", "true").lower() == "true"

user_suffix = os.environ.get("USER_SUFFIX", "@iplantcollaborative.org")

datastore = ds.DataStoreAPI(
    irods_host, irods_port, irods_user, irods_password, irods_zone
)

db_conn = None
db_conninfo = f"host={db_host} port={db_port} user={db_user} password={db_password} dbname={db_name}"

try:
    # Test connection on startup (synchronous)
    test_conn = psycopg.connect(db_conninfo)
    test_conn.close()
except Exception as e:
    print(f"Failed to connect to the database: {e}", file=sys.stderr)
    sys.exit(1)

basic_auth = HTTPBasic()
bearer_auth = HTTPBearer()


async def get_current_user(
    token: HTTPAuthorizationCredentials = Depends(bearer_auth),
) -> Any:
    return await auth.verify_token(
        keycloak_server_url, keycloak_realm, keycloak_client_id, token.credentials
    )


def extract_user_from_jwt(jwt_payload: Any) -> str:
    """Extract username from JWT token payload."""
    username = jwt_payload.get("preferred_username") or jwt_payload.get("sub")
    if not username:
        raise HTTPException(status_code=401, detail="Unable to determine user identity")
    return username


def parse_date_filter(filter_expr: str) -> tuple[str, datetime]:
    """
    Parse a date filter expression like ">2025-09-29" into SQL operator and datetime.

    Args:
        filter_expr: Date filter expression with operator prefix (e.g., ">2025-09-29",
                    "<=2024-12-31T23:59:59", "==2025-01-01T00:00:00Z")

    Returns:
        Tuple of (SQL operator, datetime object as naive UTC datetime)

    Raises:
        ValueError: If the expression format is invalid or date cannot be parsed
    """
    # Match operator followed by optional whitespace and ISO date
    # Supports: >, <, >=, <=, ==
    pattern = r"^(>=|<=|==|>|<)\s*(.+)$"
    match = re.match(pattern, filter_expr.strip())

    if not match:
        raise ValueError(
            f"Invalid date filter format: '{filter_expr}'. "
            "Expected format: <operator><date> (e.g., '>2025-09-29', '<=2024-12-31T23:59:59')"
        )

    operator, date_str = match.groups()

    # Parse the date using fromisoformat (supports various ISO 8601 formats)
    try:
        dt = datetime.fromisoformat(date_str.strip())
    except ValueError as e:
        raise ValueError(
            f"Invalid date format: '{date_str}'. Expected ISO 8601 format "
            "(e.g., '2025-09-29', '2025-09-29T14:30:00', '2025-09-29T14:30:00Z')"
        ) from e

    # Convert to naive UTC datetime for database comparison
    # PostgreSQL stores timestamp without time zone, so we need naive datetimes
    if dt.tzinfo is not None:
        # Convert to UTC then strip timezone info
        dt = dt.astimezone(timezone.utc).replace(tzinfo=None)

    # Map == to SQL =
    sql_operator = "=" if operator == "==" else operator

    return (sql_operator, dt)


async def get_interactive_apps(
    username: str,
    limit: int = 100,
    offset: int = 0,
    name: str | None = None,
    description: str | None = None,
    integrator: str | None = None,
    integration_date: str | None = None,
    edited_date: str | None = None,
) -> dict[str, Any]:
    """
    Query the database for interactive apps accessible to the user.

    Args:
        username: The username to check permissions for.
        limit: Maximum number of apps to return.
        offset: Number of apps to skip for pagination.
        name: Optional filter to match apps by name (case-insensitive partial match).
        description: Optional filter to match apps by description (case-insensitive partial match).
        integrator: Optional integrator username to filter apps (case-insensitive partial match).
        integration_date: Optional date filter for integration_date (e.g., ">2025-09-29").
        edited_date: Optional date filter for edited_date (e.g., "<=2024-12-31").

    Returns:
        Dictionary with 'total' count and 'apps' list.

    Raises:
        HTTPException: If permissions service or database query fails.
        ValueError: If date filter format is invalid.
    """
    if not permissions_client:
        raise HTTPException(
            status_code=503, detail="Permissions service not configured"
        )

    try:
        # Get accessible app IDs from permissions service
        accessible_app_ids = await permissions_client.get_accessible_app_ids(username)

        if not accessible_app_ids:
            return {"total": 0, "apps": []}

        # Convert UUIDs to strings for SQL query
        app_id_list = [str(app_id) for app_id in accessible_app_ids]

        # Build filter clauses if filter parameters provided
        search_filters = []
        search_params = []

        if name:
            search_filters.append("name ILIKE %s")
            search_params.append(f"%{name}%")

        if description:
            search_filters.append("description ILIKE %s")
            search_params.append(f"%{description}%")

        if integrator:
            # Strip user suffix if provided in the query parameter
            integrator_search = integrator
            if user_suffix and integrator_search.endswith(user_suffix):
                integrator_search = integrator_search[:-len(user_suffix)]

            search_filters.append("integrator_username ILIKE %s")
            search_params.append(f"%{integrator_search}%")

        # Parse and add date filters
        if integration_date:
            operator, dt = parse_date_filter(integration_date)
            search_filters.append(f"integration_date {operator} %s")
            search_params.append(dt)

        if edited_date:
            operator, dt = parse_date_filter(edited_date)
            # Handle nullable edited_date - only filter non-null values
            search_filters.append(f"edited_date IS NOT NULL AND edited_date {operator} %s")
            search_params.append(dt)

        # Combine filters with AND if multiple filters exist
        search_filter = ""
        if search_filters:
            search_filter = "AND " + " AND ".join(search_filters)

        # Query database for interactive apps
        # Use a subquery to get the latest version per app
        query = f"""
            WITH latest_versions AS (
                SELECT DISTINCT ON (id)
                    id,
                    version_id,
                    version,
                    version_order
                FROM app_versions_listing
                WHERE deleted = false
                  AND disabled = false
                  AND overall_job_type = 'interactive'
                  AND id = ANY(%s::uuid[])
                  {search_filter}
                ORDER BY id, version_order DESC
            )
            SELECT
                avl.id,
                avl.name,
                avl.description,
                avl.version,
                avl.integrator_username,
                avl.integration_date,
                avl.edited_date
            FROM app_versions_listing avl
            INNER JOIN latest_versions lv ON avl.id = lv.id AND avl.version_id = lv.version_id
            ORDER BY avl.name
            LIMIT %s OFFSET %s
        """

        count_query = f"""
            WITH latest_versions AS (
                SELECT DISTINCT ON (id)
                    id,
                    version_id
                FROM app_versions_listing
                WHERE deleted = false
                  AND disabled = false
                  AND overall_job_type = 'interactive'
                  AND id = ANY(%s::uuid[])
                  {search_filter}
                ORDER BY id, version_order DESC
            )
            SELECT COUNT(*) FROM latest_versions
        """

        async with await psycopg.AsyncConnection.connect(db_conninfo) as conn:
            async with conn.cursor() as cur:
                # Build parameter lists based on whether filters are provided
                count_params = [app_id_list] + search_params if search_params else [app_id_list]
                query_params = [app_id_list] + search_params + [limit, offset] if search_params else [app_id_list, limit, offset]

                # Get total count
                await cur.execute(count_query, count_params)
                result = await cur.fetchone()
                total = result[0] if result else 0

                # Get apps
                await cur.execute(query, query_params)
                rows = await cur.fetchall()

                apps = []
                for row in rows:
                    # Remove user suffix from integrator username
                    integrator_username = row[4]
                    if integrator_username and user_suffix and integrator_username.endswith(user_suffix):
                        integrator_username = integrator_username[:-len(user_suffix)]

                    apps.append(
                        {
                            "id": str(row[0]),
                            "name": row[1],
                            "description": row[2],
                            "version": row[3],
                            "integrator_username": integrator_username,
                            "integration_date": (
                                row[5].isoformat() if row[5] else None
                            ),
                            "edited_date": row[6].isoformat() if row[6] else None,
                        }
                    )

                return {"total": total, "apps": apps}

    except httpx.HTTPError as e:
        raise HTTPException(
            status_code=503, detail=f"Permissions service error: {str(e)}"
        )
    except Exception as e:
        raise HTTPException(
            status_code=500, detail=f"Failed to retrieve apps: {str(e)}"
        )


async def get_file_metadata_async(path: str, delimiter: str) -> dict[str, str]:
    """Async wrapper for getting file metadata."""
    loop = asyncio.get_event_loop()
    return await loop.run_in_executor(
        None, datastore.get_file_metadata, path, delimiter
    )


async def get_collection_metadata_async(path: str, delimiter: str) -> dict[str, str]:
    """Async wrapper for getting collection metadata."""
    loop = asyncio.get_event_loop()
    return await loop.run_in_executor(
        None, datastore.get_collection_metadata, path, delimiter
    )


async def guess_content_type_async(path: str) -> str:
    """Async wrapper for content type detection."""
    loop = asyncio.get_event_loop()
    content_type, _ = await loop.run_in_executor(None, mimetypes.guess_type, path)
    return content_type if content_type is not None else "application/octet-stream"


@app.get(
    "/apps",
    summary="List interactive apps accessible to the user",
    description="""Lists interactive applications that are accessible to the authenticated user.

Interactive apps are applications with `overall_job_type='interactive'`, which typically includes web-based tools like JupyterLab, RStudio, and other VICE (Visual Interactive Computing Environment) applications.

The endpoint returns only the latest version of each app that meets the following criteria:
- Not deleted (`deleted = false`)
- Not disabled (`disabled = false`)
- Is an interactive app (`overall_job_type = 'interactive'`)
- Is either publicly accessible or accessible to the authenticated user through permissions
- Optionally filters by app name (case-insensitive partial match)
- Optionally filters by app description (case-insensitive partial match)
- Optionally filters by integrator username (case-insensitive partial match)
- Optionally filters by integration_date using comparison operators (e.g., ">2025-09-29", "<=2024-12-31T23:59:59Z")
- Optionally filters by edited_date using comparison operators (only considers non-null values)

**Date Filter Format:**
Date filters accept an operator prefix followed by an ISO 8601 date/datetime:
- Supported operators: `>`, `<`, `>=`, `<=`, `==`
- Supported date formats: `YYYY-MM-DD`, `YYYY-MM-DDTHH:MM:SS`, `YYYY-MM-DDTHH:MM:SSZ`, `YYYY-MM-DDTHH:MM:SS±HH:MM`
- Examples: `">2025-09-29"`, `"<=2024-12-31T23:59:59"`, `">=2025-01-01T00:00:00Z"`
- Timezone handling: Dates with timezone info are converted to UTC; dates without timezone are treated as UTC

When multiple filter parameters are provided, apps must match all criteria (AND logic).

Results are paginated using `limit` and `offset` query parameters and ordered alphabetically by app name.""",
    response_description="Paginated list of interactive apps with metadata",
    responses={
        200: {
            "description": "Successfully retrieved interactive apps",
            "content": {
                "application/json": {
                    "example": {
                        "total": 150,
                        "apps": [
                            {
                                "id": "c7f05682-23c8-4182-b9a2-e09650a5f49b",
                                "name": "JupyterLab Datascience",
                                "description": "JupyterLab with scientific Python packages",
                                "version": "3.0.0",
                                "integrator_username": "wregglej",
                                "integration_date": "2023-01-15T10:30:00",
                                "edited_date": "2023-06-20T14:45:00",
                            }
                        ],
                    }
                }
            },
        },
        400: {
            "description": "Bad request - invalid pagination or date filter parameters",
            "content": {
                "application/json": {
                    "example": {
                        "detail": "Invalid date filter format: 'invalid'. Expected format: <operator><date>"
                    }
                }
            },
        },
        401: {
            "description": "Unauthorized - invalid or missing access token",
            "content": {
                "application/json": {"example": {"detail": "Not authenticated"}}
            },
        },
        500: {
            "description": "Internal server error",
            "content": {
                "application/json": {
                    "example": {"detail": "Failed to retrieve apps: Database error"}
                }
            },
        },
        503: {
            "description": "Service unavailable - permissions service is not accessible",
            "content": {
                "application/json": {
                    "example": {"detail": "Permissions service not configured"}
                }
            },
        },
    },
)
async def list_interactive_apps(
    current_user: Any = Depends(get_current_user),
    limit: int = 100,
    offset: int = 0,
    name: str | None = None,
    description: str | None = None,
    integrator: str | None = None,
    integration_date: str | None = None,
    edited_date: str | None = None,
) -> dict[str, Any]:
    """
    List interactive apps accessible to the authenticated user.

    Args:
        current_user: JWT payload from authentication (injected by Depends).
        limit: Maximum number of apps to return (default: 100, max: 1000).
        offset: Number of apps to skip for pagination (default: 0).
        name: Optional filter to match apps by name (case-insensitive partial match).
        description: Optional filter to match apps by description (case-insensitive partial match).
        integrator: Optional integrator username to filter apps (case-insensitive partial match).
        integration_date: Optional date filter for integration_date with operator prefix
                         (e.g., ">2025-09-29", "<=2024-12-31T23:59:59Z").
        edited_date: Optional date filter for edited_date with operator prefix
                    (e.g., ">2025-09-29", "<=2024-12-31T23:59:59Z").

    Returns:
        Dictionary containing:
        - total: Total number of accessible interactive apps matching the filter criteria
        - apps: List of app objects with the following fields:
            - id: Unique app identifier (UUID)
            - name: Display name of the app
            - description: Detailed description of the app's functionality
            - version: Version string of the latest app version
            - integrator_username: Username of the user who integrated the app
            - integration_date: ISO 8601 timestamp when the app was first integrated
            - edited_date: ISO 8601 timestamp of the last modification (may be null)

    Raises:
        HTTPException: With appropriate status code if authentication fails,
                      permissions service is unavailable, or database query fails.
    """
    # Validate pagination parameters
    if limit < 1 or limit > 1000:
        raise HTTPException(
            status_code=400, detail="Limit must be between 1 and 1000"
        )
    if offset < 0:
        raise HTTPException(status_code=400, detail="Offset must be non-negative")

    username = extract_user_from_jwt(current_user)

    try:
        return await get_interactive_apps(
            username, limit, offset, name, description, integrator,
            integration_date, edited_date
        )
    except ValueError as e:
        raise HTTPException(status_code=400, detail=str(e))


@app.post(
    "/login",
    summary="Authenticate user and obtain access token",
    description="Authenticates a user with username and password via HTTP Basic Auth and returns an access token from Keycloak",
    response_description="Access token and related authentication information",
    responses={
        200: {
            "description": "Successful authentication",
            "content": {
                "application/json": {
                    "example": {
                        "access_token": "eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9...",
                        "token_type": "Bearer",
                        "expires_in": 3600,
                    }
                }
            },
        },
        401: {
            "description": "Invalid credentials",
            "content": {
                "application/json": {"example": {"detail": "Invalid credentials"}}
            },
        },
        500: {
            "description": "Authentication service error or login failure",
            "content": {
                "application/json": {
                    "example": {"detail": "Authentication service error"}
                }
            },
        },
    },
)
async def login(
    credentials: HTTPBasicCredentials = Depends(basic_auth),
) -> dict[str, Any]:
    try:
        return await auth.get_access_token(
            keycloak_server_url,
            keycloak_realm,
            keycloak_client_id,
            keycloak_client_secret,
            credentials.username,
            credentials.password,
            keycloak_ssl_verify,
        )
    except httpx.HTTPStatusError as e:
        if e.response.status_code == 401:
            raise HTTPException(status_code=401, detail="Invalid credentials")
        raise HTTPException(status_code=500, detail="Authentication service error")
    except Exception as e:
        raise HTTPException(status_code=500, detail=f"Login failed: {str(e)}")


@app.get(
    "/data/browse/{path:path}",
    summary="Browse iRODS directory contents or read file",
    description="Lists the contents of a directory in iRODS or reads the contents of a file. The path parameter should be the full iRODS path. For files, returns raw file content as plain text with optional offset and limit query parameters for paging. For directories, returns JSON with file/directory listing. When include_metadata=true, both files and directories include iRODS AVU metadata as response headers (X-Datastore-{attribute}). The avu-delimiter parameter controls the separator between value and unit in headers (default: ','). Requires authentication.",
    response_description="JSON list of files and directories if path is a directory, or raw file contents as plain text if path is a file. When include_metadata=true, AVU metadata is included as X-Datastore-{attribute} response headers.",
    responses={
        200: {
            "description": "Directory contents or file contents retrieved successfully, with optional AVU metadata in response headers if include_metadata=true",
            "content": {
                "application/json": {
                    "example": {
                        "path": "/cyverse/home/wregglej",
                        "type": "collection",
                        "contents": [
                            {"name": "file1.txt", "type": "data_object"},
                            {"name": "subdirectory", "type": "collection"},
                        ],
                    },
                    "description": "JSON response when path is a directory. AVU metadata included as X-Datastore-{attribute} headers when include_metadata=true.",
                },
                "text/plain": {
                    "example": "This is the raw file content...",
                    "description": "Raw file content when path is a file. AVU metadata included as X-Datastore-{attribute} headers when include_metadata=true.",
                },
            },
        },
        401: {
            "description": "Unauthorized - invalid or missing access token",
            "content": {
                "application/json": {"example": {"detail": "Not authenticated"}}
            },
        },
        403: {
            "description": "Insufficient permissions to access directory",
            "content": {"application/json": {"example": {"detail": "Access denied"}}},
        },
        404: {
            "description": "Path not found",
            "content": {"application/json": {"example": {"detail": "Path not found"}}},
        },
        500: {
            "description": "Server error accessing iRODS",
            "content": {
                "application/json": {"example": {"detail": "Failed to access path"}}
            },
        },
    },
    response_model=None,
)
async def browse_directory(
    path: str,
    current_user: Any = Depends(get_current_user),
    offset: int = 0,
    limit: int | None = None,
    avu_delimiter: str = ",",
    include_metadata: bool = False,
):
    # Ensure path starts with / for iRODS
    irods_path = f"/{path}" if not path.startswith("/") else path

    try:
        # Check if path exists (could be file or collection)
        if not datastore.path_exists(irods_path):
            raise HTTPException(status_code=404, detail="Path not found")

        # Extract username from JWT token
        username = extract_user_from_jwt(current_user)

        # Check if user has read permissions on the path
        if not datastore.user_can_read(username, irods_path):
            raise HTTPException(status_code=403, detail="Access denied")

        # Check if it's a file
        if datastore.file_exists(irods_path):
            # Return raw file contents with optional paging
            file_data = datastore.get_file_contents(irods_path, offset, limit)

            # Create tasks for async operations
            tasks = []

            # Add content type detection task
            tasks.append(guess_content_type_async(irods_path))

            # Add metadata retrieval task if requested
            if include_metadata:
                tasks.append(get_file_metadata_async(irods_path, avu_delimiter))
            else:
                # Create a simple async function that returns empty dict
                async def empty_metadata():
                    return {}

                tasks.append(empty_metadata())

            # Execute async operations concurrently
            results = await asyncio.gather(*tasks)
            content_type = results[0]
            metadata_headers = results[1] if include_metadata else {}

            return FastAPIResponse(
                content=file_data["content"],
                headers=metadata_headers,
                media_type=content_type,
            )

        # It's a collection - ignore paging parameters
        collection = datastore.get_collection(irods_path)
        if collection is None:
            raise HTTPException(status_code=404, detail="Directory not found")

        contents = []

        if hasattr(collection, "subcollections"):
            for subcoll in collection.subcollections:
                contents.append(
                    {
                        "name": getattr(subcoll, "name", str(subcoll)),
                        "type": "collection",
                    }
                )

        if hasattr(collection, "data_objects"):
            for data_obj in collection.data_objects:
                contents.append(
                    {
                        "name": getattr(data_obj, "name", str(data_obj)),
                        "type": "data_object",
                    }
                )

        response_data = {"path": irods_path, "type": "collection", "contents": contents}

        # Get collection metadata as headers if requested asynchronously
        metadata_headers = {}
        if include_metadata:
            metadata_headers = await get_collection_metadata_async(
                irods_path, avu_delimiter
            )

        return JSONResponse(content=response_data, headers=metadata_headers)

    except HTTPException:
        raise
    except Exception as e:
        raise HTTPException(status_code=500, detail=f"Failed to access path: {str(e)}")


@app.post(
    "/app/launch",
    summary="Launch an interactive analysis",
    description="Submits an analysis job to the apps service which orchestrates launching it in Kubernetes. Returns analysis ID and access URL.",
    response_description="Analysis launch information including ID and URL",
    responses={
        200: {
            "description": "Analysis submitted successfully",
            "content": {
                "application/json": {
                    "example": {
                        "analysis_id": "a1b2c3d4-1234-5678-90ab-cdef12345678",
                        "name": "My Analysis",
                        "status": "Submitted",
                        "url": "https://a1b2c3d4.apps.example.com",
                    }
                }
            },
        },
        401: {"description": "Unauthorized - invalid token"},
        500: {"description": "Submission failed"},
        503: {"description": "Apps service unavailable"},
    },
)
async def launch_app(
    submission: AnalysisSubmission,
    current_user: Any = Depends(get_current_user),
) -> dict[str, Any]:
    if not apps_client:
        raise HTTPException(status_code=503, detail="Apps service not configured")

    username = extract_user_from_jwt(current_user)

    try:
        response = await apps_client.submit_analysis(submission, username)

        # Extract analysis ID and generate URL from subdomain if available
        analysis_id = response.get("id")
        subdomain = response.get("subdomain", analysis_id)

        # Construct minimal response
        result = {
            "analysis_id": analysis_id,
            "name": response.get("name", submission.get("name", "Unnamed")),
            "status": response.get("status", "Submitted"),
        }

        # Add URL if we have subdomain info
        if subdomain:
            result["url"] = f"https://{subdomain}.apps.example.com"

        return result
    except httpx.HTTPStatusError as e:
        raise HTTPException(
            status_code=e.response.status_code,
            detail=f"Apps service error: {e.response.text}",
        )
    except Exception as e:
        raise HTTPException(status_code=500, detail=f"Launch failed: {str(e)}")


@app.get(
    "/app/{analysis_id}/status",
    summary="Get analysis status",
    description="Retrieves current status of an analysis including whether the URL is ready for access",
    response_description="Analysis status information",
    responses={
        200: {
            "description": "Status retrieved successfully",
            "content": {
                "application/json": {
                    "example": {
                        "analysis_id": "a1b2c3d4-1234-5678-90ab-cdef12345678",
                        "status": "Running",
                        "url_ready": True,
                        "url": "https://a1b2c3d4.apps.example.com",
                    }
                }
            },
        },
        401: {"description": "Unauthorized"},
        404: {"description": "Analysis not found"},
        500: {"description": "Status check failed"},
        503: {"description": "Service unavailable"},
    },
)
async def get_app_status(
    analysis_id: str,
    current_user: Any = Depends(get_current_user),
) -> dict[str, Any]:
    if not apps_client or not app_exposer_client:
        raise HTTPException(status_code=503, detail="Required services not configured")

    username = extract_user_from_jwt(current_user)

    try:
        # Get analysis info from apps service
        analysis_uuid = UUID(analysis_id)
        analysis = await apps_client.get_analysis(analysis_uuid, username)

        # Get URL readiness from app-exposer if analysis has a subdomain
        url_ready = False
        subdomain = analysis.get("subdomain")
        if subdomain:
            try:
                ready_response = await app_exposer_client.check_url_ready(
                    subdomain, username
                )
                url_ready = ready_response.get("ready", False)
            except Exception:
                # If URL check fails, assume not ready
                url_ready = False

        result = {
            "analysis_id": analysis_id,
            "status": analysis.get("status", "Unknown"),
            "url_ready": url_ready,
        }

        if subdomain:
            result["url"] = f"https://{subdomain}.apps.example.com"

        return result
    except httpx.HTTPStatusError as e:
        if e.response.status_code == 404:
            raise HTTPException(status_code=404, detail="Analysis not found")
        raise HTTPException(
            status_code=e.response.status_code,
            detail=f"Service error: {e.response.text}",
        )
    except ValueError:
        raise HTTPException(status_code=400, detail="Invalid analysis ID format")
    except Exception as e:
        raise HTTPException(status_code=500, detail=f"Status check failed: {str(e)}")


@app.post(
    "/app/{analysis_id}/control",
    summary="Control analysis lifecycle",
    description="Perform control operations on an analysis: extend time, save and exit, or exit without saving",
    response_description="Control operation result",
    responses={
        200: {
            "description": "Operation completed successfully",
            "content": {
                "application/json": {
                    "examples": {
                        "extend": {
                            "value": {
                                "operation": "extend_time",
                                "time_limit": "2025-10-03T10:00:00Z",
                            }
                        },
                        "terminate": {
                            "value": {
                                "operation": "save_and_exit",
                                "status": "terminated",
                                "outputs_saved": True,
                            }
                        },
                    }
                }
            },
        },
        400: {"description": "Invalid operation"},
        401: {"description": "Unauthorized"},
        404: {"description": "Analysis not found"},
        500: {"description": "Operation failed"},
        503: {"description": "Service unavailable"},
    },
)
async def control_app(
    analysis_id: str,
    operation: str,
    _current_user: Any = Depends(get_current_user),  # noqa: ARG001
) -> dict[str, Any]:
    if not app_exposer_client:
        raise HTTPException(
            status_code=503, detail="App-exposer service not configured"
        )

    valid_operations = ["extend_time", "save_and_exit", "exit"]
    if operation not in valid_operations:
        raise HTTPException(
            status_code=400,
            detail=f"Invalid operation. Must be one of: {', '.join(valid_operations)}",
        )

    try:
        analysis_uuid = UUID(analysis_id)

        result: dict[str, Any]
        if operation == "extend_time":
            result = await app_exposer_client.extend_time_limit(analysis_uuid)
            result["operation"] = operation
        elif operation == "save_and_exit":
            result = await app_exposer_client.save_and_exit(analysis_uuid)
            result["operation"] = operation
        else:  # operation == "exit"
            result = await app_exposer_client.exit_without_save(analysis_uuid)
            result["operation"] = operation

        return result
    except httpx.HTTPStatusError as e:
        if e.response.status_code == 404:
            raise HTTPException(status_code=404, detail="Analysis not found")
        raise HTTPException(
            status_code=e.response.status_code,
            detail=f"Service error: {e.response.text}",
        )
    except ValueError:
        raise HTTPException(status_code=400, detail="Invalid analysis ID format")
    except Exception as e:
        raise HTTPException(status_code=500, detail=f"Operation failed: {str(e)}")
