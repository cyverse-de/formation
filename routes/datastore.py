"""Data Store routes for Formation API."""

import asyncio
import mimetypes
from typing import Any

from fastapi import APIRouter, Depends, Request
from fastapi.responses import JSONResponse
from fastapi.responses import Response as FastAPIResponse

import ds
from config import config
from dependencies import extract_user_from_jwt, get_current_user
from exceptions import BadRequestError, PermissionDeniedError, ResourceNotFoundError

router = APIRouter(prefix="", tags=["Data Store"])

# Initialize datastore client
datastore = ds.DataStoreAPI(
    config.irods_host,
    config.irods_port,
    config.irods_user,
    config.irods_password,
    config.irods_zone,
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


async def create_directory_async(path: str) -> None:
    """Async wrapper for creating a directory."""
    loop = asyncio.get_event_loop()
    await loop.run_in_executor(None, datastore.create_directory, path)


async def upload_file_async(path: str, content: bytes) -> None:
    """Async wrapper for uploading a file."""
    loop = asyncio.get_event_loop()
    await loop.run_in_executor(None, datastore.upload_file, path, content)


async def set_file_metadata_async(
    path: str, metadata: dict[str, tuple[str, str]], replace: bool = False
) -> None:
    """Async wrapper for setting file metadata."""
    loop = asyncio.get_event_loop()
    await loop.run_in_executor(None, datastore.set_file_metadata, path, metadata, replace)


async def set_collection_metadata_async(
    path: str, metadata: dict[str, tuple[str, str]], replace: bool = False
) -> None:
    """Async wrapper for setting collection metadata."""
    loop = asyncio.get_event_loop()
    await loop.run_in_executor(
        None, datastore.set_collection_metadata, path, metadata, replace
    )


async def delete_path_async(
    path: str, recurse: bool = False, dry_run: bool = False
) -> dict[str, Any]:
    """Async wrapper for deleting a path."""
    loop = asyncio.get_event_loop()
    return await loop.run_in_executor(
        None, datastore.delete_path, path, recurse, dry_run
    )


@router.get(
    "/data/{path:path}",
    summary="Browse iRODS directory contents or read file",
    description=(
        "Lists the contents of a directory in iRODS or reads the contents of a file. "
        "The path parameter should be the full iRODS path. For files, returns raw file "
        "content as plain text with optional offset and limit query parameters for paging. "
        "For directories, returns JSON with file/directory listing. When include_metadata=true, "
        "both files and directories include iRODS AVU metadata as response headers "
        "(X-Datastore-{attribute}). The avu-delimiter parameter controls the separator between "
        "value and unit in headers (default: ','). Requires authentication."
    ),
    response_description=(
        "JSON list of files and directories if path is a directory, or raw file contents "
        "as plain text if path is a file. When include_metadata=true, AVU metadata is "
        "included as X-Datastore-{attribute} response headers."
    ),
    responses={
        200: {
            "description": (
                "Directory contents or file contents retrieved successfully, "
                "with optional AVU metadata in response headers if include_metadata=true"
            ),
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
                    "description": (
                        "JSON response when path is a directory. "
                        "AVU metadata included as X-Datastore-{attribute} headers "
                        "when include_metadata=true."
                    ),
                },
                "text/plain": {
                    "example": "This is the raw file content...",
                    "description": (
                        "Raw file content when path is a file. "
                        "AVU metadata included as X-Datastore-{attribute} headers "
                        "when include_metadata=true."
                    ),
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
    """Browse iRODS directory or read file."""
    # Ensure path starts with / for iRODS
    irods_path = f"/{path}" if not path.startswith("/") else path

    # Check if path exists (could be file or collection)
    if not datastore.path_exists(irods_path):
        raise ResourceNotFoundError("Path", irods_path)

    # Extract username from JWT token
    username = extract_user_from_jwt(current_user)

    # Check if user has read permissions on the path
    if not datastore.user_can_read(username, irods_path):
        raise PermissionDeniedError()

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
        raise ResourceNotFoundError("Directory", irods_path)

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


@router.put(
    "/data/{path:path}",
    summary="Create directory, upload file, or set metadata",
    description=(
        "Create an iRODS directory, upload a file, or set metadata on an existing path. "
        "The path parameter should be the full iRODS path. "
        "\n\n"
        "**Metadata:** Metadata is provided via X-Datastore-{attribute} request headers. "
        "The avu-delimiter parameter controls how to parse value and units from header "
        "values (default: ','). "
        "Note: Custom headers cannot be set through the Swagger UI. Use curl or other "
        "HTTP clients for metadata. "
        "Example: `curl -X PUT -H 'X-Datastore-Author: username' -H "
        "'X-Datastore-Project: myproject' ...`"
        "\n\n"
        "**Operations:**\n"
        "- Create file: Send request body with file content\n"
        "- Create directory: Use resource_type=directory query parameter (no body)\n"
        "- Update file content: Send request body to existing file path\n"
        "- Update metadata only: Send request without body to existing path\n"
        "\n\n"
        "Requires authentication and write permissions."
    ),
    responses={
        200: {
            "description": "Operation successful",
            "content": {
                "application/json": {
                    "example": {
                        "path": "/cyverse/home/wregglej/test.txt",
                        "type": "data_object",
                        "created": True,
                    }
                }
            },
        },
        400: {
            "description": "Bad request - ambiguous operation or invalid parameters",
            "content": {
                "application/json": {
                    "example": {"detail": "Cannot determine operation type"}
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
            "description": "Insufficient permissions",
            "content": {"application/json": {"example": {"detail": "Access denied"}}},
        },
        404: {
            "description": "Parent directory not found",
            "content": {
                "application/json": {"example": {"detail": "Parent path not found"}}
            },
        },
        409: {
            "description": "Conflict - path exists with different type",
            "content": {
                "application/json": {
                    "example": {"detail": "File exists, cannot create directory"}
                }
            },
        },
    },
)
async def put_data(
    path: str,
    request: Request,
    current_user: Any = Depends(get_current_user),
    resource_type: str | None = None,
    avu_delimiter: str = ",",
    replace_metadata: bool = False,
):
    """Create directory, upload file, or set metadata on iRODS path."""
    # Ensure path starts with / for iRODS
    irods_path = f"/{path}" if not path.startswith("/") else path

    # Extract username from JWT token
    username = extract_user_from_jwt(current_user)

    # Read request body
    body_content = await request.body()
    has_content = len(body_content) > 0

    # Parse metadata from headers
    metadata: dict[str, tuple[str, str]] = {}
    for header_name, header_value in request.headers.items():
        if header_name.lower().startswith("x-datastore-"):
            attribute = header_name[len("x-datastore-") :]
            # Split by delimiter to get value and optional units
            parts = header_value.split(avu_delimiter, 1)
            value = parts[0]
            units = parts[1] if len(parts) > 1 else ""
            metadata[attribute] = (value, units)

    # Determine operation based on path existence and request content
    path_exists = datastore.path_exists(irods_path)
    is_file = datastore.file_exists(irods_path) if path_exists else False
    is_collection = datastore.collection_exists(irods_path) if path_exists else False

    if path_exists:
        # Path exists - either update content/metadata or metadata only
        if not datastore.user_can_write(username, irods_path):
            raise PermissionDeniedError()

        if has_content:
            # Update file content
            if is_collection:
                raise BadRequestError("Cannot upload file - path is a directory")

            await upload_file_async(irods_path, body_content)

            # Set metadata if provided
            if metadata:
                await set_file_metadata_async(irods_path, metadata, replace_metadata)

            return JSONResponse(
                content={
                    "path": irods_path,
                    "type": "data_object",
                    "created": False,
                }
            )
        else:
            # Metadata-only update
            if is_file:
                await set_file_metadata_async(irods_path, metadata, replace_metadata)
                resource_type_result = "data_object"
            else:
                await set_collection_metadata_async(
                    irods_path, metadata, replace_metadata
                )
                resource_type_result = "collection"

            return JSONResponse(
                content={
                    "path": irods_path,
                    "type": resource_type_result,
                    "created": False,
                }
            )

    else:
        # Path doesn't exist - create new file or directory
        # Check write permission on parent
        import os

        parent_path = os.path.dirname(irods_path)
        if not datastore.path_exists(parent_path):
            raise ResourceNotFoundError("Parent directory", parent_path)

        if not datastore.user_can_write(username, parent_path):
            raise PermissionDeniedError()

        if has_content:
            # Create file with content
            await upload_file_async(irods_path, body_content)

            # Set metadata if provided
            if metadata:
                await set_file_metadata_async(irods_path, metadata, replace_metadata)

            return JSONResponse(
                content={
                    "path": irods_path,
                    "type": "data_object",
                    "created": True,
                }
            )
        elif resource_type == "directory":
            # Create directory
            await create_directory_async(irods_path)

            # Set metadata if provided
            if metadata:
                await set_collection_metadata_async(
                    irods_path, metadata, replace_metadata
                )

            return JSONResponse(
                content={
                    "path": irods_path,
                    "type": "collection",
                    "created": True,
                }
            )
        else:
            # Ambiguous - no content and no type specified
            raise BadRequestError(
                "Cannot determine operation: provide file content or type=directory parameter"
            )


@router.delete(
    "/data/{path:path}",
    summary="Delete file or directory",
    description=(
        "Delete a file or directory from iRODS. "
        "\n\n"
        "**Dry-Run Mode:** Use dry_run=true to preview what would be deleted "
        "without actually deleting. This is useful for verifying the operation "
        "before executing it. The response will indicate what would be deleted "
        "and whether the operation would succeed. "
        "\n\n"
        "**Recursive Deletion:** Use recurse=true to delete non-empty directories. "
        "Default is false for safety. "
        "\n\n"
        "**Safety:** Deletions are permanent and cannot be undone. Always verify "
        "paths before deletion. Consider using dry-run mode first. "
        "\n\n"
        "Requires authentication and write permissions."
    ),
    responses={
        200: {
            "description": "Successfully deleted or dry-run completed",
            "content": {
                "application/json": {
                    "examples": {
                        "dry_run_file": {
                            "summary": "Dry-run file deletion",
                            "value": {
                                "path": "/cyverse/home/user/file.txt",
                                "type": "data_object",
                                "would_delete": True,
                                "deleted": False,
                                "dry_run": True,
                            },
                        },
                        "actual_directory": {
                            "summary": "Actual directory deletion with recurse",
                            "value": {
                                "path": "/cyverse/home/user/folder",
                                "type": "collection",
                                "would_delete": True,
                                "deleted": True,
                                "dry_run": False,
                                "item_count": 15,
                            },
                        },
                    }
                }
            },
        },
        400: {
            "description": "Bad request - non-empty directory without recurse",
            "content": {
                "application/json": {"example": {"detail": "Directory not empty"}}
            },
        },
        401: {
            "description": "Unauthorized - invalid or missing access token",
            "content": {
                "application/json": {"example": {"detail": "Not authenticated"}}
            },
        },
        403: {
            "description": "Insufficient permissions",
            "content": {"application/json": {"example": {"detail": "Access denied"}}},
        },
        404: {
            "description": "Path not found",
            "content": {
                "application/json": {"example": {"detail": "Path not found"}}
            },
        },
    },
)
async def delete_data(
    path: str,
    current_user: Any = Depends(get_current_user),
    recurse: bool = False,
    dry_run: bool = False,
):
    """Delete file or directory from iRODS."""
    # Ensure path starts with / for iRODS
    irods_path = f"/{path}" if not path.startswith("/") else path

    # Extract username from JWT token
    username = extract_user_from_jwt(current_user)

    # Check if path exists
    if not datastore.path_exists(irods_path):
        raise ResourceNotFoundError("Path", irods_path)

    # Check write permissions
    if not datastore.user_can_write(username, irods_path):
        raise PermissionDeniedError()

    # For non-empty directories without recurse, fail early (unless dry-run)
    if datastore.collection_exists(irods_path) and not recurse and not dry_run:
        collection = datastore.get_collection(irods_path)
        if collection is not None:
            has_items = False
            if hasattr(collection, "subcollections") and list(collection.subcollections):
                has_items = True
            if hasattr(collection, "data_objects") and list(collection.data_objects):
                has_items = True

            if has_items:
                raise BadRequestError(
                    "Directory not empty. Use recurse=true to delete non-empty directories."
                )

    # Perform deletion (or dry-run)
    result = await delete_path_async(irods_path, recurse=recurse, dry_run=dry_run)

    return JSONResponse(content=result)
