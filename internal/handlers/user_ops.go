package handlers

import (
	"context"

	"github.com/cyverse-de/formation/internal/clients"
)

// User provides the caller's account and data-store orientation info backed by
// terrain's bootstrap endpoint, exposed through the whoami MCP tool.
type User struct {
	terrain *clients.Terrain
}

// NewUser wires the user operations with the terrain client.
func NewUser(terrainClient *clients.Terrain) *User {
	return &User{terrain: terrainClient}
}

// UserInfo is the orientation data whoami reports for the authenticated caller.
// Optional fields (e.g. DefaultOutputFolder) are empty when terrain omits them.
type UserInfo struct {
	Username            string
	FullUsername        string
	Email               string
	FirstName           string
	LastName            string
	HomePath            string
	TrashPath           string
	DefaultOutputFolder string
}

// Bootstrap fetches and flattens the caller's terrain bootstrap into UserInfo.
func (h *User) Bootstrap(ctx context.Context, token string) (*UserInfo, error) {
	result, err := h.terrain.Bootstrap(ctx, token)
	if err != nil {
		return nil, err
	}

	userInfo := subMap(result, "user_info")
	dataInfo := subMap(result, "data_info")
	defaultOutput := subMap(subMap(result, "preferences"), "default_output_folder")

	return &UserInfo{
		Username:            mapString(userInfo, "username"),
		FullUsername:        mapString(userInfo, "full_username"),
		Email:               mapString(userInfo, "email"),
		FirstName:           mapString(userInfo, "first_name"),
		LastName:            mapString(userInfo, "last_name"),
		HomePath:            mapString(dataInfo, "user_home_path"),
		TrashPath:           mapString(dataInfo, "user_trash_path"),
		DefaultOutputFolder: mapString(defaultOutput, "path"),
	}, nil
}

// subMap returns m[key] as a nested object, or nil when absent or not a map.
func subMap(m map[string]any, key string) map[string]any {
	nested, _ := m[key].(map[string]any)
	return nested
}

// mapString returns m[key] as a string, or "" when absent or not a string.
func mapString(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return s
}
