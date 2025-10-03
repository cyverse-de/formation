"""HTTP clients for DE backend services."""

import os
from typing import Any
from uuid import UUID

import httpx


class AppsClient:
    """Client for the DE apps service."""

    def __init__(self, base_url: str | None = None, timeout: float = 30.0):
        """
        Initialize the apps service client.

        Args:
            base_url: Base URL for the apps service (defaults to env var APPS_BASE_URL)
            timeout: HTTP request timeout in seconds
        """
        self.base_url = base_url or os.environ.get("APPS_BASE_URL", "")
        if not self.base_url:
            raise ValueError("APPS_BASE_URL must be set")
        self.base_url = self.base_url.rstrip("/")
        self.timeout = timeout

    async def get_app(
        self, app_id: UUID, username: str, system_id: str = "de"
    ) -> dict[str, Any]:
        """
        Get app details by ID.

        Args:
            app_id: App UUID
            username: Username for request context
            system_id: System identifier (default: 'de' for Discovery Environment apps)

        Returns:
            App details dictionary
        """
        async with httpx.AsyncClient(timeout=self.timeout) as client:
            response = await client.get(
                f"{self.base_url}/apps/{system_id}/{app_id}",
                params={"user": username},
            )
            response.raise_for_status()
            return response.json()

    async def submit_analysis(
        self, submission: dict[str, Any], username: str, email: str
    ) -> dict[str, Any]:
        """
        Submit an analysis job.

        Args:
            submission: Analysis submission payload
            username: Username submitting the job
            email: User's email address (sent as query parameter)

        Returns:
            Analysis response with ID and status
        """
        async with httpx.AsyncClient(timeout=self.timeout) as client:
            response = await client.post(
                f"{self.base_url}/analyses",
                json=submission,
                params={"user": username, "email": email},
            )
            response.raise_for_status()
            return response.json()

    async def get_analysis(self, analysis_id: UUID, username: str) -> dict[str, Any]:
        """
        Get analysis details by ID.

        Args:
            analysis_id: Analysis UUID
            username: Username for request context

        Returns:
            Analysis details dictionary

        Raises:
            httpx.HTTPStatusError: If the request fails (including 404 if analysis not found)
        """
        import json

        # The apps service doesn't have a GET /analyses/{id} endpoint
        # Instead, we need to use GET /analyses with a filter
        # The filter must be a JSON array of filter objects
        filter_param = json.dumps([{"field": "id", "value": str(analysis_id)}])

        async with httpx.AsyncClient(timeout=self.timeout) as client:
            response = await client.get(
                f"{self.base_url}/analyses",
                params={"user": username, "filter": filter_param},
            )
            response.raise_for_status()
            result = response.json()

            # Extract the first (and only) analysis from the results
            analyses = result.get("analyses", [])
            if not analyses:
                # No analysis found - raise a 404-like error
                raise httpx.HTTPStatusError(
                    "Analysis not found",
                    request=response.request,
                    response=httpx.Response(404, request=response.request),
                )

            return analyses[0]

    async def list_apps(
        self,
        username: str,
        limit: int = 100,
        offset: int = 0,
        search: str | None = None,
    ) -> dict[str, Any]:
        """
        List apps accessible to the user.

        Args:
            username: Username for request context
            limit: Maximum number of apps to return
            offset: Number of apps to skip for pagination
            search: Optional search term to filter apps

        Returns:
            Dictionary with 'total' count and 'apps' list
        """
        params: dict[str, Any] = {
            "user": username,
            "limit": limit,
            "offset": offset,
        }
        if search:
            params["search"] = search

        async with httpx.AsyncClient(timeout=self.timeout) as client:
            response = await client.get(
                f"{self.base_url}/apps",
                params=params,
            )
            response.raise_for_status()
            return response.json()


class AppExposerClient:
    """Client for the DE app-exposer service."""

    def __init__(self, base_url: str | None = None, timeout: float = 30.0):
        """
        Initialize the app-exposer service client.

        Args:
            base_url: Base URL for app-exposer (defaults to env var APP_EXPOSER_BASE_URL)
            timeout: HTTP request timeout in seconds
        """
        self.base_url = base_url or os.environ.get("APP_EXPOSER_BASE_URL", "")
        if not self.base_url:
            raise ValueError("APP_EXPOSER_BASE_URL must be set")
        self.base_url = self.base_url.rstrip("/")
        self.timeout = timeout

    async def check_url_ready(self, host: str, username: str) -> dict[str, Any]:
        """
        Check if app URL is ready for access.

        Args:
            host: Subdomain/host for the analysis
            username: Username for permission check

        Returns:
            Dictionary with 'ready' boolean field
        """
        async with httpx.AsyncClient(timeout=self.timeout) as client:
            response = await client.get(
                f"{self.base_url}/vice/{host}/url-ready",
                params={"user": username},
            )
            response.raise_for_status()
            return response.json()

    async def extend_time_limit(self, analysis_id: UUID) -> dict[str, Any]:
        """
        Extend time limit for an analysis (admin endpoint).

        Args:
            analysis_id: Analysis UUID

        Returns:
            Updated time limit information
        """
        async with httpx.AsyncClient(timeout=self.timeout) as client:
            response = await client.post(
                f"{self.base_url}/vice/admin/analyses/{analysis_id}/time-limit"
            )
            response.raise_for_status()
            return response.json()

    async def get_time_limit(self, analysis_id: UUID) -> dict[str, Any]:
        """
        Get current time limit for an analysis (admin endpoint).

        Args:
            analysis_id: Analysis UUID

        Returns:
            Time limit information
        """
        async with httpx.AsyncClient(timeout=self.timeout) as client:
            response = await client.get(
                f"{self.base_url}/vice/admin/analyses/{analysis_id}/time-limit"
            )
            response.raise_for_status()
            return response.json()

    async def save_and_exit(self, analysis_id: UUID) -> dict[str, Any]:
        """
        Save outputs and terminate analysis (admin endpoint).

        Args:
            analysis_id: Analysis UUID

        Returns:
            Termination status
        """
        async with httpx.AsyncClient(timeout=self.timeout) as client:
            response = await client.post(
                f"{self.base_url}/vice/admin/analyses/{analysis_id}/save-and-exit"
            )
            response.raise_for_status()
            # App-exposer returns 200 with no body for this endpoint
            return {"status": "terminated", "outputs_saved": True}

    async def exit_without_save(self, analysis_id: UUID) -> dict[str, Any]:
        """
        Terminate analysis without saving outputs (admin endpoint).

        Args:
            analysis_id: Analysis UUID

        Returns:
            Termination status
        """
        async with httpx.AsyncClient(timeout=self.timeout) as client:
            response = await client.post(
                f"{self.base_url}/vice/admin/analyses/{analysis_id}/exit"
            )
            response.raise_for_status()
            # App-exposer returns 200 with no body for this endpoint
            return {"status": "terminated", "outputs_saved": False}

    async def get_external_id(self, analysis_id: UUID) -> dict[str, Any]:
        """
        Get external ID for an analysis (admin endpoint).

        Args:
            analysis_id: Analysis UUID

        Returns:
            Dictionary with 'external_id' field
        """
        async with httpx.AsyncClient(timeout=self.timeout) as client:
            response = await client.get(
                f"{self.base_url}/vice/admin/analyses/{analysis_id}/external-id"
            )
            response.raise_for_status()
            return response.json()

    async def get_async_data(self, external_id: str) -> dict[str, Any]:
        """
        Get asynchronously generated data for an analysis.

        This endpoint returns data that is generated after the analysis starts,
        including the subdomain. May return 404 if deployment is not ready yet.

        Args:
            external_id: External ID (invocation ID) of the analysis

        Returns:
            Dictionary with 'analysisID', 'subdomain', and 'ipAddr' fields
        """
        async with httpx.AsyncClient(timeout=self.timeout) as client:
            response = await client.get(
                f"{self.base_url}/vice/async-data",
                params={"external-id": external_id},
            )
            response.raise_for_status()
            return response.json()
