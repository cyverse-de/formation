"""HTTP clients for DE backend services."""

import os
from typing import TYPE_CHECKING, Any
from uuid import UUID

import httpx

if TYPE_CHECKING:
    from main import AnalysisSubmission


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

    async def get_app(self, app_id: UUID, username: str) -> dict[str, Any]:
        """
        Get app details by ID.

        Args:
            app_id: App UUID
            username: Username for request context

        Returns:
            App details dictionary
        """
        async with httpx.AsyncClient(timeout=self.timeout) as client:
            response = await client.get(
                f"{self.base_url}/apps/{app_id}",
                params={"user": username},
            )
            response.raise_for_status()
            return response.json()

    async def submit_analysis(
        self, submission: "dict[str, Any] | AnalysisSubmission", username: str
    ) -> dict[str, Any]:
        """
        Submit an analysis job.

        Args:
            submission: Analysis submission payload
            username: Username submitting the job

        Returns:
            Analysis response with ID and status
        """
        async with httpx.AsyncClient(timeout=self.timeout) as client:
            response = await client.post(
                f"{self.base_url}/analyses",
                json=submission,
                params={"user": username},
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
        """
        async with httpx.AsyncClient(timeout=self.timeout) as client:
            response = await client.get(
                f"{self.base_url}/analyses/{analysis_id}",
                params={"user": username},
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
