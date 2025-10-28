"""Tests for service account authentication functionality."""

import auth


class TestServiceAccountDetection:
    """Tests for is_service_account function."""

    def test_detects_service_account_with_service_prefix(self):
        """Test that service accounts are correctly identified by prefix."""
        payload = {"preferred_username": "service-account-my-service"}
        assert auth.is_service_account(payload) is True

    def test_detects_regular_user(self):
        """Test that regular users are not identified as service accounts."""
        payload = {"preferred_username": "regular-user"}
        assert auth.is_service_account(payload) is False

    def test_handles_missing_preferred_username(self):
        """Test that missing preferred_username is handled gracefully."""
        payload = {}
        assert auth.is_service_account(payload) is False

    def test_detects_edge_case_username(self):
        """Test edge case where username contains but doesn't start with prefix."""
        payload = {"preferred_username": "my-service-account-test"}
        assert auth.is_service_account(payload) is False


class TestServiceAccountExtraction:
    """Tests for extract_service_account_from_jwt function."""

    def test_extracts_service_account_with_roles(self):
        """Test extraction of service account info with roles."""
        payload = {
            "preferred_username": "service-account-my-service",
            "realm_access": {"roles": ["app-runner", "admin"]},
        }
        result = auth.extract_service_account_from_jwt(payload)

        assert result["username"] == "service-account-my-service"
        assert result["roles"] == ["app-runner", "admin"]

    def test_extracts_service_account_without_roles(self):
        """Test extraction when realm_access or roles are missing."""
        payload = {"preferred_username": "service-account-my-service"}
        result = auth.extract_service_account_from_jwt(payload)

        assert result["username"] == "service-account-my-service"
        assert result["roles"] == []

    def test_extracts_service_account_with_empty_roles(self):
        """Test extraction when roles list is empty."""
        payload = {
            "preferred_username": "service-account-my-service",
            "realm_access": {"roles": []},
        }
        result = auth.extract_service_account_from_jwt(payload)

        assert result["username"] == "service-account-my-service"
        assert result["roles"] == []

    def test_handles_missing_username(self):
        """Test extraction when preferred_username is missing."""
        payload = {"realm_access": {"roles": ["app-runner"]}}
        result = auth.extract_service_account_from_jwt(payload)

        assert result["username"] == ""
        assert result["roles"] == ["app-runner"]


class TestRoleEnforcement:
    """Tests for role enforcement logic."""

    def test_service_account_has_app_runner_role(self):
        """Test checking if service account has app-runner role."""
        service_account = {
            "username": "service-account-test",
            "roles": ["app-runner", "other-role"],
        }
        assert "app-runner" in service_account.get("roles", [])

    def test_service_account_missing_app_runner_role(self):
        """Test checking if service account is missing app-runner role."""
        service_account = {
            "username": "service-account-test",
            "roles": ["other-role", "admin"],
        }
        assert "app-runner" not in service_account.get("roles", [])

    def test_service_account_with_no_roles(self):
        """Test checking service account with no roles."""
        service_account = {"username": "service-account-test", "roles": []}
        assert "app-runner" not in service_account.get("roles", [])

    def test_service_account_with_only_app_runner_role(self):
        """Test checking service account with only app-runner role."""
        service_account = {"username": "service-account-test", "roles": ["app-runner"]}
        assert "app-runner" in service_account.get("roles", [])


class TestServiceAccountUsernameMapping:
    """Tests for service account username mapping."""

    def test_username_mapping_logic_with_no_mapping(self):
        """Test that missing mapping returns role name as fallback."""
        # Simulate empty mapping config
        mapping = {}
        role_name = "app-runner"

        # When no mapping exists, should return role name
        username = mapping.get(role_name, role_name)
        assert username == "app-runner"

    def test_username_mapping_logic_with_mapping(self):
        """Test that existing mapping returns configured username."""
        # Simulate config with mapping
        mapping = {"app-runner": "de-service-account"}
        role_name = "app-runner"

        # When mapping exists, should return mapped value
        username = mapping.get(role_name, role_name)
        assert username == "de-service-account"

    def test_username_mapping_logic_with_different_role(self):
        """Test mapping for a different role."""
        # Simulate config with multiple mappings
        mapping = {
            "app-runner": "de-service-account",
            "admin-runner": "admin-service",
        }

        # Test app-runner mapping
        assert mapping.get("app-runner", "app-runner") == "de-service-account"

        # Test admin-runner mapping
        assert mapping.get("admin-runner", "admin-runner") == "admin-service"

        # Test unmapped role
        assert mapping.get("other-role", "other-role") == "other-role"


class TestUsernameSanitization:
    """Tests for username sanitization."""

    def test_sanitize_removes_hyphens(self):
        """Test that hyphens are removed."""
        assert auth.sanitize_username("app-runner") == "apprunner"
        assert auth.sanitize_username("de-service-account") == "deserviceaccount"

    def test_sanitize_removes_underscores(self):
        """Test that underscores are removed."""
        assert auth.sanitize_username("service_account") == "serviceaccount"
        assert auth.sanitize_username("my_test_service") == "mytestservice"

    def test_sanitize_converts_to_lowercase(self):
        """Test that uppercase is converted to lowercase."""
        assert auth.sanitize_username("ServiceAccount") == "serviceaccount"
        assert auth.sanitize_username("APP-RUNNER") == "apprunner"
        assert auth.sanitize_username("MyService") == "myservice"

    def test_sanitize_keeps_numbers(self):
        """Test that numbers are preserved."""
        assert auth.sanitize_username("service123") == "service123"
        assert auth.sanitize_username("app-runner-v2") == "apprunnerv2"
        assert auth.sanitize_username("test1234service") == "test1234service"

    def test_sanitize_removes_special_characters(self):
        """Test that various special characters are removed."""
        assert auth.sanitize_username("service@account.com") == "serviceaccountcom"
        assert auth.sanitize_username("my.service!account") == "myserviceaccount"
        assert auth.sanitize_username("app#runner$123") == "apprunner123"
        assert auth.sanitize_username("test/service\\account") == "testserviceaccount"

    def test_sanitize_empty_string(self):
        """Test sanitization of empty string."""
        assert auth.sanitize_username("") == ""

    def test_sanitize_only_special_chars(self):
        """Test string with only special characters."""
        assert auth.sanitize_username("---@@@___") == ""
        assert auth.sanitize_username("!@#$%^&*()") == ""

    def test_sanitize_already_clean(self):
        """Test username that doesn't need sanitization."""
        assert auth.sanitize_username("myservice123") == "myservice123"
        assert auth.sanitize_username("apprunner") == "apprunner"

    def test_sanitize_mixed_case_with_numbers_and_symbols(self):
        """Test complex username with mixed case, numbers, and symbols."""
        assert auth.sanitize_username("Service_Account-V2.0") == "serviceaccountv20"
        assert (
            auth.sanitize_username("DE-Service@Account#123") == "deserviceaccount123"
        )


