"""Unit tests for app endpoints."""

import os
import pytest
from uuid import uuid4
from unittest.mock import AsyncMock, patch, MagicMock
from fastapi.testclient import TestClient


@pytest.fixture
def test_app():
    """Create test FastAPI app with mocked dependencies."""
    # Set required env vars
    os.environ["DB_HOST"] = "localhost"
    os.environ["DB_PORT"] = "5432"
    os.environ["DB_USER"] = "test"
    os.environ["DB_PASSWORD"] = "test"
    os.environ["DB_NAME"] = "test"
    os.environ["IRODS_HOST"] = "localhost"
    os.environ["IRODS_PORT"] = "1247"
    os.environ["IRODS_USER"] = "test"
    os.environ["IRODS_PASSWORD"] = "test"
    os.environ["IRODS_ZONE"] = "testzone"
    os.environ["KEYCLOAK_SERVER_URL"] = "http://keycloak.test"
    os.environ["KEYCLOAK_REALM"] = "test"
    os.environ["KEYCLOAK_CLIENT_ID"] = "test"
    os.environ["KEYCLOAK_CLIENT_SECRET"] = "test"
    os.environ["APPS_BASE_URL"] = "http://apps.test"
    os.environ["APP_EXPOSER_BASE_URL"] = "http://app-exposer.test"

    # Mock psycopg connection
    with patch("psycopg.connect"):
        import main
        return main.app


@pytest.fixture
def client(test_app):
    """Create test client."""
    return TestClient(test_app)


@pytest.fixture
def mock_jwt_user():
    """Mock JWT user payload."""
    return {"preferred_username": "testuser", "sub": "user123"}


@pytest.fixture
def mock_token_verification(mock_jwt_user):
    """Mock JWT token verification."""
    with patch("auth.verify_token", new_callable=AsyncMock) as mock:
        mock.return_value = mock_jwt_user
        yield mock


class TestLaunchApp:
    """Tests for POST /app/launch endpoint."""

    def test_launch_app_success(self, client, mock_token_verification):
        """Test successful app launch."""
        analysis_id = str(uuid4())
        submission = {
            "app_id": str(uuid4()),
            "name": "Test Analysis",
            "config": {},
        }

        with patch("clients.AppsClient.submit_analysis", new_callable=AsyncMock) as mock_submit:
            mock_submit.return_value = {
                "id": analysis_id,
                "name": "Test Analysis",
                "status": "Submitted",
                "subdomain": analysis_id,
            }

            response = client.post(
                "/app/launch",
                json=submission,
                headers={"Authorization": "Bearer fake-token"},
            )

            assert response.status_code == 200
            data = response.json()
            assert data["analysis_id"] == analysis_id
            assert data["name"] == "Test Analysis"
            assert data["status"] == "Submitted"
            assert "url" in data

    def test_launch_app_unauthorized(self, client):
        """Test launch without authentication."""
        response = client.post("/app/launch", json={})
        assert response.status_code == 403  # FastAPI returns 403 for missing bearer

    def test_launch_app_service_error(self, client, mock_token_verification):
        """Test launch with apps service error."""
        from httpx import HTTPStatusError, Response, Request

        with patch("clients.AppsClient.submit_analysis", new_callable=AsyncMock) as mock_submit:
            mock_request = MagicMock(spec=Request)
            mock_response = MagicMock(spec=Response)
            mock_response.status_code = 500
            mock_response.text = "Internal error"
            mock_submit.side_effect = HTTPStatusError(
                "Error", request=mock_request, response=mock_response
            )

            response = client.post(
                "/app/launch",
                json={"app_id": str(uuid4())},
                headers={"Authorization": "Bearer fake-token"},
            )

            assert response.status_code == 500


class TestGetAppStatus:
    """Tests for GET /app/{analysis_id}/status endpoint."""

    def test_get_status_success(self, client, mock_token_verification):
        """Test successful status retrieval."""
        analysis_id = str(uuid4())

        with patch("clients.AppsClient.get_analysis", new_callable=AsyncMock) as mock_get:
            with patch("clients.AppExposerClient.check_url_ready", new_callable=AsyncMock) as mock_ready:
                mock_get.return_value = {
                    "id": analysis_id,
                    "status": "Running",
                    "subdomain": analysis_id,
                }
                mock_ready.return_value = {"ready": True}

                response = client.get(
                    f"/app/{analysis_id}/status",
                    headers={"Authorization": "Bearer fake-token"},
                )

                assert response.status_code == 200
                data = response.json()
                assert data["analysis_id"] == analysis_id
                assert data["status"] == "Running"
                assert data["url_ready"] is True
                assert "url" in data

    def test_get_status_not_ready(self, client, mock_token_verification):
        """Test status when URL not ready."""
        analysis_id = str(uuid4())

        with patch("clients.AppsClient.get_analysis", new_callable=AsyncMock) as mock_get:
            with patch("clients.AppExposerClient.check_url_ready", new_callable=AsyncMock) as mock_ready:
                mock_get.return_value = {
                    "id": analysis_id,
                    "status": "Launching",
                    "subdomain": analysis_id,
                }
                mock_ready.return_value = {"ready": False}

                response = client.get(
                    f"/app/{analysis_id}/status",
                    headers={"Authorization": "Bearer fake-token"},
                )

                assert response.status_code == 200
                data = response.json()
                assert data["url_ready"] is False

    def test_get_status_not_found(self, client, mock_token_verification):
        """Test status for non-existent analysis."""
        analysis_id = str(uuid4())
        from httpx import HTTPStatusError, Response, Request

        with patch("clients.AppsClient.get_analysis", new_callable=AsyncMock) as mock_get:
            mock_request = MagicMock(spec=Request)
            mock_response = MagicMock(spec=Response)
            mock_response.status_code = 404
            mock_response.text = "Not found"
            mock_get.side_effect = HTTPStatusError(
                "Not found", request=mock_request, response=mock_response
            )

            response = client.get(
                f"/app/{analysis_id}/status",
                headers={"Authorization": "Bearer fake-token"},
            )

            assert response.status_code == 404

    def test_get_status_invalid_uuid(self, client, mock_token_verification):
        """Test status with invalid UUID format."""
        response = client.get(
            "/app/not-a-uuid/status",
            headers={"Authorization": "Bearer fake-token"},
        )

        assert response.status_code == 400


class TestControlApp:
    """Tests for POST /app/{analysis_id}/control endpoint."""

    def test_control_extend_time(self, client, mock_token_verification):
        """Test extending time limit."""
        analysis_id = str(uuid4())

        with patch("clients.AppExposerClient.extend_time_limit", new_callable=AsyncMock) as mock_extend:
            mock_extend.return_value = {"time_limit": "2025-10-03T10:00:00Z"}

            response = client.post(
                f"/app/{analysis_id}/control?operation=extend_time",
                headers={"Authorization": "Bearer fake-token"},
            )

            assert response.status_code == 200
            data = response.json()
            assert data["operation"] == "extend_time"
            assert "time_limit" in data

    def test_control_save_and_exit(self, client, mock_token_verification):
        """Test save and exit operation."""
        analysis_id = str(uuid4())

        with patch("clients.AppExposerClient.save_and_exit", new_callable=AsyncMock) as mock_exit:
            mock_exit.return_value = {"status": "terminated", "outputs_saved": True}

            response = client.post(
                f"/app/{analysis_id}/control?operation=save_and_exit",
                headers={"Authorization": "Bearer fake-token"},
            )

            assert response.status_code == 200
            data = response.json()
            assert data["operation"] == "save_and_exit"
            assert data["status"] == "terminated"
            assert data["outputs_saved"] is True

    def test_control_exit_without_save(self, client, mock_token_verification):
        """Test exit without save operation."""
        analysis_id = str(uuid4())

        with patch("clients.AppExposerClient.exit_without_save", new_callable=AsyncMock) as mock_exit:
            mock_exit.return_value = {"status": "terminated", "outputs_saved": False}

            response = client.post(
                f"/app/{analysis_id}/control?operation=exit",
                headers={"Authorization": "Bearer fake-token"},
            )

            assert response.status_code == 200
            data = response.json()
            assert data["operation"] == "exit"
            assert data["outputs_saved"] is False

    def test_control_invalid_operation(self, client, mock_token_verification):
        """Test with invalid operation."""
        analysis_id = str(uuid4())

        response = client.post(
            f"/app/{analysis_id}/control?operation=invalid_op",
            headers={"Authorization": "Bearer fake-token"},
        )

        assert response.status_code == 400

    def test_control_analysis_not_found(self, client, mock_token_verification):
        """Test control on non-existent analysis."""
        analysis_id = str(uuid4())
        from httpx import HTTPStatusError, Response, Request

        with patch("clients.AppExposerClient.extend_time_limit", new_callable=AsyncMock) as mock_extend:
            mock_request = MagicMock(spec=Request)
            mock_response = MagicMock(spec=Response)
            mock_response.status_code = 404
            mock_response.text = "Not found"
            mock_extend.side_effect = HTTPStatusError(
                "Not found", request=mock_request, response=mock_response
            )

            response = client.post(
                f"/app/{analysis_id}/control?operation=extend_time",
                headers={"Authorization": "Bearer fake-token"},
            )

            assert response.status_code == 404
