"""Unit tests for app endpoints."""

import os
from unittest.mock import AsyncMock, MagicMock, patch
from uuid import uuid4

import pytest
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
    os.environ["VICE_DOMAIN"] = ".cyverse.run"
    os.environ["PATH_PREFIX"] = ""

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
    return {"preferred_username": "testuser", "sub": "user123", "email": "testuser@example.com"}


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
        app_id = str(uuid4())
        external_id = str(uuid4())
        submission = {
            "name": "Test Analysis",
            "config": {},
        }

        with patch("clients.AppsClient.submit_analysis", new_callable=AsyncMock) as mock_submit:
            with patch("clients.AppExposerClient.get_external_id", new_callable=AsyncMock) as mock_external_id:
                with patch("clients.AppExposerClient.get_async_data", new_callable=AsyncMock) as mock_async_data:
                    mock_submit.return_value = {
                        "id": analysis_id,
                        "name": "Test Analysis",
                        "status": "Submitted",
                    }
                    mock_external_id.return_value = {"external_id": external_id}
                    mock_async_data.return_value = {
                        "analysisID": analysis_id,
                        "subdomain": "a12345678",
                        "ipAddr": "10.0.0.1",
                    }

                    response = client.post(
                        f"/app/launch/de/{app_id}",
                        json=submission,
                        headers={"Authorization": "Bearer fake-token"},
                    )

                    assert response.status_code == 200
                    data = response.json()
                    assert data["analysis_id"] == analysis_id
                    assert data["status"] == "Submitted"
                    assert "url" in data
                    # URL includes the VICE_DOMAIN from config (which may include port)
                    assert "a12345678" in data["url"]
                    assert data["url"].startswith("https://")

                    # Verify defaults were added by the endpoint
                    mock_submit.assert_called_once()
                    submitted_payload = mock_submit.call_args[0][0]
                    username_arg = mock_submit.call_args[0][1]
                    email_arg = mock_submit.call_args[0][2]

                    assert email_arg == "testuser@example.com"
                    assert username_arg == "testuser"
                    assert submitted_payload["name"] == "Test Analysis"
                    assert submitted_payload["config"] == {}

                    # Verify external ID and async data were fetched
                    mock_external_id.assert_called_once()
                    mock_async_data.assert_called_once_with(external_id)

    def test_launch_app_success_with_explicit_system_id(self, client, mock_token_verification):
        """Test successful app launch with explicit system_id."""
        analysis_id = str(uuid4())
        app_id = str(uuid4())
        submission = {
            "name": "Test Analysis",
            "system_id": "de",
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
                f"/app/launch/de/{app_id}",
                json=submission,
                headers={"Authorization": "Bearer fake-token"},
            )

            assert response.status_code == 200

            # Verify explicit system_id was preserved (from path parameter)
            mock_submit.assert_called_once()
            submitted_payload = mock_submit.call_args[0][0]
            assert submitted_payload["system_id"] == "de"

    def test_launch_app_with_explicit_debug_and_notify(self, client, mock_token_verification):
        """Test that explicit debug and notify values are preserved."""
        analysis_id = str(uuid4())
        app_id = str(uuid4())
        submission = {
            "name": "Test Analysis",
            "debug": True,
            "notify": False,
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
                f"/app/launch/de/{app_id}",
                json=submission,
                headers={"Authorization": "Bearer fake-token"},
            )

            assert response.status_code == 200

            # Verify explicit values were preserved
            mock_submit.assert_called_once()
            submitted_payload = mock_submit.call_args[0][0]
            assert submitted_payload["debug"] is True
            assert submitted_payload["notify"] is False

    def test_launch_app_without_config(self, client, mock_token_verification):
        """Test that config defaults to empty dict when not provided."""
        analysis_id = str(uuid4())
        app_id = str(uuid4())
        submission = {
            "name": "Test Analysis",
            # Intentionally omit config
        }

        with patch("clients.AppsClient.submit_analysis", new_callable=AsyncMock) as mock_submit:
            mock_submit.return_value = {
                "id": analysis_id,
                "name": "Test Analysis",
                "status": "Submitted",
                "subdomain": analysis_id,
            }

            response = client.post(
                f"/app/launch/de/{app_id}",
                json=submission,
                headers={"Authorization": "Bearer fake-token"},
            )

            assert response.status_code == 200

            # Verify config was set to empty dict
            mock_submit.assert_called_once()
            submitted_payload = mock_submit.call_args[0][0]
            assert submitted_payload["config"] == {}

    def test_launch_app_auto_generate_name(self, client, mock_token_verification):
        """Test that name is auto-generated from app name when not provided."""
        analysis_id = str(uuid4())
        app_id = str(uuid4())
        submission = {
            # Intentionally omit name
        }

        with patch("clients.AppsClient.get_app", new_callable=AsyncMock) as mock_get_app:
            with patch("clients.AppsClient.submit_analysis", new_callable=AsyncMock) as mock_submit:
                # Mock get_app to return app details
                mock_get_app.return_value = {
                    "id": str(app_id),
                    "name": "Jupyter Lab",
                    "description": "Interactive Jupyter environment",
                }

                # Mock submit_analysis
                mock_submit.return_value = {
                    "id": analysis_id,
                    "name": "jupyter-lab-2025-10-01-141523",
                    "status": "Submitted",
                    "subdomain": analysis_id,
                }

                response = client.post(
                    f"/app/launch/de/{app_id}",
                    json=submission,
                    headers={"Authorization": "Bearer fake-token"},
                )

                assert response.status_code == 200
                data = response.json()

                # Verify name was auto-generated
                assert "name" in data
                assert data["name"].startswith("jupyter-lab-")
                assert len(data["name"]) > len("jupyter-lab-")

                # Verify get_app was called to fetch app name
                mock_get_app.assert_called_once()

                # Verify name was added to submission
                mock_submit.assert_called_once()
                submitted_payload = mock_submit.call_args[0][0]
                assert "name" in submitted_payload
                assert submitted_payload["name"].startswith("jupyter-lab-")

    def test_launch_app_with_empty_body(self, client, mock_token_verification):
        """Test that completely empty body works with all defaults."""
        analysis_id = str(uuid4())
        app_id = str(uuid4())

        with patch("clients.AppsClient.get_app", new_callable=AsyncMock) as mock_get_app:
            with patch("clients.AppsClient.submit_analysis", new_callable=AsyncMock) as mock_submit:
                # Mock get_app to return app details
                mock_get_app.return_value = {
                    "id": str(app_id),
                    "name": "RStudio",
                    "description": "Interactive R environment",
                }

                # Mock submit_analysis
                mock_submit.return_value = {
                    "id": analysis_id,
                    "name": "rstudio-2025-10-01-141523",
                    "status": "Submitted",
                    "subdomain": analysis_id,
                }

                response = client.post(
                    f"/app/launch/de/{app_id}",
                    headers={"Authorization": "Bearer fake-token"},
                )

                assert response.status_code == 200
                data = response.json()

                # Verify all defaults were applied
                mock_submit.assert_called_once()
                submitted_payload = mock_submit.call_args[0][0]
                assert submitted_payload["app_id"] == str(app_id)
                assert submitted_payload["system_id"] == "de"  # From path parameter
                assert submitted_payload["debug"] is False
                assert submitted_payload["notify"] is True
                assert submitted_payload["config"] == {}
                assert submitted_payload["name"].startswith("rstudio-")

    def test_launch_app_email_fallback(self, client):
        """Test email fallback when not in JWT token."""
        analysis_id = str(uuid4())
        app_id = str(uuid4())

        # Mock JWT user without email field
        mock_user_no_email = {"preferred_username": "testuser", "sub": "user123"}

        with patch("auth.verify_token", new_callable=AsyncMock) as mock_verify:
            with patch("clients.AppsClient.submit_analysis", new_callable=AsyncMock) as mock_submit:
                mock_verify.return_value = mock_user_no_email
                mock_submit.return_value = {
                    "id": analysis_id,
                    "name": "Test Analysis",
                    "status": "Submitted",
                    "subdomain": analysis_id,
                }

                response = client.post(
                    f"/app/launch/de/{app_id}",
                    headers={"Authorization": "Bearer fake-token"},
                )

                assert response.status_code == 200

                # Verify email was constructed from username + suffix and passed as query param
                mock_submit.assert_called_once()
                email_arg = mock_submit.call_args[0][2]
                assert email_arg == "testuser@iplantcollaborative.org"

    def test_launch_app_unauthorized(self, client):
        """Test launch without authentication."""
        app_id = str(uuid4())
        response = client.post(f"/app/launch/de/{app_id}")
        assert response.status_code == 403  # FastAPI returns 403 for missing bearer

    def test_launch_app_invalid_uuid(self, client, mock_token_verification):
        """Test launch with invalid app_id format."""
        response = client.post(
            "/app/launch/de/not-a-uuid",
            headers={"Authorization": "Bearer fake-token"},
        )
        assert response.status_code == 400
        assert "Invalid app ID format" in response.json()["detail"]

    def test_launch_app_service_error(self, client, mock_token_verification):
        """Test launch with apps service error."""
        app_id = str(uuid4())
        from httpx import HTTPStatusError, Request, Response

        with patch("clients.AppsClient.submit_analysis", new_callable=AsyncMock) as mock_submit:
            mock_request = MagicMock(spec=Request)
            mock_response = MagicMock(spec=Response)
            mock_response.status_code = 500
            mock_response.text = "Internal error"
            mock_submit.side_effect = HTTPStatusError(
                "Error", request=mock_request, response=mock_response
            )

            response = client.post(
                f"/app/launch/de/{app_id}",
                headers={"Authorization": "Bearer fake-token"},
            )

            # External service errors are now returned as 502 Bad Gateway
            assert response.status_code == 502


class TestGetAppStatus:
    """Tests for GET /app/{analysis_id}/status endpoint."""

    def test_get_status_success(self, client, mock_token_verification):
        """Test successful status retrieval."""
        analysis_id = str(uuid4())
        external_id = str(uuid4())

        with patch("clients.AppsClient.get_analysis", new_callable=AsyncMock) as mock_get:
            with patch("clients.AppExposerClient.get_external_id", new_callable=AsyncMock) as mock_external:
                with patch("clients.AppExposerClient.get_async_data", new_callable=AsyncMock) as mock_async:
                    with patch("routes.apps.check_vice_url_ready", new_callable=AsyncMock) as mock_ready:
                        mock_get.return_value = {
                            "id": analysis_id,
                            "status": "Running",
                        }
                        mock_external.return_value = {"external_id": external_id}
                        mock_async.return_value = {
                            "analysisID": analysis_id,
                            "subdomain": "test-subdomain",
                            "ipAddr": "10.0.0.1",
                        }
                        mock_ready.return_value = (True, {"status_code": 200, "response_time_ms": 100})

                        response = client.get(
                            f"/apps/analyses/{analysis_id}/status",
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
                    f"/apps/analyses/{analysis_id}/status",
                    headers={"Authorization": "Bearer fake-token"},
                )

                assert response.status_code == 200
                data = response.json()
                assert data["url_ready"] is False

    def test_get_status_not_found(self, client, mock_token_verification):
        """Test status for non-existent analysis."""
        analysis_id = str(uuid4())
        from httpx import HTTPStatusError, Request, Response

        with patch("clients.AppsClient.get_analysis", new_callable=AsyncMock) as mock_get:
            mock_request = MagicMock(spec=Request)
            mock_response = MagicMock(spec=Response)
            mock_response.status_code = 404
            mock_response.text = "Not found"
            mock_get.side_effect = HTTPStatusError(
                "Not found", request=mock_request, response=mock_response
            )

            response = client.get(
                f"/apps/analyses/{analysis_id}/status",
                headers={"Authorization": "Bearer fake-token"},
            )

            # External service errors now return 502 Bad Gateway
            assert response.status_code == 502

    def test_get_status_invalid_uuid(self, client, mock_token_verification):
        """Test status with invalid UUID format."""
        response = client.get(
            "/apps/analyses/not-a-uuid/status",
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
                f"/apps/analyses/{analysis_id}/control?operation=extend_time",
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
                f"/apps/analyses/{analysis_id}/control?operation=save_and_exit",
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
                f"/apps/analyses/{analysis_id}/control?operation=exit",
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
            f"/apps/analyses/{analysis_id}/control?operation=invalid_op",
            headers={"Authorization": "Bearer fake-token"},
        )

        assert response.status_code == 400

    def test_control_analysis_not_found(self, client, mock_token_verification):
        """Test control on non-existent analysis."""
        analysis_id = str(uuid4())
        from httpx import HTTPStatusError, Request, Response

        with patch("clients.AppExposerClient.extend_time_limit", new_callable=AsyncMock) as mock_extend:
            mock_request = MagicMock(spec=Request)
            mock_response = MagicMock(spec=Response)
            mock_response.status_code = 404
            mock_response.text = "Not found"
            mock_extend.side_effect = HTTPStatusError(
                "Not found", request=mock_request, response=mock_response
            )

            response = client.post(
                f"/apps/analyses/{analysis_id}/control?operation=extend_time",
                headers={"Authorization": "Bearer fake-token"},
            )

            # External service errors now return 502 Bad Gateway
            assert response.status_code == 502


class TestGetAppConfig:
    """Tests for GET /apps/{app_id}/config endpoint."""

    def test_get_config_success(self, client, mock_token_verification):
        """Test successful config retrieval."""
        app_id = str(uuid4())
        expected_config = {
            "parameters": [
                {
                    "id": "param1",
                    "name": "Input File",
                    "type": "FileInput",
                    "required": True,
                }
            ]
        }

        with patch("clients.AppsClient.get_app", new_callable=AsyncMock) as mock_get:
            mock_get.return_value = {
                "id": app_id,
                "name": "Test App",
                "config": expected_config,
            }

            response = client.get(
                f"/apps/de/{app_id}/config",
                headers={"Authorization": "Bearer fake-token"},
            )

            assert response.status_code == 200
            data = response.json()
            assert data == expected_config

    def test_get_config_empty(self, client, mock_token_verification):
        """Test config retrieval when app has no config section."""
        app_id = str(uuid4())

        with patch("clients.AppsClient.get_app", new_callable=AsyncMock) as mock_get:
            mock_get.return_value = {
                "id": app_id,
                "name": "Test App",
            }

            response = client.get(
                f"/apps/de/{app_id}/config",
                headers={"Authorization": "Bearer fake-token"},
            )

            assert response.status_code == 200
            data = response.json()
            assert data == {}

    def test_get_config_not_found(self, client, mock_token_verification):
        """Test config retrieval for non-existent app."""
        app_id = str(uuid4())
        from httpx import HTTPStatusError, Request, Response

        with patch("clients.AppsClient.get_app", new_callable=AsyncMock) as mock_get:
            mock_request = MagicMock(spec=Request)
            mock_response = MagicMock(spec=Response)
            mock_response.status_code = 404
            mock_response.text = "Not found"
            mock_get.side_effect = HTTPStatusError(
                "Not found", request=mock_request, response=mock_response
            )

            response = client.get(
                f"/apps/de/{app_id}/config",
                headers={"Authorization": "Bearer fake-token"},
            )

            # External service errors now return 502 Bad Gateway
            assert response.status_code == 502

    def test_get_config_invalid_uuid(self, client, mock_token_verification):
        """Test config retrieval with invalid UUID format."""
        response = client.get(
            "/apps/de/not-a-uuid/config",
            headers={"Authorization": "Bearer fake-token"},
        )

        assert response.status_code == 400
        data = response.json()
        assert "Invalid app ID format" in data["detail"]

    def test_get_config_unauthorized(self, client):
        """Test config retrieval without authentication."""
        app_id = str(uuid4())
        response = client.get(f"/apps/de/{app_id}/config")
        assert response.status_code == 403  # FastAPI returns 403 for missing bearer


class TestListAnalyses:
    """Tests for the GET /apps/analyses/ endpoint."""

    def test_list_analyses_default_running(self, client, mock_token_verification, httpx_mock):
        """Test listing analyses with default Running status filter."""
        import json

        username = "testuser"
        analysis_1_id = str(uuid4())
        analysis_2_id = str(uuid4())
        app_1_id = str(uuid4())
        app_2_id = str(uuid4())

        # Mock the apps service response
        filter_param = json.dumps([{"field": "status", "value": "Running"}])
        httpx_mock.add_response(
            url=f"http://apps/analyses?user={username}&filter={filter_param}",
            json={
                "analyses": [
                    {
                        "id": analysis_1_id,
                        "app_id": app_1_id,
                        "system_id": "de",
                        "status": "Running",
                        "name": "Test Analysis 1",
                    },
                    {
                        "id": analysis_2_id,
                        "app_id": app_2_id,
                        "system_id": "de",
                        "status": "Running",
                        "name": "Test Analysis 2",
                    },
                ]
            },
            status_code=200,
        )

        response = client.get(
            "/apps/analyses/",
            headers={"Authorization": "Bearer fake-token"},
        )

        assert response.status_code == 200
        data = response.json()
        assert "analyses" in data
        assert len(data["analyses"]) == 2
        assert data["analyses"][0]["analysis_id"] == analysis_1_id
        assert data["analyses"][0]["app_id"] == app_1_id
        assert data["analyses"][0]["system_id"] == "de"
        assert data["analyses"][0]["status"] == "Running"
        assert data["analyses"][1]["analysis_id"] == analysis_2_id

    def test_list_analyses_with_completed_status(self, client, mock_token_verification, httpx_mock):
        """Test listing analyses with Completed status filter."""
        import json

        username = "testuser"
        analysis_id = str(uuid4())
        app_id = str(uuid4())

        filter_param = json.dumps([{"field": "status", "value": "Completed"}])
        httpx_mock.add_response(
            url=f"http://apps/analyses?user={username}&filter={filter_param}",
            json={
                "analyses": [
                    {
                        "id": analysis_id,
                        "app_id": app_id,
                        "system_id": "de",
                        "status": "Completed",
                        "name": "Test Analysis",
                    },
                ]
            },
            status_code=200,
        )

        response = client.get(
            "/apps/analyses/?status=Completed",
            headers={"Authorization": "Bearer fake-token"},
        )

        assert response.status_code == 200
        data = response.json()
        assert "analyses" in data
        assert len(data["analyses"]) == 1
        assert data["analyses"][0]["status"] == "Completed"

    def test_list_analyses_empty(self, client, mock_token_verification, httpx_mock):
        """Test listing when no analyses match the filter."""
        import json

        username = "testuser"
        filter_param = json.dumps([{"field": "status", "value": "Running"}])

        httpx_mock.add_response(
            url=f"http://apps/analyses?user={username}&filter={filter_param}",
            json={"analyses": []},
            status_code=200,
        )

        response = client.get(
            "/apps/analyses/",
            headers={"Authorization": "Bearer fake-token"},
        )

        assert response.status_code == 200
        data = response.json()
        assert "analyses" in data
        assert len(data["analyses"]) == 0

    def test_list_analyses_unauthorized(self, client):
        """Test listing analyses without authentication."""
        response = client.get("/apps/analyses/")
        assert response.status_code == 403  # FastAPI returns 403 for missing bearer
