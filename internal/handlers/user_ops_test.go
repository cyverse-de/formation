package handlers

import (
	"context"
	"net/http"
	"testing"

	"github.com/cyverse-de/formation/internal/clients"
	"github.com/cyverse-de/formation/internal/terraintest"
)

// newUserOps wires a User against a fake terrain whose /secured/bootstrap
// returns the given payload.
func newUserOps(t *testing.T, payload any) *User {
	t.Helper()

	terrain := terraintest.New(t, func(r *http.Request) (int, any) {
		if r.URL.Path != "/secured/bootstrap" {
			return http.StatusInternalServerError, nil
		}
		return http.StatusOK, payload
	})
	terrainClient, err := clients.NewTerrain(terrain.URL())
	if err != nil {
		t.Fatal(err)
	}
	return NewUser(terrainClient)
}

func TestBootstrap(t *testing.T) {
	fullPayload := map[string]any{
		"user_info": map[string]any{
			"username":      "alice",
			"full_username": "alice@iplantcollaborative.org",
			"email":         "alice@example.org",
			"first_name":    "Alice",
			"last_name":     "Liddell",
		},
		"data_info": map[string]any{
			"user_home_path":  "/cyverse/home/alice",
			"user_trash_path": "/cyverse/trash/home/alice",
		},
		"preferences": map[string]any{
			"default_output_folder": map[string]any{"path": "/cyverse/home/alice/analyses"},
		},
	}

	tests := []struct {
		name    string
		payload any
		want    UserInfo
	}{
		{
			name:    "full payload",
			payload: fullPayload,
			want: UserInfo{
				Username:            "alice",
				FullUsername:        "alice@iplantcollaborative.org",
				Email:               "alice@example.org",
				FirstName:           "Alice",
				LastName:            "Liddell",
				HomePath:            "/cyverse/home/alice",
				TrashPath:           "/cyverse/trash/home/alice",
				DefaultOutputFolder: "/cyverse/home/alice/analyses",
			},
		},
		{
			name: "missing preferences",
			payload: map[string]any{
				"user_info": fullPayload["user_info"],
				"data_info": fullPayload["data_info"],
			},
			want: UserInfo{
				Username:     "alice",
				FullUsername: "alice@iplantcollaborative.org",
				Email:        "alice@example.org",
				FirstName:    "Alice",
				LastName:     "Liddell",
				HomePath:     "/cyverse/home/alice",
				TrashPath:    "/cyverse/trash/home/alice",
			},
		},
		{
			name: "missing data_info",
			payload: map[string]any{
				"user_info": fullPayload["user_info"],
			},
			want: UserInfo{
				Username:     "alice",
				FullUsername: "alice@iplantcollaborative.org",
				Email:        "alice@example.org",
				FirstName:    "Alice",
				LastName:     "Liddell",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			user := newUserOps(t, tt.payload)
			got, err := user.Bootstrap(context.Background(), opsToken)
			if err != nil {
				t.Fatal(err)
			}
			if *got != tt.want {
				t.Errorf("Bootstrap() = %+v, want %+v", *got, tt.want)
			}
		})
	}
}
