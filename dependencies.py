"""Shared dependencies for route handlers."""

from typing import Any
from fastapi import Depends, HTTPException
from fastapi.security import HTTPBearer, HTTPAuthorizationCredentials

import auth
from config import config

bearer_auth = HTTPBearer()


async def get_current_user(
    token: HTTPAuthorizationCredentials = Depends(bearer_auth),
) -> Any:
    """Verify JWT token and return user payload."""
    return await auth.verify_token(
        config.keycloak_server_url,
        config.keycloak_realm,
        config.keycloak_client_id,
        token.credentials,
    )


def extract_user_from_jwt(jwt_payload: Any) -> str:
    """Extract username from JWT token payload."""
    username = jwt_payload.get("preferred_username") or jwt_payload.get("sub")
    if not username:
        raise HTTPException(status_code=401, detail="Unable to determine user identity")
    return username
