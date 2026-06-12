package handlers

import (
	"net/http"
	"testing"

	"github.com/cyverse-de/formation/internal/clients"
	"github.com/cyverse-de/formation/internal/terraintest"
)

// newTerrainClient wires a terrain client against a fake terrain server
// driven by the given respond function.
func newTerrainClient(t *testing.T, respond func(r *http.Request) (int, any)) (*clients.Terrain, *terraintest.Server) {
	t.Helper()

	terrain := terraintest.New(t, respond)
	client, err := clients.NewTerrain(terrain.URL())
	if err != nil {
		t.Fatal(err)
	}
	return client, terrain
}
