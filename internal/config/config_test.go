package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// minimalJSON satisfies every required value via the JSON file, using the
// legacy irods.zone key to exercise the OutputZone fallback.
const minimalJSON = `{
	"irods": {"zone": "tempZone"},
	"keycloak": {"server_url": "https://kc.example.org/auth", "realm": "de"}
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
		"OUTPUT_ZONE", "IRODS_ZONE",
		"KEYCLOAK_SERVER_URL", "KEYCLOAK_REALM", "KEYCLOAK_SSL_VERIFY", "TERRAIN_BASE_URL",
		"USER_SUFFIX", "VICE_DOMAIN", "PATH_PREFIX", "VICE_URL_CHECK_TIMEOUT",
		"VICE_URL_CHECK_RETRIES", "VICE_URL_CHECK_CACHE_TTL",
		"MCP_CLIENT_ID", "PUBLIC_BASE_URL",
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
				if cfg.TerrainBaseURL != DefaultTerrainBaseURL {
					t.Errorf("TerrainBaseURL = %q, want %q", cfg.TerrainBaseURL, DefaultTerrainBaseURL)
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
			},
		},
		{
			name: "output zone falls back to the legacy irods zone key",
			json: minimalJSON,
			check: func(t *testing.T, cfg *Config) {
				if cfg.OutputZone != "tempZone" {
					t.Errorf("OutputZone = %q, want \"tempZone\"", cfg.OutputZone)
				}
			},
		},
		{
			name: "output zone from env wins over the irods fallback",
			json: minimalJSON,
			env:  map[string]string{"OUTPUT_ZONE": "otherZone"},
			check: func(t *testing.T, cfg *Config) {
				if cfg.OutputZone != "otherZone" {
					t.Errorf("OutputZone = %q, want \"otherZone\"", cfg.OutputZone)
				}
			},
		},
		{
			name: "output zone from the application section",
			json: `{
				"keycloak": {"server_url": "https://kc/", "realm": "de"},
				"application": {"output_zone": "appZone"}
			}`,
			check: func(t *testing.T, cfg *Config) {
				if cfg.OutputZone != "appZone" {
					t.Errorf("OutputZone = %q, want \"appZone\"", cfg.OutputZone)
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
				"TERRAIN_BASE_URL": "http://terrain-from-env",
				"USER_SUFFIX":      "@example.org",
			},
			check: func(t *testing.T, cfg *Config) {
				if cfg.TerrainBaseURL != "http://terrain-from-env" {
					t.Errorf("TerrainBaseURL = %q, want env value", cfg.TerrainBaseURL)
				}
				if cfg.UserSuffix != "@example.org" {
					t.Errorf("UserSuffix = %q, want env value", cfg.UserSuffix)
				}
			},
		},
		{
			name: "empty env var falls through to JSON",
			json: `{
				"irods": {"zone": "tempZone"},
				"keycloak": {"server_url": "https://kc/", "realm": "de"},
				"services": {"terrain_base_url": "http://terrain-from-json"}
			}`,
			env: map[string]string{"TERRAIN_BASE_URL": ""},
			check: func(t *testing.T, cfg *Config) {
				if cfg.TerrainBaseURL != "http://terrain-from-json" {
					t.Errorf("TerrainBaseURL = %q, want json value", cfg.TerrainBaseURL)
				}
			},
		},
		{
			name:    "missing required value errors",
			json:    `{"irods": {"zone": "z"}}`,
			wantErr: "KEYCLOAK_SERVER_URL",
		},
		{
			name:    "missing output zone errors",
			json:    `{"keycloak": {"server_url": "https://kc/", "realm": "de"}}`,
			wantErr: "OUTPUT_ZONE",
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
				`"realm": "de"`,
				`"realm": "de", "ssl_verify": false`, 1),
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
				"keycloak": {"server_url": "https://kc/", "realm": "r"},
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
			name:    "malformed config file errors",
			json:    "{not json",
			wantErr: "error parsing JSON config file",
		},
		{
			name: "mcp defaults and trailing slash trimmed",
			json: minimalJSON,
			env:  map[string]string{"PUBLIC_BASE_URL": "https://de.example.org/formation/"},
			check: func(t *testing.T, cfg *Config) {
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
			name:    "missing mcp client id errors",
			json:    minimalJSON,
			env:     map[string]string{"MCP_CLIENT_ID": ""},
			wantErr: "MCP_CLIENT_ID",
		},
		{
			name:    "missing public base url errors",
			json:    minimalJSON,
			env:     map[string]string{"PUBLIC_BASE_URL": ""},
			wantErr: "PUBLIC_BASE_URL",
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
			json: strings.Replace(minimalJSON, `"realm": "de"`,
				`"realm": "de", "mcp_client_id": "json-mcp-client", "mcp_scopes": "openid"`, 1),
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
		"OUTPUT_ZONE":         "z",
		"KEYCLOAK_SERVER_URL": "https://kc/", "KEYCLOAK_REALM": "r",
		"MCP_CLIENT_ID": "m", "PUBLIC_BASE_URL": "https://de.example.org/formation",
	} {
		t.Setenv(k, v)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.OutputZone != "z" || cfg.KeycloakRealm != "r" {
		t.Errorf("unexpected config: %+v", cfg)
	}
}
