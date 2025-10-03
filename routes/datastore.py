"""Data Store routes for Formation API."""

import asyncio
import mimetypes
import sys
from typing import Any

import psycopg
from fastapi import APIRouter, Depends, HTTPException
from fastapi.responses import JSONResponse
from fastapi.responses import Response as FastAPIResponse

import ds
from config import config
from dependencies import get_current_user, extract_user_from_jwt


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


@router.get(
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
    """Browse iRODS directory or read file."""
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
