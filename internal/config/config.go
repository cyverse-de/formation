// Package config loads formation's configuration with the same precedence as
// the original Python implementation: environment variables override values
// from a JSON config file, which override hard-coded defaults.
package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Defaults applied when a value is absent from both the environment and the JSON file.
const (
	DefaultConfigFile           = "config.json"
	DefaultTerrainBaseURL       = "http://terrain"
	DefaultUserSuffix           = "@iplantcollaborative.org"
	DefaultViceDomain           = ".cyverse.run"
	DefaultPathPrefix           = "/formation"
	DefaultViceURLCheckTimeout  = 5 * time.Second
	DefaultViceURLCheckRetries  = 3
	DefaultViceURLCheckCacheTTL = 5 * time.Second
	DefaultMCPScopes            = "openid profile email"
	DefaultMCPLaunchMaxWait     = 540 * time.Second
	DefaultMCPBrowseByteLimit   = 1 << 20
)

// Config holds all formation settings.
type Config struct {
	KeycloakServerURL    string
	KeycloakRealm        string
	KeycloakClientID     string
	KeycloakClientSecret string
	KeycloakSSLVerify    bool

	TerrainBaseURL string

	UserSuffix string
	ViceDomain string
	PathPrefix string

	ViceURLCheckTimeout  time.Duration
	ViceURLCheckRetries  int
	ViceURLCheckCacheTTL time.Duration

	// OutputZone is the iRODS zone where analysis outputs land.
	OutputZone string

	ServiceAccountsOnly     bool
	ServiceAccountUsernames map[string]string

	// MCP server settings. PublicBaseURL is formation's externally visible
	// base URL (e.g. https://de.cyverse.org/formation), used to build the
	// OAuth resource identifier and discovery metadata. MCPClientID is the
	// shared public Keycloak client returned by the registration shim.
	MCPEnabled         bool
	MCPClientID        string
	PublicBaseURL      string
	MCPScopes          string
	MCPLaunchMaxWait   time.Duration
	MCPBrowseByteLimit int
}

// Load reads the JSON file named by CONFIG_FILE (default config.json, relative
// paths resolved against the working directory) and merges it with environment
// variables. It returns an error naming the first missing required value.
func Load() (*Config, error) {
	raw, err := loadJSONFile()
	if err != nil {
		return nil, err
	}

	irods := section(raw, "irods")
	keycloak := section(raw, "keycloak")
	services := section(raw, "services")
	app := section(raw, "application")

	cfg := &Config{}

	if cfg.KeycloakServerURL, err = required("KEYCLOAK_SERVER_URL", keycloak, "server_url"); err != nil {
		return nil, err
	}
	if !strings.HasSuffix(cfg.KeycloakServerURL, "/") {
		cfg.KeycloakServerURL += "/"
	}
	if cfg.KeycloakRealm, err = required("KEYCLOAK_REALM", keycloak, "realm"); err != nil {
		return nil, err
	}
	if cfg.KeycloakClientID, err = required("KEYCLOAK_CLIENT_ID", keycloak, "client_id"); err != nil {
		return nil, err
	}
	if cfg.KeycloakClientSecret, err = required("KEYCLOAK_CLIENT_SECRET", keycloak, "client_secret"); err != nil {
		return nil, err
	}
	cfg.KeycloakSSLVerify = boolValue("KEYCLOAK_SSL_VERIFY", keycloak, "ssl_verify", true)

	cfg.TerrainBaseURL = optional("TERRAIN_BASE_URL", services, "terrain_base_url", DefaultTerrainBaseURL)

	cfg.UserSuffix = optional("USER_SUFFIX", app, "user_suffix", DefaultUserSuffix)
	cfg.ViceDomain = optional("VICE_DOMAIN", app, "vice_domain", DefaultViceDomain)
	cfg.PathPrefix = optional("PATH_PREFIX", app, "path_prefix", DefaultPathPrefix)
	// Normalize once so consumers can use the prefix verbatim; without a
	// leading slash StripPathPrefix would be a silent no-op and the landing
	// page would emit broken path-relative links.
	cfg.PathPrefix = strings.TrimSuffix(cfg.PathPrefix, "/")
	if cfg.PathPrefix != "" && !strings.HasPrefix(cfg.PathPrefix, "/") {
		cfg.PathPrefix = "/" + cfg.PathPrefix
	}

	if cfg.ViceURLCheckTimeout, err = duration("VICE_URL_CHECK_TIMEOUT", app, "vice_url_check_timeout", DefaultViceURLCheckTimeout); err != nil {
		return nil, err
	}
	if cfg.ViceURLCheckRetries, err = integer("VICE_URL_CHECK_RETRIES", app, "vice_url_check_retries", DefaultViceURLCheckRetries); err != nil {
		return nil, err
	}
	if cfg.ViceURLCheckCacheTTL, err = duration("VICE_URL_CHECK_CACHE_TTL", app, "vice_url_check_cache_ttl", DefaultViceURLCheckCacheTTL); err != nil {
		return nil, err
	}

	// OUTPUT_ZONE replaces the old IRODS_ZONE-derived value; the IRODS_ZONE
	// env var and irods.zone JSON key still work as fallbacks so existing
	// deployments keep starting.
	cfg.OutputZone = optional("OUTPUT_ZONE", app, "output_zone", "")
	if cfg.OutputZone == "" {
		cfg.OutputZone = optional("IRODS_ZONE", irods, "zone", "")
	}
	if cfg.OutputZone == "" {
		return nil, fmt.Errorf("configuration value OUTPUT_ZONE is not set (not in environment or JSON config)")
	}

	cfg.ServiceAccountsOnly = boolValue("SERVICE_ACCOUNTS_ONLY", app, "service_accounts_only", false)

	if cfg.ServiceAccountUsernames, err = usernameMap(app); err != nil {
		return nil, err
	}

	cfg.MCPEnabled = boolValue("MCP_ENABLED", app, "mcp_enabled", true)
	if cfg.MCPEnabled {
		if cfg.MCPClientID, err = required("MCP_CLIENT_ID", keycloak, "mcp_client_id"); err != nil {
			return nil, err
		}
		if cfg.PublicBaseURL, err = required("PUBLIC_BASE_URL", app, "public_base_url"); err != nil {
			return nil, err
		}
		cfg.PublicBaseURL = strings.TrimSuffix(cfg.PublicBaseURL, "/")
		// This URL is embedded in every OAuth discovery document; a malformed
		// value (e.g. "https:host" without "//") breaks MCP clients with
		// errors that point nowhere near the cause, so fail fast instead.
		if err := validatePublicBaseURL(cfg.PublicBaseURL); err != nil {
			return nil, err
		}
	}
	cfg.MCPScopes = optional("MCP_SCOPES", keycloak, "mcp_scopes", DefaultMCPScopes)
	if cfg.MCPLaunchMaxWait, err = duration("MCP_LAUNCH_MAX_WAIT", app, "mcp_launch_max_wait", DefaultMCPLaunchMaxWait); err != nil {
		return nil, err
	}
	if cfg.MCPBrowseByteLimit, err = integer("MCP_BROWSE_BYTE_LIMIT", app, "mcp_browse_byte_limit", DefaultMCPBrowseByteLimit); err != nil {
		return nil, err
	}

	return cfg, nil
}

// validatePublicBaseURL requires an absolute http(s) URL with a host.
func validatePublicBaseURL(value string) error {
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return fmt.Errorf(
			"invalid value for PUBLIC_BASE_URL: %q must be an absolute http(s) URL like https://de.cyverse.org/formation",
			value)
	}
	return nil
}

func loadJSONFile() (map[string]any, error) {
	path := os.Getenv("CONFIG_FILE")
	if path == "" {
		path = DefaultConfigFile
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]any{}, nil
		}
		return nil, fmt.Errorf("error loading config file %s: %w", path, err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var raw map[string]any
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("error parsing JSON config file %s: %w", path, err)
	}
	return raw, nil
}

func section(raw map[string]any, name string) map[string]any {
	if m, ok := raw[name].(map[string]any); ok {
		return m
	}
	return map[string]any{}
}

// stringify mirrors Python's str() for the JSON value types we accept.
func stringify(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case json.Number:
		return x.String()
	case bool:
		if x {
			return "True"
		}
		return "False"
	default:
		return fmt.Sprint(x)
	}
}

// required returns the env var when non-empty, else the JSON value, else an error.
func required(envVar string, sec map[string]any, key string) (string, error) {
	if v := os.Getenv(envVar); v != "" {
		return v, nil
	}
	if v, ok := sec[key]; ok && v != nil {
		return stringify(v), nil
	}
	return "", fmt.Errorf("configuration value %s is not set (not in environment or JSON config)", envVar)
}

func optional(envVar string, sec map[string]any, key, fallback string) string {
	if v := os.Getenv(envVar); v != "" {
		return v
	}
	if v, ok := sec[key]; ok && v != nil {
		return stringify(v)
	}
	return fallback
}

// boolValue uses set-at-all env semantics (an empty env var means false), matching Python.
func boolValue(envVar string, sec map[string]any, key string, fallback bool) bool {
	if v, ok := os.LookupEnv(envVar); ok {
		return strings.EqualFold(v, "true")
	}
	if v, ok := sec[key]; ok {
		return truthy(v)
	}
	return fallback
}

// truthy mirrors Python's bool() for JSON-decoded values.
func truthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case json.Number:
		f, err := x.Float64()
		return err != nil || f != 0
	default:
		return true
	}
}

func float(envVar string, sec map[string]any, key string, fallback float64) (float64, error) {
	s := optional(envVar, sec, key, "")
	if s == "" {
		return fallback, nil
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid value for %s: %q is not a number", envVar, s)
	}
	return f, nil
}

func duration(envVar string, sec map[string]any, key string, fallback time.Duration) (time.Duration, error) {
	f, err := float(envVar, sec, key, fallback.Seconds())
	if err != nil {
		return 0, err
	}
	return time.Duration(f * float64(time.Second)), nil
}

func integer(envVar string, sec map[string]any, key string, fallback int) (int, error) {
	s := optional(envVar, sec, key, "")
	if s == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("invalid value for %s: %q is not an integer", envVar, s)
	}
	return n, nil
}

func usernameMap(app map[string]any) (map[string]string, error) {
	usernames := map[string]string{}
	if v, ok := os.LookupEnv("SERVICE_ACCOUNT_USERNAMES"); ok {
		if err := json.Unmarshal([]byte(v), &usernames); err != nil {
			return nil, fmt.Errorf("invalid JSON in SERVICE_ACCOUNT_USERNAMES: %s: %w", v, err)
		}
		return usernames, nil
	}
	if m, ok := app["service_account_usernames"].(map[string]any); ok {
		for role, name := range m {
			usernames[role] = stringify(name)
		}
	}
	return usernames, nil
}
