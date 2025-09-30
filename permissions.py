"""
Permissions service client for accessing app permissions.

This module provides functions to interact with the permissions service
to determine which apps are accessible to users.
"""

import os
from typing import Set
from uuid import UUID

import httpx


class PermissionsClient:
    """Client for interacting with the permissions service."""

    def __init__(self, base_url: str | None = None):
        """
        Initialize the permissions client.

        Args:
            base_url: Base URL of the permissions service. If not provided,
                     uses the PERMISSIONS_BASE_URL environment variable.
        """
        self.base_url = base_url or os.environ.get(
            "PERMISSIONS_BASE_URL", "http://permissions"
        )
        self.grouper_user_group_id = os.environ.get(
            "GROUPER_USER_GROUP_ID", "de-users"
        )

    async def get_public_app_ids(self) -> Set[UUID]:
        """
        Get the set of all public app IDs.

        Public apps are those that have read permission granted to the
        grouper user group (typically "de-users").

        Returns:
            Set of UUIDs for all public apps.

        Raises:
            httpx.HTTPError: If the permissions service request fails.
        """
        async with httpx.AsyncClient() as client:
            response = await client.get(
                f"{self.base_url}/permissions/abbreviated/subjects/group/{self.grouper_user_group_id}/app",
                params={"lookup": "false"},
                timeout=30.0,
            )
            response.raise_for_status()
            data = response.json()

            # Extract app IDs from the permissions response
            app_ids = set()
            for perm in data.get("permissions", []):
                resource_name = perm.get("resource_name")
                if resource_name:
                    try:
                        app_ids.add(UUID(resource_name))
                    except ValueError:
                        # Skip invalid UUIDs
                        continue

            return app_ids

    async def get_user_accessible_app_ids(
        self, username: str, min_level: str = "read"
    ) -> Set[UUID]:
        """
        Get the set of app IDs accessible to a specific user.

        Args:
            username: The username to check permissions for.
            min_level: Minimum permission level required (default: "read").

        Returns:
            Set of UUIDs for apps the user can access.

        Raises:
            httpx.HTTPError: If the permissions service request fails.
        """
        async with httpx.AsyncClient() as client:
            response = await client.get(
                f"{self.base_url}/permissions/abbreviated/subjects/user/{username}/app",
                params={"lookup": "true", "min_level": min_level},
                timeout=30.0,
            )
            response.raise_for_status()
            data = response.json()

            # Extract app IDs from the permissions response
            app_ids = set()
            for perm in data.get("permissions", []):
                resource_name = perm.get("resource_name")
                if resource_name:
                    try:
                        app_ids.add(UUID(resource_name))
                    except ValueError:
                        # Skip invalid UUIDs
                        continue

            return app_ids

    async def get_accessible_app_ids(self, username: str | None = None) -> Set[UUID]:
        """
        Get the set of all app IDs accessible to a user.

        This combines public app IDs with user-specific accessible app IDs.
        If no username is provided, only returns public apps.

        Args:
            username: The username to check permissions for. If None, only
                     returns public apps.

        Returns:
            Set of UUIDs for all accessible apps.

        Raises:
            httpx.HTTPError: If the permissions service request fails.
        """
        public_apps = await self.get_public_app_ids()

        if username:
            user_apps = await self.get_user_accessible_app_ids(username)
            return public_apps | user_apps

        return public_apps
