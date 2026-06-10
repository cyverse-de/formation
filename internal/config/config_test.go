package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// minimalJSON satisfies every required value via the JSON file.
const minimalJSON = `{
	"irods": {"host": "irods.example.org", "port": 1247, "user": "rods", "password": "secret", "zone": "tempZone"},
	"keycloak": {"server_url": "https://kc.example.org/auth", "realm": "de", "client_id": "formation", "client_secret": "kcsecret"}
}`

func writeConfig(t *testing.T, contents string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CONFIG_FILE", path)
}

// clearEnv guards against ambient env vars influencing precedence tests.
func clearEnv(t *testing.T) {
	t.Helper()
	for _, v := range []string{
		"IRODS_HOST", "IRODS_PORT", "IRODS_USER", "IRODS_PASSWORD", "IRODS_ZONE",
		"KEYCLOAK_SERVER_URL", "KEYCLOAK_REALM", "KEYCLOAK_CLIENT_ID", "KEYCLOAK_CLIENT_SECRET",
		"KEYCLOAK_SSL_VERIFY", "APPS_BASE_URL", "APP_EXPOSER_BASE_URL", "PERMISSIONS_BASE_URL",
		"USER_SUFFIX", "VICE_DOMAIN", "PATH_PREFIX", "VICE_URL_CHECK_TIMEOUT",
		"VICE_URL_CHECK_RETRIES", "VICE_URL_CHECK_CACHE_TTL", "SERVICE_ACCOUNTS_ONLY",
		"SERVICE_ACCOUNT_USERNAMES", "MCP_ENABLED", "MCP_CLIENT_ID", "PUBLIC_BASE_URL",
		"MCP_SCOPES", "MCP_LAUNCH_MAX_WAIT", "MCP_BROWSE_BYTE_LIMIT",
	} {
		t.Setenv(v, "")
		_ = os.Unsetenv(v)
	}
}

func TestLoad(t *testing.T) {
	tests := []struct {
		name    string
		json    string
		env     map[string]string
		wantErr string
		check   func(t *testing.T, cfg *Config)
	}{
		{
			name: "defaults applied with minimal config",
			json: minimalJSON,
			check: func(t *testing.T, cfg *Config) {
				if cfg.AppsBaseURL != DefaultAppsBaseURL {
					t.Errorf("AppsBaseURL = %q, want %q", cfg.AppsBaseURL, DefaultAppsBaseURL)
				}
				if cfg.AppExposerBaseURL != DefaultAppExposerBaseURL {
					t.Errorf("AppExposerBaseURL = %q, want %q", cfg.AppExposerBaseURL, DefaultAppExposerBaseURL)
				}
				if cfg.UserSuffix != DefaultUserSuffix {
					t.Errorf("UserSuffix = %q, want %q", cfg.UserSuffix, DefaultUserSuffix)
				}
				if cfg.ViceDomain != DefaultViceDomain {
					t.Errorf("ViceDomain = %q, want %q", cfg.ViceDomain, DefaultViceDomain)
				}
				if cfg.PathPrefix != DefaultPathPrefix {
					t.Errorf("PathPrefix = %q, want %q", cfg.PathPrefix, DefaultPathPrefix)
				}
				if cfg.ViceURLCheckTimeout != DefaultViceURLCheckTimeout {
					t.Errorf("ViceURLCheckTimeout = %v, want %v", cfg.ViceURLCheckTimeout, DefaultViceURLCheckTimeout)
				}
				if cfg.ViceURLCheckRetries != DefaultViceURLCheckRetries {
					t.Errorf("ViceURLCheckRetries = %d, want %d", cfg.ViceURLCheckRetries, DefaultViceURLCheckRetries)
				}
				if !cfg.KeycloakSSLVerify {
					t.Error("KeycloakSSLVerify = false, want true by default")
				}
				if cfg.ServiceAccountsOnly {
					t.Error("ServiceAccountsOnly = true, want false by default")
				}
				if len(cfg.ServiceAccountUsernames) != 0 {
					t.Errorf("ServiceAccountUsernames = %v, want empty", cfg.ServiceAccountUsernames)
				}
			},
		},
		{
			name: "numeric port stringified and output zone mirrors irods zone",
			json: minimalJSON,
			check: func(t *testing.T, cfg *Config) {
				if cfg.IRODSPort != "1247" {
					t.Errorf("IRODSPort = %q, want \"1247\"", cfg.IRODSPort)
				}
				if cfg.OutputZone != "tempZone" {
					t.Errorf("OutputZone = %q, want \"tempZone\"", cfg.OutputZone)
				}
			},
		},
		{
			name: "trailing slash appended to keycloak server url",
			json: minimalJSON,
			check: func(t *testing.T, cfg *Config) {
				if cfg.KeycloakServerURL != "https://kc.example.org/auth/" {
					t.Errorf("KeycloakServerURL = %q, want trailing slash", cfg.KeycloakServerURL)
				}
			},
		},
		{
			name: "env overrides JSON",
			json: minimalJSON,
			env: map[string]string{
				"IRODS_HOST":    "env-host",
				"APPS_BASE_URL": "http://apps-from-env",
				"USER_SUFFIX":   "@example.org",
			},
			check: func(t *testing.T, cfg *Config) {
				if cfg.IRODSHost != "env-host" {
					t.Errorf("IRODSHost = %q, want env-host", cfg.IRODSHost)
				}
				if cfg.AppsBaseURL != "http://apps-from-env" {
					t.Errorf("AppsBaseURL = %q, want env value", cfg.AppsBaseURL)
				}
				if cfg.UserSuffix != "@example.org" {
					t.Errorf("UserSuffix = %q, want env value", cfg.UserSuffix)
				}
			},
		},
		{
			name: "empty env var falls through to JSON",
			json: `{
				"irods": {"host": "json-host", "port": "1247", "user": "rods", "password": "secret", "zone": "tempZone"},
				"keycloak": {"server_url": "https://kc/", "realm": "de", "client_id": "f", "client_secret": "s"},
				"services": {"apps_base_url": "http://apps-from-json"}
			}`,
			env: map[string]string{"IRODS_HOST": "", "APPS_BASE_URL": ""},
			check: func(t *testing.T, cfg *Config) {
				if cfg.IRODSHost != "json-host" {
					t.Errorf("IRODSHost = %q, want json-host", cfg.IRODSHost)
				}
				if cfg.AppsBaseURL != "http://apps-from-json" {
					t.Errorf("AppsBaseURL = %q, want json value", cfg.AppsBaseURL)
				}
			},
		},
		{
			name:    "missing required value errors",
			json:    `{"irods": {"host": "h", "port": "1247", "user": "u", "password": "p", "zone": "z"}}`,
			wantErr: "KEYCLOAK_SERVER_URL",
		},
		{
			name:    "missing irods value errors",
			json:    `{}`,
			wantErr: "IRODS_HOST",
		},
		{
			name: "ssl_verify env set-at-all semantics",
			json: minimalJSON,
			env:  map[string]string{"KEYCLOAK_SSL_VERIFY": "false"},
			check: func(t *testing.T, cfg *Config) {
				if cfg.KeycloakSSLVerify {
					t.Error("KeycloakSSLVerify = true, want false from env")
				}
			},
		},
		{
			name: "ssl_verify false in JSON",
			json: strings.Replace(minimalJSON,
				`"client_secret": "kcsecret"`,
				`"client_secret": "kcsecret", "ssl_verify": false`, 1),
			check: func(t *testing.T, cfg *Config) {
				if cfg.KeycloakSSLVerify {
					t.Error("KeycloakSSLVerify = true, want false from JSON")
				}
			},
		},
		{
			name: "vice url check settings from JSON",
			json: `{
				"irods": {"host": "h", "port": "1", "user": "u", "password": "p", "zone": "z"},
				"keycloak": {"server_url": "https://kc/", "realm": "r", "client_id": "c", "client_secret": "s"},
				"application": {"vice_url_check_timeout": 2.5, "vice_url_check_retries": 7, "vice_url_check_cache_ttl": 10}
			}`,
			check: func(t *testing.T, cfg *Config) {
				if cfg.ViceURLCheckTimeout != 2500*time.Millisecond {
					t.Errorf("ViceURLCheckTimeout = %v, want 2.5s", cfg.ViceURLCheckTimeout)
				}
				if cfg.ViceURLCheckRetries != 7 {
					t.Errorf("ViceURLCheckRetries = %d, want 7", cfg.ViceURLCheckRetries)
				}
				if cfg.ViceURLCheckCacheTTL != 10*time.Second {
					t.Errorf("ViceURLCheckCacheTTL = %v, want 10s", cfg.ViceURLCheckCacheTTL)
				}
			},
		},
		{
			name:    "invalid vice timeout errors",
			json:    minimalJSON,
			env:     map[string]string{"VICE_URL_CHECK_TIMEOUT": "not-a-number"},
			wantErr: "VICE_URL_CHECK_TIMEOUT",
		},
		{
			name: "service account usernames from JSON",
			json: strings.Replace(minimalJSON, `"keycloak"`,
				`"application": {"service_account_usernames": {"app-runner": "de-service-account"}, "service_accounts_only": true}, "keycloak"`, 1),
			check: func(t *testing.T, cfg *Config) {
				if cfg.ServiceAccountUsernames["app-runner"] != "de-service-account" {
					t.Errorf("ServiceAccountUsernames = %v", cfg.ServiceAccountUsernames)
				}
				if !cfg.ServiceAccountsOnly {
					t.Error("ServiceAccountsOnly = false, want true")
				}
			},
		},
		{
			name: "service account usernames env overrides JSON",
			json: minimalJSON,
			env:  map[string]string{"SERVICE_ACCOUNT_USERNAMES": `{"app-runner": "from-env"}`},
			check: func(t *testing.T, cfg *Config) {
				if cfg.ServiceAccountUsernames["app-runner"] != "from-env" {
					t.Errorf("ServiceAccountUsernames = %v", cfg.ServiceAccountUsernames)
				}
			},
		},
		{
			name:    "invalid service account usernames JSON errors",
			json:    minimalJSON,
			env:     map[string]string{"SERVICE_ACCOUNT_USERNAMES": "{not json"},
			wantErr: "SERVICE_ACCOUNT_USERNAMES",
		},
		{
			name:    "malformed config file errors",
			json:    "{not json",
			wantErr: "error parsing JSON config file",
		},
		{
			name: "mcp defaults and trailing slash trimmed",
			json: minimalJSON,
			env:  map[string]string{"PUBLIC_BASE_URL": "https://de.example.org/formation/"},
			check: func(t *testing.T, cfg *Config) {
				if !cfg.MCPEnabled {
					t.Error("MCPEnabled = false, want true by default")
				}
				if cfg.PublicBaseURL != "https://de.example.org/formation" {
					t.Errorf("PublicBaseURL = %q, want trailing slash trimmed", cfg.PublicBaseURL)
				}
				if cfg.MCPScopes != DefaultMCPScopes {
					t.Errorf("MCPScopes = %q, want %q", cfg.MCPScopes, DefaultMCPScopes)
				}
				if cfg.MCPLaunchMaxWait != DefaultMCPLaunchMaxWait {
					t.Errorf("MCPLaunchMaxWait = %v, want %v", cfg.MCPLaunchMaxWait, DefaultMCPLaunchMaxWait)
				}
				if cfg.MCPBrowseByteLimit != DefaultMCPBrowseByteLimit {
					t.Errorf("MCPBrowseByteLimit = %d, want %d", cfg.MCPBrowseByteLimit, DefaultMCPBrowseByteLimit)
				}
			},
		},
		{
			name: "mcp disabled skips required mcp values",
			json: minimalJSON,
			env:  map[string]string{"MCP_ENABLED": "false", "MCP_CLIENT_ID": "", "PUBLIC_BASE_URL": ""},
			check: func(t *testing.T, cfg *Config) {
				if cfg.MCPEnabled {
					t.Error("MCPEnabled = true, want false")
				}
				if cfg.MCPClientID != "" || cfg.PublicBaseURL != "" {
					t.Errorf("MCP values should stay empty when disabled, got %q %q", cfg.MCPClientID, cfg.PublicBaseURL)
				}
			},
		},
		{
			name:    "missing mcp client id errors when enabled",
			json:    minimalJSON,
			env:     map[string]string{"MCP_CLIENT_ID": ""},
			wantErr: "MCP_CLIENT_ID",
		},
		{
			// "https:host" parses as scheme+opaque with no host; it must be rejected.
			name:    "public base url missing slashes errors",
			json:    minimalJSON,
			env:     map[string]string{"PUBLIC_BASE_URL": "https:qa.cyverse.org/formation"},
			wantErr: "PUBLIC_BASE_URL",
		},
		{
			name:    "public base url without scheme errors",
			json:    minimalJSON,
			env:     map[string]string{"PUBLIC_BASE_URL": "qa.cyverse.org/formation"},
			wantErr: "PUBLIC_BASE_URL",
		},
		{
			name: "mcp settings from JSON",
			json: strings.Replace(minimalJSON, `"client_secret": "kcsecret"`,
				`"client_secret": "kcsecret", "mcp_client_id": "json-mcp-client", "mcp_scopes": "openid"`, 1),
			env: map[string]string{"MCP_CLIENT_ID": ""},
			check: func(t *testing.T, cfg *Config) {
				if cfg.MCPClientID != "json-mcp-client" {
					t.Errorf("MCPClientID = %q, want json-mcp-client", cfg.MCPClientID)
				}
				if cfg.MCPScopes != "openid" {
					t.Errorf("MCPScopes = %q, want openid", cfg.MCPScopes)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearEnv(t)
			writeConfig(t, tt.json)
			// MCP is on by default and requires these; tests override as needed.
			t.Setenv("MCP_CLIENT_ID", "formation-mcp")
			t.Setenv("PUBLIC_BASE_URL", "https://de.example.org/formation")
			for k, v := range tt.env {
				t.Setenv(k, v)
			}

			cfg, err := Load()
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("Load() error = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			tt.check(t, cfg)
		})
	}
}

func TestLoadMissingFileUsesEnvOnly(t *testing.T) {
	clearEnv(t)
	t.Setenv("CONFIG_FILE", filepath.Join(t.TempDir(), "does-not-exist.json"))
	for k, v := range map[string]string{
		"IRODS_HOST": "h", "IRODS_PORT": "1247", "IRODS_USER": "u",
		"IRODS_PASSWORD": "p", "IRODS_ZONE": "z",
		"KEYCLOAK_SERVER_URL": "https://kc/", "KEYCLOAK_REALM": "r",
		"KEYCLOAK_CLIENT_ID": "c", "KEYCLOAK_CLIENT_SECRET": "s",
		"MCP_CLIENT_ID": "m", "PUBLIC_BASE_URL": "https://de.example.org/formation",
	} {
		t.Setenv(k, v)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.IRODSHost != "h" || cfg.KeycloakRealm != "r" {
		t.Errorf("unexpected config: %+v", cfg)
	}
}
