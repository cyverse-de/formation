import asyncio
import mimetypes
import os
import sys
from typing import Any, Awaitable, Callable

import httpx
import psycopg
from fastapi import Depends, FastAPI, HTTPException, Request, Response
from fastapi.responses import Response as FastAPIResponse
from fastapi.responses import JSONResponse
from fastapi.security import (
    HTTPAuthorizationCredentials,
    HTTPBasic,
    HTTPBasicCredentials,
    HTTPBearer,
)
from starlette.exceptions import HTTPException as StarletteHTTPException

import auth
import ds

app = FastAPI()


@app.exception_handler(StarletteHTTPException)
async def http_exception_handler(
    request: Request, exc: StarletteHTTPException
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

datastore = ds.DataStoreAPI(
    irods_host, irods_port, irods_user, irods_password, irods_zone
)

try:
    db_conn = psycopg.connect(
        f"host={db_host} port={db_port} user={db_user} password={db_password} dbname={db_name}"
    )
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


async def get_file_metadata_async(path: str, delimiter: str) -> dict[str, str]:
    """Async wrapper for getting file metadata."""
    loop = asyncio.get_event_loop()
    return await loop.run_in_executor(None, datastore.get_file_metadata, path, delimiter)


async def get_collection_metadata_async(path: str, delimiter: str) -> dict[str, str]:
    """Async wrapper for getting collection metadata."""
    loop = asyncio.get_event_loop()
    return await loop.run_in_executor(None, datastore.get_collection_metadata, path, delimiter)


async def guess_content_type_async(path: str) -> str:
    """Async wrapper for content type detection."""
    loop = asyncio.get_event_loop()
    content_type, _ = await loop.run_in_executor(None, mimetypes.guess_type, path)
    return content_type if content_type is not None else "application/octet-stream"


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
                    "description": "JSON response when path is a directory. AVU metadata included as X-Datastore-{attribute} headers when include_metadata=true."
                },
                "text/plain": {
                    "example": "This is the raw file content...",
                    "description": "Raw file content when path is a file. AVU metadata included as X-Datastore-{attribute} headers when include_metadata=true."
                }
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
            "content": {
                "application/json": {"example": {"detail": "Access denied"}}
            },
        },
        404: {
            "description": "Path not found",
            "content": {
                "application/json": {"example": {"detail": "Path not found"}}
            },
        },
        500: {
            "description": "Server error accessing iRODS",
            "content": {
                "application/json": {
                    "example": {"detail": "Failed to access path"}
                }
            },
        },
    },
    response_model=None
)
async def browse_directory(
    path: str, 
    current_user: Any = Depends(get_current_user),
    offset: int = 0,
    limit: int | None = None,
    avu_delimiter: str = ",",
    include_metadata: bool = False
):
    # Ensure path starts with / for iRODS
    irods_path = f"/{path}" if not path.startswith('/') else path

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
                media_type=content_type
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
            metadata_headers = await get_collection_metadata_async(irods_path, avu_delimiter)
        
        return JSONResponse(content=response_data, headers=metadata_headers)

    except HTTPException:
        raise
    except Exception as e:
        raise HTTPException(
            status_code=500, detail=f"Failed to access path: {str(e)}"
        )
