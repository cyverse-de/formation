"""Authentication routes for Formation API."""

from typing import Any

from fastapi import APIRouter, Depends

from dependencies import get_current_user, extract_user_from_jwt


router = APIRouter(prefix="", tags=["Authentication"])


@router.get("/user")
async def get_user_info(
    user: Any = Depends(get_current_user),
) -> dict[str, Any]:
    """Get information about the authenticated user.

    Returns the user's profile information extracted from the JWT token.
    """
    username = extract_user_from_jwt(user)

    return {
        "username": username,
        "email": user.get("email"),
        "name": user.get("name"),
        "preferred_username": user.get("preferred_username"),
    }
