// Package config loads Formation's runtime configuration. Values are resolved
// with environment variables taking precedence over an optional JSON config
// file, which in turn takes precedence over built-in defaults — matching the
// behavior of the original Python implementation.
package config

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds all resolved configuration for the service.
type Config struct {
	// iRODS
	IRODSHost     string
	IRODSPort     int
	IRODSUser     string
	IRODSPassword string
	IRODSZone     string

	// Keycloak
	KeycloakServerURL    string // always has a trailing slash
	KeycloakRealm        string
	KeycloakClientID     string
	KeycloakClientSecret string
	KeycloakSSLVerify    bool

	// Downstream services
	AppsBaseURL        *url.URL
	AppExposerBaseURL  *url.URL
	PermissionsBaseURL *url.URL

	// Application behavior
	UserSuffix string
	ViceDomain string
	PathPrefix string
	OutputZone string

	// Public base URL of this server, used to advertise OAuth resource metadata.
	PublicBaseURL string

	// VICE URL readiness probing
	ViceURLCheckTimeout  time.Duration
	ViceURLCheckRetries  int
	ViceURLCheckCacheTTL time.Duration

	// Service accounts
	ServiceAccountsOnly     bool
	ServiceAccountUsernames map[string]string

	// HTTP
	ListenAddr  string
	HTTPTimeout time.Duration

	// IRODSConnIdleTTL is how long an idle, unreferenced per-user iRODS
	// connection is kept in the pool before being closed.
	IRODSConnIdleTTL time.Duration
}

// jsonConfig mirrors the structure of the optional config.json file.
type jsonConfig struct {
	IRODS struct {
		Host     string `json:"host"`
		Port     any    `json:"port"`
		User     string `json:"user"`
		Password string `json:"password"`
		Zone     string `json:"zone"`
	} `json:"irods"`
	Keycloak struct {
		ServerURL    string `json:"server_url"`
		Realm        string `json:"realm"`
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
		SSLVerify    *bool  `json:"ssl_verify"`
	} `json:"keycloak"`
	Services struct {
		AppsBaseURL        string `json:"apps_base_url"`
		AppExposerBaseURL  string `json:"app_exposer_base_url"`
		PermissionsBaseURL string `json:"permissions_base_url"`
	} `json:"services"`
	Application struct {
		UserSuffix              string            `json:"user_suffix"`
		ViceDomain              string            `json:"vice_domain"`
		PathPrefix              string            `json:"path_prefix"`
		PublicBaseURL           string            `json:"public_base_url"`
		ViceURLCheckTimeout     *float64          `json:"vice_url_check_timeout"`
		ViceURLCheckRetries     *int              `json:"vice_url_check_retries"`
		ViceURLCheckCacheTTL    *float64          `json:"vice_url_check_cache_ttl"`
		ServiceAccountsOnly     *bool             `json:"service_accounts_only"`
		ServiceAccountUsernames map[string]string `json:"service_account_usernames"`
	} `json:"application"`
}

// Load reads configuration from the optional JSON file (CONFIG_FILE, default
// config.json) and the environment, validating that required values are set.
func Load() (*Config, error) {
	jc, err := loadJSONConfig()
	if err != nil {
		return nil, err
	}

	c := &Config{}

	if c.IRODSHost, err = requireStr("IRODS_HOST", jc.IRODS.Host); err != nil {
		return nil, err
	}
	portStr, err := requireStr("IRODS_PORT", anyToStr(jc.IRODS.Port))
	if err != nil {
		return nil, err
	}
	if c.IRODSPort, err = strconv.Atoi(portStr); err != nil {
		return nil, fmt.Errorf("IRODS_PORT must be an integer: %w", err)
	}
	if c.IRODSUser, err = requireStr("IRODS_USER", jc.IRODS.User); err != nil {
		return nil, err
	}
	if c.IRODSPassword, err = requireStr("IRODS_PASSWORD", jc.IRODS.Password); err != nil {
		return nil, err
	}
	if c.IRODSZone, err = requireStr("IRODS_ZONE", jc.IRODS.Zone); err != nil {
		return nil, err
	}

	if c.KeycloakServerURL, err = requireStr("KEYCLOAK_SERVER_URL", jc.Keycloak.ServerURL); err != nil {
		return nil, err
	}
	if !strings.HasSuffix(c.KeycloakServerURL, "/") {
		c.KeycloakServerURL += "/"
	}
	if c.KeycloakRealm, err = requireStr("KEYCLOAK_REALM", jc.Keycloak.Realm); err != nil {
		return nil, err
	}
	if c.KeycloakClientID, err = requireStr("KEYCLOAK_CLIENT_ID", jc.Keycloak.ClientID); err != nil {
		return nil, err
	}
	if c.KeycloakClientSecret, err = requireStr("KEYCLOAK_CLIENT_SECRET", jc.Keycloak.ClientSecret); err != nil {
		return nil, err
	}
	c.KeycloakSSLVerify = boolValue("KEYCLOAK_SSL_VERIFY", jc.Keycloak.SSLVerify, true)

	if c.AppsBaseURL, err = parseURL(strValue("APPS_BASE_URL", jc.Services.AppsBaseURL, "http://apps")); err != nil {
		return nil, err
	}
	if c.AppExposerBaseURL, err = parseURL(strValue("APP_EXPOSER_BASE_URL", jc.Services.AppExposerBaseURL, "http://app-exposer")); err != nil {
		return nil, err
	}
	if c.PermissionsBaseURL, err = parseURL(strValue("PERMISSIONS_BASE_URL", jc.Services.PermissionsBaseURL, "http://permissions")); err != nil {
		return nil, err
	}

	c.UserSuffix = strValue("USER_SUFFIX", jc.Application.UserSuffix, "@iplantcollaborative.org")
	c.ViceDomain = strValue("VICE_DOMAIN", jc.Application.ViceDomain, ".cyverse.run")
	c.PathPrefix = normalizePathPrefix(strValue("PATH_PREFIX", jc.Application.PathPrefix, "/formation"))
	c.OutputZone = c.IRODSZone
	c.PublicBaseURL = strings.TrimRight(strValue("PUBLIC_BASE_URL", jc.Application.PublicBaseURL, ""), "/")

	c.ViceURLCheckTimeout = secondsValue("VICE_URL_CHECK_TIMEOUT", jc.Application.ViceURLCheckTimeout, 5*time.Second)
	c.ViceURLCheckRetries = intValue("VICE_URL_CHECK_RETRIES", jc.Application.ViceURLCheckRetries, 3)
	c.ViceURLCheckCacheTTL = secondsValue("VICE_URL_CHECK_CACHE_TTL", jc.Application.ViceURLCheckCacheTTL, 5*time.Second)

	c.ServiceAccountsOnly = boolValue("SERVICE_ACCOUNTS_ONLY", jc.Application.ServiceAccountsOnly, false)
	if c.ServiceAccountUsernames, err = serviceAccountUsernames(jc.Application.ServiceAccountUsernames); err != nil {
		return nil, err
	}

	c.ListenAddr = strValue("LISTEN_ADDR", "", ":8080")
	c.HTTPTimeout = secondsValue("HTTP_TIMEOUT", nil, 30*time.Second)
	c.IRODSConnIdleTTL = secondsValue("IRODS_CONN_IDLE_TTL", nil, 10*time.Minute)

	return c, nil
}

// KeycloakIssuer returns the OIDC issuer URL for the configured realm.
func (c *Config) KeycloakIssuer() string {
	return c.KeycloakServerURL + "realms/" + c.KeycloakRealm
}

// ResourceMetadataURL returns the public URL of the OAuth protected-resource
// metadata document, or an empty string when no public base URL is configured.
func (c *Config) ResourceMetadataURL() string {
	if c.PublicBaseURL == "" {
		return ""
	}
	return c.PublicBaseURL + "/.well-known/oauth-protected-resource"
}

func loadJSONConfig() (*jsonConfig, error) {
	path := os.Getenv("CONFIG_FILE")
	if path == "" {
		path = "config.json"
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &jsonConfig{}, nil
		}
		return nil, fmt.Errorf("reading config file %s: %w", path, err)
	}
	var jc jsonConfig
	if err := json.Unmarshal(data, &jc); err != nil {
		return nil, fmt.Errorf("parsing config file %s: %w", path, err)
	}
	return &jc, nil
}

func requireStr(envVar, jsonValue string) (string, error) {
	if v := os.Getenv(envVar); v != "" {
		return v, nil
	}
	if jsonValue != "" {
		return jsonValue, nil
	}
	return "", fmt.Errorf("configuration value %s is not set (not in environment or JSON config)", envVar)
}

func strValue(envVar, jsonValue, def string) string {
	if v := os.Getenv(envVar); v != "" {
		return v
	}
	if jsonValue != "" {
		return jsonValue
	}
	return def
}

func boolValue(envVar string, jsonValue *bool, def bool) bool {
	if v := os.Getenv(envVar); v != "" {
		return strings.EqualFold(v, "true")
	}
	if jsonValue != nil {
		return *jsonValue
	}
	return def
}

func intValue(envVar string, jsonValue *int, def int) int {
	if v := os.Getenv(envVar); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	if jsonValue != nil {
		return *jsonValue
	}
	return def
}

func secondsValue(envVar string, jsonValue *float64, def time.Duration) time.Duration {
	if v := os.Getenv(envVar); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return time.Duration(f * float64(time.Second))
		}
	}
	if jsonValue != nil {
		return time.Duration(*jsonValue * float64(time.Second))
	}
	return def
}

func serviceAccountUsernames(jsonValue map[string]string) (map[string]string, error) {
	if v := os.Getenv("SERVICE_ACCOUNT_USERNAMES"); v != "" {
		var m map[string]string
		if err := json.Unmarshal([]byte(v), &m); err != nil {
			return nil, fmt.Errorf("invalid JSON in SERVICE_ACCOUNT_USERNAMES: %w", err)
		}
		return m, nil
	}
	if jsonValue != nil {
		return jsonValue, nil
	}
	return map[string]string{}, nil
}

func parseURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid URL %q: %w", raw, err)
	}
	return u, nil
}

// normalizePathPrefix returns "" for an empty or "/" prefix, otherwise a value
// guaranteed to start with "/".
func normalizePathPrefix(p string) string {
	if p == "" || p == "/" {
		return ""
	}
	if !strings.HasPrefix(p, "/") {
		return "/" + p
	}
	return p
}

func anyToStr(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case float64:
		return strconv.FormatInt(int64(t), 10)
	default:
		return fmt.Sprintf("%v", t)
	}
}
