"""Main FastAPI application module."""

import sys
import traceback
from collections.abc import Awaitable, Callable
from typing import Any

import httpx
from fastapi import Depends, FastAPI, HTTPException, Request, Response
from fastapi.responses import JSONResponse
from fastapi.security import HTTPBasic, HTTPBasicCredentials
from starlette.exceptions import HTTPException as StarletteHTTPException

import auth
import ds
from config import config
from exceptions import FormationError
from routes import apps, datastore
from routes import auth as auth_routes

tags_metadata = [
    {
        "name": "Authentication",
        "description": "User authentication and session management via Keycloak OIDC",
    },
    {
        "name": "Apps",
        "description": (
            "Discover, launch, and manage Discovery Environment applications and analyses"
        ),
    },
    {
        "name": "Data Store",
        "description": (
            "Browse and access files in iRODS distributed file system with metadata support"
        ),
    },
]

# Normalize path prefix - empty string or "/" means no prefix
path_prefix = config.path_prefix
if path_prefix in ("", "/"):
    path_prefix = ""
elif not path_prefix.startswith("/"):
    path_prefix = f"/{path_prefix}"

app = FastAPI(
    openapi_tags=tags_metadata,
    root_path=path_prefix,
)


@app.get("/", status_code=200, tags=["Health"])
def greeting():
    """
    Health check endpoint that returns a greeting message.

    This endpoint is intentionally unauthenticated to allow health checks
    from monitoring systems and load balancers.
    """
    return "Hello from formation."


# Include routers
app.include_router(apps.router)
app.include_router(auth_routes.router)
app.include_router(datastore.router)


@app.exception_handler(FormationError)
async def formation_exception_handler(
    request: Request, exc: FormationError
) -> JSONResponse:
    """Handle custom Formation exceptions."""
    del request  # Unused but required by FastAPI signature
    print(f"{exc.__class__.__name__}: {exc.message}", file=sys.stderr)
    response_content: dict[str, Any] = {"detail": exc.message}
    if exc.details:
        response_content["details"] = exc.details
    return JSONResponse(content=response_content, status_code=exc.status_code)


@app.exception_handler(StarletteHTTPException)
async def http_exception_handler(
    request: Request, exc: StarletteHTTPException
) -> JSONResponse:
    """Handle FastAPI HTTPException."""
    del request  # Unused but required by FastAPI signature
    print(exc, file=sys.stderr)
    return JSONResponse(content={"detail": exc.detail}, status_code=exc.status_code)


@app.exception_handler(httpx.HTTPStatusError)
async def httpx_exception_handler(
    request: Request, exc: httpx.HTTPStatusError
) -> JSONResponse:
    """Handle httpx HTTP errors from external services."""
    del request  # Unused but required by FastAPI signature
    print(f"External service error: {exc.response.status_code}", file=sys.stderr)
    return JSONResponse(
        content={
            "detail": f"External service error: {exc.response.text}",
            "status_code": exc.response.status_code,
        },
        status_code=502,  # Bad Gateway for external service errors
    )


@app.middleware("http")
async def exception_handling_middleware(
    request: Request, call_next: Callable[[Request], Awaitable[Response]]
) -> Response:
    """Catch-all middleware for unexpected exceptions."""
    try:
        return await call_next(request)
    except Exception as e:
        print(
            f"Unhandled exception ({e.__class__.__name__}): {str(e)}", file=sys.stderr
        )
        print(traceback.format_exc(), file=sys.stderr)
        return JSONResponse(
            content={"detail": "Internal server error", "error": str(e)},
            status_code=500,
        )


# Initialize datastore with config
datastore_api = ds.DataStoreAPI(
    config.irods_host,
    config.irods_port,
    config.irods_user,
    config.irods_password,
    config.irods_zone,
)

basic_auth = HTTPBasic()


@app.post(
    "/login",
    tags=["Authentication"],
    summary="Authenticate user and obtain access token",
    description=(
        "Authenticates a user with username and password via HTTP Basic Auth and returns "
        "an access token from Keycloak"
    ),
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
    """Authenticate user and obtain access token."""
    try:
        return await auth.get_access_token(
            config.keycloak_server_url,
            config.keycloak_realm,
            config.keycloak_client_id,
            config.keycloak_client_secret,
            credentials.username,
            credentials.password,
            config.keycloak_ssl_verify,
        )
    except httpx.HTTPStatusError as e:
        if e.response.status_code == 401:
            raise HTTPException(status_code=401, detail="Invalid credentials")
        raise HTTPException(status_code=500, detail="Authentication service error")
    except Exception as e:
        raise HTTPException(status_code=500, detail=f"Login failed: {str(e)}")
