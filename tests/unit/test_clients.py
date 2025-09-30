"""Unit tests for HTTP clients."""

import pytest
from uuid import UUID, uuid4

from clients import AppsClient, AppExposerClient


class TestAppsClient:
    """Tests for AppsClient."""

    @pytest.fixture
    def client(self):
        """Create AppsClient with test base URL."""
        return AppsClient(base_url="http://apps.test")

    @pytest.mark.asyncio
    async def test_get_app_success(self, client, httpx_mock):
        """Test successful app retrieval."""
        app_id = uuid4()
        username = "testuser"
        expected_response = {
            "id": str(app_id),
            "name": "Test App",
            "description": "A test application",
        }

        httpx_mock.add_response(
            url=f"http://apps.test/apps/{app_id}?user={username}",
            json=expected_response,
            status_code=200,
        )

        result = await client.get_app(app_id, username)
        assert result == expected_response

    @pytest.mark.asyncio
    async def test_get_app_not_found(self, client, httpx_mock):
        """Test app not found error."""
        app_id = uuid4()
        username = "testuser"

        httpx_mock.add_response(
            url=f"http://apps.test/apps/{app_id}?user={username}",
            status_code=404,
        )

        with pytest.raises(Exception):  # httpx.HTTPStatusError
            await client.get_app(app_id, username)

    @pytest.mark.asyncio
    async def test_submit_analysis_success(self, client, httpx_mock):
        """Test successful analysis submission."""
        username = "testuser"
        analysis_id = uuid4()
        submission = {
            "app_id": str(uuid4()),
            "name": "Test Analysis",
            "config": {},
        }
        expected_response = {
            "id": str(analysis_id),
            "name": "Test Analysis",
            "status": "Submitted",
        }

        httpx_mock.add_response(
            url=f"http://apps.test/analyses?user={username}",
            json=expected_response,
            status_code=200,
        )

        result = await client.submit_analysis(submission, username)
        assert result == expected_response
        assert result["id"] == str(analysis_id)

    @pytest.mark.asyncio
    async def test_get_analysis_success(self, client, httpx_mock):
        """Test successful analysis retrieval."""
        analysis_id = uuid4()
        username = "testuser"
        expected_response = {
            "id": str(analysis_id),
            "name": "Test Analysis",
            "status": "Running",
        }

        httpx_mock.add_response(
            url=f"http://apps.test/analyses/{analysis_id}?user={username}",
            json=expected_response,
            status_code=200,
        )

        result = await client.get_analysis(analysis_id, username)
        assert result == expected_response


class TestAppExposerClient:
    """Tests for AppExposerClient."""

    @pytest.fixture
    def client(self):
        """Create AppExposerClient with test base URL."""
        return AppExposerClient(base_url="http://app-exposer.test")

    @pytest.mark.asyncio
    async def test_check_url_ready_true(self, client, httpx_mock):
        """Test URL ready check returns true."""
        host = "test-analysis"
        username = "testuser"
        expected_response = {"ready": True}

        httpx_mock.add_response(
            url=f"http://app-exposer.test/vice/{host}/url-ready?user={username}",
            json=expected_response,
            status_code=200,
        )

        result = await client.check_url_ready(host, username)
        assert result == expected_response
        assert result["ready"] is True

    @pytest.mark.asyncio
    async def test_check_url_ready_false(self, client, httpx_mock):
        """Test URL ready check returns false."""
        host = "test-analysis"
        username = "testuser"
        expected_response = {"ready": False}

        httpx_mock.add_response(
            url=f"http://app-exposer.test/vice/{host}/url-ready?user={username}",
            json=expected_response,
            status_code=200,
        )

        result = await client.check_url_ready(host, username)
        assert result["ready"] is False

    @pytest.mark.asyncio
    async def test_extend_time_limit_success(self, client, httpx_mock):
        """Test successful time limit extension."""
        analysis_id = uuid4()
        expected_response = {
            "time_limit": "2025-10-03T10:00:00Z",
        }

        httpx_mock.add_response(
            url=f"http://app-exposer.test/vice/admin/analyses/{analysis_id}/time-limit",
            json=expected_response,
            status_code=200,
            method="POST",
        )

        result = await client.extend_time_limit(analysis_id)
        assert result == expected_response

    @pytest.mark.asyncio
    async def test_get_time_limit_success(self, client, httpx_mock):
        """Test successful time limit retrieval."""
        analysis_id = uuid4()
        expected_response = {
            "time_limit": "2025-10-01T10:00:00Z",
        }

        httpx_mock.add_response(
            url=f"http://app-exposer.test/vice/admin/analyses/{analysis_id}/time-limit",
            json=expected_response,
            status_code=200,
            method="GET",
        )

        result = await client.get_time_limit(analysis_id)
        assert result == expected_response

    @pytest.mark.asyncio
    async def test_save_and_exit_success(self, client, httpx_mock):
        """Test successful save and exit."""
        analysis_id = uuid4()

        httpx_mock.add_response(
            url=f"http://app-exposer.test/vice/admin/analyses/{analysis_id}/save-and-exit",
            status_code=200,
            method="POST",
        )

        result = await client.save_and_exit(analysis_id)
        assert result["status"] == "terminated"
        assert result["outputs_saved"] is True

    @pytest.mark.asyncio
    async def test_exit_without_save_success(self, client, httpx_mock):
        """Test successful exit without save."""
        analysis_id = uuid4()

        httpx_mock.add_response(
            url=f"http://app-exposer.test/vice/admin/analyses/{analysis_id}/exit",
            status_code=200,
            method="POST",
        )

        result = await client.exit_without_save(analysis_id)
        assert result["status"] == "terminated"
        assert result["outputs_saved"] is False
