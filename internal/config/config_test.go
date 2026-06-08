package config

import (
	"testing"
	"time"
)

// allEnvVars are every variable Load consults; tests clear them for hermeticity
// since the developer's shell (direnv) may export real values.
var allEnvVars = []string{
	"IRODS_HOST", "IRODS_PORT", "IRODS_USER", "IRODS_PASSWORD", "IRODS_ZONE",
	"KEYCLOAK_SERVER_URL", "KEYCLOAK_REALM", "KEYCLOAK_CLIENT_ID",
	"KEYCLOAK_CLIENT_SECRET", "KEYCLOAK_SSL_VERIFY",
	"APPS_BASE_URL", "APP_EXPOSER_BASE_URL", "PERMISSIONS_BASE_URL",
	"USER_SUFFIX", "VICE_DOMAIN", "PATH_PREFIX", "PUBLIC_BASE_URL",
	"VICE_URL_CHECK_TIMEOUT", "VICE_URL_CHECK_RETRIES", "VICE_URL_CHECK_CACHE_TTL",
	"SERVICE_ACCOUNTS_ONLY", "SERVICE_ACCOUNT_USERNAMES",
	"LISTEN_ADDR", "HTTP_TIMEOUT", "IRODS_CONN_IDLE_TTL",
}

func clearEnv(t *testing.T) {
	t.Helper()
	for _, k := range allEnvVars {
		t.Setenv(k, "")
	}
	t.Setenv("CONFIG_FILE", "/nonexistent/config.json")
}

func TestLoadEnvFirst(t *testing.T) {
	clearEnv(t)
	required := map[string]string{
		"IRODS_HOST":             "irods.example.com",
		"IRODS_PORT":             "1247",
		"IRODS_USER":             "rods",
		"IRODS_PASSWORD":         "secret",
		"IRODS_ZONE":             "iplant",
		"KEYCLOAK_SERVER_URL":    "https://keycloak.example.com",
		"KEYCLOAK_REALM":         "cyverse",
		"KEYCLOAK_CLIENT_ID":     "formation",
		"KEYCLOAK_CLIENT_SECRET": "shh",
	}
	for k, v := range required {
		t.Setenv(k, v)
	}

	tests := []struct {
		name  string
		env   map[string]string
		check func(t *testing.T, c *Config)
	}{
		{
			name: "defaults applied",
			check: func(t *testing.T, c *Config) {
				if c.KeycloakServerURL != "https://keycloak.example.com/" {
					t.Errorf("server url not trailing-slashed: %q", c.KeycloakServerURL)
				}
				if c.KeycloakIssuer() != "https://keycloak.example.com/realms/cyverse" {
					t.Errorf("issuer = %q", c.KeycloakIssuer())
				}
				if c.AppsBaseURL.String() != "http://apps" {
					t.Errorf("apps default = %q", c.AppsBaseURL)
				}
				if c.PathPrefix != "/formation" {
					t.Errorf("path prefix = %q", c.PathPrefix)
				}
				if c.OutputZone != "iplant" {
					t.Errorf("output zone = %q", c.OutputZone)
				}
				if c.ViceURLCheckTimeout != 5*time.Second {
					t.Errorf("vice timeout = %v", c.ViceURLCheckTimeout)
				}
				if !c.KeycloakSSLVerify {
					t.Error("ssl verify should default true")
				}
				if c.IRODSConnIdleTTL != 10*time.Minute {
					t.Errorf("idle ttl = %v, want 10m", c.IRODSConnIdleTTL)
				}
			},
		},
		{
			name: "env overrides and parsing",
			env: map[string]string{
				"APPS_BASE_URL":             "http://apps:8080",
				"PATH_PREFIX":               "/",
				"VICE_URL_CHECK_TIMEOUT":    "2.5",
				"VICE_URL_CHECK_RETRIES":    "7",
				"SERVICE_ACCOUNTS_ONLY":     "true",
				"SERVICE_ACCOUNT_USERNAMES": `{"app-runner":"de-service-account"}`,
				"KEYCLOAK_SSL_VERIFY":       "false",
				"IRODS_CONN_IDLE_TTL":       "120",
			},
			check: func(t *testing.T, c *Config) {
				if c.AppsBaseURL.String() != "http://apps:8080" {
					t.Errorf("apps url = %q", c.AppsBaseURL)
				}
				if c.PathPrefix != "" {
					t.Errorf(`path prefix "/" should normalize to "", got %q`, c.PathPrefix)
				}
				if c.ViceURLCheckTimeout != 2500*time.Millisecond {
					t.Errorf("vice timeout = %v", c.ViceURLCheckTimeout)
				}
				if c.ViceURLCheckRetries != 7 {
					t.Errorf("retries = %d", c.ViceURLCheckRetries)
				}
				if !c.ServiceAccountsOnly {
					t.Error("service accounts only should be true")
				}
				if c.ServiceAccountUsernames["app-runner"] != "de-service-account" {
					t.Errorf("sa usernames = %v", c.ServiceAccountUsernames)
				}
				if c.KeycloakSSLVerify {
					t.Error("ssl verify should be false")
				}
				if c.IRODSConnIdleTTL != 2*time.Minute {
					t.Errorf("idle ttl = %v, want 2m", c.IRODSConnIdleTTL)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			c, err := Load()
			if err != nil {
				t.Fatalf("Load() error: %v", err)
			}
			tc.check(t, c)
		})
	}
}

func TestLoadMissingRequired(t *testing.T) {
	clearEnv(t)
	// Intentionally leave IRODS_HOST etc. unset.
	if _, err := Load(); err == nil {
		t.Fatal("expected error for missing required config")
	}
}
