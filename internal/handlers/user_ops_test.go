package handlers

import (
	"context"
	"net/http"
	"testing"

	"github.com/cyverse-de/formation/internal/auth"
	"github.com/cyverse-de/formation/internal/terraintest"
)

// newUserOps wires a User against a fake terrain: the data fake serves the
// home-directory stat, and prefs answers /secured/preferences.
func newUserOps(t *testing.T, prefs func(r *http.Request) (int, any)) (*User, *terraintest.Data) {
	t.Helper()

	fakeData := terraintest.NewData()
	client, _ := newTerrainClient(t, func(r *http.Request) (int, any) {
		if r.URL.Path == "/secured/preferences" {
			if prefs == nil {
				return http.StatusInternalServerError, nil
			}
			return prefs(r)
		}
		return fakeData.Respond(r)
	})
	return NewUser(client, "iplant", "@iplantcollaborative.org"), fakeData
}

func strPtr(s string) *string { return &s }

func TestUserInfo(t *testing.T) {
	aliceClaims := &auth.Claims{
		PreferredUsername: strPtr("alice"),
		Email:             strPtr("alice@example.org"),
		Name:              strPtr("Alice Liddell"),
	}
	alicePrefs := func(*http.Request) (int, any) {
		return http.StatusOK, map[string]any{
			"default_output_folder": map[string]any{"path": "/iplant/home/alice/analyses"},
		}
	}

	tests := []struct {
		name   string
		claims *auth.Claims
		prefs  func(*http.Request) (int, any)
		want   UserInfo
	}{
		{
			name:   "full info",
			claims: aliceClaims,
			prefs:  alicePrefs,
			want: UserInfo{
				Username:            "alice",
				FullUsername:        "alice@iplantcollaborative.org",
				Name:                "Alice Liddell",
				Email:               "alice@example.org",
				HomePath:            "/iplant/home/alice",
				TrashPath:           "/iplant/trash/home/alice",
				DefaultOutputFolder: "/iplant/home/alice/analyses",
			},
		},
		{
			name: "name falls back to given and family names",
			claims: &auth.Claims{
				PreferredUsername: strPtr("alice"),
				GivenName:         strPtr("Alice"),
				FamilyName:        strPtr("Liddell"),
			},
			prefs: alicePrefs,
			want: UserInfo{
				Username:            "alice",
				FullUsername:        "alice@iplantcollaborative.org",
				Name:                "Alice Liddell",
				HomePath:            "/iplant/home/alice",
				TrashPath:           "/iplant/trash/home/alice",
				DefaultOutputFolder: "/iplant/home/alice/analyses",
			},
		},
		{
			name:   "preferences outage omits the default output folder",
			claims: aliceClaims,
			prefs:  nil,
			want: UserInfo{
				Username:     "alice",
				FullUsername: "alice@iplantcollaborative.org",
				Name:         "Alice Liddell",
				Email:        "alice@example.org",
				HomePath:     "/iplant/home/alice",
				TrashPath:    "/iplant/trash/home/alice",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			user, fake := newUserOps(t, tt.prefs)
			fake.Dirs["/iplant/home/alice"] = nil

			got, err := user.Info(context.Background(), opsToken, tt.claims)
			if err != nil {
				t.Fatal(err)
			}
			if *got != tt.want {
				t.Errorf("Info() = %+v, want %+v", *got, tt.want)
			}
		})
	}

	t.Run("missing home directory is an error", func(t *testing.T) {
		user, _ := newUserOps(t, alicePrefs)

		_, err := user.Info(context.Background(), opsToken, aliceClaims)
		wantAPIError(t, err, http.StatusNotFound, "Home directory '/iplant/home/alice' not found")
	})
}
