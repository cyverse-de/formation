package handlers

import (
	"context"
	"fmt"

	"github.com/cyverse-de/formation/internal/auth"
	"github.com/cyverse-de/formation/internal/clients"
)

// User provides the caller's account and data-store orientation info for the
// whoami MCP tool. Identity comes from the verified JWT claims; the home path
// is constructed from the configured zone and verified with a stat; the
// default output folder comes from terrain's preferences endpoint. This stays
// off terrain's bootstrap aggregator, which records a DE login event and fans
// out to five services per call.
type User struct {
	terrain    *clients.Terrain
	zone       string
	userSuffix string
}

// NewUser wires the user operations with the terrain client, the iRODS zone
// user paths live in, and the suffix that qualifies usernames.
func NewUser(terrainClient *clients.Terrain, zone, userSuffix string) *User {
	return &User{terrain: terrainClient, zone: zone, userSuffix: userSuffix}
}

// UserInfo is the orientation data whoami reports for the authenticated caller.
// Optional fields (e.g. DefaultOutputFolder) are empty when unavailable.
type UserInfo struct {
	Username            string
	FullUsername        string
	Name                string
	Email               string
	HomePath            string
	TrashPath           string
	DefaultOutputFolder string
}

// Info assembles the caller's orientation info. The stat on the constructed
// home path keeps the reported location authoritative rather than assumed.
func (h *User) Info(ctx context.Context, token string, claims *auth.Claims) (*UserInfo, error) {
	username, err := claims.Username()
	if err != nil {
		return nil, err
	}

	info := &UserInfo{
		Username:     username,
		FullUsername: username + h.userSuffix,
		Name:         claims.DisplayName(),
		Email:        claims.EmailAddress(),
		TrashPath:    fmt.Sprintf("/%s/trash/home/%s", h.zone, username),
	}

	// Preferences are auxiliary: a user-prefs outage shouldn't break whoami,
	// so a failed lookup just omits the default output folder. This runs
	// before the home stat because terrain creates the default output dir
	// (and with it a brand-new user's home tree) while serving preferences.
	if prefs, err := h.terrain.GetPreferences(ctx, token); err == nil {
		info.DefaultOutputFolder = mapString(subMap(prefs, "default_output_folder"), "path")
	}

	homePath := fmt.Sprintf("/%s/home/%s", h.zone, username)
	if _, err := h.statPath(ctx, token, homePath); err != nil {
		return nil, err
	}
	info.HomePath = homePath
	return info, nil
}

// statPath verifies the home path exists with formation error mapping.
func (h *User) statPath(ctx context.Context, token, irodsPath string) (*clients.StatInfo, error) {
	info, err := h.terrain.Stat(ctx, token, irodsPath)
	if err != nil {
		return nil, mapDataError(err, irodsPath, "Home directory")
	}
	return info, nil
}
