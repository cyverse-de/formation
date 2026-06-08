package datastore

import (
	"testing"

	"github.com/cyverse/go-irodsclient/irods/types"
)

func TestHasAccess(t *testing.T) {
	acls := []*types.IRODSAccess{
		{UserName: "alice", AccessLevel: types.IRODSAccessLevelReadObject},
		{UserName: "bob", AccessLevel: types.IRODSAccessLevelOwner},
		{UserName: "carol", AccessLevel: types.IRODSAccessLevelModifyObject},
	}

	tests := []struct {
		name     string
		user     string
		required []types.IRODSAccessLevelType
		want     bool
	}{
		{"reader can read", "alice", readLevels, true},
		{"reader cannot write", "alice", writeLevels, false},
		{"owner can read", "bob", readLevels, true},
		{"owner can write", "bob", writeLevels, true},
		{"modifier can write", "carol", writeLevels, true},
		{"unknown user denied", "dave", readLevels, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := hasAccess(acls, tc.user, tc.required); got != tc.want {
				t.Errorf("hasAccess(%q) = %v, want %v", tc.user, got, tc.want)
			}
		})
	}
}

func TestFormatMetadataHeaders(t *testing.T) {
	metas := []*types.IRODSMeta{
		{Name: "Author", Value: "alice"},
		{Name: "Size", Value: "10", Units: "MB"},
	}
	tests := []struct {
		name      string
		delimiter string
		want      map[string]string
	}{
		{
			name:      "default delimiter",
			delimiter: "",
			want: map[string]string{
				"X-Datastore-Author": "alice",
				"X-Datastore-Size":   "10,MB",
			},
		},
		{
			name:      "custom delimiter",
			delimiter: "|",
			want: map[string]string{
				"X-Datastore-Author": "alice",
				"X-Datastore-Size":   "10|MB",
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := formatMetadataHeaders(metas, tc.delimiter)
			if len(got) != len(tc.want) {
				t.Fatalf("got %d headers, want %d", len(got), len(tc.want))
			}
			for k, v := range tc.want {
				if got[k] != v {
					t.Errorf("%s = %q, want %q", k, got[k], v)
				}
			}
		})
	}
}

func TestNormalizePath(t *testing.T) {
	tests := []struct{ in, want string }{
		{"cyverse/home/alice", "/cyverse/home/alice"},
		{"/cyverse/home/alice", "/cyverse/home/alice"},
	}
	for _, tc := range tests {
		if got := normalizePath(tc.in); got != tc.want {
			t.Errorf("normalizePath(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
