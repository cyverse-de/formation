from typing import Any
from urllib.parse import urljoin

import httpx
from fastapi import Depends, HTTPException
from fastapi.security import HTTPBearer
from jose import JWTError, jwt

security = HTTPBearer()


async def get_keycloak_public_key(server_url: str, realm: str):
    well_known_url = urljoin(
        server_url, f"realms/{realm}/.well-known/openid-configuration"
    )

    async with httpx.AsyncClient() as client:
        response = await client.get(well_known_url)
        response.raise_for_status()
        config = response.json()
        certs_url = config["jwks_uri"]

    async with httpx.AsyncClient() as client:
        response = await client.get(certs_url)
        response.raise_for_status()
        return response.json()


async def verify_token(
    server_url: str, realm: str, client_id: str, token: str = Depends(security)
):
    try:
        jwks = await get_keycloak_public_key(server_url, realm)

        unverified_header = jwt.get_unverified_header(token)

        rsa_key = {}
        for key in jwks["keys"]:
            if key["kid"] == unverified_header["kid"]:
                rsa_key = {
                    "kty": key["kty"],
                    "kid": key["kid"],
                    "use": key["use"],
                    "n": key["n"],
                    "e": key["e"],
                }

        if rsa_key:
            payload = jwt.decode(
                token,
                rsa_key,
                algorithms=["RS256"],
                options={"verify_aud": False},
                issuer=urljoin(server_url, f"realms/{realm}"),
            )
            return payload
        else:
            raise HTTPException(
                status_code=401, detail="Unable to find appropriate key"
            )

    except JWTError as e:
        raise HTTPException(
            status_code=401, detail=f"Token validation failed: {str(e)}"
        )
    except Exception as e:
        raise HTTPException(status_code=401, detail=f"Authentication error: {str(e)}")


def is_service_account(jwt_payload: dict[str, Any]) -> bool:
    """
    Check if a JWT payload represents a service account.

    Service accounts are identified by their preferred_username starting with "service-account-".

    Args:
        jwt_payload: Decoded JWT payload dictionary

    Returns:
        True if the payload represents a service account, False otherwise
    """
    preferred_username = jwt_payload.get("preferred_username", "")
    return preferred_username.startswith("service-account-")


def extract_service_account_from_jwt(jwt_payload: dict[str, Any]) -> dict[str, Any]:
    """
    Extract service account information from a JWT payload.

    Args:
        jwt_payload: Decoded JWT payload dictionary

    Returns:
        Dictionary containing service account username and roles:
        {
            "username": "service-account-...",
            "roles": ["role1", "role2", ...]
        }
    """
    return {
        "username": jwt_payload.get("preferred_username", ""),
        "roles": jwt_payload.get("realm_access", {}).get("roles", []),
    }


def sanitize_username(username: str) -> str:
    """
    Sanitize a username for use with backend systems.

    Removes all special characters, keeping only lowercase letters and numbers.
    This ensures usernames are compatible with backend system requirements.

    Args:
        username: The username to sanitize (e.g., "de-service-account", "app-runner")

    Returns:
        Sanitized username with only lowercase letters and numbers

    Examples:
        >>> sanitize_username("de-service-account")
        'deserviaceaccount'
        >>> sanitize_username("app-runner")
        'apprunner'
        >>> sanitize_username("Service123_Account")
        'service123account'
    """
    # Remove all non-alphanumeric characters and convert to lowercase
    return "".join(char.lower() for char in username if char.isalnum())


async def get_access_token(
    keycloak_server_url: str,
    keycloak_realm: str,
    keycloak_client_id: str,
    keycloak_client_secret: str,
    username: str,
    password: str,
    ssl_verify: bool = True,
) -> dict[str, Any]:
    token_url = urljoin(
        keycloak_server_url, f"realms/{keycloak_realm}/protocol/openid-connect/token"
    )

    data = {
        "grant_type": "password",
        "client_id": keycloak_client_id,
        "client_secret": keycloak_client_secret,
        "username": username,
        "password": password,
    }

    async with httpx.AsyncClient(verify=ssl_verify) as client:
        response = await client.post(
            token_url,
            data=data,
        )
        response.raise_for_status()
        return response.json()
