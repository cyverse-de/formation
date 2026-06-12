package handlers

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/cyverse-de/formation/internal/apierror"
	"github.com/cyverse-de/formation/internal/clients"
	"github.com/cyverse-de/formation/internal/terraintest"
)

const opsToken = "test-token"

// newDataOps wires a Data instance against the in-memory fake of terrain's
// data endpoints, bypassing the HTTP layer entirely.
func newDataOps(t *testing.T) (*Data, *terraintest.Data) {
	t.Helper()

	fakeData := terraintest.NewData()
	terrainClient, _ := newTerrainClient(t, fakeData.Respond)
	return NewData(terrainClient), fakeData
}

// wantAPIError fails the test unless err is an *apierror.Error with the given
// status and message.
func wantAPIError(t *testing.T, err error, status int, message string) {
	t.Helper()
	var apiErr *apierror.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %v, want *apierror.Error", err)
	}
	if apiErr.Status != status || apiErr.Message != message {
		t.Errorf("error = %d %q, want %d %q", apiErr.Status, apiErr.Message, status, message)
	}
}

func TestBrowse(t *testing.T) {
	t.Run("directory listing", func(t *testing.T) {
		data, fake := newDataOps(t)
		fake.Dirs["/iplant/home/alice"] = []terraintest.DataEntry{
			{Name: "subdir", Dir: true},
			{Name: "file1.txt"},
		}

		result, err := data.Browse(context.Background(), opsToken, "/iplant/home/alice", 0, 0, false, 1024)
		if err != nil {
			t.Fatal(err)
		}
		if result.Path != "/iplant/home/alice" || result.Type != TypeCollection {
			t.Errorf("result = %+v", result)
		}
		want := []Entry{
			{Name: "subdir", Type: TypeCollection},
			{Name: "file1.txt", Type: TypeDataObject},
		}
		if !slices.Equal(result.Entries, want) {
			t.Errorf("entries = %v, want %v", result.Entries, want)
		}
	})

	t.Run("file reads", func(t *testing.T) {
		// "aédef" pages losslessly at limit=2 as "a", "é", "de", "f": a rune
		// that doesn't fit in the window is deferred to the next page, and
		// NextOffset reports where that page starts.
		tests := []struct {
			name           string
			content        string
			offset, limit  int
			maxBytes       int
			want           string
			wantTruncated  bool
			wantNextOffset int
		}{
			{"full contents", "0123456789", 0, 0, 1024, "0123456789", false, 10},
			{"offset", "0123456789", 4, 0, 1024, "456789", false, 10},
			{"limit leaving more bytes", "0123456789", 0, 3, 1024, "012", true, 3},
			{"offset and limit", "0123456789", 2, 4, 1024, "2345", true, 6},
			{"offset past end", "0123456789", 100, 0, 1024, "", false, 100},
			{"negative offset reads from the start", "0123456789", -5, 3, 1024, "012", true, 3},
			{"truncated at maxBytes", "0123456789", 0, 0, 4, "0123", true, 4},
			{"limit below maxBytes wins", "0123456789", 0, 3, 4, "012", true, 3},
			{"rune split at the window edge is deferred", "aédef", 0, 2, 1024, "a", true, 1},
			{"deferred rune is delivered whole on the next page", "aédef", 1, 2, 1024, "é", true, 3},
			{"limit narrower than the next rune delivers it whole", "日本語", 0, 2, 1024, "日", true, 3},
			{"offset inside a character advances by one byte", "日本語", 7, 2, 1024, "", true, 8},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				data, fake := newDataOps(t)
				fake.Files["/iplant/file.txt"] = []byte(tt.content)

				result, err := data.Browse(context.Background(), opsToken, "/iplant/file.txt",
					tt.offset, tt.limit, false, tt.maxBytes)
				if err != nil {
					t.Fatal(err)
				}
				if result.Type != TypeDataObject {
					t.Errorf("type = %q", result.Type)
				}
				if string(result.Content) != tt.want {
					t.Errorf("content = %q, want %q", result.Content, tt.want)
				}
				if result.Truncated != tt.wantTruncated {
					t.Errorf("truncated = %v, want %v", result.Truncated, tt.wantTruncated)
				}
				if result.Binary {
					t.Error("binary = true for text content")
				}
				if result.FileSize != int64(len(tt.content)) {
					t.Errorf("fileSize = %d, want %d", result.FileSize, len(tt.content))
				}
				if result.NextOffset != tt.wantNextOffset {
					t.Errorf("nextOffset = %d, want %d", result.NextOffset, tt.wantNextOffset)
				}
			})
		}
	})

	t.Run("binary detection", func(t *testing.T) {
		tests := []struct {
			name    string
			content []byte
		}{
			{"NUL byte", []byte{0xff, 0xfe, 0x00, 0x01}},
			{"NUL-free invalid bytes", []byte{0xff, 0xfe, 0xff, 0xfe, 'A'}},
			// One substituted byte breaks paging's byte arithmetic, so even
			// mostly-text content is refused rather than silently skipped.
			{"single substituted byte", []byte{'a', 0xff, 'b', 'c'}},
			// A literal U+FFFD is indistinguishable from a substitution.
			{"literal replacement character", []byte("ok�")},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				data, fake := newDataOps(t)
				fake.Files["/iplant/blob.bin"] = tt.content

				result, err := data.Browse(context.Background(), opsToken, "/iplant/blob.bin", 0, 0, false, 1024)
				if err != nil {
					t.Fatal(err)
				}
				if !result.Binary {
					t.Errorf("binary = false for %q", tt.content)
				}
				if result.FileSize != int64(len(tt.content)) {
					t.Errorf("fileSize = %d, want %d", result.FileSize, len(tt.content))
				}
			})
		}
	})

	t.Run("metadata included on request", func(t *testing.T) {
		data, fake := newDataOps(t)
		fake.Files["/iplant/file.txt"] = []byte("x")
		fake.Meta["/iplant/file.txt"] = []terraintest.DataAVU{{Attr: "Sample_Type", Value: "abc-123"}}

		result, err := data.Browse(context.Background(), opsToken, "/iplant/file.txt", 0, 0, true, 1024)
		if err != nil {
			t.Fatal(err)
		}
		want := []clients.MetadataAVU{{Attr: "Sample_Type", Value: "abc-123"}}
		if !slices.Equal(result.Metadata, want) {
			t.Errorf("metadata = %v, want %v", result.Metadata, want)
		}
	})

	t.Run("metadata errors are swallowed", func(t *testing.T) {
		data, fake := newDataOps(t)
		fake.Files["/iplant/file.txt"] = []byte("x")
		fake.MetaErr = true

		result, err := data.Browse(context.Background(), opsToken, "/iplant/file.txt", 0, 0, true, 1024)
		if err != nil {
			t.Fatal(err)
		}
		if result.Metadata != nil {
			t.Errorf("metadata = %v, want none when the lookup fails", result.Metadata)
		}
	})
}

// TestDataErrorMapping exercises the error_code-based mapping of terrain's
// data errors (ERR_DOES_NOT_EXIST arrives as an HTTP 500, unreadable paths
// read as nonexistent) into the formation 404/403 responses.
func TestDataErrorMapping(t *testing.T) {
	t.Run("missing path is not found", func(t *testing.T) {
		data, _ := newDataOps(t)
		_, err := data.Browse(context.Background(), opsToken, "/iplant/nope", 0, 0, false, 1024)
		wantAPIError(t, err, 404, "Path '/iplant/nope' not found")
	})

	t.Run("unreadable path is access denied", func(t *testing.T) {
		data, fake := newDataOps(t)
		fake.Files["/iplant/secret.txt"] = []byte("hidden")
		fake.Perms["/iplant/secret.txt"] = "none"

		// Stat sees the path but omits it from the response, which surfaces as
		// a permission error rather than a missing path.
		_, err := data.Browse(context.Background(), opsToken, "/iplant/secret.txt", 0, 0, false, 1024)
		wantAPIError(t, err, 403, "Access denied")
	})

	t.Run("unwritable delete is access denied", func(t *testing.T) {
		data, fake := newDataOps(t)
		fake.Files["/iplant/locked.txt"] = []byte("x")
		fake.Perms["/iplant/locked.txt"] = "read"

		_, err := data.DeletePath(context.Background(), opsToken, "/iplant/locked.txt", false, false)
		wantAPIError(t, err, 403, "Access denied")
	})

	t.Run("missing delete path is not found", func(t *testing.T) {
		data, _ := newDataOps(t)
		_, err := data.DeletePath(context.Background(), opsToken, "/iplant/nope", false, false)
		wantAPIError(t, err, 404, "Path '/iplant/nope' not found")
	})
}

func TestUploadFile(t *testing.T) {
	t.Run("create with metadata", func(t *testing.T) {
		data, fake := newDataOps(t)
		fake.Dirs["/iplant/home/alice"] = nil
		metadata := []clients.MetadataAVU{
			{Attr: "author", Value: "alice"},
			{Attr: "weight", Value: "12", Unit: "kg"},
		}

		result, err := data.UploadFile(context.Background(), opsToken, "/iplant/home/alice/new.txt",
			strings.NewReader("hello world"), metadata, false)
		if err != nil {
			t.Fatal(err)
		}
		if result["path"] != "/iplant/home/alice/new.txt" || result["type"] != TypeDataObject || result["created"] != true {
			t.Errorf("result = %v", result)
		}
		if got := string(fake.Files["/iplant/home/alice/new.txt"]); got != "hello world" {
			t.Errorf("uploaded content = %q", got)
		}

		if len(fake.MetaAdds) != 1 {
			t.Fatalf("metadata adds = %d, want 1", len(fake.MetaAdds))
		}
		want := []terraintest.DataAVU{{Attr: "author", Value: "alice"}, {Attr: "weight", Value: "12", Unit: "kg"}}
		if got := fake.MetaAdds[0].AVUs; !slices.Equal(got, want) {
			t.Errorf("avus = %v, want %v", got, want)
		}
	})

	t.Run("overwrite existing file", func(t *testing.T) {
		data, fake := newDataOps(t)
		fake.Files["/iplant/file.txt"] = []byte("old")

		result, err := data.UploadFile(context.Background(), opsToken, "/iplant/file.txt",
			strings.NewReader("new contents"), nil, false)
		if err != nil {
			t.Fatal(err)
		}
		if result["created"] != false || result["type"] != TypeDataObject {
			t.Errorf("result = %v", result)
		}
		if got := string(fake.Files["/iplant/file.txt"]); got != "new contents" {
			t.Errorf("uploaded content = %q", got)
		}
		if len(fake.MetaSets)+len(fake.MetaAdds) != 0 {
			t.Error("metadata should not be set without AVUs")
		}
	})

	t.Run("errors", func(t *testing.T) {
		tests := []struct {
			name        string
			setup       func(fake *terraintest.Data)
			path        string
			wantStatus  int
			wantMessage string
		}{
			{
				name:        "upload to a directory",
				setup:       func(d *terraintest.Data) { d.Dirs["/iplant/dir"] = nil },
				path:        "/iplant/dir",
				wantStatus:  400,
				wantMessage: "Cannot upload file - path is a directory",
			},
			{
				name:        "missing parent",
				setup:       func(d *terraintest.Data) {},
				path:        "/iplant/nope/new.txt",
				wantStatus:  404,
				wantMessage: "Parent directory '/iplant/nope' not found",
			},
			{
				name: "unwritable parent",
				setup: func(d *terraintest.Data) {
					d.Dirs["/iplant/readonly"] = nil
					d.Perms["/iplant/readonly"] = "read"
				},
				path:        "/iplant/readonly/new.txt",
				wantStatus:  403,
				wantMessage: "Access denied",
			},
			{
				name: "unwritable existing path",
				setup: func(d *terraintest.Data) {
					d.Files["/iplant/locked.txt"] = []byte("x")
					d.Perms["/iplant/locked.txt"] = "read"
				},
				path:        "/iplant/locked.txt",
				wantStatus:  403,
				wantMessage: "Access denied",
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				data, fake := newDataOps(t)
				tt.setup(fake)

				_, err := data.UploadFile(context.Background(), opsToken, tt.path,
					strings.NewReader("content"), nil, false)
				wantAPIError(t, err, tt.wantStatus, tt.wantMessage)
			})
		}
	})
}

func TestUpdateMetadata(t *testing.T) {
	t.Run("replace clears the attributes being set", func(t *testing.T) {
		data, fake := newDataOps(t)
		fake.Files["/iplant/file.txt"] = []byte("x")
		fake.Meta["/iplant/file.txt"] = []terraintest.DataAVU{
			{Attr: "author", Value: "old-author"},
			{Attr: "project", Value: "proj0"},
		}

		result, err := data.UpdateMetadata(context.Background(), opsToken, "/iplant/file.txt",
			[]clients.MetadataAVU{{Attr: "author", Value: "bob"}}, true)
		if err != nil {
			t.Fatal(err)
		}
		if result["type"] != TypeDataObject || result["created"] != false {
			t.Errorf("result = %v", result)
		}
		// author's existing AVU is replaced; project survives untouched.
		want := []terraintest.DataAVU{{Attr: "project", Value: "proj0"}, {Attr: "author", Value: "bob"}}
		if len(fake.MetaSets) != 1 || !slices.Equal(fake.MetaSets[0].AVUs, want) {
			t.Errorf("metadata sets = %+v, want %v", fake.MetaSets, want)
		}
	})

	t.Run("without replace adds alongside existing values", func(t *testing.T) {
		data, fake := newDataOps(t)
		fake.Files["/iplant/file.txt"] = []byte("x")
		fake.Meta["/iplant/file.txt"] = []terraintest.DataAVU{{Attr: "author", Value: "old-author"}}

		_, err := data.UpdateMetadata(context.Background(), opsToken, "/iplant/file.txt",
			[]clients.MetadataAVU{{Attr: "author", Value: "bob"}}, false)
		if err != nil {
			t.Fatal(err)
		}
		// Adds go through the add endpoint, leaving existing values in place.
		wantAdd := []terraintest.DataAVU{{Attr: "author", Value: "bob"}}
		if len(fake.MetaAdds) != 1 || !slices.Equal(fake.MetaAdds[0].AVUs, wantAdd) {
			t.Errorf("metadata adds = %+v, want %v", fake.MetaAdds, wantAdd)
		}
		wantMeta := []terraintest.DataAVU{{Attr: "author", Value: "old-author"}, {Attr: "author", Value: "bob"}}
		if got := fake.Meta["/iplant/file.txt"]; !slices.Equal(got, wantMeta) {
			t.Errorf("final metadata = %v, want %v", got, wantMeta)
		}
	})

	t.Run("on a collection", func(t *testing.T) {
		data, fake := newDataOps(t)
		fake.Dirs["/iplant/dir"] = nil

		result, err := data.UpdateMetadata(context.Background(), opsToken, "/iplant/dir",
			[]clients.MetadataAVU{{Attr: "project", Value: "proj1"}}, false)
		if err != nil {
			t.Fatal(err)
		}
		if result["type"] != TypeCollection || result["created"] != false {
			t.Errorf("result = %v", result)
		}
		if len(fake.MetaAdds) != 1 || fake.MetaAdds[0].Path != "/iplant/dir" {
			t.Errorf("metadata adds = %+v", fake.MetaAdds)
		}
	})
}

func TestMakeDirectory(t *testing.T) {
	t.Run("create", func(t *testing.T) {
		data, fake := newDataOps(t)
		fake.Dirs["/iplant/home/alice"] = nil

		result, err := data.MakeDirectory(context.Background(), opsToken, "/iplant/home/alice/newdir", nil, false)
		if err != nil {
			t.Fatal(err)
		}
		if result["type"] != TypeCollection || result["created"] != true {
			t.Errorf("result = %v", result)
		}
		if !slices.Contains(fake.CreatedDirs, "/iplant/home/alice/newdir") {
			t.Errorf("createdDirs = %v", fake.CreatedDirs)
		}
	})

	t.Run("existing path becomes a metadata-only update", func(t *testing.T) {
		data, fake := newDataOps(t)
		fake.Dirs["/iplant/dir"] = nil

		result, err := data.MakeDirectory(context.Background(), opsToken, "/iplant/dir",
			[]clients.MetadataAVU{{Attr: "project", Value: "proj1"}}, false)
		if err != nil {
			t.Fatal(err)
		}
		if result["created"] != false {
			t.Errorf("result = %v", result)
		}
		if len(fake.CreatedDirs) != 0 {
			t.Errorf("createdDirs = %v, want none", fake.CreatedDirs)
		}
		if len(fake.MetaAdds) != 1 {
			t.Errorf("metadata adds = %+v", fake.MetaAdds)
		}
	})

	t.Run("missing parent", func(t *testing.T) {
		data, _ := newDataOps(t)
		_, err := data.MakeDirectory(context.Background(), opsToken, "/iplant/nope/newdir", nil, false)
		wantAPIError(t, err, 404, "Parent directory '/iplant/nope' not found")
	})
}

func TestDeletePath(t *testing.T) {
	t.Run("file delete", func(t *testing.T) {
		data, fake := newDataOps(t)
		fake.Files["/iplant/file.txt"] = []byte("x")

		result, err := data.DeletePath(context.Background(), opsToken, "/iplant/file.txt", false, false)
		if err != nil {
			t.Fatal(err)
		}
		want := map[string]any{
			"path": "/iplant/file.txt", "type": TypeDataObject,
			"would_delete": true, "deleted": true, "dry_run": false,
		}
		for key, value := range want {
			if result[key] != value {
				t.Errorf("result[%q] = %v, want %v", key, result[key], value)
			}
		}
		if !slices.Contains(fake.Deleted, "/iplant/file.txt") {
			t.Errorf("deleted = %v", fake.Deleted)
		}
	})

	t.Run("dry run does not delete", func(t *testing.T) {
		data, fake := newDataOps(t)
		fake.Files["/iplant/file.txt"] = []byte("x")

		result, err := data.DeletePath(context.Background(), opsToken, "/iplant/file.txt", false, true)
		if err != nil {
			t.Fatal(err)
		}
		if result["deleted"] != false || result["dry_run"] != true || result["would_delete"] != true {
			t.Errorf("result = %v", result)
		}
		if len(fake.Deleted) != 0 {
			t.Errorf("deleted = %v, want none", fake.Deleted)
		}
	})

	t.Run("non-empty dir without recurse fails", func(t *testing.T) {
		data, fake := newDataOps(t)
		fake.Dirs["/iplant/full"] = []terraintest.DataEntry{{Name: "child.txt"}}

		_, err := data.DeletePath(context.Background(), opsToken, "/iplant/full", false, false)
		wantAPIError(t, err, 400, "Directory not empty. Use recurse=true to delete non-empty directories.")
		if len(fake.Deleted) != 0 {
			t.Errorf("deleted = %v, want none", fake.Deleted)
		}

		// The dry run reports the same error a real delete would.
		_, err = data.DeletePath(context.Background(), opsToken, "/iplant/full", false, true)
		wantAPIError(t, err, 400, "Directory not empty. Use recurse=true to delete non-empty directories.")
	})

	t.Run("recurse reports item_count", func(t *testing.T) {
		data, fake := newDataOps(t)
		fake.Dirs["/iplant/full"] = []terraintest.DataEntry{{Name: "child.txt"}}

		result, err := data.DeletePath(context.Background(), opsToken, "/iplant/full", true, false)
		if err != nil {
			t.Fatal(err)
		}
		if result["item_count"] != 1 || result["deleted"] != true || result["type"] != TypeCollection {
			t.Errorf("result = %v", result)
		}
		if !slices.Contains(fake.Deleted, "/iplant/full") {
			t.Errorf("deleted = %v", fake.Deleted)
		}
	})

	t.Run("recurse dry run keeps item_count but does not delete", func(t *testing.T) {
		data, fake := newDataOps(t)
		fake.Dirs["/iplant/full"] = []terraintest.DataEntry{{Name: "child.txt"}}

		result, err := data.DeletePath(context.Background(), opsToken, "/iplant/full", true, true)
		if err != nil {
			t.Fatal(err)
		}
		if result["item_count"] != 1 || result["deleted"] != false || result["dry_run"] != true {
			t.Errorf("result = %v", result)
		}
		if len(fake.Deleted) != 0 {
			t.Errorf("deleted = %v, want none", fake.Deleted)
		}
	})
}
