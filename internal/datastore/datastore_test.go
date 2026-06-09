package datastore

import (
	"errors"
	"fmt"
	"testing"

	"github.com/cyverse/go-irodsclient/irods/common"
	"github.com/cyverse/go-irodsclient/irods/types"

	"github.com/cyverse-de/formation/internal/apperr"
)

func TestClassify(t *testing.T) {
	d := &DataStore{}
	tests := []struct {
		name   string
		err    error
		assert func(t *testing.T, got error)
	}{
		{
			name: "file not found -> NotFound",
			err:  types.NewFileNotFoundError("/p"),
			assert: func(t *testing.T, got error) {
				if !apperr.AsNotFound(got) {
					t.Errorf("want NotFoundError, got %v", got)
				}
			},
		},
		{
			name: "no access permission -> PermissionDenied",
			err:  types.NewIRODSError(common.CAT_NO_ACCESS_PERMISSION),
			assert: func(t *testing.T, got error) {
				var pd *apperr.PermissionDeniedError
				if !errors.As(got, &pd) {
					t.Errorf("want PermissionDeniedError, got %v", got)
				}
			},
		},
		{
			name: "insufficient privilege -> PermissionDenied",
			err:  types.NewIRODSError(common.CAT_INSUFFICIENT_PRIVILEGE_LEVEL),
			assert: func(t *testing.T, got error) {
				var pd *apperr.PermissionDeniedError
				if !errors.As(got, &pd) {
					t.Errorf("want PermissionDeniedError, got %v", got)
				}
			},
		},
		{
			name: "collection not empty -> BadRequest",
			err:  types.NewCollectionNotEmptyError("/p"),
			assert: func(t *testing.T, got error) {
				var br *apperr.BadRequestError
				if !errors.As(got, &br) {
					t.Errorf("want BadRequestError, got %v", got)
				}
			},
		},
		{
			name: "unknown error is sanitized",
			err:  fmt.Errorf("connection reset by peer to internal-host:1247"),
			assert: func(t *testing.T, got error) {
				if got.Error() != "data store: op failed" {
					t.Errorf("error not sanitized: %q", got.Error())
				}
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.assert(t, d.classify(nil, "op", "/p", tc.err))
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
