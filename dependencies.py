"""Shared dependencies for route handlers."""

from typing import Any

from fastapi import Depends, HTTPException
from fastapi.security import HTTPAuthorizationCredentials, HTTPBearer

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


async def get_current_user_or_service_account(
    token: HTTPAuthorizationCredentials = Depends(bearer_auth),
) -> dict[str, Any]:
    """
    Verify JWT token and return either user payload or service account info.

    Service accounts must have the "app-runner" role in their Keycloak realm roles.
    If config.service_accounts_only is True, only service account authentication
    is accepted and regular user authentication will be rejected with 403.

    Returns a dictionary with one of two structures:
    - For regular users: {"type": "user", "user": <jwt_payload>}
    - For service accounts: {"type": "service_account", "service_account": <sa_info>}
    """
    payload = await auth.verify_token(
        config.keycloak_server_url,
        config.keycloak_realm,
        config.keycloak_client_id,
        token.credentials,
    )

    if auth.is_service_account(payload):
        service_account = auth.extract_service_account_from_jwt(payload)

        # Enforce app-runner role requirement for service accounts
        sa_roles = service_account.get("roles", [])
        if "app-runner" not in sa_roles:
            raise HTTPException(
                status_code=403,
                detail='Service account missing required role: "app-runner"',
            )

        return {
            "type": "service_account",
            "service_account": service_account,
        }

    # Check if service_accounts_only mode is enabled
    if config.service_accounts_only:
        raise HTTPException(
            status_code=403,
            detail="Service accounts only mode: regular user authentication is disabled",
        )

    return {"type": "user", "user": payload}


def require_service_account_with_role(allowed_roles: list[str]):
    """
    Factory function that creates a dependency requiring service account authentication
    with specific roles.

    Args:
        allowed_roles: List of role names that are allowed to access the endpoint

    Returns:
        Async dependency function that verifies service account and role membership
    """

    async def check_service_account(
        token: HTTPAuthorizationCredentials = Depends(bearer_auth),
    ) -> dict[str, Any]:
        payload = await auth.verify_token(
            config.keycloak_server_url,
            config.keycloak_realm,
            config.keycloak_client_id,
            token.credentials,
        )

        if not auth.is_service_account(payload):
            raise HTTPException(
                status_code=403,
                detail="This endpoint requires service account authentication",
            )

        service_account = auth.extract_service_account_from_jwt(payload)
        sa_roles = service_account.get("roles", [])

        if not any(role in sa_roles for role in allowed_roles):
            raise HTTPException(
                status_code=403,
                detail=f"Service account missing required role. Required: {allowed_roles}",
            )

        return {"type": "service_account", "service_account": service_account}

    return check_service_account


def extract_user_from_jwt(jwt_payload: Any) -> str:
    """Extract username from JWT token payload."""
    username = jwt_payload.get("preferred_username") or jwt_payload.get("sub")
    if not username:
        raise HTTPException(status_code=401, detail="Unable to determine user identity")
    return username


def extract_username_from_auth(auth_info: dict[str, Any]) -> str:
    """
    Extract username from either user or service account authentication info.

    For service accounts, maps the "app-runner" role to a configured username,
    then sanitizes it by removing special characters and converting to lowercase.
    If no mapping exists in config.service_account_usernames, uses "app-runner"
    as the username. For regular users, extracts the username from the JWT payload.

    Args:
        auth_info: Authentication info dict from get_current_user_or_service_account()

    Returns:
        Username string to use for backend service calls (sanitized for service accounts)
    """
    if auth_info["type"] == "service_account":
        # Use "app-runner" as the key since it's the required role
        role_name = "app-runner"

        # Look up mapping in config, fall back to role name if not found
        username = config.service_account_usernames.get(role_name, role_name)

        # Sanitize the username for backend system compatibility
        return auth.sanitize_username(username)
    else:
        return extract_user_from_jwt(auth_info["user"])
