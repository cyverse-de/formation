"""Configuration management for Formation API."""

import json
import os
import sys
from pathlib import Path
from typing import Any


class Config:
    """Application configuration loaded from JSON file or environment variables.

    Configuration priority (highest to lowest):
    1. Environment variables
    2. JSON configuration file
    3. Hard-coded defaults (where applicable)
    """

    def __init__(self):
        """Initialize configuration from JSON file and/or environment variables."""
        # Load JSON config if it exists
        json_config = self._load_json_config()

        # iRODS configuration
        irods_config = json_config.get("irods", {})
        self.irods_host = self._get_config("IRODS_HOST", irods_config.get("host"))
        self.irods_port = self._get_config("IRODS_PORT", irods_config.get("port"))
        self.irods_user = self._get_config("IRODS_USER", irods_config.get("user"))
        self.irods_password = self._get_config("IRODS_PASSWORD", irods_config.get("password"))
        self.irods_zone = self._get_config("IRODS_ZONE", irods_config.get("zone"))

        # Keycloak configuration
        keycloak_config = json_config.get("keycloak", {})
        self.keycloak_server_url = self._get_config(
            "KEYCLOAK_SERVER_URL", keycloak_config.get("server_url")
        )
        self.keycloak_realm = self._get_config("KEYCLOAK_REALM", keycloak_config.get("realm"))
        self.keycloak_client_id = self._get_config(
            "KEYCLOAK_CLIENT_ID", keycloak_config.get("client_id")
        )
        self.keycloak_client_secret = self._get_config(
            "KEYCLOAK_CLIENT_SECRET", keycloak_config.get("client_secret")
        )

        # Handle boolean ssl_verify
        ssl_verify_env = os.environ.get("KEYCLOAK_SSL_VERIFY")
        if ssl_verify_env is not None:
            self.keycloak_ssl_verify = ssl_verify_env.lower() == "true"
        else:
            ssl_verify_json = keycloak_config.get("ssl_verify", True)
            self.keycloak_ssl_verify = bool(ssl_verify_json)

        # Service URLs
        services_config = json_config.get("services", {})
        self.apps_base_url = os.environ.get("APPS_BASE_URL") or services_config.get(
            "apps_base_url", "http://apps"
        )
        self.app_exposer_base_url = os.environ.get(
            "APP_EXPOSER_BASE_URL"
        ) or services_config.get("app_exposer_base_url", "http://app-exposer")
        self.permissions_base_url = os.environ.get(
            "PERMISSIONS_BASE_URL"
        ) or services_config.get("permissions_base_url", "http://permissions")

        # Application settings
        app_config = json_config.get("application", {})
        self.user_suffix = os.environ.get("USER_SUFFIX") or app_config.get(
            "user_suffix", "@iplantcollaborative.org"
        )
        self.vice_domain = os.environ.get("VICE_DOMAIN") or app_config.get(
            "vice_domain", ".cyverse.run"
        )
        self.path_prefix = os.environ.get("PATH_PREFIX") or app_config.get(
            "path_prefix", "/formation"
        )

        # VICE URL checking settings
        self.vice_url_check_timeout = float(
            os.environ.get("VICE_URL_CHECK_TIMEOUT")
            or app_config.get("vice_url_check_timeout", 5.0)
        )
        self.vice_url_check_retries = int(
            os.environ.get("VICE_URL_CHECK_RETRIES")
            or app_config.get("vice_url_check_retries", 3)
        )
        self.vice_url_check_cache_ttl = float(
            os.environ.get("VICE_URL_CHECK_CACHE_TTL")
            or app_config.get("vice_url_check_cache_ttl", 5.0)
        )

        # Use irods zone as the output zone
        self.output_zone = self.irods_zone

    def _load_json_config(self) -> dict[str, Any]:
        """Load configuration from JSON file if it exists.

        Returns:
            Dictionary containing configuration from JSON file, or empty dict if file doesn't exist.
        """
        # Check for config file path in environment variable, default to config.json
        config_file = os.environ.get("CONFIG_FILE", "config.json")
        config_path = Path(config_file)

        if not config_path.is_absolute():
            # If relative path, look in the directory containing this file
            config_path = Path(__file__).parent / config_file

        if config_path.exists():
            try:
                with open(config_path) as f:
                    return json.load(f)
            except json.JSONDecodeError as e:
                print(f"Error parsing JSON config file {config_path}: {e}", file=sys.stderr)
                sys.exit(1)
            except Exception as e:
                print(f"Error loading config file {config_path}: {e}", file=sys.stderr)
                sys.exit(1)

        return {}

    def _get_config(self, env_var: str, json_value: Any) -> str:
        """Get configuration value from environment variable or JSON, with validation.

        Args:
            env_var: Environment variable name
            json_value: Value from JSON config (can be None)

        Returns:
            Configuration value as string

        Raises:
            SystemExit: If neither environment variable nor JSON value is set
        """
        # Environment variable takes precedence
        value = os.environ.get(env_var)
        if value:
            return value

        # Fall back to JSON value
        if json_value is not None:
            return str(json_value)

        # Neither is set - this is an error for required values
        print(
            f"Configuration value {env_var} is not set (not in environment or JSON config).",
            file=sys.stderr,
        )
        sys.exit(1)


# Global config instance
config = Config()
